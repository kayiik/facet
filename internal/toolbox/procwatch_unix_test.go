//go:build unix

package toolbox

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// pidWatch follows a process by PID. POSIX allocates PIDs sequentially, so a
// PID is not reused within the seconds a test watches it.
type pidWatch struct {
	pid int
}

func watchPID(t *testing.T, pid int) *pidWatch {
	t.Helper()
	w := &pidWatch{pid: pid}
	t.Cleanup(func() {
		if w.running() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return w
}

func (w *pidWatch) running() bool {
	if err := syscall.Kill(w.pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	// A zombie has exited and waits only for its new parent to reap it.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", w.pid)); err == nil {
		if i := bytes.LastIndexByte(data, ')'); i >= 0 && i+2 < len(data) && data[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func (w *pidWatch) exitedWithin(limit time.Duration) bool {
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
