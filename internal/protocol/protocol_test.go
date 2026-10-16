package protocol

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateVectors = flag.Bool("update", false, "rewrite testdata/protocol_vectors.json")

func seq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

type vectorFile struct {
	Notice string `json:"notice"`
	Agent  struct {
		Account     string `json:"account"`
		Name        string `json:"name"`
		Relay       string `json:"relay"`
		SigSeed     string `json:"sig_seed_b64"`
		SigPub      string `json:"sig_pub_b64"`
		EncPriv     string `json:"enc_priv_b64"`
		EncPub      string `json:"enc_pub_b64"`
		Secret      string `json:"secret_b64u"`
		Kid         string `json:"kid"`
		SafetyCode  string `json:"safety_code"`
		PairingHash string `json:"pairing_hash"`
		PairingCode string `json:"pairing_code"`
	} `json:"agent"`
	Device struct {
		SigSeed string `json:"sig_seed_b64"`
		SigPub  string `json:"sig_pub_b64"`
	} `json:"device"`
	Request struct {
		Method       string `json:"method"`
		Path         string `json:"path"`
		Key          string `json:"key"`
		Timestamp    string `json:"timestamp"`
		Nonce        string `json:"nonce"`
		Body         string `json:"body"`
		BodySHA256   string `json:"body_sha256"`
		StringToSign string `json:"string_to_sign"`
		Signature    string `json:"signature_b64"`
	} `json:"request"`
	Envelopes []struct {
		Plaintext     string   `json:"plaintext"`
		EphemeralPriv string   `json:"ephemeral_priv_b64"`
		SharedSecret  string   `json:"shared_secret_b64"`
		Key           string   `json:"key_b64"`
		AAD           string   `json:"aad"`
		Envelope      Envelope `json:"envelope"`
	} `json:"envelopes"`
}

func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	var v vectorFile
	v.Notice = "PUBLIC SYNTHETIC TEST KEYS ONLY. Never use these identities with a real relay."
	sigSeed := seq(1, 32)
	encPriv := seq(0x40, 32)
	devSeed := seq(0x80, 32)
	sig := ed25519.NewKeyFromSeed(sigSeed)
	enc, err := ecdh.X25519().NewPrivateKey(encPriv)
	if err != nil {
		t.Fatal(err)
	}
	dev := ed25519.NewKeyFromSeed(devSeed)
	a := &v.Agent
	a.Account = "hb_" + B64U(seq(0xa0, 16))
	a.Name = "Test Agent"
	a.Relay = "https://relay.example.com"
	a.SigSeed, a.SigPub = B64(sigSeed), B64(sig.Public().(ed25519.PublicKey))
	a.EncPriv, a.EncPub = B64(encPriv), B64(enc.PublicKey().Bytes())
	secret := seq(0xc0, 32)
	a.Secret = B64U(secret)
	a.Kid = KeyID(enc.PublicKey().Bytes())
	a.SafetyCode = SafetyCode(a.Account, sig.Public().(ed25519.PublicKey), enc.PublicKey().Bytes())
	a.PairingHash = PairingHash(secret)
	a.PairingCode, err = Pairing{V: 1, Relay: a.Relay, Account: a.Account, Sig: a.SigPub, Enc: a.EncPub, Secret: a.Secret, Name: a.Name}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	v.Device.SigSeed, v.Device.SigPub = B64(devSeed), B64(dev.Public().(ed25519.PublicKey))

	r := &v.Request
	r.Method, r.Path = "POST", "/v1/envelopes"
	r.Key = DeviceKey("dev_" + B64U(seq(0x10, 16)))
	r.Nonce = B64U(seq(0x20, 16))
	r.Body = `{"envelopes":[]}`
	r.BodySHA256 = SHA256Hex([]byte(r.Body))
	h := Sign(dev, r.Method, r.Path, r.Key, []byte(r.Body), time.Unix(1760000000, 0), r.Nonce)
	r.Timestamp, r.Signature = h.Timestamp, h.Signature
	r.StringToSign = StringToSign(r.Method, r.Path, r.Key, r.Timestamp, r.Nonce, []byte(r.Body))

	plain := `{"schema":1,"generated_at":"2026-10-13T08:00:00Z","timezone":"Europe/Berlin","days":{"2026-10-12":{"activity":{"steps":9214},"heart":{"resting_bpm":54}}},"workouts":[]}`
	for i, compression := range []string{CompressionNone, CompressionDeflate} {
		ephBytes := seq(byte(0x50+i), 32)
		eph, _ := ecdh.X25519().NewPrivateKey(ephBytes)
		env, err := Seal([]byte(plain), enc.PublicKey(), SealParams{
			Account: a.Account, ID: "6f1c0b8e-3d1a-4c55-9a3e-7a6c8d1e2f3" + string(rune('0'+i)),
			Kind: KindSync, Compression: compression, CreatedAt: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC),
			Ephemeral: eph, Nonce: seq(byte(0x60+i), 12),
		})
		if err != nil {
			t.Fatal(err)
		}
		shared, _ := eph.ECDH(enc.PublicKey())
		key, _ := deriveKey(shared, eph.PublicKey().Bytes(), enc.PublicKey().Bytes())
		v.Envelopes = append(v.Envelopes, struct {
			Plaintext     string   `json:"plaintext"`
			EphemeralPriv string   `json:"ephemeral_priv_b64"`
			SharedSecret  string   `json:"shared_secret_b64"`
			Key           string   `json:"key_b64"`
			AAD           string   `json:"aad"`
			Envelope      Envelope `json:"envelope"`
		}{plain, B64(ephBytes), B64(shared), B64(key), string(env.AAD()), env})
	}
	return v
}

func TestVectors(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "protocol_vectors.json")
	v := buildVectors(t)
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if *updateVectors {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors (run with -update to create): %v", err)
	}
	if !bytes.Equal(stored, encoded) {
		t.Fatal("testdata/protocol_vectors.json is out of date; run go test ./internal/protocol -run TestVectors -update")
	}
	// The stored vectors must decrypt with the published private key.
	enc, _ := ecdh.X25519().NewPrivateKey(seq(0x40, 32))
	for _, e := range v.Envelopes {
		plain, err := Open(e.Envelope, v.Agent.Account, enc)
		if err != nil || string(plain) != e.Plaintext {
			t.Fatalf("vector %s does not open: %v", e.Envelope.Compression, err)
		}
	}
}

func TestPairingRoundTrip(t *testing.T) {
	sig, _, _ := ed25519.GenerateKey(rand.Reader)
	enc, _ := ecdh.X25519().GenerateKey(rand.Reader)
	p := Pairing{V: 1, Relay: "http://127.0.0.1:8787", Account: NewAccountID(), Sig: B64(sig), Enc: B64(enc.PublicKey().Bytes()), Secret: B64U(RandomBytes(32))}
	code, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePairing(code)
	if err != nil || got != p {
		t.Fatalf("round trip failed: %v %+v", err, got)
	}
	for _, bad := range []string{"", "HB2:abc", "HB1:!!!", code[:len(code)-4]} {
		if _, err := DecodePairing(bad); err == nil {
			t.Errorf("accepted invalid code %q", bad)
		}
	}
	p.Relay = "http://example.com"
	if _, err := p.Encode(); err == nil {
		t.Error("accepted public plain-http relay")
	}
}

func TestSignatureVerification(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	body := []byte(`{"up_to":3}`)
	h := Sign(priv, "POST", "/v1/envelopes/ack", AgentKey("hb_x"), body, now, NewNonce())
	if err := h.Verify(pub, "POST", "/v1/envelopes/ack", body, now); err != nil {
		t.Fatal(err)
	}
	if h.Verify(pub, "POST", "/v1/envelopes/ack", []byte(`{"up_to":4}`), now) == nil {
		t.Error("tampered body verified")
	}
	if h.Verify(pub, "GET", "/v1/envelopes/ack", body, now) == nil {
		t.Error("changed method verified")
	}
	if h.Verify(pub, "POST", "/v1/envelopes/ack", body, now.Add(6*time.Minute)) == nil {
		t.Error("stale request verified")
	}
}

func TestEnvelopeRejectsTampering(t *testing.T) {
	enc, _ := ecdh.X25519().GenerateKey(rand.Reader)
	other, _ := ecdh.X25519().GenerateKey(rand.Reader)
	account := NewAccountID()
	env, err := Seal([]byte(`{"schema":1}`), enc.PublicKey(), SealParams{Account: account})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(env, account, enc); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(env, account, other); err == nil {
		t.Error("opened with wrong key")
	}
	if _, err := Open(env, NewAccountID(), enc); err == nil {
		t.Error("opened for wrong account")
	}
	changed := env
	changed.Kind = KindBackfill
	if _, err := Open(changed, account, enc); err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Errorf("AAD not enforced: %v", err)
	}
	changed = env
	changed.CreatedAt = "2020-01-01T00:00:00Z"
	if _, err := Open(changed, account, enc); err == nil {
		t.Error("created_at not bound")
	}
}

func TestInflateLimit(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 1<<20)
	z, _ := Deflate(big)
	if _, err := Inflate(z, 1000); err == nil {
		t.Fatal("limit not enforced")
	}
	out, err := Inflate(z, 2<<20)
	if err != nil || len(out) != len(big) {
		t.Fatal("inflate failed")
	}
}

func TestCleanLabelRemovesAllControlCharacters(t *testing.T) {
	got := CleanLabel("  Desk\x00\x1b[31m\u0085\u009b Agent\x7f  ")
	if got != "Desk[31m Agent" {
		t.Fatalf("CleanLabel: %q", got)
	}
	if long := CleanLabel(strings.Repeat("ä", MaxLabelLength+5)); len([]rune(long)) != MaxLabelLength {
		t.Fatalf("length: %d", len([]rune(long)))
	}
}
