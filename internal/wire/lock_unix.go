//go:build unix

package wire

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes the exclusive lock Compa's own writers take on
// <config>.flock (flock(LOCK_EX)). It waits up to timeout for a writer that
// holds it.
func lockFile(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
