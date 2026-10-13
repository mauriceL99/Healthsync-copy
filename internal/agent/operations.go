package agent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/mauriceL99/Healthsync-copy/internal/client"
	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// relayTimeout bounds every relay request of the CLI.
const relayTimeout = 30 * time.Second

// Env carries process-wide dependencies so commands are testable.
type Env struct {
	Out io.Writer
	Err io.Writer
	// Now replaces the wall clock (tests); nil means time.Now.
	Now func() time.Time
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// AgentClient builds a signed relay client for the agent identity.
func (e Env) AgentClient(s *State, id *Identity) (*client.Client, error) {
	c, err := client.New(s.Config.Relay, protocol.AgentKey(s.Config.Account), id.Sig, relayTimeout)
	if err != nil {
		return nil, err
	}
	c.Now = e.now
	c.RegistrationToken = s.Config.RelayToken
	return c, nil
}

// OnboardOptions configures `healthbridge onboard`.
type OnboardOptions struct {
	Relay          string
	RelayToken     string
	Name           string
	Rotate         bool
	NewPairingCode bool
	Offline        bool
	QRInTerminal   bool
}

// Onboard creates or reuses the identity, registers it at the relay and shows
// the pairing QR code.
func Onboard(ctx context.Context, env Env, s *State, o OnboardOptions) error {
	now := env.now()
	if s.Initialized() && o.Rotate {
		// Relay, name and registration token carry over unless given again. Check
		// them before archiving, so a bad --relay cannot leave no identity at all.
		o.Relay = strings.TrimRight(firstNonEmpty(o.Relay, s.Config.Relay), "/")
		if err := protocol.ValidateRelayURL(o.Relay); err != nil {
			return err
		}
		o.Name = firstNonEmpty(o.Name, s.Config.Name)
		o.RelayToken = firstNonEmpty(o.RelayToken, s.Config.RelayToken)
		backup, err := s.ArchiveIdentity(now)
		if err != nil {
			return fmt.Errorf("archive old identity: %w", err)
		}
		fmt.Fprintf(env.Out, "Previous identity archived to %s\n", backup)
		// The new account starts with an empty relay mailbox (fetch position 0);
		// local health data is kept.
		s.Config = Config{}
	}
	var id *Identity
	if s.Initialized() {
		var err error
		if id, err = s.LoadIdentity(); err != nil {
			return err
		}
		if o.Relay != "" && strings.TrimRight(o.Relay, "/") != s.Config.Relay {
			return errors.New("this identity is paired with another relay; use --rotate to create a new identity for a different relay")
		}
		if o.Name != "" {
			s.Config.Name = protocol.CleanLabel(o.Name)
		}
		if o.RelayToken != "" {
			s.Config.RelayToken = o.RelayToken
		}
		if o.NewPairingCode {
			if err := newPairingCode(s); err != nil {
				return err
			}
			fmt.Fprintln(env.Out, "Created a new pairing code; the previous QR code no longer works.")
		}
	} else {
		relay := strings.TrimRight(firstNonEmpty(o.Relay, os.Getenv("HEALTHBRIDGE_RELAY")), "/")
		if relay == "" {
			return errors.New("missing --relay URL (or HEALTHBRIDGE_RELAY); start your relay first, e.g. healthbridge-relay --addr :8787")
		}
		if err := protocol.ValidateRelayURL(relay); err != nil {
			return err
		}
		_, sig, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		enc, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		id = &Identity{Sig: sig, Enc: enc}
		name := o.Name
		if name == "" {
			name, _ = os.Hostname()
		}
		s.Config = Config{
			ConfigVersion: 1, Account: protocol.NewAccountID(), Relay: relay, Name: protocol.CleanLabel(name),
			SigPub: protocol.B64(sig.Public().(ed25519.PublicKey)), EncPub: protocol.B64(enc.PublicKey().Bytes()),
			CreatedAt: protocol.FormatTime(now), RelayToken: firstNonEmpty(o.RelayToken, os.Getenv("HEALTHBRIDGE_RELAY_TOKEN")),
		}
		if err := s.SaveIdentity(id); err != nil {
			return err
		}
		if err := newPairingCode(s); err != nil {
			return err
		}
	}
	if err := s.Save(); err != nil {
		return err
	}
	// Create the private (0600) database and its schema now, so the state
	// directory is complete before the first fetch.
	db, err := OpenDB(s.DBPath())
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if o.Offline {
		fmt.Fprintln(env.Out, "Offline mode: the identity was not registered at the relay. Run onboard again without --offline before pairing.")
	} else {
		if err := register(ctx, env, s, id); err != nil {
			return err
		}
	}
	return showPairing(env, s, o.QRInTerminal)
}

func newPairingCode(s *State) error {
	p := protocol.Pairing{
		V: protocol.Version, Relay: s.Config.Relay, Account: s.Config.Account,
		Sig: s.Config.SigPub, Enc: s.Config.EncPub, Secret: protocol.B64U(protocol.RandomBytes(32)), Name: s.Config.Name,
	}
	code, err := p.Encode()
	if err != nil {
		return err
	}
	// The safety code is what the user compares against the phone to rule out a
	// swapped QR code, so it must never silently end up empty.
	safety, err := p.SafetyCode()
	if err != nil {
		return fmt.Errorf("derive safety code: %w", err)
	}
	s.Config.PairingCode, s.Config.SafetyCode = code, safety
	s.Config.Registered = false
	return nil
}

func register(ctx context.Context, env Env, s *State, id *Identity) error {
	p, err := protocol.DecodePairing(s.Config.PairingCode)
	if err != nil {
		return fmt.Errorf("stored pairing code is invalid: %w", err)
	}
	secret, _ := protocol.DecodeB64U(p.Secret, 32)
	c, err := env.AgentClient(s, id)
	if err != nil {
		return err
	}
	created, err := c.RegisterAccount(ctx, s.Config.Account, id.Sig.Public().(ed25519.PublicKey), id.Enc.PublicKey().Bytes(), protocol.PairingHash(secret), s.Config.Name)
	if err != nil {
		return fmt.Errorf("register at relay %s: %w", s.Config.Relay, err)
	}
	if created && s.Config.LastSeq != 0 {
		// The relay did not know this account any more (e.g. its database was
		// reset), so its sequence numbers start at 1 again. Keeping the old fetch
		// position would skip every new upload until they expire.
		fmt.Fprintf(env.Err, "The relay registered this account anew; resetting the fetch position (was %d).\n", s.Config.LastSeq)
		s.Config.LastSeq = 0
	}
	s.Config.Registered = true
	return s.Save()
}

func showPairing(env Env, s *State, terminal bool) error {
	qr, err := qrcode.New(s.Config.PairingCode, qrcode.Medium)
	if err != nil {
		return err
	}
	png, err := qr.PNG(512)
	if err != nil {
		return err
	}
	if err := writePrivateAtomic(s.QRPath(), png); err != nil {
		return err
	}
	if terminal {
		fmt.Fprintln(env.Out, qr.ToSmallString(false))
	}
	fmt.Fprintf(env.Out, `HealthBridge identity ready.
  State dir:    %s
  Relay:        %s
  Agent name:   %s
  Safety code:  %s
  QR image:     %s

Pair your iPhone:
  1. Open HealthBridge on the iPhone and choose "Pair with agent".
  2. Scan the QR code (or paste the pairing code below).
  3. Check that the app shows the same safety code: %s
  4. Choose metrics, allow Apple Health access and run the first sync.
  5. Then run: healthbridge fetch && healthbridge summary

Pairing code (treat like a password; it lets a device upload data to you):
%s
`, s.Dir, s.Config.Relay, s.Config.Name, s.Config.SafetyCode, s.QRPath(), s.Config.SafetyCode, s.Config.PairingCode)
	return nil
}

// ShowPairing re-prints the QR code and pairing code of an existing identity.
func ShowPairing(env Env, s *State, terminal bool) error {
	if err := s.RequireInit(); err != nil {
		return err
	}
	return showPairing(env, s, terminal)
}

// FetchOptions configures `healthbridge fetch`.
type FetchOptions struct {
	KeepOnRelay bool
}

// FetchResult summarizes one fetch run.
type FetchResult struct {
	Envelopes    int    `json:"envelopes"`
	Failed       int    `json:"failed"`
	Recovered    int    `json:"recovered_from_quarantine"`
	Quarantined  int    `json:"in_quarantine"`
	RepairedDays int    `json:"repaired_days"`
	Days         int    `json:"days_updated"`
	Workouts     int    `json:"workouts_updated"`
	Dropped      int    `json:"values_dropped"`
	Acked        int64  `json:"deleted_on_relay"`
	LastSeq      int64  `json:"last_seq"`
	FirstDate    string `json:"first_date,omitempty"`
	LastDate     string `json:"last_date,omitempty"`
}

// Fetch downloads, decrypts, validates and stores all pending envelopes.
func Fetch(ctx context.Context, env Env, s *State, o FetchOptions) (res FetchResult, err error) {
	now := env.now()
	defer func() {
		s.Config.LastFetchAt = protocol.FormatTime(now)
		if err != nil {
			s.Config.LastFetchStatus, s.Config.LastFetchError = "error", err.Error()
		} else {
			s.Config.LastFetchStatus, s.Config.LastFetchError = "ok", ""
			s.Config.LastSuccessAt = s.Config.LastFetchAt
		}
		if saveErr := s.Save(); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	id, err := s.LoadIdentity()
	if err != nil {
		return res, err
	}
	c, err := env.AgentClient(s, id)
	if err != nil {
		return res, err
	}
	db, err := OpenDB(s.DBPath())
	if err != nil {
		return res, err
	}
	defer db.Close()
	f := &fetchRun{ctx: ctx, env: env, s: s, key: id.Enc, db: db, now: now, days: map[string]bool{}}
	if err := f.retryQuarantine(); err != nil {
		return f.res, err
	}
	if err := f.drainRelay(c); err != nil {
		return f.res, err
	}
	res = f.res
	cov, err := db.Coverage(ctx)
	if err != nil {
		return res, err
	}
	res.Quarantined = cov.Quarantined
	res.Days = len(f.days)
	for d := range f.days {
		if res.FirstDate == "" || d < res.FirstDate {
			res.FirstDate = d
		}
		if d > res.LastDate {
			res.LastDate = d
		}
	}
	if !o.KeepOnRelay && res.LastSeq > 0 && res.Envelopes > 0 {
		if res.Acked, err = c.Ack(ctx, res.LastSeq); err != nil {
			return res, fmt.Errorf("data was stored, but deleting it from the relay failed: %w", err)
		}
	}
	return res, nil
}

// fetchRun holds the state of one Fetch.
type fetchRun struct {
	ctx  context.Context
	env  Env
	s    *State
	key  *ecdh.PrivateKey
	db   *DB
	now  time.Time
	res  FetchResult
	days map[string]bool
}

// process stores one envelope and returns why it could not be read, if so.
// Envelopes that cannot be opened or parsed go to the local quarantine: the
// relay copy is acked like any other, so a bad upload cannot block the mailbox,
// but the ciphertext is kept and retried on every fetch (an updated CLI may read
// what this one cannot).
func (f *fetchRun) process(e protocol.StoredEnvelope) (failure error, err error) {
	r := EnvelopeResult{Seq: e.Seq, ID: e.ID, Kind: e.Kind, Created: e.CreatedAt, Status: "ok"}
	batch, rep, perr := openEnvelope(e.Envelope, f.s.Config.Account, f.key)
	if perr != nil {
		r.Status, r.ErrorMsg = "error", perr.Error()
		if err := f.db.Quarantine(f.ctx, e, perr.Error(), f.now); err != nil {
			return perr, err
		}
		return perr, f.db.LogFailure(f.ctx, r, f.now)
	}
	r.Report = rep
	repaired, err := f.db.Apply(f.ctx, batch, r, f.now)
	if err != nil {
		return nil, err
	}
	f.res.RepairedDays += repaired
	for d := range batch.Days {
		f.days[d] = true
	}
	f.res.Workouts += len(batch.Workouts)
	f.res.Dropped += rep.DroppedDays + rep.DroppedValues + rep.DroppedWorkouts
	return nil, nil
}

// retryQuarantine processes the locally kept envelopes again and releases the
// ones that can be read now.
func (f *fetchRun) retryQuarantine() error {
	quarantined, err := f.db.Quarantined(f.ctx)
	if err != nil {
		return err
	}
	for _, q := range quarantined {
		failure, err := f.process(q.Envelope)
		if err != nil {
			return err
		}
		if failure == nil {
			if err := f.db.Release(f.ctx, q.Envelope.Seq); err != nil {
				return err
			}
			f.res.Recovered++
		}
	}
	return nil
}

// drainRelay processes every new envelope on the relay, page by page, and
// saves the position after each page.
func (f *fetchRun) drainRelay(c *client.Client) error {
	cursor := f.s.Config.LastSeq
	defer func() { f.res.LastSeq = cursor }()
	for {
		envelopes, more, err := c.Envelopes(f.ctx, cursor, 50)
		if err != nil {
			return fmt.Errorf("fetch envelopes: %w", err)
		}
		for _, e := range envelopes {
			failure, err := f.process(e)
			if err != nil {
				return err
			}
			if failure != nil {
				f.res.Failed++
				fmt.Fprintf(f.env.Err, "warning: envelope %d could not be read (%v); kept in local quarantine and retried on the next fetch\n", e.Seq, failure)
			}
			f.res.Envelopes++
			cursor = e.Seq
			f.s.Config.LastSeq = cursor
		}
		if err := f.s.Save(); err != nil {
			return err
		}
		if !more || len(envelopes) == 0 {
			return nil
		}
	}
}

func openEnvelope(e protocol.Envelope, account string, key *ecdh.PrivateKey) (healthdata.Batch, healthdata.Report, error) {
	plain, err := protocol.Open(e, account, key)
	if err != nil {
		return healthdata.Batch{}, healthdata.Report{}, err
	}
	return healthdata.Parse(plain)
}

// LastFetch summarizes the most recent fetch (empty strings when none ran yet).
type LastFetch struct {
	At            string `json:"at"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	LastSuccessAt string `json:"last_success_at"`
}

// StatusReport is printed by `healthbridge status`.
type StatusReport struct {
	StateDir   string         `json:"state_dir"`
	Relay      string         `json:"relay"`
	AgentName  string         `json:"agent_name"`
	SafetyCode string         `json:"safety_code"`
	Registered bool           `json:"registered"`
	LastFetch  LastFetch      `json:"last_fetch"`
	Local      Coverage       `json:"local_data"`
	RelayState *client.Status `json:"relay_status,omitempty"`
	RelayError string         `json:"relay_error,omitempty"`
}

// Status collects local and (optionally) relay state.
func Status(ctx context.Context, env Env, s *State, offline bool) (StatusReport, error) {
	if err := s.RequireInit(); err != nil {
		return StatusReport{}, err
	}
	r := StatusReport{
		StateDir: s.Dir, Relay: s.Config.Relay, AgentName: s.Config.Name, SafetyCode: s.Config.SafetyCode, Registered: s.Config.Registered,
		LastFetch: LastFetch{At: s.Config.LastFetchAt, Status: s.Config.LastFetchStatus, Error: s.Config.LastFetchError, LastSuccessAt: s.Config.LastSuccessAt},
	}
	db, err := OpenDB(s.DBPath())
	if err != nil {
		return r, err
	}
	defer db.Close()
	if r.Local, err = db.Coverage(ctx); err != nil {
		return r, err
	}
	if offline {
		return r, nil
	}
	id, err := s.LoadIdentity()
	if err != nil {
		return r, err
	}
	c, err := env.AgentClient(s, id)
	if err != nil {
		return r, err
	}
	if st, err := c.Status(ctx); err != nil {
		r.RelayError = err.Error()
	} else {
		r.RelayState = &st
	}
	return r, nil
}

// Unlink removes the paired device at the relay; local keys and data stay.
func Unlink(ctx context.Context, env Env, s *State, deleteAccount bool) error {
	id, err := s.LoadIdentity()
	if err != nil {
		return err
	}
	c, err := env.AgentClient(s, id)
	if err != nil {
		return err
	}
	if deleteAccount {
		if err := c.DeleteAccount(ctx); err != nil {
			return err
		}
		s.Config.Registered = false
		if err := s.Save(); err != nil {
			return err
		}
		fmt.Fprintln(env.Out, "Relay account, linked device and pending envelopes were deleted. Local keys and health data were kept.")
		fmt.Fprintln(env.Out, "Run `healthbridge onboard` to register this identity again.")
		return nil
	}
	unlinked, err := c.UnlinkCurrent(ctx)
	if err != nil {
		return err
	}
	if unlinked {
		fmt.Fprintln(env.Out, "iPhone unlinked. Local keys and health data were kept; the same QR code can pair again.")
	} else {
		fmt.Fprintln(env.Out, "No device was linked.")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
