//go:build !unix

package agent

import (
	"errors"
	"os"
	"path/filepath"
)

// lockDir uses an exclusive lock file on platforms without flock.
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, errors.New("another healthbridge command is using this state directory (delete .lock if no command is running)")
	}
	if err != nil {
		return nil, err
	}
	f.Close()
	return func() { os.Remove(path) }, nil
}
