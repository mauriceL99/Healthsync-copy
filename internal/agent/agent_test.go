package agent

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/client"
	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	"github.com/mauriceL99/Healthsync-copy/internal/relay"
)

func startRelay(t *testing.T) string {
	t.Helper()
	store, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := httptest.NewServer(relay.NewServer(store, relay.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, Env{Out: &out, Err: &errOut})
	return out.String(), errOut.String(), code
}

func TestEndToEnd(t *testing.T) {
	relayURL := startRelay(t)
	dir := t.TempDir()
	sd := "--state-dir=" + dir

	if _, e, code := run(t, sd, "onboard", "--relay", relayURL, "--name", "Test Agent", "--no-terminal-qr"); code != 0 {
		t.Fatalf("onboard: %s", e)
	}
	if out, _, _ := run(t, sd, "fetch"); !strings.Contains(out, "No new data") {
		t.Fatalf("empty fetch: %s", out)
	}
	if _, e, code := run(t, sd, "dev", "simulate-device", "--days", "40", "--seed", "3"); code != 0 {
		t.Fatalf("simulate: %s", e)
	}
	// A second device cannot link while the first one is linked.
	if _, e, code := run(t, sd, "dev", "simulate-device", "--days", "2"); code == 0 || !strings.Contains(e, "device_already_linked") {
		t.Fatalf("second device: %d %s", code, e)
	}
	out, e, code := run(t, sd, "fetch", "--json")
	if code != 0 {
		t.Fatalf("fetch: %s", e)
	}
	var res FetchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Days != 40 || res.Failed != 0 || res.Acked != res.LastSeq {
		t.Fatalf("fetch result: %+v %v", res, err)
	}
	out, _, code = run(t, sd, "summary", "--format", "json")
	var report Report
	if code != 0 || json.Unmarshal([]byte(out), &report) != nil || report.DaysWithData != 7 || len(report.Highlights) < 4 || report.Workouts.Count == 0 {
		t.Fatalf("summary: %d %s", code, out)
	}
	if out, _, _ := run(t, sd, "summary", "--period", "month"); !strings.Contains(out, "Steps") || !strings.Contains(out, "Not medical advice") {
		t.Fatalf("text summary: %s", out)
	}
	if out, _, code := run(t, sd, "series", "heart.hrv_sdnn_ms", "--days", "5", "--format", "json"); code != 0 || !strings.Contains(out, `"points"`) {
		t.Fatalf("series: %s", out)
	}
	if out, _, code := run(t, sd, "export", "--format", "csv", "--days", "3"); code != 0 || strings.Count(out, "\n") != 4 {
		t.Fatalf("export: %s", out)
	}
	html := filepath.Join(dir, "d.html")
	if _, e, code := run(t, sd, "dashboard", "--out", html); code != 0 {
		t.Fatalf("dashboard: %s", e)
	}
	if _, e, code := run(t, sd, "summary", "--out", html); code == 0 || !strings.Contains(e, "already exists") {
		t.Fatalf("overwrite protection: %s", e)
	}
	if out, _, _ := run(t, sd, "unlink"); !strings.Contains(out, "unlinked") {
		t.Fatalf("unlink: %s", out)
	}
	// After unlinking, the same pairing code links a new device.
	if _, e, code := run(t, sd, "dev", "simulate-device", "--days", "2"); code != 0 {
		t.Fatalf("re-pair: %s", e)
	}
	if _, e, code := run(t, sd, "fetch"); code != 0 {
		t.Fatalf("second fetch: %s", e)
	}
}

func TestFetchSkipsBadEnvelopes(t *testing.T) {
	ctx := context.Background()
	relayURL := startRelay(t)
	dir := t.TempDir()
	if _, e, code := run(t, "--state-dir", dir, "onboard", "--relay", relayURL, "--no-terminal-qr"); code != 0 {
		t.Fatal(e)
	}
	s, err := OpenState(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := protocol.DecodePairing(s.Config.PairingCode)
	s.Close()
	secret, _ := protocol.DecodeB64U(p.Secret, 32)
	encPub, _ := protocol.DecodeB64(p.Enc, 32)
	agentKey, _ := ecdh.X25519().NewPublicKey(encPub)
	devPub, devPriv, _ := ed25519.GenerateKey(rand.Reader)
	c, _ := client.New(relayURL, protocol.KeyNewDevice, devPriv, 5*time.Second)
	id, err := c.LinkDevice(ctx, p.Account, secret, devPub, "test", "ios", "1")
	if err != nil {
		t.Fatal(err)
	}
	dev := c.WithKey(protocol.DeviceKey(id))
	loc, _ := time.LoadLocation("Europe/Berlin")
	good, _ := json.Marshal(healthdata.Synthetic(time.Now(), 3, loc, 1))
	wrongKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	e1, _ := protocol.Seal(good, wrongKey.PublicKey(), protocol.SealParams{Account: p.Account})
	e2, _ := protocol.Seal([]byte(`{"schema":99}`), agentKey, protocol.SealParams{Account: p.Account})
	e3, _ := protocol.Seal(good, agentKey, protocol.SealParams{Account: p.Account})
	if _, err := dev.Upload(ctx, []protocol.Envelope{e1, e2, e3}); err != nil {
		t.Fatal(err)
	}
	s, _ = OpenState(dir)
	defer s.Close()
	res, err := Fetch(ctx, Env{Out: io.Discard, Err: io.Discard}, s, FetchOptions{})
	if err != nil || res.Envelopes != 3 || res.Failed != 2 || res.Days != 3 {
		t.Fatalf("fetch: %+v %v", res, err)
	}
	db, _ := OpenDB(s.DBPath())
	defer db.Close()
	cov, _ := db.Coverage(ctx)
	if cov.Failed != 2 || cov.Envelopes != 3 {
		t.Fatalf("envelope log: %+v", cov)
	}
}

func TestMCPProtocol(t *testing.T) {
	relayURL := startRelay(t)
	dir := t.TempDir()
	if _, e, code := run(t, "--state-dir", dir, "onboard", "--relay", relayURL, "--no-terminal-qr"); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := run(t, "--state-dir", dir, "dev", "simulate-device", "--days", "10"); code != 0 {
		t.Fatal(e)
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"health_sync","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"health_summary","arguments":{"period":"week"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"health_workouts","arguments":{"days":7,"bogus":1}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"unknown/method"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	m := &MCP{Env: Env{Out: io.Discard, Err: io.Discard}, StateDir: dir}
	if err := m.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 responses, got %d:\n%s", len(lines), out.String())
	}
	var responses []map[string]any
	for _, l := range lines {
		var r map[string]any
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, r)
	}
	if v := responses[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Fatalf("version negotiation: %v", v)
	}
	summary := responses[2]["result"].(map[string]any)
	if summary["isError"] != false || !strings.Contains(summary["content"].([]any)[0].(map[string]any)["text"].(string), "Health summary") {
		t.Fatalf("summary tool: %v", summary)
	}
	if responses[3]["result"].(map[string]any)["isError"] != true {
		t.Fatal("unknown argument accepted")
	}
	if responses[4]["error"].(map[string]any)["code"].(float64) != -32601 || responses[5]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("error codes: %v %v", responses[4], responses[5])
	}
}
