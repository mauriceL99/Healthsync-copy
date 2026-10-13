// Package relay implements the HealthBridge relay: a small mailbox that stores
// end-to-end encrypted envelopes between a paired iPhone and an agent. It
// never sees private keys or plaintext health data.
package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
  account      TEXT PRIMARY KEY,
  sig_pub      BLOB NOT NULL,
  enc_pub      BLOB NOT NULL,
  pairing_hash TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  device_id    TEXT PRIMARY KEY,
  account      TEXT NOT NULL REFERENCES accounts(account) ON DELETE CASCADE,
  sig_pub      BLOB NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  platform     TEXT NOT NULL DEFAULT '',
  app_version  TEXT NOT NULL DEFAULT '',
  linked_at    INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  unlinked_at  INTEGER
);
CREATE INDEX IF NOT EXISTS devices_by_account ON devices(account, unlinked_at);
CREATE TABLE IF NOT EXISTS envelopes (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  account     TEXT NOT NULL REFERENCES accounts(account) ON DELETE CASCADE,
  id          TEXT NOT NULL,
  body        TEXT NOT NULL,
  size        INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  UNIQUE(account, id)
);
CREATE INDEX IF NOT EXISTS envelopes_by_received ON envelopes(received_at);
CREATE TABLE IF NOT EXISTS nonces (
  key        TEXT NOT NULL,
  nonce      TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (key, nonce)
);
CREATE INDEX IF NOT EXISTS nonces_by_expiry ON nonces(expires_at);
`

// Errors returned by the store.
var (
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrDeviceUnlinked = errors.New("device unlinked")
	ErrReplay         = errors.New("nonce already used")
)

// Store is the relay's SQLite persistence.
type Store struct{ db *sql.DB }

// Account is a registered agent identity.
type Account struct {
	ID          string
	SigPub      []byte
	EncPub      []byte
	PairingHash string
	Name        string
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// Device is a linked (or formerly linked) iPhone.
type Device struct {
	ID         string     `json:"device_id"`
	Account    string     `json:"-"`
	SigPub     []byte     `json:"-"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	AppVersion string     `json:"app_version"`
	LinkedAt   time.Time  `json:"-"`
	LastSeenAt time.Time  `json:"-"`
	UnlinkedAt *time.Time `json:"-"`
}

// OpenStore opens (and migrates) the relay database at path.
func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate relay database: %w", err)
	}
	return &Store{db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// UseNonce records a nonce for key; it fails with ErrReplay when already used.
func (s *Store) UseNonce(ctx context.Context, key, nonce string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO nonces(key, nonce, expires_at) VALUES(?,?,?)`,
		key, nonce, now.Add(protocol.NonceWindow).Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrReplay
	}
	return nil
}

// UpsertAccount creates an account or updates pairing hash and name when the
// keys match. created reports whether a new account was inserted.
func (s *Store) UpsertAccount(ctx context.Context, a Account, now time.Time) (created bool, err error) {
	existing, err := s.Account(ctx, a.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		_, err = s.db.ExecContext(ctx, `INSERT INTO accounts(account, sig_pub, enc_pub, pairing_hash, name, created_at, last_seen_at) VALUES(?,?,?,?,?,?,?)`,
			a.ID, a.SigPub, a.EncPub, a.PairingHash, a.Name, now.Unix(), now.Unix())
		return err == nil, err
	case err != nil:
		return false, err
	}
	if string(existing.SigPub) != string(a.SigPub) || string(existing.EncPub) != string(a.EncPub) {
		return false, ErrConflict
	}
	_, err = s.db.ExecContext(ctx, `UPDATE accounts SET pairing_hash = ?, name = ?, last_seen_at = ? WHERE account = ?`,
		a.PairingHash, a.Name, now.Unix(), a.ID)
	return false, err
}

// Account loads one account.
func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	var a Account
	var created, seen int64
	err := s.db.QueryRowContext(ctx, `SELECT account, sig_pub, enc_pub, pairing_hash, name, created_at, last_seen_at FROM accounts WHERE account = ?`, id).
		Scan(&a.ID, &a.SigPub, &a.EncPub, &a.PairingHash, &a.Name, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt, a.LastSeenAt = time.Unix(created, 0), time.Unix(seen, 0)
	return a, err
}

// TouchAccount updates last_seen_at.
func (s *Store) TouchAccount(ctx context.Context, id string, now time.Time) {
	_, _ = s.db.ExecContext(ctx, `UPDATE accounts SET last_seen_at = ? WHERE account = ?`, now.Unix(), id)
}

// DeleteAccount removes an account with its devices and envelopes.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE account = ?`, id)
	return err
}

const deviceColumns = `device_id, account, sig_pub, name, platform, app_version, linked_at, last_seen_at, unlinked_at`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var linked, seen int64
	var unlinked sql.NullInt64
	err := row.Scan(&d.ID, &d.Account, &d.SigPub, &d.Name, &d.Platform, &d.AppVersion, &linked, &seen, &unlinked)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.LinkedAt, d.LastSeenAt = time.Unix(linked, 0), time.Unix(seen, 0)
	if unlinked.Valid {
		t := time.Unix(unlinked.Int64, 0)
		d.UnlinkedAt = &t
	}
	return d, err
}

// Device loads a device by ID (linked or not).
func (s *Store) Device(ctx context.Context, id string) (Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE device_id = ?`, id))
}

// ActiveDevice returns the linked device of an account.
func (s *Store) ActiveDevice(ctx context.Context, account string) (Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE account = ? AND unlinked_at IS NULL ORDER BY linked_at DESC LIMIT 1`, account))
}

// LinkDevice links a new device. Re-linking the same key is idempotent; a
// different key while another device is linked returns ErrConflict.
func (s *Store) LinkDevice(ctx context.Context, d Device, now time.Time) (Device, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	active, err := scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE account = ? AND unlinked_at IS NULL LIMIT 1`, d.Account))
	switch {
	case err == nil && string(active.SigPub) == string(d.SigPub):
		return active, tx.Commit()
	case err == nil:
		return d, ErrConflict
	case !errors.Is(err, ErrNotFound):
		return d, err
	}
	d.ID = protocol.NewDeviceID()
	d.LinkedAt, d.LastSeenAt = now, now
	if _, err = tx.ExecContext(ctx, `INSERT INTO devices(device_id, account, sig_pub, name, platform, app_version, linked_at, last_seen_at) VALUES(?,?,?,?,?,?,?,?)`,
		d.ID, d.Account, d.SigPub, d.Name, d.Platform, d.AppVersion, now.Unix(), now.Unix()); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// TouchDevice updates last_seen_at.
func (s *Store) TouchDevice(ctx context.Context, id string, now time.Time) {
	_, _ = s.db.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE device_id = ?`, now.Unix(), id)
}

// UnlinkDevice marks a device as unlinked.
func (s *Store) UnlinkDevice(ctx context.Context, id string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET unlinked_at = ? WHERE device_id = ? AND unlinked_at IS NULL`, now.Unix(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddEnvelope stores an envelope; a duplicate ID returns the existing seq.
func (s *Store) AddEnvelope(ctx context.Context, e protocol.Envelope, now time.Time) (int64, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	var seq int64
	err = s.db.QueryRowContext(ctx, `SELECT seq FROM envelopes WHERE account = ? AND id = ?`, e.Account, e.ID).Scan(&seq)
	if err == nil {
		return seq, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO envelopes(account, id, body, size, received_at) VALUES(?,?,?,?,?)`,
		e.Account, e.ID, string(body), len(e.Ct), now.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Envelopes lists envelopes after seq, limited by count and total ciphertext size.
func (s *Store) Envelopes(ctx context.Context, account string, after int64, limit int, maxBytes int) ([]protocol.StoredEnvelope, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, body, size, received_at FROM envelopes WHERE account = ? AND seq > ? ORDER BY seq LIMIT ?`, account, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []protocol.StoredEnvelope
	total, more := 0, false
	for rows.Next() {
		var seq, received int64
		var body string
		var size int
		if err := rows.Scan(&seq, &body, &size, &received); err != nil {
			return nil, false, err
		}
		if len(out) == limit || (len(out) > 0 && total+size > maxBytes) {
			more = true
			break
		}
		var e protocol.StoredEnvelope
		if err := json.Unmarshal([]byte(body), &e.Envelope); err != nil {
			return nil, false, err
		}
		e.Seq, e.ReceivedAt = seq, protocol.FormatTime(time.Unix(received, 0))
		out = append(out, e)
		total += size
	}
	return out, more, rows.Err()
}

// Ack deletes envelopes up to and including seq.
func (s *Store) Ack(ctx context.Context, account string, upTo int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM envelopes WHERE account = ? AND seq <= ?`, account, upTo)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Pending returns the number of stored envelopes and the oldest receive time.
func (s *Store) Pending(ctx context.Context, account string) (int64, *time.Time, error) {
	var n int64
	var oldest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(received_at) FROM envelopes WHERE account = ?`, account).Scan(&n, &oldest)
	if err != nil || !oldest.Valid {
		return n, nil, err
	}
	t := time.Unix(oldest.Int64, 0)
	return n, &t, nil
}

// Cleanup removes expired nonces and envelopes older than ttl.
func (s *Store) Cleanup(ctx context.Context, now time.Time, ttl time.Duration) (envelopes int64, err error) {
	if _, err = s.db.ExecContext(ctx, `DELETE FROM nonces WHERE expires_at < ?`, now.Unix()); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM envelopes WHERE received_at < ?`, now.Add(-ttl).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
