// Package client is a signed HTTP client for the HealthBridge relay API, used
// by the agent CLI and by the device simulator.
package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// maxResponseBytes bounds a relay response. A fetch page carries at most about
// 32 MiB of ciphertext (relay MaxFetchBytes); base64 and JSON framing add about a
// third, so 96 MiB leaves headroom without letting a broken relay exhaust memory.
const maxResponseBytes = 96 << 20

// APIError is an error response from the relay.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("relay error %d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode reports whether err is an APIError with the given code.
func IsCode(err error, code string) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == code
}

// Client signs every request with one Ed25519 key.
type Client struct {
	// RegistrationToken is sent as HB-Registration-Token when registering an account.
	RegistrationToken string

	base *url.URL
	http *http.Client
	key  string
	priv ed25519.PrivateKey
	Now  func() time.Time
}

// New creates a client for the relay base URL with HB-Key key.
func New(baseURL, key string, priv ed25519.PrivateKey, timeout time.Duration) (*Client, error) {
	if err := protocol.ValidateRelayURL(baseURL); err != nil {
		return nil, err
	}
	base, _ := url.Parse(strings.TrimRight(baseURL, "/"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &Client{
		base: base, key: key, priv: priv, Now: time.Now,
		http: &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("relay redirects are not followed")
		}},
	}, nil
}

// WithKey returns a copy that signs with a different HB-Key value.
func (c *Client) WithKey(key string) *Client {
	copy := *c
	copy.key = key
	return &copy
}

// Do sends a signed JSON request and decodes the JSON response into out.
func (c *Client) Do(ctx context.Context, method, pathAndQuery string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "healthbridge-cli")
	if c.RegistrationToken != "" && pathAndQuery == "/v1/accounts" {
		req.Header.Set("HB-Registration-Token", c.RegistrationToken)
	}
	protocol.Sign(c.priv, method, c.base.Path+pathAndQuery, c.key, body, c.Now(), protocol.NewNonce()).Apply(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("relay request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error.Code == "" {
			e.Error.Code, e.Error.Message = "http_"+strconv.Itoa(resp.StatusCode), http.StatusText(resp.StatusCode)
		}
		return &APIError{resp.StatusCode, e.Error.Code, e.Error.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("relay returned invalid JSON")
	}
	return nil
}

// Health checks GET /healthz without authentication.
func (c *Client) Health(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out) != nil || !out.OK {
		return "", fmt.Errorf("relay health check failed (HTTP %d)", resp.StatusCode)
	}
	return out.Version, nil
}

// Device is the linked device as reported by GET /v1/status.
type Device struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	LinkedAt   string `json:"linked_at"`
	LastSeenAt string `json:"last_seen_at"`
}

// Status is the response of GET /v1/status.
type Status struct {
	Account          string  `json:"account"`
	Device           *Device `json:"device"`
	PendingEnvelopes int64   `json:"pending_envelopes"`
	OldestPendingAt  *string `json:"oldest_pending_at"`
}

// RegisterAccount publishes the agent's public keys and pairing hash.
func (c *Client) RegisterAccount(ctx context.Context, account string, sig, enc []byte, pairingHash, name string) (bool, error) {
	var out struct {
		Created bool `json:"created"`
	}
	err := c.Do(ctx, http.MethodPost, "/v1/accounts", map[string]string{
		"account": account, "sig": protocol.B64(sig), "enc": protocol.B64(enc), "pairing_hash": pairingHash, "name": name,
	}, &out)
	return out.Created, err
}

// Status fetches account status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	return out, c.Do(ctx, http.MethodGet, "/v1/status", nil, &out)
}

// Envelopes lists stored envelopes after seq.
func (c *Client) Envelopes(ctx context.Context, after int64, limit int) ([]protocol.StoredEnvelope, bool, error) {
	var out struct {
		Envelopes []protocol.StoredEnvelope `json:"envelopes"`
		More      bool                      `json:"more"`
	}
	err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v1/envelopes?after=%d&limit=%d", after, limit), nil, &out)
	return out.Envelopes, out.More, err
}

// Ack deletes envelopes up to seq on the relay.
func (c *Client) Ack(ctx context.Context, upTo int64) (int64, error) {
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	return out.Deleted, c.Do(ctx, http.MethodPost, "/v1/envelopes/ack", map[string]int64{"up_to": upTo}, &out)
}

// UnlinkCurrent unlinks the account's device.
func (c *Client) UnlinkCurrent(ctx context.Context) (bool, error) {
	var out struct {
		Unlinked bool `json:"unlinked"`
	}
	return out.Unlinked, c.Do(ctx, http.MethodDelete, "/v1/devices/current", nil, &out)
}

// DeleteAccount removes the account and all relay data.
func (c *Client) DeleteAccount(ctx context.Context) error {
	return c.Do(ctx, http.MethodDelete, "/v1/accounts/self", nil, nil)
}

// LinkDevice registers a device with a pairing secret (key must be new-device).
func (c *Client) LinkDevice(ctx context.Context, account string, secret, deviceSig []byte, name, platform, appVersion string) (string, error) {
	var out struct {
		DeviceID string `json:"device_id"`
	}
	err := c.Do(ctx, http.MethodPost, "/v1/devices", map[string]string{
		"account": account, "secret": protocol.B64U(secret), "device_sig": protocol.B64(deviceSig),
		"device_name": name, "platform": platform, "app_version": appVersion,
	}, &out)
	return out.DeviceID, err
}

// Upload sends envelopes as a device.
func (c *Client) Upload(ctx context.Context, envelopes []protocol.Envelope) ([]int64, error) {
	var out struct {
		Accepted []struct {
			Seq int64 `json:"seq"`
		} `json:"accepted"`
	}
	if err := c.Do(ctx, http.MethodPost, "/v1/envelopes", map[string]any{"envelopes": envelopes}, &out); err != nil {
		return nil, err
	}
	seqs := make([]int64, len(out.Accepted))
	for i, a := range out.Accepted {
		seqs[i] = a.Seq
	}
	return seqs, nil
}

// UnlinkSelf unlinks the calling device.
func (c *Client) UnlinkSelf(ctx context.Context) error {
	return c.Do(ctx, http.MethodDelete, "/v1/devices/self", nil, nil)
}
