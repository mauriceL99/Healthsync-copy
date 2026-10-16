//go:build !unix

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// lockDir uses an exclusive lock file on platforms without flock, waiting up
// to lockWait for another command to finish.
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another healthbridge command is using this state directory (delete .lock if no command is running)")
		}
		time.Sleep(lockRetry)
	}
}
