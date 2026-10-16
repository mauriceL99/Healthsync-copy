//go:build unix

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockDir takes an exclusive advisory lock so concurrent commands (for
// example a scheduled fetch and an MCP call) never corrupt the state. It waits
// up to lockWait, so a CLI command run next to `healthbridge serve` does not
// fail just because a background fetch holds the lock for a moment.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, errors.New("another healthbridge command is using this state directory; try again shortly")
			}
			return nil, err
		}
		time.Sleep(lockRetry)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
