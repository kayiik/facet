// Package proctree runs external commands so that stopping one stops the whole
// process tree it created, not only the process Facet started.
//
// os/exec cancels a command by killing the single process it started. Media
// work is rarely a single process: node starts a headless browser, the browser
// starts renderer and GPU helpers, a wrapper starts ffmpeg. Killing only the
// root leaves the rest running: they keep consuming CPU, keep writing to files
// the caller has already abandoned, and keep the output pipes open so that
// Wait does not return.
//
// A Cmd places its child in a kill group before the child can start anything:
//
//   - Windows: a Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. The child
//     is created suspended, assigned to the job, and only then resumed, so no
//     descendant can be created outside the job. Every descendant inherits the
//     job. Closing the job handle — including when this process exits for any
//     reason — terminates every member.
//   - POSIX: a new process group (Setpgid). Killing sends SIGKILL to the group,
//     which every descendant inherits, and to every descendant traced from the
//     root at that moment, which also reaches one that moved to a session of
//     its own (a browser a launcher started "detached").
//
// Cancelling the context, explicitly or by its deadline, kills the whole tree.
// WaitDelay then bounds how long Wait waits for I/O pipes that an escaped
// descendant might still hold. When Wait returns, anything still in the kill
// group is killed too: a stateless tool leaves nothing running.
package proctree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

// DefaultWaitDelay is the exec.Cmd.WaitDelay every Cmd starts with.
//
// After the context is done the tree is killed with an uncatchable signal (or
// TerminateJobObject), so its members exit at once; the delay only matters for
// pipes held open by something outside the group, and for a root that exited
// while a descendant still holds its stdout or stderr. Two seconds is long
// enough for an orderly exit and short enough that a cancelled render returns
// promptly.
const DefaultWaitDelay = 2 * time.Second

// Cmd is an exec.Cmd whose cancellation and completion terminate the entire
// process tree.
//
// Configure it through the embedded exec.Cmd fields (Dir, Env, Stdin, Stdout,
// Stderr, WaitDelay) and run it only through this type's Start, Wait, Run,
// Output or CombinedOutput. The embedded Cancel function is owned by Cmd and
// must not be replaced.
type Cmd struct {
	*exec.Cmd

	mu      sync.Mutex
	tree    *tree
	started bool
	killed  bool
	waited  bool
}

// CommandContext returns a Cmd that runs name with args. When ctx is done the
// command and every process it started are killed.
//
// Like exec.CommandContext it panics if ctx is nil.
func CommandContext(ctx context.Context, name string, args ...string) *Cmd {
	c := &Cmd{Cmd: exec.CommandContext(ctx, name, args...)}
	c.Cmd.Cancel = c.Kill
	c.Cmd.WaitDelay = DefaultWaitDelay
	return c
}

// Start starts the command inside a new kill group.
//
// If the child cannot be placed in the group it is stopped and reaped, and
// Start reports the error: a command never runs without the guarantee that it
// can be stopped as a whole.
func (c *Cmd) Start() error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("proctree: Start called more than once")
	}
	c.started = true
	t, err := newTree(c.Cmd)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.tree = t
	c.mu.Unlock()

	if err := c.Cmd.Start(); err != nil {
		c.mu.Lock()
		_ = c.tree.release()
		c.mu.Unlock()
		return err
	}

	// The context may already have been cancelled between the process being
	// created and this point; Kill has then stopped it and there is nothing to
	// attach.
	c.mu.Lock()
	if !c.killed {
		err = c.tree.attach(c.Cmd.Process)
	}
	c.mu.Unlock()
	if err != nil {
		_ = c.Kill()
		_ = c.Cmd.Wait()
		c.mu.Lock()
		c.waited = true
		_ = c.tree.release()
		c.mu.Unlock()
		return err
	}
	return nil
}

// Kill terminates the command and every process it started.
//
// It is the embedded exec.Cmd's Cancel function, so cancelling the context
// calls it. It is safe to call more than once; after Wait has returned there
// is nothing left to kill and it reports os.ErrProcessDone.
func (c *Cmd) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killed = true
	if c.waited {
		return os.ErrProcessDone
	}
	if c.Cmd.Process == nil {
		return nil
	}
	if c.tree == nil {
		return c.Cmd.Process.Kill()
	}
	return c.tree.kill(c.Cmd.Process)
}

// Wait waits for the command to exit, then terminates anything it left
// running and releases the kill group.
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	c.mu.Lock()
	// exec.Cmd.Wait returns only after its cancellation goroutine finished,
	// so no Kill can be in flight from it once this is set.
	c.waited = true
	if c.tree != nil {
		// Best effort: the command's own result is what the caller asked
		// about, and a member that cannot be signalled cannot be helped here.
		_ = c.tree.release()
	}
	c.mu.Unlock()
	return err
}

// Run starts the command and waits for it to finish.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Output runs the command and returns its standard output. It mirrors
// exec.Cmd.Output, including attaching captured stderr to an *exec.ExitError.
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	captureErr := c.Stderr == nil
	if captureErr {
		c.Stderr = &stderr
	}
	err := c.Run()
	if err != nil && captureErr {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitErr.Stderr = stderr.Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput runs the command and returns its combined standard output
// and standard error.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	return b.Bytes(), err
}
