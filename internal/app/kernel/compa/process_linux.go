//go:build linux

package compa

import (
	"os/exec"
	"syscall"
)

// The kernel gets SIGTERM when the App's thread that started it dies.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
}

func killWithApp(*exec.Cmd) error { return nil }
