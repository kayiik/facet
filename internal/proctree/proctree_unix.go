//go:build unix

package proctree

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// tree is the process group led by the command. Every descendant inherits
// the group unless it deliberately leaves it — and some do: a launcher that
// starts a browser "detached" puts it in a new session, so it can kill the
// browser's own tree later. kill therefore also sweeps every descendant it
// can see, wherever its group.
type tree struct {
	pgid     int
	released bool
}

// newTree makes the child the leader of a new process group. The child calls
// setpgid itself before exec, so the group exists before it can start
// anything.
func newTree(cmd *exec.Cmd) (*tree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	return &tree{}, nil
}

func (t *tree) attach(p *os.Process) error {
	t.pgid = p.Pid
	return nil
}

// kill sends SIGKILL to the whole group and to every descendant of the root,
// including any that moved to a group or session of their own. SIGKILL cannot
// be caught, so no member can delay or refuse it.
//
// The descendants are listed before anything is signalled: once a parent dies
// its children are re-parented and can no longer be traced back to the root.
func (t *tree) kill(p *os.Process) error {
	if t.released {
		// The leader was reaped and the group already killed; never signal a
		// group ID that is no longer ours.
		return p.Kill()
	}
	pgid := t.pgid
	if pgid == 0 {
		// Not yet recorded: the group still exists and is led by the child.
		pgid = p.Pid
	}
	escaped := descendants(p.Pid)
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	for _, pid := range escaped {
		// A descendant that leads its own group takes that group with it;
		// for any other process the group signal finds nothing.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if killErr := p.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return errors.Join(err, killErr)
	}
	return nil
}

// release kills whatever is left of the group after the leader was reaped.
//
// A process group ID stays allocated while any member is alive, so it cannot
// have been reused for an unrelated group while survivors exist; with no
// survivors the signal finds nothing.
func (t *tree) release() error {
	if t.released || t.pgid == 0 {
		t.released = true
		return nil
	}
	t.released = true
	err := syscall.Kill(-t.pgid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

// descendants lists every live descendant of root, best effort: from /proc
// where it exists, otherwise from ps. A missing listing only means the group
// kill has to suffice.
func descendants(root int) []int {
	parents := processParents()
	children := map[int][]int{}
	for pid, ppid := range parents {
		children[ppid] = append(children[ppid], pid)
	}
	var out []int
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !seen[child] {
				seen[child] = true
				out = append(out, child)
				queue = append(queue, child)
			}
		}
	}
	return out
}

// processParents maps every visible PID to its parent PID.
func processParents() map[int]int {
	if runtime.GOOS == "linux" {
		if parents, ok := procParents(); ok {
			return parents
		}
	}
	return psParents()
}

func procParents() (map[int]int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	parents := map[int]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...; comm may itself contain spaces and
		// parentheses, so parse after the last ')'.
		i := bytes.LastIndexByte(data, ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(string(data[i+1:]))
		if len(fields) < 2 {
			continue
		}
		if ppid, err := strconv.Atoi(fields[1]); err == nil {
			parents[pid] = ppid
		}
	}
	return parents, true
}

func psParents() map[int]int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "ppid=")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	parents := map[int]int{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil {
			parents[pid] = ppid
		}
	}
	return parents
}
