//go:build !windows && !linux

package compa

import (
	"os/exec"
	"syscall"
)

// macOS has no parent-death signal; Stop shuts the kernel down, and the
// kernel's own process group lets Stop reach its children.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killWithApp(*exec.Cmd) error { return nil }
