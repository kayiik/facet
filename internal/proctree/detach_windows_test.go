//go:build windows

package proctree

import (
	"os/exec"
	"syscall"
	"testing"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detach starts cmd detached from the console in a new process group, as a
// launcher does for a browser. Job membership is still inherited.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

// A descendant started detached must still die with the cancelled command.
func TestCancelKillsDetachedGrandchild(t *testing.T) {
	testCancelKillsGrandchild(t, "parent-detached")
}
