//go:build windows

package wire

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile takes the exclusive lock Compa's own writers take on
// <config>.flock: byte 0, length 1, through LockFileEx. It waits up to timeout
// for a writer that holds it.
func lockFile(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	deadline := time.Now().Add(timeout)
	for {
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, new(windows.Overlapped))
		_ = f.Close()
	}, nil
}
