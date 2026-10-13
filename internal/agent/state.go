// Package agent implements the healthbridge CLI: identity and pairing,
// fetching and decrypting envelopes, local storage, summaries, dashboards,
// exports and an MCP server for AI agents.
package agent

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// Config is persisted as config.json in the state directory.
type Config struct {
	ConfigVersion   int    `json:"config_version"`
	Account         string `json:"account"`
	Relay           string `json:"relay"`
	RelayToken      string `json:"relay_registration_token,omitempty"`
	Name            string `json:"name"`
	SigPub          string `json:"sig_pub"`
	EncPub          string `json:"enc_pub"`
	PairingCode     string `json:"pairing_code"`
	SafetyCode      string `json:"safety_code"`
	CreatedAt       string `json:"created_at"`
	Registered      bool   `json:"registered"`
	LastSeq         int64  `json:"last_seq"`
	LastFetchAt     string `json:"last_fetch_at,omitempty"`
	LastFetchStatus string `json:"last_fetch_status,omitempty"`
	LastFetchError  string `json:"last_fetch_error,omitempty"`
	LastSuccessAt   string `json:"last_fetch_success_at,omitempty"`
}

// Identity holds the agent's private keys.
type Identity struct {
	Sig ed25519.PrivateKey
	Enc *ecdh.PrivateKey
}

// State is an opened state directory.
type State struct {
	Dir    string
	Config Config
	unlock func()
}

// DefaultStateDir is $HEALTHBRIDGE_HOME or ~/.healthbridge.
func DefaultStateDir() string {
	if dir := os.Getenv("HEALTHBRIDGE_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".healthbridge"
	}
	return filepath.Join(home, ".healthbridge")
}

func (s *State) path(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// ConfigPath, DBPath, QRPath and KeysDir locate files in the state directory.
func (s *State) ConfigPath() string { return s.path("config.json") }
func (s *State) DBPath() string     { return s.path("health.db") }
func (s *State) QRPath() string     { return s.path("pairing-qr.png") }
func (s *State) KeysDir() string    { return s.path("keys") }

// OpenState locks and loads a state directory. A missing config is not an
// error; callers use Initialized to check.
func OpenState(dir string) (*State, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(abs); err != nil {
		return nil, err
	}
	unlock, err := lockDir(abs)
	if err != nil {
		return nil, err
	}
	s := &State{Dir: abs, unlock: unlock}
	data, err := os.ReadFile(s.ConfigPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		s.Close()
		return nil, err
	}
	if err := json.Unmarshal(data, &s.Config); err != nil {
		s.Close()
		return nil, fmt.Errorf("config.json is corrupt: %w", err)
	}
	return s, nil
}

// Close releases the directory lock.
func (s *State) Close() {
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

// Initialized reports whether onboarding created an identity.
func (s *State) Initialized() bool { return s.Config.Account != "" }

// RequireInit returns a helpful error when onboarding has not happened.
func (s *State) RequireInit() error {
	if !s.Initialized() {
		return fmt.Errorf("no HealthBridge identity in %s; run: healthbridge onboard --relay https://your-relay.example", s.Dir)
	}
	return nil
}

// Save atomically writes config.json with private permissions.
func (s *State) Save() error {
	data, err := json.MarshalIndent(s.Config, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateAtomic(s.ConfigPath(), append(data, '\n'))
}

// LoadIdentity reads and cross-checks the private keys.
func (s *State) LoadIdentity() (*Identity, error) {
	if err := s.RequireInit(); err != nil {
		return nil, err
	}
	sigKey, err := readPrivateKey(filepath.Join(s.KeysDir(), "signing.pem"))
	if err != nil {
		return nil, err
	}
	encKey, err := readPrivateKey(filepath.Join(s.KeysDir(), "encryption.pem"))
	if err != nil {
		return nil, err
	}
	sig, ok := sigKey.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("signing.pem is not an Ed25519 key")
	}
	enc, ok := encKey.(*ecdh.PrivateKey)
	if !ok || enc.Curve() != ecdh.X25519() {
		return nil, errors.New("encryption.pem is not an X25519 key")
	}
	if protocol.B64(sig.Public().(ed25519.PublicKey)) != s.Config.SigPub || protocol.B64(enc.PublicKey().Bytes()) != s.Config.EncPub {
		return nil, errors.New("keys do not match config.json; restore the complete state directory")
	}
	return &Identity{Sig: sig, Enc: enc}, nil
}

// SaveIdentity writes both private keys (PKCS#8 PEM, mode 0600).
func (s *State) SaveIdentity(id *Identity) error {
	if err := ensurePrivateDir(s.KeysDir()); err != nil {
		return err
	}
	for name, key := range map[string]any{"signing.pem": id.Sig, "encryption.pem": id.Enc} {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		if err := writePrivateAtomic(filepath.Join(s.KeysDir(), name), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
			return err
		}
	}
	return nil
}

// ArchiveIdentity moves keys, config and QR into backups/<timestamp>/.
func (s *State) ArchiveIdentity(now time.Time) (string, error) {
	dest := s.path("backups", now.UTC().Format("20060102T150405Z")+"-"+s.Config.Account)
	if err := ensurePrivateDir(dest); err != nil {
		return "", err
	}
	for _, name := range []string{"keys", "config.json", "pairing-qr.png"} {
		if _, err := os.Lstat(s.path(name)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := os.Rename(s.path(name), filepath.Join(dest, name)); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func readPrivateKey(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PKCS#8 PEM key", filepath.Base(path))
	}
	return x509.ParsePKCS8PrivateKey(block.Bytes)
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// writePrivateAtomic writes data with mode 0600 via a temporary file.
func writePrivateAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeNewPrivate creates a new file (never overwriting) with mode 0600.
func writeNewPrivate(path string, data []byte, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; choose another path or pass --force", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
