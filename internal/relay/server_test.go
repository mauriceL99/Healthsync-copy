package relay_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/client"
	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
	"github.com/mauriceL99/Healthsync-copy/internal/relay"
)

type fixture struct {
	srv       *httptest.Server
	account   string
	agent     *client.Client
	enc       *ecdh.PrivateKey
	secret    []byte
	agentPriv ed25519.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := httptest.NewServer(relay.NewServer(store, relay.Config{Version: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(srv.Close)
	sigPub, sigPriv, _ := ed25519.GenerateKey(rand.Reader)
	enc, _ := ecdh.X25519().GenerateKey(rand.Reader)
	f := &fixture{srv: srv, account: protocol.NewAccountID(), enc: enc, secret: protocol.RandomBytes(32), agentPriv: sigPriv}
	f.agent, err = client.New(srv.URL, protocol.AgentKey(f.account), sigPriv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	created, err := f.agent.RegisterAccount(context.Background(), f.account, sigPub, enc.PublicKey().Bytes(), protocol.PairingHash(f.secret), "Laptop")
	if err != nil || !created {
		t.Fatalf("register: %v %v", created, err)
	}
	return f
}

func (f *fixture) device(t *testing.T, secret []byte) (*client.Client, error) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	c, err := client.New(f.srv.URL, protocol.KeyNewDevice, priv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.LinkDevice(context.Background(), f.account, secret, pub, "iPhone", "ios", "1.0")
	if err != nil {
		return nil, err
	}
	return c.WithKey(protocol.DeviceKey(id)), nil
}

func TestPairUploadFetchAckUnlink(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dev, err := f.device(t, f.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.device(t, f.secret); !client.IsCode(err, "device_already_linked") {
		t.Fatalf("second device: %v", err)
	}
	if _, err := f.device(t, protocol.RandomBytes(32)); !client.IsCode(err, "invalid_pairing_secret") {
		t.Fatalf("wrong secret: %v", err)
	}
	var envs []protocol.Envelope
	for i := 0; i < 3; i++ {
		e, err := protocol.Seal([]byte(`{"schema":1}`), f.enc.PublicKey(), protocol.SealParams{Account: f.account})
		if err != nil {
			t.Fatal(err)
		}
		envs = append(envs, e)
	}
	seqs, err := dev.Upload(ctx, envs)
	if err != nil || len(seqs) != 3 {
		t.Fatalf("upload: %v %v", seqs, err)
	}
	again, err := dev.Upload(ctx, envs[:1])
	if err != nil || again[0] != seqs[0] {
		t.Fatalf("duplicate upload not idempotent: %v %v", again, err)
	}
	foreign := envs[0]
	foreign.Account = protocol.NewAccountID()
	if _, err := dev.Upload(ctx, []protocol.Envelope{foreign}); !client.IsCode(err, "wrong_account") {
		t.Fatalf("foreign account: %v", err)
	}
	st, err := f.agent.Status(ctx)
	if err != nil || st.Device == nil || st.PendingEnvelopes != 3 {
		t.Fatalf("status: %+v %v", st, err)
	}
	got, more, err := f.agent.Envelopes(ctx, 0, 2)
	if err != nil || len(got) != 2 || !more {
		t.Fatalf("page 1: %d %v %v", len(got), more, err)
	}
	for _, e := range got {
		if _, err := protocol.Open(e.Envelope, f.account, f.enc); err != nil {
			t.Fatalf("relay altered envelope: %v", err)
		}
	}
	rest, more, err := f.agent.Envelopes(ctx, got[1].Seq, 50)
	if err != nil || len(rest) != 1 || more {
		t.Fatalf("page 2: %d %v %v", len(rest), more, err)
	}
	if n, err := f.agent.Ack(ctx, rest[0].Seq); err != nil || n != 3 {
		t.Fatalf("ack: %d %v", n, err)
	}
	if unlinked, err := f.agent.UnlinkCurrent(ctx); err != nil || !unlinked {
		t.Fatalf("unlink: %v %v", unlinked, err)
	}
	if _, err := dev.Upload(ctx, envs); !client.IsCode(err, "device_unlinked") {
		t.Fatalf("unlinked device upload: %v", err)
	}
	// The same pairing code works again after unlinking.
	if _, err := f.device(t, f.secret); err != nil {
		t.Fatalf("re-pair: %v", err)
	}
}

func TestRejectsReplayAndForgery(t *testing.T) {
	f := newFixture(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	forged, _ := client.New(f.srv.URL, protocol.AgentKey(f.account), priv, 10*time.Second)
	if _, err := forged.Status(context.Background()); !client.IsCode(err, "bad_signature") {
		t.Fatalf("forged signature: %v", err)
	}
	send := func(h protocol.SignedHeaders) int {
		req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/v1/status", nil)
		h.Apply(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	h := protocol.Sign(f.agentPriv, "GET", "/v1/status", protocol.AgentKey(f.account), nil, time.Now(), protocol.NewNonce())
	if code := send(h); code != http.StatusOK {
		t.Fatalf("first request: %d", code)
	}
	if code := send(h); code != http.StatusUnauthorized {
		t.Fatalf("replay accepted: %d", code)
	}
	stale := protocol.Sign(f.agentPriv, "GET", "/v1/status", protocol.AgentKey(f.account), nil, time.Now().Add(-10*time.Minute), protocol.NewNonce())
	if code := send(stale); code != http.StatusUnauthorized {
		t.Fatalf("stale request accepted: %d", code)
	}
	otherPath := protocol.Sign(f.agentPriv, "GET", "/v1/envelopes", protocol.AgentKey(f.account), nil, time.Now(), protocol.NewNonce())
	if code := send(otherPath); code != http.StatusUnauthorized {
		t.Fatalf("signature for another path accepted: %d", code)
	}
}

func TestRegistrationToken(t *testing.T) {
	store, err := relay.OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := httptest.NewServer(relay.NewServer(store, relay.Config{RegistrationToken: "s3cret", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	defer srv.Close()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	enc, _ := ecdh.X25519().GenerateKey(rand.Reader)
	account := protocol.NewAccountID()
	c, _ := client.New(srv.URL, protocol.AgentKey(account), priv, 5*time.Second)
	hash := protocol.PairingHash(protocol.RandomBytes(32))
	if _, err := c.RegisterAccount(context.Background(), account, pub, enc.PublicKey().Bytes(), hash, "x"); !client.IsCode(err, "registration_token_required") {
		t.Fatalf("missing token: %v", err)
	}
	c.RegistrationToken = "s3cret"
	if _, err := c.RegisterAccount(context.Background(), account, pub, enc.PublicKey().Bytes(), hash, "x"); err != nil {
		t.Fatalf("with token: %v", err)
	}
}

func TestConcurrentDuplicateUploadsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dev, err := f.device(t, f.secret)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := protocol.Seal([]byte(`{"schema":1}`), f.enc.PublicKey(), protocol.SealParams{Account: f.account})
	const n = 8
	seqs := make(chan int64, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			s, err := dev.Upload(ctx, []protocol.Envelope{e})
			if err != nil {
				errs <- err
				return
			}
			seqs <- s[0]
		}()
	}
	var first int64
	for i := 0; i < n; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent duplicate upload failed: %v", err)
		case s := <-seqs:
			if first == 0 {
				first = s
			} else if s != first {
				t.Fatalf("duplicate got a new seq: %d vs %d", s, first)
			}
		}
	}
}
