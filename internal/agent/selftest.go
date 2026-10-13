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
	enc, _ := ecdh.X25519().GenerateKey(rand.Reader)
	account := protocol.NewAccountID()
	loc, _ := time.LoadLocation("Europe/Berlin")
	if loc == nil {
		loc = time.UTC
	}
	var env1 protocol.Envelope
	var plain []byte
	if err := step("encrypt synthetic batch (X25519 + HKDF-SHA256 + ChaCha20-Poly1305)", func() error {
		plain, err = json.Marshal(healthdata.Synthetic(time.Now(), 14, loc, 1))
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
	if err := step("store in SQLite", func() error {
		db, err = OpenDB(filepath.Join(dir, "health.db"))
		if err != nil {
			return err
		}
		_, err = db.Apply(ctx, batch, EnvelopeResult{Seq: 1, ID: env1.ID, Kind: env1.Kind, Created: env1.CreatedAt, Status: "ok"}, time.Now())
		return err
	}); err != nil {
		return err
	}
	defer db.Close()
	return step("summarize and render dashboard", func() error {
		cov, err := db.Coverage(ctx)
		if err != nil {
			return err
		}
		w, err := ResolveWindow("week", 0, "", cov.LastDate, time.Now())
		if err != nil {
			return err
		}
		r, err := BuildReport(ctx, db, w, time.Now())
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
