//go:build unix

package proctree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// watched follows a process by PID. POSIX allocates PIDs sequentially, so a
// PID is not reused within the few seconds a test watches it.
type watched struct {
	pid int
}

func watchProcess(t *testing.T, pid int) *watched {
	t.Helper()
	w := &watched{pid: pid}
	t.Cleanup(func() {
		if w.running() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return w
}

func (w *watched) running() bool {
	if err := syscall.Kill(w.pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	// A zombie has already exited and waits only for its new parent to reap
	// it, which a container's init may do late or never.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", w.pid)); err == nil {
		if i := bytes.LastIndexByte(data, ')'); i >= 0 && i+2 < len(data) && data[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func (w *watched) exitedWithin(limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		if !w.running() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
