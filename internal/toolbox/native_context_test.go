package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The toolbox test binary doubles as a fake media program that starts a
// long-sleeping child of its own. Each role exits by itself after
// treeHelperLifetime, so nothing it starts can outlive the run for long.
const (
	treeRoleEnv        = "FACET_TEST_TREE_ROLE"
	treePIDFileEnv     = "FACET_TEST_TREE_PIDFILE"
	treeHelperLifetime = 90 * time.Second
)

func init() {
	switch os.Getenv(treeRoleEnv) {
	case "sleeper":
		time.Sleep(treeHelperLifetime)
		os.Exit(0)
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), treeRoleEnv+"=sleeper")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		pidFile := os.Getenv(treePIDFileEnv)
		if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(4)
		}
		if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
			os.Exit(5)
		}
		time.Sleep(treeHelperLifetime)
		os.Exit(0)
	}
}

// installTestBinary copies the running test binary into dir under name, so
// it can stand in for a real program found on PATH.
func installTestBinary(t *testing.T, dir, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(dir, name+exeSuffix())
	dest, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dest, source)
	if closeErr := dest.Close(); copyErr != nil || closeErr != nil {
		t.Fatalf("copy test binary: %v %v", copyErr, closeErr)
	}
	return target
}

func TestNativeRunRejectsCancelledContextBeforeToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := RunContext(ctx, "subtitle_gen", []byte(`{"segments":[{"text":"must not write","start":0,"end":1}],"output_path":"must-not-write.srt"}`))
	if result.OK || result.Error == nil || result.Error.Code != "cancelled" {
		t.Fatalf("cancelled tool accepted: %+v", result)
	}
}

// Cancelling a running media command must stop the program AND everything it
// started. os/exec alone killed only the direct child, so a renderer's
// browser or a wrapper's encoder kept running after the run was abandoned.
func TestNativeCancellationKillsTheWholeProcessTree(t *testing.T) {
	bin := t.TempDir()
	installTestBinary(t, bin, "ffmpeg")
	t.Setenv("PATH", bin)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	t.Setenv(treeRoleEnv, "parent")
	t.Setenv(treePIDFileEnv, pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runCommandContext(ctx, "ffmpeg")
		done <- err
	}()

	var grandchild *pidWatch
	deadline := time.Now().Add(30 * time.Second)
	for grandchild == nil {
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			grandchild = watchPID(t, pid)
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the fake program did not start its child within 30s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !grandchild.running() {
		t.Fatal("the grandchild is not running before cancellation; the test has no subject")
	}

	started := time.Now()
	cancel()
	select {
	case err := <-done:
		var tf *toolFailure
		if !errors.As(err, &tf) || tf.err.Code != "command_timeout" {
			t.Fatalf("cancellation was not reported as a timeout: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the cancelled command did not return within 20s")
	}
	if !grandchild.exitedWithin(10 * time.Second) {
		t.Fatal("the grandchild of a cancelled media command is still running")
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestSavedCompositionPreservesProfileDuringEstimate(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "props.json")
	if err := os.WriteFile(file, []byte(`{"width":640,"height":360,"fps":24,"duration_seconds":2,"cuts":[{"id":"title","type":"hero_title","text":"TEST","in_seconds":0,"out_seconds":2}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"input_path": file})
	result, _, err := doVideoComposeContext(context.Background(), "estimate", request)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["estimated_duration_seconds"] == nil {
		t.Fatal("saved composition did not resolve to render estimate")
	}
}

func TestSilentGraphicPropsDoesNotMuteDeclaredMedia(t *testing.T) {
	for _, test := range []struct {
		body   string
		silent bool
	}{
		{`{"cuts":[{"type":"hero_title","text":"Silent"}]}`, true},
		{`{"cuts":[{"type":"video","source":"clip.mp4"}]}`, false},
		{`{"cuts":[{"type":"hero_title","text":"Narrated"}],"audio":{"narration":{"src":"voice.mp3"}}}`, false},
	} {
		if got := silentGraphicProps([]byte(test.body)); got != test.silent {
			t.Fatalf("silentGraphicProps(%s)=%v", test.body, got)
		}
	}
}
