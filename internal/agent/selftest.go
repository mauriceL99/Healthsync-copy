package agent

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	"github.com/mauriceL99/Healthsync-copy/internal/version"
)

// SelfTest exercises crypto, validation, storage and reporting offline in a
// temporary directory. It never touches the real state or the network.
func SelfTest(ctx context.Context, env Env) error {
	step := func(name string, fn func() error) error {
		if err := fn(); err != nil {
			fmt.Fprintf(env.Out, "  FAIL %s: %v\n", name, err)
			return fmt.Errorf("self-test failed at %s", name)
		}
		fmt.Fprintf(env.Out, "  ok   %s\n", name)
		return nil
	}
	fmt.Fprintf(env.Out, "healthbridge %s self-test\n", version.Version)
	dir, err := os.MkdirTemp("", "healthbridge-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	enc, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	account := protocol.NewAccountID()
	// A zone with daylight saving time exercises local-day handling; systems
	// without a time-zone database still run every other check in UTC.
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		loc = time.UTC
	}
	now := env.now()
	var env1 protocol.Envelope
	var plain []byte
	if err := step("encrypt synthetic batch (X25519 + HKDF-SHA256 + ChaCha20-Poly1305)", func() error {
		plain, err = json.Marshal(healthdata.Synthetic(now, 14, loc, 1))
		if err != nil {
			return err
		}
		env1, err = protocol.Seal(plain, enc.PublicKey(), protocol.SealParams{Account: account})
		return err
	}); err != nil {
		return err
	}
	var batch healthdata.Batch
	if err := step("decrypt, authenticate and validate", func() error {
		out, err := protocol.Open(env1, account, enc)
		if err != nil {
			return err
		}
		var rep healthdata.Report
		batch, rep, err = healthdata.Parse(out)
		if err != nil {
			return err
		}
		if rep.Days != 14 {
			return fmt.Errorf("expected 14 days, got %s", rep)
		}
		tampered := env1
		tampered.Kind = protocol.KindBackfill
		if _, err := protocol.Open(tampered, account, enc); err == nil {
			return fmt.Errorf("tampered envelope was accepted")
		}
		return nil
	}); err != nil {
		return err
	}
	var db *DB
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	if err := step("store in SQLite", func() error {
		db, err = OpenDB(filepath.Join(dir, "health.db"))
		if err != nil {
			return err
		}
		_, err = db.Apply(ctx, batch, EnvelopeResult{Seq: 1, ID: env1.ID, Kind: env1.Kind, Created: env1.CreatedAt, Status: "ok"}, now)
		return err
	}); err != nil {
		return err
	}
	if err := step("import and analyse a synthetic Garmin run (FIT)", func() error {
		paths, err := WriteSampleFIT(filepath.Join(dir, "fit"), now)
		if err != nil {
			return err
		}
		res, err := Import(ctx, db, filepath.Join(dir, "tmp"), paths[:1], now)
		if err != nil {
			return err
		}
		if res.Imported != 1 {
			return fmt.Errorf("expected 1 imported activity, got %+v", res)
		}
		d, err := ActivityDetailFor(ctx, db, "", "", false, 0)
		if err != nil {
			return err
		}
		if b := d.Activity.Bests; len(b) < 2 || b[1].Label != "1 km" || b[1].Seconds != 250 {
			return fmt.Errorf("best 1 km is wrong: %+v", b)
		}
		return nil
	}); err != nil {
		return err
	}
	return step("summarize and render dashboard", func() error {
		cov, err := db.Coverage(ctx)
		if err != nil {
			return err
		}
		w, err := ResolveWindow("week", 0, "", cov.LastDate, now)
		if err != nil {
			return err
		}
		r, err := BuildReport(ctx, db, w, now)
		if err != nil {
			return err
		}
		if len(r.Metrics) == 0 || !strings.Contains(RenderText(r), "Steps") {
			return fmt.Errorf("summary is empty")
		}
		_, err = Dashboard(ctx, db, w, r)
		return err
	})
}
