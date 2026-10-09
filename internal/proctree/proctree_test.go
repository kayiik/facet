package proctree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the helper processes. A helper never runs tests,
// and every helper exits on its own after helperLifetime, so even a broken
// implementation cannot leave anything running for long.
const (
	helperEnv      = "PROCTREE_TEST_HELPER"
	pidFileEnv     = "PROCTREE_TEST_PIDFILE"
	goFileEnv      = "PROCTREE_TEST_GOFILE"
	helperLifetime = 90 * time.Second
)

func TestMain(m *testing.M) {
	switch role := os.Getenv(helperEnv); role {
	case "sleeper":
		time.Sleep(helperLifetime)
		os.Exit(0)
	case "parent", "parent-exits", "parent-holds-pipe", "parent-detached":
		runParent(role)
	case "exit-ok":
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runParent starts a long-sleeping grandchild and records its PID. A
// "parent" then sleeps until it is killed; the other roles exit as soon as
// the test says so, leaving the grandchild behind.
func runParent(role string) {
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = append(os.Environ(), helperEnv+"=sleeper")
	if role == "parent-holds-pipe" {
		// The grandchild inherits this process's stdout and stderr, which
		// are the caller's pipes: the shape that kept exec.Cmd.Wait blocked.
		grandchild.Stdout, grandchild.Stderr = os.Stdout, os.Stderr
	}
	if role == "parent-detached" {
		// What a launcher does when it starts a browser "detached": the
		// grandchild leaves the process group for a session of its own.
		detach(grandchild)
	}
	if err := grandchild.Start(); err != nil {
		os.Exit(3)
	}
	pidFile := os.Getenv(pidFileEnv)
	temp := pidFile + ".tmp"
	if err := os.WriteFile(temp, []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600); err != nil {
		os.Exit(4)
	}
	if err := os.Rename(temp, pidFile); err != nil {
		os.Exit(5)
	}
	if role == "parent" || role == "parent-detached" {
		time.Sleep(helperLifetime)
		os.Exit(0)
	}
	// Exit only once the test holds a handle on the grandchild, so the test
	// observes the very process this helper started.
	deadline := time.Now().Add(helperLifetime)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Getenv(goFileEnv)); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(0)
}

type helper struct {
	cmd     *Cmd
	pidFile string
	goFile  string
}

func newHelper(ctx context.Context, t *testing.T, role string) *helper {
	t.Helper()
	dir := t.TempDir()
	h := &helper{
		cmd:     CommandContext(ctx, os.Args[0]),
		pidFile: filepath.Join(dir, "grandchild.pid"),
		goFile:  filepath.Join(dir, "go"),
	}
	h.cmd.Env = append(os.Environ(), helperEnv+"="+role, pidFileEnv+"="+h.pidFile, goFileEnv+"="+h.goFile)
	// Whatever the test concluded, the helper may not outlive it. The
	// grandchild is cleaned up by its watcher.
	t.Cleanup(func() { _ = h.cmd.Kill() })
	return h
}

// grandchild waits for the helper to report its grandchild and starts
// watching it while it is known to be alive.
func (h *helper) grandchild(t *testing.T) *watched {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(h.pidFile); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			w := watchProcess(t, pid)
			if !w.running() {
				t.Fatal("the grandchild is not running; the test has no subject")
			}
			return w
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the helper did not start its grandchild within 30s")
	return nil
}

func (h *helper) release(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(h.goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitBounded runs wait and fails the test if it does not return in time.
func waitBounded(t *testing.T, limit time.Duration, wait func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Wait did not return within %s", limit)
		return nil
	}
}

func TestCancelKillsGrandchild(t *testing.T) {
	testCancelKillsGrandchild(t, "parent")
}

// testCancelKillsGrandchild starts a helper of the given role, cancels it once
// its grandchild runs, and requires both to be gone.
func testCancelKillsGrandchild(t *testing.T, role string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHelper(ctx, t, role)
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := h.grandchild(t)

	started := time.Now()
	cancel()
	if err := waitBounded(t, 15*time.Second, h.cmd.Wait); err == nil {
		t.Fatal("a cancelled command reported success")
	}
	if h.cmd.ProcessState == nil {
		t.Fatal("the direct child was not reaped")
	}
	if !grandchild.exitedWithin(10 * time.Second) {
		t.Fatal("the grandchild outlived its cancelled parent")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestDeadlineKillsGrandchild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	h := newHelper(ctx, t, "parent")
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := h.grandchild(t)

	if err := waitBounded(t, 25*time.Second, h.cmd.Wait); err == nil {
		t.Fatal("a command past its deadline reported success")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("the command ended before its deadline: %v", ctx.Err())
	}
	if !grandchild.exitedWithin(10 * time.Second) {
		t.Fatal("the grandchild outlived the deadline")
	}
}

// A command that exits normally but leaves a descendant running must not
// leave it running: Wait releases the group, and the release kills it.
func TestWaitKillsDescendantsLeftBehind(t *testing.T) {
	h := newHelper(context.Background(), t, "parent-exits")
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := h.grandchild(t)
	h.release(t)
	if err := waitBounded(t, 30*time.Second, h.cmd.Wait); err != nil {
		t.Fatalf("the parent exited cleanly but Wait failed: %v", err)
	}
	if !grandchild.exitedWithin(10 * time.Second) {
		t.Fatal("a descendant outlived the completed command")
	}
}

// A descendant holding the root's output pipes kept exec.Cmd.Wait blocked
// for as long as it lived. WaitDelay bounds that, and the descendant dies.
func TestWaitDelayBoundsInheritedPipes(t *testing.T) {
	h := newHelper(context.Background(), t, "parent-holds-pipe")
	h.cmd.WaitDelay = 500 * time.Millisecond
	var out bytes.Buffer
	h.cmd.Stdout = &out
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	grandchild := h.grandchild(t)
	h.release(t)
	started := time.Now()
	err := waitBounded(t, 30*time.Second, h.cmd.Wait)
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("held pipes delayed Wait by %s", elapsed)
	}
	if !grandchild.exitedWithin(10 * time.Second) {
		t.Fatal("the pipe-holding descendant outlived the command")
	}
}

func TestCancelledBeforeStartRunsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := newHelper(ctx, t, "parent")
	if err := h.cmd.Start(); err == nil {
		_ = h.cmd.Wait()
		t.Fatal("a command whose context was already cancelled started")
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(h.pidFile); !os.IsNotExist(err) {
		t.Fatal("a cancelled command started a grandchild")
	}
}

func TestRunReportsSuccessAndStartTwiceFails(t *testing.T) {
	h := newHelper(context.Background(), t, "exit-ok")
	if err := waitBounded(t, 30*time.Second, h.cmd.Run); err != nil {
		t.Fatalf("a clean exit was reported as %v", err)
	}
	if h.cmd.ProcessState == nil || !h.cmd.ProcessState.Success() {
		t.Fatalf("unexpected process state: %v", h.cmd.ProcessState)
	}
	if err := h.cmd.Start(); err == nil {
		t.Fatal("a Cmd started twice")
	}
	if err := h.cmd.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Kill after exit: %v", err)
	}
}

func TestCommandContextConfiguresCancellation(t *testing.T) {
	cmd := CommandContext(context.Background(), os.Args[0])
	if cmd.Cancel == nil {
		t.Fatal("cancellation is not wired to the tree kill")
	}
	if cmd.WaitDelay != DefaultWaitDelay {
		t.Fatalf("WaitDelay = %s, want %s", cmd.WaitDelay, DefaultWaitDelay)
	}
}

func TestOutputMirrorsExec(t *testing.T) {
	h := newHelper(context.Background(), t, "exit-ok")
	out, err := h.cmd.Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("unexpected output %q", out)
	}
	preset := newHelper(context.Background(), t, "exit-ok")
	preset.cmd.Stdout = &bytes.Buffer{}
	if _, err := preset.cmd.Output(); err == nil {
		t.Fatal("Output accepted a preset Stdout")
	}
}
