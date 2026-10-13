package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	_ "modernc.org/sqlite"
)

const dbSchema = `
CREATE TABLE IF NOT EXISTS days (
  date       TEXT PRIMARY KEY,
  data       TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workouts (
  id         TEXT PRIMARY KEY,
  date       TEXT NOT NULL,
  type       TEXT NOT NULL,
  start      TEXT NOT NULL,
  data       TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS workouts_by_date ON workouts(date);
CREATE TABLE IF NOT EXISTS envelopes (
  seq          INTEGER PRIMARY KEY,
  id           TEXT NOT NULL,
  kind         TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  processed_at TEXT NOT NULL,
  status       TEXT NOT NULL,
  days         INTEGER NOT NULL DEFAULT 0,
  workouts     INTEGER NOT NULL DEFAULT 0,
  dropped      INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS quarantine (
  seq             INTEGER PRIMARY KEY,
  envelope        TEXT NOT NULL,
  error           TEXT NOT NULL,
  first_failed_at TEXT NOT NULL,
  last_attempt_at TEXT NOT NULL,
  attempts        INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// DB is the agent's local SQLite store of decrypted, sanitized data.
type DB struct{ db *sql.DB }

// OpenDB opens or creates the health database (mode 0600).
func OpenDB(path string) (*DB, error) {
	if err := touchPrivate(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(dbSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate health database: %w", err)
	}
	return &DB{db}, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// EnvelopeResult records how one envelope was processed.
type EnvelopeResult struct {
	Seq      int64
	ID       string
	Kind     string
	Created  string
	Status   string
	Report   healthdata.Report
	ErrorMsg string
}

// Apply merges a sanitized batch into the store and logs the envelope. It
// returns how many stored days were unreadable and therefore replaced.
func (d *DB) Apply(ctx context.Context, b healthdata.Batch, res EnvelopeResult, now time.Time) (repaired int, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stamp := protocol.FormatTime(now)
	for date, day := range b.Days {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT data FROM days WHERE date = ?`, date).Scan(&existing)
		merged := day
		switch {
		case err == nil:
			var old healthdata.Day
			if json.Unmarshal([]byte(existing), &old) == nil {
				merged = healthdata.MergeDay(old, day)
			} else {
				// A corrupt stored day cannot be merged. Replacing it with the
				// fresh data heals the store; the caller reports the count.
				repaired++
			}
		case !errors.Is(err, sql.ErrNoRows):
			return 0, err
		}
		data, err := json.Marshal(merged)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO days(date, data, updated_at) VALUES(?,?,?)
			ON CONFLICT(date) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, date, string(data), stamp); err != nil {
			return 0, err
		}
	}
	loc := b.Location()
	for _, w := range b.Workouts {
		data, err := json.Marshal(w)
		if err != nil {
			return 0, err
		}
		date := w.StartTime().In(loc).Format("2006-01-02")
		if _, err := tx.ExecContext(ctx, `INSERT INTO workouts(id, date, type, start, data, updated_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET date = excluded.date, type = excluded.type, start = excluded.start, data = excluded.data, updated_at = excluded.updated_at`,
			w.ID, date, w.Type, w.Start, string(data), stamp); err != nil {
			return 0, err
		}
	}
	if b.Timezone != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('timezone', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, b.Timezone); err != nil {
			return 0, err
		}
	}
	if err := logEnvelope(ctx, tx, res, stamp); err != nil {
		return 0, err
	}
	return repaired, tx.Commit()
}

// Location returns the device time zone of the latest batch. It is UTC when
// no batch named one (or the zone is unknown to this system's tz database);
// database errors are returned, not hidden behind UTC.
func (d *DB) Location(ctx context.Context) (*time.Location, error) {
	var tz string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'timezone'`).Scan(&tz)
	if errors.Is(err, sql.ErrNoRows) {
		return time.UTC, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read time zone: %w", err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// QuarantinedEnvelope is an envelope that could not be processed. It is kept
// locally (ciphertext only) so nothing is lost when the relay copy is deleted.
type QuarantinedEnvelope struct {
	Envelope protocol.StoredEnvelope
	Error    string
	Attempts int
}

// Quarantine stores or updates an envelope that failed to process.
func (d *DB) Quarantine(ctx context.Context, e protocol.StoredEnvelope, reason string, now time.Time) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	stamp := protocol.FormatTime(now)
	_, err = d.db.ExecContext(ctx, `INSERT INTO quarantine(seq, envelope, error, first_failed_at, last_attempt_at, attempts) VALUES(?,?,?,?,?,1)
		ON CONFLICT(seq) DO UPDATE SET error = excluded.error, last_attempt_at = excluded.last_attempt_at, attempts = attempts + 1`,
		e.Seq, string(data), reason, stamp, stamp)
	return err
}

// Quarantined lists quarantined envelopes in sequence order.
func (d *DB) Quarantined(ctx context.Context) ([]QuarantinedEnvelope, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT envelope, error, attempts FROM quarantine ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QuarantinedEnvelope
	for rows.Next() {
		var raw string
		var q QuarantinedEnvelope
		if err := rows.Scan(&raw, &q.Error, &q.Attempts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &q.Envelope); err != nil {
			return nil, fmt.Errorf("corrupt quarantined envelope: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Release removes an envelope from quarantine after it was processed.
func (d *DB) Release(ctx context.Context, seq int64) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM quarantine WHERE seq = ?`, seq)
	return err
}

// LogFailure records an envelope that could not be processed.
func (d *DB) LogFailure(ctx context.Context, res EnvelopeResult, now time.Time) error {
	return logEnvelope(ctx, d.db, res, protocol.FormatTime(now))
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func logEnvelope(ctx context.Context, x execer, r EnvelopeResult, stamp string) error {
	_, err := x.ExecContext(ctx, `INSERT OR REPLACE INTO envelopes(seq, id, kind, created_at, processed_at, status, days, workouts, dropped, error) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.Seq, r.ID, r.Kind, r.Created, stamp, r.Status, r.Report.Days, r.Report.Workouts,
		r.Report.DroppedDays+r.Report.DroppedValues+r.Report.DroppedWorkouts, r.ErrorMsg)
	return err
}

// Days loads all days in [from, to] (inclusive, YYYY-MM-DD).
func (d *DB) Days(ctx context.Context, from, to string) (map[string]healthdata.Day, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT date, data FROM days WHERE date >= ? AND date <= ? ORDER BY date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]healthdata.Day{}
	for rows.Next() {
		var date, data string
		if err := rows.Scan(&date, &data); err != nil {
			return nil, err
		}
		var day healthdata.Day
		if err := json.Unmarshal([]byte(data), &day); err != nil {
			return nil, fmt.Errorf("corrupt day %s: %w", date, err)
		}
		out[date] = day
	}
	return out, rows.Err()
}

// Workouts loads workouts with a local start date in [from, to].
func (d *DB) Workouts(ctx context.Context, from, to string) ([]healthdata.Workout, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT data FROM workouts WHERE date >= ? AND date <= ? ORDER BY start`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query workouts %s..%s: %w", from, to, err)
	}
	defer rows.Close()
	var out []healthdata.Workout
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("read workout row: %w", err)
		}
		var w healthdata.Workout
		if err := json.Unmarshal([]byte(data), &w); err != nil {
			return nil, fmt.Errorf("corrupt workout: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkoutDates maps workout IDs to their stored local start date, for workouts
// starting in [from, to].
func (d *DB) WorkoutDates(ctx context.Context, from, to string) (map[string]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, date FROM workouts WHERE date >= ? AND date <= ?`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, date string
		if err := rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		out[id] = date
	}
	return out, rows.Err()
}

// Coverage describes the stored date range.
type Coverage struct {
	FirstDate   string `json:"first_date,omitempty"`
	LastDate    string `json:"last_date,omitempty"`
	Days        int    `json:"days"`
	Workouts    int    `json:"workouts"`
	Envelopes   int    `json:"envelopes_processed"`
	Failed      int    `json:"envelopes_failed"`
	Quarantined int    `json:"envelopes_in_quarantine"`
}

// Coverage reports what is stored locally.
func (d *DB) Coverage(ctx context.Context) (Coverage, error) {
	var c Coverage
	var first, last sql.NullString
	if err := d.db.QueryRowContext(ctx, `SELECT MIN(date), MAX(date), COUNT(*) FROM days`).Scan(&first, &last, &c.Days); err != nil {
		return c, err
	}
	c.FirstDate, c.LastDate = first.String, last.String
	if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workouts`).Scan(&c.Workouts); err != nil {
		return c, err
	}
	if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(status != 'ok'), 0) FROM envelopes`).Scan(&c.Envelopes, &c.Failed); err != nil {
		return c, err
	}
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quarantine`).Scan(&c.Quarantined)
	return c, err
}

// MetricPaths lists metric paths that have at least one stored value.
func (d *DB) MetricPaths(ctx context.Context) (map[string]int, error) {
	days, err := d.Days(ctx, "0000-00-00", "9999-99-99")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, day := range days {
		for group, values := range day {
			for key := range values {
				if _, ok := healthdata.Lookup(group + "." + key); ok {
					counts[group+"."+key]++
				}
			}
		}
	}
	return counts, nil
}

// SortedDates returns the keys of a day map in ascending order.
func SortedDates(days map[string]healthdata.Day) []string {
	return slices.Sorted(maps.Keys(days))
}

func touchPrivate(path string) error {
	f, err := openPrivate(path)
	if err != nil {
		return err
	}
	return f.Close()
}
