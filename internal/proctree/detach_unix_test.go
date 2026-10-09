//go:build unix

package proctree

import (
	"os/exec"
	"syscall"
	"testing"
)

// detach puts cmd in a new session, as a launcher does for a browser it
// starts "detached".
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// A descendant that left the process group — a browser started detached —
// must still die with the cancelled command.
func TestCancelKillsDetachedGrandchild(t *testing.T) {
	testCancelKillsGrandchild(t, "parent-detached")
}
