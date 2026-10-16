package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

func TestClientIPUsesProxyAppendedEntry(t *testing.T) {
	s := &Server{cfg: Config{TrustProxy: true}}
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	if got := s.clientIP(r); got != "203.0.113.7" {
		t.Fatalf("forged leftmost entry was used: %s", got)
	}
	s.cfg.TrustProxy = false
	r.RemoteAddr = "192.0.2.1:5555"
	if got := s.clientIP(r); got != "192.0.2.1" {
		t.Fatalf("without trust-proxy the peer address must be used: %s", got)
	}
}

func TestReadBodyDistinguishesErrors(t *testing.T) {
	w := httptest.NewRecorder()
	if _, err := readBody(w, httptest.NewRequest(http.MethodGet, "/", strings.NewReader("{}")), 0, 10); !strings.Contains(err.Error(), "unexpected_body") {
		t.Fatalf("body on a no-body endpoint: %v", err)
	}
	if _, err := readBody(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 20))), 10, 10); !strings.Contains(err.Error(), "body_too_large") {
		t.Fatalf("oversized body: %v", err)
	}
	if body, err := readBody(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("abc")), -1, 10); err != nil || string(body) != "abc" {
		t.Fatalf("upload-limit body: %q %v", body, err)
	}
}

func TestInternalErrorsAreLogged(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	srv := NewServer(store, Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	account := protocol.NewAccountID()
	enc := protocol.RandomBytes(32)
	if _, err := store.UpsertAccount(context.Background(), Account{ID: account, SigPub: pub, EncPub: enc, PairingHash: protocol.PairingHash(enc)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store.Close() // every further query fails
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	protocol.Sign(priv, "GET", "/v1/status", protocol.AgentKey(account), nil, time.Now(), protocol.NewNonce()).Apply(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "internal error") {
		t.Fatalf("status %d, log %q", rec.Code, logs.String())
	}
}
