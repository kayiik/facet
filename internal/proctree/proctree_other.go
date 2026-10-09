//go:build !unix && !windows

package proctree

import (
	"os"
	"os/exec"
)

// tree is a fallback for platforms with neither job objects nor process
// groups. Only the direct child can be stopped there.
type tree struct{}

func newTree(*exec.Cmd) (*tree, error) { return &tree{}, nil }

func (*tree) attach(*os.Process) error { return nil }

func (*tree) kill(p *os.Process) error { return p.Kill() }

func (*tree) release() error { return nil }
