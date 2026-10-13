package protocol

import (
	"bytes"
	"compress/flate"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// Envelope constants.
const (
	EnvelopeAlg        = "HB1-X25519-HKDF-SHA256-CHACHA20POLY1305"
	KindSync           = "sync"
	KindBackfill       = "backfill"
	CompressionNone    = "none"
	CompressionDeflate = "deflate"

	// MaxPlaintext bounds the decompressed payload of one envelope.
	MaxPlaintext = 64 << 20
	// MaxCiphertext bounds the encrypted payload of one envelope.
	MaxCiphertext = 16 << 20

	hkdfInfo = "HB1-ENVELOPE-KEY"
)

// Envelope is one encrypted HealthBatch as uploaded by a device.
type Envelope struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Kind        string `json:"kind"`
	Alg         string `json:"alg"`
	Kid         string `json:"kid"`
	Epk         string `json:"epk"`
	Nonce       string `json:"nonce"`
	Compression string `json:"compression"`
	CreatedAt   string `json:"created_at"`
	Ct          string `json:"ct"`
}

// StoredEnvelope is an envelope as returned by the relay.
type StoredEnvelope struct {
	Envelope
	Seq        int64  `json:"seq"`
	ReceivedAt string `json:"received_at"`
}

// AAD returns the associated data bound to the ciphertext.
func (e Envelope) AAD() []byte {
	return []byte(strings.Join([]string{"HB1-ENVELOPE", e.Account, e.ID, e.Kind, e.Compression, e.CreatedAt}, "\n"))
}

// ValidateHeader checks all cleartext envelope fields without decrypting.
func (e Envelope) ValidateHeader() error {
	if !ValidUUID(e.ID) {
		return errors.New("envelope id must be a UUID")
	}
	if !ValidAccountID(e.Account) {
		return errors.New("invalid envelope account")
	}
	if e.Kind != KindSync && e.Kind != KindBackfill {
		return errors.New("envelope kind must be sync or backfill")
	}
	if e.Alg != EnvelopeAlg {
		return errors.New("unsupported envelope algorithm")
	}
	if _, err := DecodeB64U(e.Kid, 12); err != nil {
		return errors.New("invalid envelope kid")
	}
	if _, err := DecodeB64(e.Epk, PublicKeySize); err != nil {
		return errors.New("invalid ephemeral public key")
	}
	if _, err := DecodeB64(e.Nonce, chacha20poly1305.NonceSize); err != nil {
		return errors.New("invalid nonce")
	}
	if e.Compression != CompressionNone && e.Compression != CompressionDeflate {
		return errors.New("compression must be none or deflate")
	}
	if _, err := ParseTime(e.CreatedAt); err != nil {
		return errors.New("invalid created_at")
	}
	if len(e.Ct) == 0 || len(e.Ct) > (MaxCiphertext/3+1)*4 {
		return errors.New("ciphertext missing or too large")
	}
	return nil
}

// deriveKey implements HKDF-SHA256(shared, salt = epk ‖ recipient, info).
func deriveKey(shared, epk, recipient []byte) ([]byte, error) {
	salt := append(append([]byte{}, epk...), recipient...)
	return hkdf.Key(sha256.New, shared, salt, hkdfInfo, chacha20poly1305.KeySize)
}

// SealParams allows deterministic sealing for test vectors. Production code
// leaves Ephemeral and Nonce nil so fresh randomness is used.
type SealParams struct {
	Account     string
	ID          string
	Kind        string
	Compression string
	CreatedAt   time.Time
	Ephemeral   *ecdh.PrivateKey
	Nonce       []byte
}

// Seal encrypts plaintext for the recipient X25519 public key.
func Seal(plaintext []byte, recipient *ecdh.PublicKey, p SealParams) (Envelope, error) {
	if p.ID == "" {
		p.ID = NewUUID()
	}
	if p.Kind == "" {
		p.Kind = KindSync
	}
	if p.Compression == "" {
		p.Compression = CompressionDeflate
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	eph := p.Ephemeral
	if eph == nil {
		var err error
		if eph, err = ecdh.X25519().GenerateKey(rand.Reader); err != nil {
			return Envelope{}, err
		}
	}
	nonce := p.Nonce
	if nonce == nil {
		nonce = RandomBytes(chacha20poly1305.NonceSize)
	}
	shared, err := eph.ECDH(recipient)
	if err != nil {
		return Envelope{}, errors.New("invalid recipient key")
	}
	key, err := deriveKey(shared, eph.PublicKey().Bytes(), recipient.Bytes())
	if err != nil {
		return Envelope{}, err
	}
	body := plaintext
	if p.Compression == CompressionDeflate {
		if body, err = Deflate(plaintext); err != nil {
			return Envelope{}, err
		}
	}
	env := Envelope{
		ID: p.ID, Account: p.Account, Kind: p.Kind, Alg: EnvelopeAlg,
		Kid: KeyID(recipient.Bytes()), Epk: B64(eph.PublicKey().Bytes()), Nonce: B64(nonce),
		Compression: p.Compression, CreatedAt: FormatTime(p.CreatedAt),
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return Envelope{}, err
	}
	env.Ct = B64(aead.Seal(nil, nonce, body, env.AAD()))
	return env, nil
}

// Open authenticates and decrypts an envelope addressed to account/priv.
func Open(e Envelope, account string, priv *ecdh.PrivateKey) ([]byte, error) {
	if err := e.ValidateHeader(); err != nil {
		return nil, err
	}
	if e.Account != account {
		return nil, errors.New("envelope belongs to another account")
	}
	recipient := priv.PublicKey().Bytes()
	if e.Kid != KeyID(recipient) {
		return nil, errors.New("envelope is not addressed to this encryption key")
	}
	epkBytes, _ := DecodeB64(e.Epk, PublicKeySize)
	nonce, _ := DecodeB64(e.Nonce, chacha20poly1305.NonceSize)
	ct, err := DecodeB64(e.Ct, -1)
	if err != nil || len(ct) > MaxCiphertext || len(ct) < chacha20poly1305.Overhead {
		return nil, errors.New("invalid ciphertext")
	}
	epk, err := ecdh.X25519().NewPublicKey(epkBytes)
	if err != nil {
		return nil, errors.New("invalid ephemeral key")
	}
	shared, err := priv.ECDH(epk)
	if err != nil {
		return nil, errors.New("key agreement failed")
	}
	key, err := deriveKey(shared, epkBytes, recipient)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	body, err := aead.Open(nil, nonce, ct, e.AAD())
	if err != nil {
		return nil, errors.New("envelope authentication failed")
	}
	if e.Compression == CompressionDeflate {
		return Inflate(body, MaxPlaintext)
	}
	if len(body) > MaxPlaintext {
		return nil, errors.New("plaintext too large")
	}
	return body, nil
}

// Deflate compresses with raw DEFLATE (RFC 1951), compatible with Apple's COMPRESSION_ZLIB.
func Deflate(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(b); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Inflate decompresses raw DEFLATE while enforcing limit.
func Inflate(b []byte, limit int64) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(b))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("invalid deflate data: %w", err)
	}
	if int64(len(out)) > limit {
		return nil, errors.New("decompressed payload exceeds limit")
	}
	return out, nil
}
