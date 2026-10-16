package agent

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	"github.com/mauriceL99/Healthsync-copy/internal/training"
)

// Workouts imported from watch files. Garmin offers no data API for personal
// projects, so the user exports the original FIT files (per activity, from the
// watch over USB, or the whole account export) and imports them; see
// docs/GARMIN.md.

const (
	maxFITBytes       = 32 << 20 // one activity file (real ones are a few MB)
	maxNestedZipBytes = 4 << 30  // one archive inside an archive (account export parts)
	maxZipDepth       = 3
	maxImportErrors   = 10 // failures listed by name; the rest are only counted
	maxImportListed   = 50 // activities listed in an import result
)

// ImportedActivity is one activity an import added or refreshed.
type ImportedActivity struct {
	ID         string  `json:"id"`
	Date       string  `json:"date"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name,omitempty"`
	Minutes    float64 `json:"minutes"`
	DistanceKm float64 `json:"distance_km,omitempty"`
	New        bool    `json:"new"`
}

// ImportResult reports what an import did.
type ImportResult struct {
	Files      int                `json:"fit_files"`
	Imported   int                `json:"imported"`
	Updated    int                `json:"already_known"`
	Skipped    int                `json:"skipped_not_activities"` // sleep, settings, monitoring, …
	Failed     int                `json:"failed"`
	Errors     []string           `json:"errors,omitempty"`
	Activities []ImportedActivity `json:"activities"`
	Unlisted   int                `json:"activities_not_listed,omitempty"`
}

type importer struct {
	ctx context.Context
	db  *DB
	loc *time.Location
	now time.Time
	tmp string // directory for archives nested in archives
	res ImportResult
}

// Import reads FIT files, ZIP archives (also nested, as in the Garmin account
// export) and folders, and stores every activity they contain. Files that are
// not activities are skipped; damaged files are counted and named.
func Import(ctx context.Context, db *DB, tmpDir string, paths []string, now time.Time) (ImportResult, error) {
	im, err := newImporter(ctx, db, tmpDir, now)
	if err != nil {
		return ImportResult{}, err
	}
	for _, p := range paths {
		if err := im.path(p); err != nil {
			return im.result(), err
		}
	}
	return im.result(), nil
}

func newImporter(ctx context.Context, db *DB, tmpDir string, now time.Time) (*importer, error) {
	loc, err := db.Location(ctx)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(tmpDir); err != nil {
		return nil, fmt.Errorf("create import directory: %w", err)
	}
	return &importer{ctx: ctx, db: db, loc: loc, now: now, tmp: tmpDir, res: ImportResult{Activities: []ImportedActivity{}}}, nil
}

func (im *importer) result() ImportResult {
	r := im.res
	// The same workout can arrive twice (watch copy and Connect export):
	// list it once, as new if any copy was.
	seen := map[string]int{}
	var unique []ImportedActivity
	for _, a := range r.Activities {
		if i, ok := seen[a.ID]; ok {
			unique[i].New = unique[i].New || a.New
			continue
		}
		seen[a.ID] = len(unique)
		unique = append(unique, a)
	}
	r.Activities = unique
	slices.SortFunc(r.Activities, func(a, b ImportedActivity) int { return strings.Compare(b.Date, a.Date) })
	if len(r.Activities) > maxImportListed {
		r.Unlisted = len(r.Activities) - maxImportListed
		r.Activities = r.Activities[:maxImportListed]
	}
	return r
}

func (im *importer) fail(name string, err error) {
	im.res.Failed++
	if len(im.res.Errors) < maxImportErrors {
		im.res.Errors = append(im.res.Errors, fmt.Sprintf("%s: %v", name, err))
	}
}

// path imports a file or, recursively, a folder. Only errors that stop the
// whole import (database, context) are returned.
func (im *importer) path(p string) error {
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return im.file(p)
	}
	return filepath.WalkDir(p, func(sub string, d fs.DirEntry, err error) error {
		if err != nil {
			im.fail(sub, err)
			return nil
		}
		if d.IsDir() || !isImportable(sub) {
			return nil
		}
		return im.file(sub)
	})
}

func isImportable(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".fit" || ext == ".zip" || ext == ".gz" // Connect sometimes packs .fit.gz
}

// gunzipFIT unpacks a gzip-compressed FIT file.
func gunzipFIT(r io.Reader) ([]byte, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, maxFITBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFITBytes {
		return nil, errors.New("file too large for an activity")
	}
	if !isFIT(data) {
		return nil, errors.New("compressed file is not a FIT file")
	}
	return data, nil
}

func (im *importer) file(p string) error {
	f, err := os.Open(p)
	if err != nil {
		im.fail(p, err)
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		im.fail(p, err)
		return nil
	}
	return im.reader(filepath.Base(p), f, info.Size())
}

// reader imports one FIT file or ZIP archive, told apart by content.
func (im *importer) reader(name string, r io.ReaderAt, size int64) error {
	head := make([]byte, 12)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		return im.zip(name, r, size, 0)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		data, err := gunzipFIT(io.NewSectionReader(r, 0, size))
		if err != nil {
			im.fail(name, err)
			return nil
		}
		return im.fit(name, data)
	case isFIT(head):
		if size > maxFITBytes {
			im.fail(name, fmt.Errorf("file too large for an activity (%d MB)", size>>20))
			return nil
		}
		data := make([]byte, size)
		if _, err := r.ReadAt(data, 0); err != nil && !errors.Is(err, io.EOF) {
			im.fail(name, err)
			return nil
		}
		return im.fit(name, data)
	}
	im.fail(name, errors.New("not a FIT file, .fit.gz or ZIP archive"))
	return nil
}

// isFIT checks the FIT file header signature (".FIT" at bytes 8–11).
func isFIT(head []byte) bool { return len(head) >= 12 && string(head[8:12]) == ".FIT" }

func (im *importer) zip(name string, r io.ReaderAt, size int64, depth int) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		im.fail(name, fmt.Errorf("unreadable ZIP archive: %w", err))
		return nil
	}
	for _, f := range zr.File {
		if err := im.ctx.Err(); err != nil {
			return err
		}
		if f.FileInfo().IsDir() || !isImportable(f.Name) {
			continue // account exports also hold JSON and images
		}
		entry := name + "/" + f.Name
		if strings.EqualFold(filepath.Ext(f.Name), ".zip") {
			if depth+1 >= maxZipDepth {
				im.fail(entry, errors.New("archives nested too deeply"))
				continue
			}
			if err := im.nestedZip(entry, f, depth+1); err != nil {
				return err
			}
			continue
		}
		data, err := readZipEntry(f, maxFITBytes)
		if err == nil && bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
			data, err = gunzipFIT(bytes.NewReader(data))
		}
		if err != nil {
			im.fail(entry, err)
			continue
		}
		if !isFIT(data) {
			im.fail(entry, errors.New("not a FIT file"))
			continue
		}
		if err := im.fit(entry, data); err != nil {
			return err
		}
	}
	return nil
}

// nestedZip spools an archive inside an archive to disk, since ZIP needs
// random access and account-export parts can be hundreds of megabytes.
func (im *importer) nestedZip(name string, f *zip.File, depth int) error {
	rc, err := f.Open()
	if err != nil {
		im.fail(name, err)
		return nil
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(im.tmp, "nested-*.zip")
	if err != nil {
		return fmt.Errorf("spool nested archive: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	n, err := io.Copy(tmp, io.LimitReader(rc, maxNestedZipBytes+1))
	if err != nil {
		im.fail(name, err)
		return nil
	}
	if n > maxNestedZipBytes {
		im.fail(name, errors.New("nested archive too large"))
		return nil
	}
	return im.zip(name, tmp, n, depth)
}

func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file too large for an activity")
	}
	return data, nil
}

// fit stores one FIT file. Only database errors are returned.
func (im *importer) fit(name string, data []byte) error {
	im.res.Files++
	a, err := training.Parse(data)
	switch {
	case errors.Is(err, training.ErrNotActivity):
		im.res.Skipped++
		return nil
	case err != nil:
		im.fail(name, err)
		return nil
	}
	date := training.LocalDate(a, im.loc)
	isNew, err := im.db.PutActivity(im.ctx, a, date, data, name, im.now)
	if err != nil {
		return fmt.Errorf("store activity from %s: %w", name, err)
	}
	if isNew {
		im.res.Imported++
	} else {
		im.res.Updated++
	}
	im.res.Activities = append(im.res.Activities, ImportedActivity{ID: a.ID, Date: date, Kind: a.Kind, Name: a.Name,
		Minutes: math.Round(a.Summary.TimerS / 60), DistanceKm: math.Round(a.Summary.DistanceM/100) / 10, New: isNew})
	return nil
}

// PutActivity stores or replaces an activity. It reports whether the
// activity was new; a re-import keeps the first import time.
func (d *DB) PutActivity(ctx context.Context, a *training.Activity, date string, fit []byte, source string, now time.Time) (bool, error) {
	summary, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var known int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM activities WHERE id = ?`, a.ID).Scan(&known); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO activities(id, date, start, kind, version, summary, fit, source, imported_at) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET date = excluded.date, start = excluded.start, kind = excluded.kind, version = excluded.version,
		summary = excluded.summary, fit = excluded.fit, source = excluded.source`,
		a.ID, date, protocol.FormatTime(a.Start), a.Kind, a.Version, string(summary), fit, source, protocol.FormatTime(now)); err != nil {
		return false, err
	}
	return known == 0, tx.Commit()
}

// Activities loads the activities with a local date in [from, to], oldest
// first. Activities stored by an older analysis version are analysed again
// from their original file; if that fails, the stored result is returned with
// a warning.
func (d *DB) Activities(ctx context.Context, from, to string) ([]*training.Activity, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, version, summary FROM activities WHERE date >= ? AND date <= ? ORDER BY start`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query activities: %w", err)
	}
	var out []*training.Activity
	var stale []int
	for rows.Next() {
		var id, summary string
		var version int
		if err := rows.Scan(&id, &version, &summary); err != nil {
			rows.Close()
			return nil, err
		}
		a := &training.Activity{}
		if err := json.Unmarshal([]byte(summary), a); err != nil {
			a = &training.Activity{} // re-analysed below
			version = -1
		}
		a.ID = id // the row is authoritative
		if version != training.AnalysisVersion {
			stale = append(stale, len(out))
		}
		out = append(out, a)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, i := range stale {
		fresh, err := d.reanalyze(ctx, out[i].ID)
		if err != nil {
			if out[i].Kind == "" {
				return nil, err // nothing usable stored
			}
			out[i].Warnings = append(out[i].Warnings, "re-analysis failed: "+err.Error())
			continue
		}
		fresh.Stream = nil
		out[i] = fresh
	}
	return out, nil
}

func (d *DB) reanalyze(ctx context.Context, id string) (*training.Activity, error) {
	a, err := d.Activity(ctx, id)
	if err != nil {
		return nil, err
	}
	loc, err := d.Location(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	if _, err := d.db.ExecContext(ctx, `UPDATE activities SET version = ?, kind = ?, summary = ?, date = ?, start = ? WHERE id = ?`,
		a.Version, a.Kind, string(summary), training.LocalDate(a, loc), protocol.FormatTime(a.Start), id); err != nil {
		return nil, fmt.Errorf("store re-analysed activity %s: %w", id, err)
	}
	return a, nil
}

// errNoActivity is returned for an unknown activity ID.
var errNoActivity = errors.New("no imported activity with this ID")

// Activity loads one activity from its original file, with the per-second
// stream.
func (d *DB) Activity(ctx context.Context, id string) (*training.Activity, error) {
	var fit []byte
	err := d.db.QueryRowContext(ctx, `SELECT fit FROM activities WHERE id = ?`, id).Scan(&fit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", errNoActivity, id)
	}
	if err != nil {
		return nil, err
	}
	a, err := training.Parse(fit)
	if err != nil {
		return nil, fmt.Errorf("analyse activity %s: %w", id, err)
	}
	return a, nil
}

// LatestActivityID returns the most recent activity, of one kind if kind is
// set. It is "" when none is stored.
func (d *DB) LatestActivityID(ctx context.Context, kind string) (string, error) {
	var id string
	err := d.db.QueryRowContext(ctx, `SELECT id FROM activities WHERE ? = '' OR kind = ? ORDER BY start DESC LIMIT 1`, kind, kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// DeleteActivities removes imported activities by ID and reports how many
// were stored.
func (d *DB) DeleteActivities(ctx context.Context, ids []string) (int, error) {
	n := 0
	for _, id := range ids {
		res, err := d.db.ExecContext(ctx, `DELETE FROM activities WHERE id = ?`, id)
		if err != nil {
			return n, err
		}
		k, err := res.RowsAffected()
		if err != nil {
			return n, err
		}
		n += int(k)
	}
	return n, nil
}

// DeleteActivitiesWithPrefix removes all activities whose ID starts with
// prefix (e.g. the synthetic samples of one made-up watch).
func (d *DB) DeleteActivitiesWithPrefix(ctx context.Context, prefix string) (int, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM activities WHERE substr(id, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
