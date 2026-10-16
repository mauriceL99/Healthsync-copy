package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postMCP(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(data) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out
}

func TestMCPOverHTTP(t *testing.T) {
	relayURL := startRelay(t)
	dir := t.TempDir()
	if _, e, code := run(t, "--state-dir", dir, "onboard", "--relay", relayURL, "--no-terminal-qr"); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := run(t, "--state-dir", dir, "dev", "simulate-device", "--days", "10"); code != 0 {
		t.Fatal(e)
	}
	urlOut, e, code := run(t, "--state-dir", dir, "connector-url", "--public-url", "http://localhost:1")
	if code != 0 {
		t.Fatal(e)
	}
	token := strings.TrimPrefix(strings.TrimSpace(urlOut), "http://localhost:1/mcp/")

	m := &MCP{Env: Env{Out: io.Discard, Err: io.Discard}, StateDir: dir}
	srv := httptest.NewServer(m.HTTPHandler(func() (string, error) { return readToken(dir, mcpTokenFile) }, func() (string, error) { return readToken(dir, uploadTokenFile) }))
	defer srv.Close()
	endpoint := srv.URL + "/mcp/" + token

	status, init := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if status != 200 || init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %d %v", status, init)
	}
	if status, _ := postMCP(t, endpoint, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); status != http.StatusAccepted {
		t.Fatalf("notification: %d", status)
	}
	status, sync := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"health_sync","arguments":{}}}`)
	if status != 200 || !strings.Contains(sync["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string), "10 days") {
		t.Fatalf("health_sync: %d %v", status, sync)
	}
	status, summary := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"health_summary","arguments":{"period":"week"}}}`)
	if status != 200 || summary["result"].(map[string]any)["isError"] != false {
		t.Fatalf("health_summary: %d %v", status, summary)
	}

	// Batches answer every request in one array.
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(`[{"jsonrpc":"2.0","id":4,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":5,"method":"tools/list"}]`))
	if err != nil {
		t.Fatal(err)
	}
	var batch []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&batch)
	resp.Body.Close()
	if len(batch) != 2 {
		t.Fatalf("batch: %v", batch)
	}

	if status, body := postMCP(t, endpoint, `not json`); status != 400 || body["error"] == nil {
		t.Fatalf("parse error: %d %v", status, body)
	}
	if status, _ := postMCP(t, srv.URL+"/mcp/wrong-"+token, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); status != 404 {
		t.Fatalf("wrong token: %d", status)
	}
	if resp, _ := http.Get(endpoint); resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "POST" {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	if status, _ := postMCP(t, endpoint, `{"x":"`+strings.Repeat("a", maxMCPRequestBytes)+`"}`); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", status)
	}

	// Rotating the secret takes effect without a restart.
	newURL, e, code := run(t, "--state-dir", dir, "connector-url", "--public-url", "http://localhost:1", "--rotate")
	if code != 0 {
		t.Fatal(e)
	}
	newToken := strings.TrimPrefix(strings.TrimSpace(newURL), "http://localhost:1/mcp/")
	if status, _ := postMCP(t, endpoint, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); status != 404 {
		t.Fatalf("old token after rotate: %d", status)
	}
	if status, _ := postMCP(t, srv.URL+"/mcp/"+newToken, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); status != 200 {
		t.Fatalf("new token: %d", status)
	}
}

func TestFetchLoopStoresNewData(t *testing.T) {
	relayURL := startRelay(t)
	dir := t.TempDir()
	if _, e, code := run(t, "--state-dir", dir, "onboard", "--relay", relayURL, "--no-terminal-qr"); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := run(t, "--state-dir", dir, "dev", "simulate-device", "--days", "5"); code != 0 {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var log strings.Builder
	done := make(chan struct{})
	go func() {
		fetchLoop(ctx, Env{Out: io.Discard, Err: &log}, dir, time.Hour, &noLock{})
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var days int
		_ = withDB(context.Background(), dir, func(_ *State, _ *DB, cov Coverage) error {
			days = cov.Days
			return nil
		})
		if days == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background fetch stored %d days; log: %s", days, log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestConnectorURLRequiresHTTPS(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"https://1-2-3-4.sslip.io/", "https://1-2-3-4.sslip.io/mcp/T"},
		{"https://example.com/base", "https://example.com/base/mcp/T"},
		{"http://localhost:8788", "http://localhost:8788/mcp/T"},
		{"http://example.com", ""},
		{"https://example.com/?x=1", ""},
		{"example.com", ""},
	} {
		got, err := ConnectorURL(tc.in, "T")
		if (tc.want == "") != (err != nil) || got != tc.want {
			t.Errorf("ConnectorURL(%q) = %q, %v", tc.in, got, err)
		}
	}
}

type noLock struct{}

func (*noLock) Lock()   {}
func (*noLock) Unlock() {}
