package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// Config tunes the relay.
type Config struct {
	Version        string
	EnvelopeTTL    time.Duration
	MaxUploadBytes int64
	MaxFetchBytes  int
	RatePerMinute  int
	TrustProxy     bool
	// RegistrationToken, when set, is required (HB-Registration-Token header)
	// to create new accounts, so strangers cannot use a public relay.
	RegistrationToken string
	Now               func() time.Time
	Logger            *slog.Logger
}

// Server serves the relay HTTP API.
type Server struct {
	store   *Store
	cfg     Config
	limiter *rateLimiter
	mux     *http.ServeMux
}

type ctxKey int

const (
	ctxAccount ctxKey = iota
	ctxDevice
)

// apiError is rendered as {"error":{"code","message"}}.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func fail(status int, code, message string) *apiError { return &apiError{status, code, message} }

// NewServer wires routes and defaults.
func NewServer(store *Store, cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.EnvelopeTTL == 0 {
		cfg.EnvelopeTTL = 30 * 24 * time.Hour
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 25 << 20
	}
	if cfg.MaxFetchBytes == 0 {
		cfg.MaxFetchBytes = 32 << 20
	}
	if cfg.RatePerMinute == 0 {
		cfg.RatePerMinute = 120
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{store: store, cfg: cfg, limiter: newRateLimiter(cfg.RatePerMinute), mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.route("POST /v1/accounts", authAccountRegistration, 64<<10, s.registerAccount)
	s.route("GET /v1/status", authAgent, 0, s.status)
	s.route("DELETE /v1/accounts/self", authAgent, 0, s.deleteAccount)
	s.route("DELETE /v1/devices/current", authAgent, 0, s.unlinkCurrent)
	s.route("GET /v1/envelopes", authAgent, 0, s.listEnvelopes)
	s.route("POST /v1/envelopes/ack", authAgent, 4<<10, s.ackEnvelopes)
	s.route("POST /v1/devices", authNewDevice, 64<<10, s.linkDevice)
	s.route("GET /v1/devices/self", authDevice, 0, s.deviceSelf)
	s.route("DELETE /v1/devices/self", authDevice, 0, s.unlinkSelf)
	s.route("POST /v1/envelopes", authDevice, -1, s.uploadEnvelopes)
	return s
}

// ServeHTTP applies security headers, rate limiting and logging.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.cfg.Now()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	rec.Header().Set("Cache-Control", "no-store")
	rec.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.limiter.allow(s.clientIP(r), start) {
		s.writeError(rec, r, fail(http.StatusTooManyRequests, "rate_limited", "too many requests, slow down"))
	} else {
		s.mux.ServeHTTP(rec, r)
	}
	// Never log bodies, keys or query strings.
	s.cfg.Logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", s.cfg.Now().Sub(start).Milliseconds())
}

// RunJanitor deletes expired envelopes and nonces until ctx is done.
func (s *Server) RunJanitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.store.Cleanup(ctx, s.cfg.Now(), s.cfg.EnvelopeTTL); err != nil {
			s.cfg.Logger.Error("cleanup failed", "error", err)
		} else if n > 0 {
			s.cfg.Logger.Info("expired envelopes deleted", "count", n)
		}
		s.limiter.prune(s.cfg.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type authMode int

const (
	authAgent authMode = iota
	authDevice
	authNewDevice
	authAccountRegistration
)

type handler func(w http.ResponseWriter, r *http.Request, body []byte) (any, int, error)

// route registers a signed endpoint. bodyLimit 0 means no body allowed and -1
// means the configured upload limit.
func (s *Server) route(pattern string, mode authMode, bodyLimit int64, h handler) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body, err := readBody(w, r, bodyLimit, s.cfg.MaxUploadBytes)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		ctx, err := s.authenticate(r, mode, body)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		result, status, err := h(w, r.WithContext(ctx), body)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, status, result)
	})
}

// readBody reads the request body within the endpoint's limit. A limit of 0
// means the endpoint takes no body; -1 means the configured upload limit.
func readBody(w http.ResponseWriter, r *http.Request, limit, uploadLimit int64) ([]byte, error) {
	if limit < 0 {
		limit = uploadLimit
	}
	if limit == 0 {
		// One byte is enough to tell "no body" from "unexpected body".
		probe, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil {
			return nil, fail(http.StatusBadRequest, "unreadable_body", "the request body could not be read")
		}
		if len(probe) > 0 {
			return nil, fail(http.StatusBadRequest, "unexpected_body", "this endpoint takes no body")
		}
		return nil, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, fail(http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds the limit")
	case err != nil:
		// Client aborts and read timeouts are not size problems.
		return nil, fail(http.StatusBadRequest, "unreadable_body", "the request body could not be read")
	}
	return body, nil
}

func (s *Server) authenticate(r *http.Request, mode authMode, body []byte) (context.Context, error) {
	h, err := protocol.ParseSignedHeaders(r)
	if err != nil {
		return nil, fail(http.StatusUnauthorized, "unauthenticated", err.Error())
	}
	ctx, now := r.Context(), s.cfg.Now()
	var pub ed25519.PublicKey
	nonceKey := h.Key
	switch mode {
	case authAgent, authAccountRegistration:
		account, ok := strings.CutPrefix(h.Key, "agent:")
		if !ok || !protocol.ValidAccountID(account) {
			return nil, fail(http.StatusUnauthorized, "unauthenticated", "agent key required")
		}
		if mode == authAccountRegistration {
			var req struct {
				Account string `json:"account"`
				Sig     string `json:"sig"`
			}
			if json.Unmarshal(body, &req) != nil || req.Account != account {
				return nil, fail(http.StatusBadRequest, "invalid_request", "account in body must match HB-Key")
			}
			if pub, err = protocol.DecodeB64(req.Sig, protocol.PublicKeySize); err != nil {
				return nil, fail(http.StatusBadRequest, "invalid_request", "invalid sig public key")
			}
		} else {
			a, err := s.store.Account(ctx, account)
			if errors.Is(err, ErrNotFound) {
				return nil, fail(http.StatusUnauthorized, "unknown_account", "account is not registered")
			} else if err != nil {
				return nil, err
			}
			pub = a.SigPub
		}
		ctx = context.WithValue(ctx, ctxAccount, account)
	case authDevice:
		id, ok := strings.CutPrefix(h.Key, "device:")
		if !ok || !protocol.ValidDeviceID(id) {
			return nil, fail(http.StatusUnauthorized, "unauthenticated", "device key required")
		}
		d, err := s.store.Device(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, fail(http.StatusUnauthorized, "unknown_device", "device is not registered")
		} else if err != nil {
			return nil, err
		}
		pub = d.SigPub
		ctx = context.WithValue(ctx, ctxDevice, d)
	case authNewDevice:
		if h.Key != protocol.KeyNewDevice {
			return nil, fail(http.StatusUnauthorized, "unauthenticated", "new-device key required")
		}
		var req struct {
			DeviceSig string `json:"device_sig"`
		}
		if json.Unmarshal(body, &req) != nil {
			return nil, fail(http.StatusBadRequest, "invalid_request", "invalid JSON body")
		}
		if pub, err = protocol.DecodeB64(req.DeviceSig, protocol.PublicKeySize); err != nil {
			return nil, fail(http.StatusBadRequest, "invalid_request", "invalid device_sig")
		}
		nonceKey = h.Key + ":" + req.DeviceSig
	}
	if err := h.Verify(pub, r.Method, r.URL.RequestURI(), body, now); errors.Is(err, protocol.ErrStaleRequest) {
		return nil, fail(http.StatusUnauthorized, "stale_request", err.Error())
	} else if err != nil {
		return nil, fail(http.StatusUnauthorized, "bad_signature", err.Error())
	}
	if err := s.store.UseNonce(ctx, nonceKey, h.Nonce, now); errors.Is(err, ErrReplay) {
		return nil, fail(http.StatusUnauthorized, "replayed_request", "nonce was already used")
	} else if err != nil {
		return nil, err
	}
	// Only reveal the unlink state to the authenticated device itself.
	if d, ok := ctx.Value(ctxDevice).(Device); ok && d.UnlinkedAt != nil {
		return nil, fail(http.StatusGone, "device_unlinked", "this device was unlinked; pair again")
	}
	return ctx, nil
}

func accountFrom(r *http.Request) string { return r.Context().Value(ctxAccount).(string) }
func deviceFrom(r *http.Request) Device  { return r.Context().Value(ctxDevice).(Device) }

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.cfg.Version})
}

func (s *Server) registerAccount(_ http.ResponseWriter, r *http.Request, body []byte) (any, int, error) {
	var req struct {
		Account     string `json:"account"`
		Sig         string `json:"sig"`
		Enc         string `json:"enc"`
		PairingHash string `json:"pairing_hash"`
		Name        string `json:"name"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid JSON body")
	}
	if s.cfg.RegistrationToken != "" {
		// Only new accounts need the token; a lookup failure must not skip the check.
		_, err := s.store.Account(r.Context(), req.Account)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, 0, err
		}
		if errors.Is(err, ErrNotFound) &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("HB-Registration-Token")), []byte(s.cfg.RegistrationToken)) != 1 {
			return nil, 0, fail(http.StatusForbidden, "registration_token_required", "this relay requires a registration token for new accounts")
		}
	}
	// sig was decoded and checked by the authAccountRegistration middleware.
	sig, _ := protocol.DecodeB64(req.Sig, protocol.PublicKeySize)
	enc, err := protocol.DecodeB64(req.Enc, protocol.PublicKeySize)
	if err != nil {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid enc public key")
	}
	if _, err := protocol.DecodeB64(req.PairingHash, sha256.Size); err != nil {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid pairing_hash")
	}
	created, err := s.store.UpsertAccount(r.Context(), Account{ID: req.Account, SigPub: sig, EncPub: enc, PairingHash: req.PairingHash, Name: protocol.CleanLabel(req.Name)}, s.cfg.Now())
	if errors.Is(err, ErrConflict) {
		return nil, 0, fail(http.StatusConflict, "account_exists", "account exists with different keys")
	} else if err != nil {
		return nil, 0, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return map[string]any{"account": req.Account, "created": created}, status, nil
}

func (s *Server) status(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	ctx, account, now := r.Context(), accountFrom(r), s.cfg.Now()
	s.logTouch(s.store.TouchAccount(ctx, account, now))
	result := map[string]any{"account": account, "device": nil, "oldest_pending_at": nil}
	if d, err := s.store.ActiveDevice(ctx, account); err == nil {
		result["device"] = deviceJSON(d)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, 0, err
	}
	n, oldest, err := s.store.Pending(ctx, account)
	if err != nil {
		return nil, 0, err
	}
	result["pending_envelopes"] = n
	if oldest != nil {
		result["oldest_pending_at"] = protocol.FormatTime(*oldest)
	}
	return result, http.StatusOK, nil
}

func deviceJSON(d Device) map[string]any {
	return map[string]any{
		"device_id": d.ID, "name": d.Name, "platform": d.Platform, "app_version": d.AppVersion,
		"linked_at": protocol.FormatTime(d.LinkedAt), "last_seen_at": protocol.FormatTime(d.LastSeenAt),
	}
}

func (s *Server) deleteAccount(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	if err := s.store.DeleteAccount(r.Context(), accountFrom(r)); err != nil {
		return nil, 0, err
	}
	return map[string]any{"ok": true}, http.StatusOK, nil
}

func (s *Server) unlinkCurrent(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	ctx := r.Context()
	d, err := s.store.ActiveDevice(ctx, accountFrom(r))
	if errors.Is(err, ErrNotFound) {
		return map[string]any{"ok": true, "unlinked": false}, http.StatusOK, nil
	} else if err != nil {
		return nil, 0, err
	}
	unlinked, err := s.store.UnlinkDevice(ctx, d.ID, s.cfg.Now())
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"ok": true, "unlinked": unlinked}, http.StatusOK, nil
}

func (s *Server) listEnvelopes(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	q := r.URL.Query()
	after, err := strconv.ParseInt(q.Get("after"), 10, 64)
	if q.Get("after") == "" {
		after, err = 0, nil
	}
	if err != nil || after < 0 {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "after must be a non-negative integer")
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 || limit > 200 {
			return nil, 0, fail(http.StatusBadRequest, "invalid_request", "limit must be 1-200")
		}
	}
	ctx, account := r.Context(), accountFrom(r)
	s.logTouch(s.store.TouchAccount(ctx, account, s.cfg.Now()))
	envelopes, more, err := s.store.Envelopes(ctx, account, after, limit, s.cfg.MaxFetchBytes)
	if err != nil {
		return nil, 0, err
	}
	if envelopes == nil {
		envelopes = []protocol.StoredEnvelope{}
	}
	return map[string]any{"envelopes": envelopes, "more": more}, http.StatusOK, nil
}

func (s *Server) ackEnvelopes(_ http.ResponseWriter, r *http.Request, body []byte) (any, int, error) {
	var req struct {
		UpTo *int64 `json:"up_to"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.UpTo == nil || *req.UpTo < 0 {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "up_to must be a non-negative integer")
	}
	n, err := s.store.Ack(r.Context(), accountFrom(r), *req.UpTo)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"deleted": n}, http.StatusOK, nil
}

func (s *Server) linkDevice(_ http.ResponseWriter, r *http.Request, body []byte) (any, int, error) {
	var req struct {
		Account    string `json:"account"`
		Secret     string `json:"secret"`
		DeviceSig  string `json:"device_sig"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
		AppVersion string `json:"app_version"`
	}
	if err := json.Unmarshal(body, &req); err != nil || !protocol.ValidAccountID(req.Account) {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid JSON body or account")
	}
	secret, err := protocol.DecodeB64U(req.Secret, protocol.SecretSize)
	if err != nil {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid pairing secret")
	}
	ctx := r.Context()
	a, err := s.store.Account(ctx, req.Account)
	if errors.Is(err, ErrNotFound) {
		return nil, 0, fail(http.StatusNotFound, "unknown_account", "the agent has not registered this pairing code; run healthbridge onboard again")
	} else if err != nil {
		return nil, 0, err
	}
	if subtle.ConstantTimeCompare([]byte(protocol.PairingHash(secret)), []byte(a.PairingHash)) != 1 {
		return nil, 0, fail(http.StatusForbidden, "invalid_pairing_secret", "pairing code is not valid for this account")
	}
	// device_sig was decoded and checked by the authNewDevice middleware.
	sig, _ := protocol.DecodeB64(req.DeviceSig, protocol.PublicKeySize)
	d, err := s.store.LinkDevice(ctx, Device{
		Account: req.Account, SigPub: sig, Name: protocol.CleanLabel(req.DeviceName),
		Platform: protocol.CleanLabel(req.Platform), AppVersion: protocol.CleanLabel(req.AppVersion),
	}, s.cfg.Now())
	if errors.Is(err, ErrConflict) {
		return nil, 0, fail(http.StatusConflict, "device_already_linked", "another device is linked; run healthbridge unlink first")
	} else if err != nil {
		return nil, 0, err
	}
	return map[string]any{"device_id": d.ID, "account": d.Account, "linked_at": protocol.FormatTime(d.LinkedAt)}, http.StatusCreated, nil
}

func (s *Server) deviceSelf(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	ctx, d := r.Context(), deviceFrom(r)
	s.logTouch(s.store.TouchDevice(ctx, d.ID, s.cfg.Now()))
	a, err := s.store.Account(ctx, d.Account)
	if err != nil {
		return nil, 0, err
	}
	n, _, err := s.store.Pending(ctx, d.Account)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"device_id": d.ID, "account": d.Account, "linked": true, "pending_envelopes": n, "agent_name": a.Name}, http.StatusOK, nil
}

func (s *Server) unlinkSelf(_ http.ResponseWriter, r *http.Request, _ []byte) (any, int, error) {
	if _, err := s.store.UnlinkDevice(r.Context(), deviceFrom(r).ID, s.cfg.Now()); err != nil {
		return nil, 0, err
	}
	return map[string]any{"ok": true}, http.StatusOK, nil
}

func (s *Server) uploadEnvelopes(_ http.ResponseWriter, r *http.Request, body []byte) (any, int, error) {
	var req struct {
		Envelopes []protocol.Envelope `json:"envelopes"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "invalid JSON body")
	}
	if len(req.Envelopes) == 0 || len(req.Envelopes) > 32 {
		return nil, 0, fail(http.StatusBadRequest, "invalid_request", "send 1-32 envelopes per request")
	}
	ctx, d, now := r.Context(), deviceFrom(r), s.cfg.Now()
	for i, e := range req.Envelopes {
		if err := e.ValidateHeader(); err != nil {
			return nil, 0, fail(http.StatusBadRequest, "invalid_envelope", "envelope "+strconv.Itoa(i)+": "+err.Error())
		}
		if e.Account != d.Account {
			return nil, 0, fail(http.StatusForbidden, "wrong_account", "envelope account does not match the device")
		}
	}
	accepted := make([]map[string]any, 0, len(req.Envelopes))
	for _, e := range req.Envelopes {
		seq, err := s.store.AddEnvelope(ctx, e, now)
		if err != nil {
			return nil, 0, err
		}
		accepted = append(accepted, map[string]any{"id": e.ID, "seq": seq})
	}
	s.logTouch(s.store.TouchDevice(ctx, d.ID, now))
	return map[string]any{"accepted": accepted}, http.StatusOK, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError renders an apiError as is. Any other error is an internal failure:
// the client gets a generic 500 and the operator gets the cause in the log
// (method and path only; never bodies, keys or query strings).
// logTouch records a failed last-seen update without failing the request.
func (s *Server) logTouch(err error) {
	if err != nil {
		s.cfg.Logger.Warn("last-seen update failed", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var api *apiError
	if !errors.As(err, &api) {
		s.cfg.Logger.Error("internal error", "method", r.Method, "path", r.URL.Path, "error", err)
		api = fail(http.StatusInternalServerError, "internal_error", "internal server error")
	}
	writeJSON(w, api.Status, map[string]any{"error": map[string]string{"code": api.Code, "message": api.Message}})
}

// clientIP returns the address used for rate limiting. Behind a trusted proxy
// it takes the RIGHTMOST X-Forwarded-For entry: that is the one the proxy
// itself appended (or set), while entries further left come from the client
// and can be forged to get a fresh rate-limit bucket per request.
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// rateLimiter is a per-IP token bucket.
type rateLimiter struct {
	mu      sync.Mutex
	perMin  float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{perMin: float64(perMinute), buckets: map[string]*bucket{}}
}

func (l *rateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[ip]
	if b == nil {
		b = &bucket{tokens: l.perMin, last: now}
		l.buckets[ip] = b
	}
	b.tokens = min(l.perMin, b.tokens+now.Sub(b.last).Minutes()*l.perMin)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, ip)
		}
	}
}
