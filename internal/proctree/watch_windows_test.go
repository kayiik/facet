//go:build windows

package proctree

import (
	"syscall"
	"testing"
	"time"
)

const (
	synchronize                    = 0x00100000
	processQueryLimitedInformation = 0x00001000
)

// watched holds a handle on a process opened while it was known to be the
// process under test. A handle names that exact process object, so a later
// reuse of its PID cannot be mistaken for it, and cleanup can never
// terminate an unrelated process.
type watched struct {
	pid    int
	handle syscall.Handle
}

func watchProcess(t *testing.T, pid int) *watched {
	t.Helper()
	handle, err := syscall.OpenProcess(synchronize|processQueryLimitedInformation|processTerminate, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	w := &watched{pid: pid, handle: handle}
	t.Cleanup(func() {
		if w.running() {
			_ = syscall.TerminateProcess(handle, 1)
			_, _ = syscall.WaitForSingleObject(handle, 5000)
		}
		_ = syscall.CloseHandle(handle)
	})
	return w
}

func (w *watched) running() bool {
	event, err := syscall.WaitForSingleObject(w.handle, 0)
	return err == nil && event == syscall.WAIT_TIMEOUT
}

func (w *watched) exitedWithin(limit time.Duration) bool {
	event, err := syscall.WaitForSingleObject(w.handle, uint32(limit/time.Millisecond))
	return err == nil && event == syscall.WAIT_OBJECT_0
}
