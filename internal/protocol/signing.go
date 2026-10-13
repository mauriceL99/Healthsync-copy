package protocol

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Request authentication headers.
const (
	HeaderKey       = "HB-Key"
	HeaderTimestamp = "HB-Timestamp"
	HeaderNonce     = "HB-Nonce"
	HeaderSignature = "HB-Signature"

	// KeyNewDevice is the HB-Key value for POST /v1/devices, which is
	// self-certified by the device public key in the request body.
	KeyNewDevice = "new-device"

	// MaxClockSkew is the accepted difference between client and relay clocks.
	MaxClockSkew = 300 * time.Second
	// NonceWindow is how long the relay remembers nonces.
	NonceWindow = 600 * time.Second
)

// ErrStaleRequest reports a timestamp outside MaxClockSkew (usually a wrong device clock).
var ErrStaleRequest = errors.New("request timestamp outside the allowed window; check the device clock")

// AgentKey returns the HB-Key value for an agent account.
func AgentKey(account string) string { return "agent:" + account }

// DeviceKey returns the HB-Key value for a linked device.
func DeviceKey(deviceID string) string { return "device:" + deviceID }

// StringToSign builds the canonical string covered by HB-Signature.
func StringToSign(method, pathAndQuery, key, timestamp, nonce string, body []byte) string {
	return strings.Join([]string{
		"HB1-REQUEST",
		strings.ToUpper(method),
		pathAndQuery,
		key,
		timestamp,
		nonce,
		SHA256Hex(body),
	}, "\n")
}

// SignedHeaders contains the four authentication header values.
type SignedHeaders struct {
	Key, Timestamp, Nonce, Signature string
}

// Sign produces authentication headers for one request.
func Sign(priv ed25519.PrivateKey, method, pathAndQuery, key string, body []byte, now time.Time, nonce string) SignedHeaders {
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := ed25519.Sign(priv, []byte(StringToSign(method, pathAndQuery, key, ts, nonce, body)))
	return SignedHeaders{Key: key, Timestamp: ts, Nonce: nonce, Signature: B64(sig)}
}

// Apply sets the headers on an outgoing request.
func (h SignedHeaders) Apply(r *http.Request) {
	r.Header.Set(HeaderKey, h.Key)
	r.Header.Set(HeaderTimestamp, h.Timestamp)
	r.Header.Set(HeaderNonce, h.Nonce)
	r.Header.Set(HeaderSignature, h.Signature)
}

// ParseSignedHeaders reads and syntactically validates authentication headers.
func ParseSignedHeaders(r *http.Request) (SignedHeaders, error) {
	h := SignedHeaders{
		Key:       r.Header.Get(HeaderKey),
		Timestamp: r.Header.Get(HeaderTimestamp),
		Nonce:     r.Header.Get(HeaderNonce),
		Signature: r.Header.Get(HeaderSignature),
	}
	if h.Key == "" || h.Timestamp == "" || h.Nonce == "" || h.Signature == "" {
		return h, errors.New("missing authentication headers")
	}
	if len(h.Key) > 64 || len(h.Timestamp) > 16 {
		return h, errors.New("malformed authentication headers")
	}
	if _, err := DecodeB64U(h.Nonce, 16); err != nil {
		return h, errors.New("nonce must be 16 bytes base64url")
	}
	return h, nil
}

// Verify checks freshness and the Ed25519 signature for a received request.
func (h SignedHeaders) Verify(pub ed25519.PublicKey, method, pathAndQuery string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(h.Timestamp, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew > MaxClockSkew || skew < -MaxClockSkew {
		return ErrStaleRequest
	}
	sig, err := DecodeB64(h.Signature, ed25519.SignatureSize)
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	if !ed25519.Verify(pub, []byte(StringToSign(method, pathAndQuery, h.Key, h.Timestamp, h.Nonce, body)), sig) {
		return errors.New("signature verification failed")
	}
	return nil
}
