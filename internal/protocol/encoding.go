// Package protocol implements the HealthBridge v1 (HB1) wire protocol shared by
// the relay, the agent CLI and the iOS app. See docs/PROTOCOL.md.
package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Version is the protocol generation encoded in pairing codes and signatures.
const Version = 1

var (
	accountPattern = regexp.MustCompile(`^hb_[A-Za-z0-9_-]{22}$`)
	devicePattern  = regexp.MustCompile(`^dev_[A-Za-z0-9_-]{22}$`)
	uuidPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// B64 encodes with standard Base64 including padding.
func B64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// B64U encodes with Base64url without padding.
func B64U(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// DecodeB64 decodes standard Base64 and enforces an exact decoded length when n >= 0.
func DecodeB64(s string, n int) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, errors.New("invalid base64")
	}
	if n >= 0 && len(b) != n {
		return nil, fmt.Errorf("expected %d bytes, got %d", n, len(b))
	}
	return b, nil
}

// DecodeB64U decodes unpadded Base64url and enforces an exact length when n >= 0.
func DecodeB64U(s string, n int) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, errors.New("invalid base64url")
	}
	if n >= 0 && len(b) != n {
		return nil, fmt.Errorf("expected %d bytes, got %d", n, len(b))
	}
	return b, nil
}

// SHA256Hex returns the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// RandomBytes returns n cryptographically secure random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return b
}

// NewAccountID returns "hb_" + 16 random bytes in Base64url.
func NewAccountID() string { return "hb_" + B64U(RandomBytes(16)) }

// NewDeviceID returns "dev_" + 16 random bytes in Base64url.
func NewDeviceID() string { return "dev_" + B64U(RandomBytes(16)) }

// NewNonce returns a fresh request nonce.
func NewNonce() string { return B64U(RandomBytes(16)) }

// NewUUID returns a random lowercase RFC 4122 version 4 UUID.
func NewUUID() string {
	b := RandomBytes(16)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// ValidAccountID reports whether s is a syntactically valid account ID.
func ValidAccountID(s string) bool { return accountPattern.MatchString(s) }

// ValidDeviceID reports whether s is a syntactically valid device ID.
func ValidDeviceID(s string) bool { return devicePattern.MatchString(s) }

// ValidUUID reports whether s is a UUID string.
func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

// FormatTime renders t as RFC 3339 UTC with second precision.
func FormatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// ParseTime parses an RFC 3339 timestamp.
func ParseTime(s string) (time.Time, error) {
	if len(s) > 40 {
		return time.Time{}, errors.New("timestamp too long")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, errors.New("invalid RFC 3339 timestamp")
	}
	return t, nil
}

// MaxLabelLength is the longest agent or device label, in Unicode code points.
const MaxLabelLength = 64

// CleanLabel normalizes a user-visible name (agent or device label): it trims
// surrounding whitespace, removes control characters (Unicode category Cc: C0,
// DEL and C1) and keeps at most MaxLabelLength code points. The iOS app applies
// the same rule (HB1.cleanLabel).
func CleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if runes := []rune(s); len(runes) > MaxLabelLength {
		s = string(runes[:MaxLabelLength])
	}
	return s
}
