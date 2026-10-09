//go:build windows

package toolbox

import (
	"syscall"
	"testing"
	"time"
)

// pidWatch holds a handle opened while the process was known to be the one
// under test, so PID reuse cannot be mistaken for it and cleanup can never
// terminate an unrelated process.
type pidWatch struct {
	handle syscall.Handle
}

func watchPID(t *testing.T, pid int) *pidWatch {
	t.Helper()
	const synchronize, queryLimited, terminate = 0x00100000, 0x00001000, 0x00000001
	handle, err := syscall.OpenProcess(synchronize|queryLimited|terminate, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	w := &pidWatch{handle: handle}
	t.Cleanup(func() {
		if w.running() {
			_ = syscall.TerminateProcess(handle, 1)
			_, _ = syscall.WaitForSingleObject(handle, 5000)
		}
		_ = syscall.CloseHandle(handle)
	})
	return w
}

func (w *pidWatch) running() bool {
	event, err := syscall.WaitForSingleObject(w.handle, 0)
	return err == nil && event == syscall.WAIT_TIMEOUT
}

func (w *pidWatch) exitedWithin(limit time.Duration) bool {
	event, err := syscall.WaitForSingleObject(w.handle, uint32(limit/time.Millisecond))
	return err == nil && event == syscall.WAIT_OBJECT_0
}
