package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
)

// PairingPrefix starts every pairing code.
const PairingPrefix = "HB1:"

// Pairing is the public pairing payload shown as QR code.
type Pairing struct {
	V       int    `json:"v"`
	Relay   string `json:"relay"`
	Account string `json:"account"`
	Sig     string `json:"sig"`
	Enc     string `json:"enc"`
	Secret  string `json:"secret"`
	Name    string `json:"name,omitempty"`
}

// Encode renders the pairing code "HB1:<b64u(json)>".
func (p Pairing) Encode() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return PairingPrefix + B64U(data), nil
}

// DecodePairing parses and validates a pairing code.
func DecodePairing(code string) (Pairing, error) {
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, PairingPrefix) {
		return Pairing{}, errors.New("pairing code must start with HB1:")
	}
	if len(code) > 4096 {
		return Pairing{}, errors.New("pairing code too long")
	}
	raw, err := DecodeB64U(strings.TrimPrefix(code, PairingPrefix), -1)
	if err != nil {
		return Pairing{}, err
	}
	var p Pairing
	if err := json.Unmarshal(raw, &p); err != nil {
		return Pairing{}, errors.New("pairing code is not valid JSON")
	}
	return p, p.Validate()
}

// Validate checks every field of the pairing payload.
func (p Pairing) Validate() error {
	if p.V != Version {
		return errors.New("unsupported pairing version")
	}
	if err := ValidateRelayURL(p.Relay); err != nil {
		return err
	}
	if !ValidAccountID(p.Account) {
		return errors.New("invalid account ID")
	}
	if _, err := DecodeB64(p.Sig, 32); err != nil {
		return errors.New("invalid signing public key")
	}
	if _, err := DecodeB64(p.Enc, 32); err != nil {
		return errors.New("invalid encryption public key")
	}
	if _, err := DecodeB64U(p.Secret, 32); err != nil {
		return errors.New("invalid pairing secret")
	}
	if len([]rune(p.Name)) > 64 {
		return errors.New("agent name too long")
	}
	return nil
}

// SafetyCode returns the human-comparable code for this pairing.
func (p Pairing) SafetyCode() (string, error) {
	sig, err := DecodeB64(p.Sig, 32)
	if err != nil {
		return "", err
	}
	enc, err := DecodeB64(p.Enc, 32)
	if err != nil {
		return "", err
	}
	return SafetyCode(p.Account, sig, enc), nil
}

// SafetyCode derives XXXX-XXXX-XXXX from the account and both agent public keys.
func SafetyCode(account string, sigPub, encPub []byte) string {
	h := sha256.New()
	h.Write([]byte("HB1-SAFETY"))
	h.Write([]byte{0})
	h.Write([]byte(account))
	h.Write([]byte{0})
	h.Write(sigPub)
	h.Write(encPub)
	s := strings.ToUpper(hex.EncodeToString(h.Sum(nil)[:6]))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
}

// PairingHash is the value the relay stores instead of the pairing secret.
func PairingHash(secret []byte) string {
	sum := sha256.Sum256(secret)
	return B64(sum[:])
}

// KeyID addresses an X25519 recipient key: b64u(SHA256(pub)[0:12]).
func KeyID(encPub []byte) string {
	sum := sha256.Sum256(encPub)
	return B64U(sum[:12])
}

// ValidateRelayURL accepts https URLs, and plain http only for loopback or
// private-network hosts used during development.
func ValidateRelayURL(raw string) error {
	if len(raw) > 512 {
		return errors.New("relay URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid relay URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
			return nil
		}
		return errors.New("plain http is only allowed for localhost or private networks")
	default:
		return errors.New("relay URL must use https")
	}
}
