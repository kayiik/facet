package toolbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A renamed test executable is a portable, offline fake CLI, including on Windows.
func init() {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != "gflow" || os.Getenv("FACET_TEST_GFLOW") == "" {
		return
	}
	mode := os.Getenv("FACET_TEST_GFLOW")
	if len(os.Args) > 1 && os.Args[1] == "hold-pipes" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	log, _ := os.OpenFile(os.Getenv("FACET_TEST_GFLOW_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	_ = json.NewEncoder(log).Encode(os.Args[1:])
	_ = log.Close()
	var stage string
	count := 1
	for i, arg := range os.Args {
		if arg == "-o" {
			stage = os.Args[i+1]
		}
		if arg == "-c" {
			count, _ = strconv.Atoi(os.Args[i+1])
		}
	}
	kind := os.Args[1]
	ext := ".png"
	mime := "image/png"
	if kind == "video" {
		ext, mime = ".mp4", "video/mp4"
	}
	var media []gflowMedia
	for i := 0; i < count; i++ {
		path := filepath.Join(stage, fmt.Sprintf("generated-%d%s", i, ext))
		_ = os.WriteFile(path, []byte("offline provider fixture"), 0600)
		if mode == "escape" {
			path = os.Getenv("FACET_TEST_GFLOW_OUTSIDE")
		}
		if mode == "directory" {
			path = stage
		}
		if mode == "empty" {
			_ = os.WriteFile(path, nil, 0600)
		}
		if mode == "relative" {
			path = filepath.Base(path)
		}
		media = append(media, gflowMedia{ID: fmt.Sprint("media-", i), Type: kind, MIMEType: mime,
			URL: "https://example.invalid/media?Signature=do-not-return", LocalPath: path})
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "sensitive provider diagnostic")
		os.Exit(1)
	}
	if mode == "timeout" {
		time.Sleep(2 * time.Minute)
		os.Exit(1)
	}
	if mode == "wrong-type" {
		media[0].Type = "audio"
	}
	if mode == "missing-id" {
		media[0].ID = ""
	}
	if mode == "missing-file" {
		_ = os.Remove(media[0].LocalPath)
	}
	if mode == "malformed" {
		fmt.Print(`{"video_url":"https://example.invalid/not-the-cli-schema"}`)
		os.Exit(0)
	}
	if mode == "pipe-exit" || mode == "pipe-timeout" {
		child := exec.Command(os.Args[0], "hold-pipes")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile(os.Getenv("FACET_TEST_GFLOW_LOG")+".child", []byte(strconv.Itoa(child.Process.Pid)), 0600)
		if mode == "pipe-timeout" {
			time.Sleep(2 * time.Minute)
		}
	}
	if mode == "progress" {
		for _, poll := range []string{".", ".", ".\n", ".", ".\r\n\t "} {
			fmt.Print(poll)
		}
	}
	if mode == "prose" {
		fmt.Print("... Upscaling complete\n")
	}
	if mode == "overflow" {
		fmt.Print(strings.Repeat(".", gflowStdoutLimit+1))
	}
	fmt.Fprintln(os.Stderr, "ordinary progress is not JSON")
	_ = json.NewEncoder(os.Stdout).Encode(media)
	if mode == "trailing-json" {
		fmt.Print(`[]`)
	}
	if mode == "trailing-dots" {
		fmt.Print("...")
	}
	os.Exit(0)
}

func fakeGFlow(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	name := "gflow"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(dest, source)
	closeErr := dest.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("copy test CLI: %v %v", err, closeErr)
	}
	log := filepath.Join(dir, "calls.jsonl")
	t.Cleanup(func() {
		data, err := os.ReadFile(log + ".child")
		if err == nil {
			pid, _ := strconv.Atoi(string(data))
			if child, err := os.FindProcess(pid); err == nil {
				_ = child.Kill()
				_, _ = child.Wait()
			}
		}
	})
	t.Setenv("PATH", dir)
	t.Setenv("FACET_TEST_GFLOW", mode)
	t.Setenv("FACET_TEST_GFLOW_LOG", log)
	return log
}

func providerRequest(t *testing.T, value map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGFlowDirectoryResponse(t *testing.T) {
	for _, kind := range []string{"image", "video", "progress"} {
		t.Run(kind, func(t *testing.T) {
			mode := "success"
			if kind == "progress" {
				kind, mode = "video", "progress"
			}
			log := fakeGFlow(t, mode)
			dir := t.TempDir()
			out := filepath.Join(dir, "final.mp4")
			request := map[string]any{"prompt": "--literal prompt", "output_path": out}
			fn := doGFlowVideo
			count := 1
			if kind == "image" {
				fn, count = doGFlowImage, 2
				request["count"], request["model"], request["reference_image"] = 2, "gem_pix_2", "ref-id"
			} else {
				request["start_frame"], request["end_frame"] = "start-id", "end-id"
			}
			value, _, err := fn("run", providerRequest(t, request))
			if err != nil {
				t.Fatal(err)
			}
			res := value.(map[string]any)
			outputs := res["outputs"].([]gflowOutput)
			if res["mock"] != false || len(outputs) != count {
				t.Fatalf("result: %#v", res)
			}
			for _, item := range outputs {
				data, err := os.ReadFile(item.Output)
				hash := sha256.Sum256(data)
				if err != nil || string(data) != "offline provider fixture" || item.SHA256 != hex.EncodeToString(hash[:]) {
					t.Fatalf("copy/provenance: %#v %v", item, err)
				}
			}
			encoded, _ := json.Marshal(res)
			if strings.Contains(string(encoded), "Signature") {
				t.Fatal("signed URL leaked")
			}
			calls, _ := os.ReadFile(log)
			if strings.Count(string(calls), "\n") != 1 {
				t.Fatalf("expected one invocation: %s", calls)
			}
			var args []string
			_ = json.Unmarshal(calls, &args)
			if args[len(args)-2] != "--" || args[len(args)-1] != "--literal prompt" {
				t.Fatalf("prompt not preserved: %v", args)
			}
			if kind == "video" && !strings.Contains(string(calls), `"1080p"`) {
				t.Fatal("default upscaling resolution not requested")
			}
			for _, flag := range []string{"--start", "--end", "--ref"} {
				if (kind == "video" && flag != "--ref") || (kind == "image" && flag == "--ref") {
					if !strings.Contains(string(calls), flag) {
						t.Fatalf("missing supported flag %s", flag)
					}
				}
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != count {
				t.Fatalf("staging leaked: %v", entries)
			}
		})
	}
}

func TestGFlowFailureNeverMocksOrRetries(t *testing.T) {
	for _, mode := range []string{"fail", "malformed", "escape", "directory", "empty", "wrong-type", "missing-id", "missing-file", "timeout", "prose", "trailing-json", "trailing-dots", "overflow", "pipe-exit", "pipe-timeout"} {
		t.Run(mode, func(t *testing.T) {
			log := fakeGFlow(t, mode)
			dir := t.TempDir()
			out := filepath.Join(dir, "existing.mp4")
			_ = os.WriteFile(out, []byte("preserve"), 0600)
			t.Setenv("FACET_TEST_GFLOW_OUTSIDE", out)
			timeout := 30
			start := time.Now()
			res, _, err := doGFlowVideo("run", providerRequest(t, map[string]any{"prompt": "test", "output_path": out, "timeout_seconds": timeout}))
			if err == nil || res != nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("expected sanitized error, got %v %v", res, err)
			}
			var toolErr *toolFailure
			if !errors.As(err, &toolErr) {
				t.Fatalf("expected structured failure: %v", err)
			}
			encoded, _ := json.Marshal(toolErr.err)
			if strings.Contains(string(encoded), "Signature") || strings.Contains(string(encoded), "example.invalid") {
				t.Fatal("provider response leaked into error")
			}
			if strings.HasPrefix(mode, "pipe-") {
				// Allow Windows executable scanning/startup under full-suite load,
				// but return well before the descendant's two-minute pipe hold.
				if time.Since(start) > 45*time.Second || toolErr.err.Code != "command_timeout" {
					t.Fatalf("descendant pipes were not bounded: %v %v", time.Since(start), err)
				}
			}
			if mode == "overflow" && !strings.Contains(err.Error(), "1 MiB") {
				t.Fatalf("missing overflow error: %v", err)
			}
			data, _ := os.ReadFile(out)
			if string(data) != "preserve" {
				t.Fatal("existing output changed")
			}
			calls, _ := os.ReadFile(log)
			if strings.Count(string(calls), "\n") != 1 {
				t.Fatalf("expected exactly one invocation: %s", calls)
			}
			entries, _ := os.ReadDir(dir)
			recovery, ok := toolErr.err.Details["recovery_path"].(string)
			if mode == "missing-file" {
				// Nothing was downloaded: no staging folder is left behind
				// and no recovery is offered.
				if ok || toolErr.err.Details["quarantined"] != nil || len(entries) != 1 {
					t.Fatalf("an empty staging folder was kept: %#v %v", toolErr.err, entries)
				}
				return
			}
			// TempDir may retain forward slashes on Windows; require the same parent, not spelling.
			parentRel, relErr := filepath.Rel(dir, filepath.Dir(recovery))
			if !ok || !filepath.IsAbs(recovery) || relErr != nil || parentRel != "." || !strings.HasPrefix(filepath.Base(recovery), ".gflow-") || toolErr.err.Details["quarantined"] != true || len(entries) != 2 {
				t.Fatalf("missing safe quarantine: %#v %v", toolErr.err, entries)
			}
			if info, err := os.Stat(recovery); err != nil || !info.IsDir() {
				t.Fatalf("recovery directory not retained: %v", err)
			}
			if mode != "empty" {
				data, err := os.ReadFile(filepath.Join(recovery, "generated-0.mp4"))
				if err != nil || string(data) != "offline provider fixture" {
					t.Fatalf("download lost: %v", err)
				}
			}
		})
	}
}

func TestGFlowStdoutBound(t *testing.T) {
	var output gflowStdout
	chunk := []byte(strings.Repeat(".", 32768))
	for i := 0; i < gflowStdoutLimit/len(chunk); i++ {
		if n, err := output.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if output.overflow || output.data.Len() != gflowStdoutLimit {
		t.Fatal("exact limit should be accepted")
	}
	for i := 0; i < 100; i++ {
		if n, err := output.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("excess output must drain: %d %v", n, err)
		}
	}
	if !output.overflow || output.data.Len() != gflowStdoutLimit {
		t.Fatal("stdout capture exceeded limit")
	}
}

func TestGFlowMissingDependencyAndExplicitMock(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for i, fn := range []func(string, []byte) (any, []string, error){doGFlowVideo, doGFlowImage} {
		dir := t.TempDir()
		out := filepath.Join(dir, "new", "output")
		r := map[string]any{"prompt": "test", "output_path": out}
		if value, _, err := fn("run", providerRequest(t, r)); err == nil || value != nil || !strings.Contains(err.Error(), "gflow CLI is required") {
			t.Fatalf("missing dependency: %v %v", value, err)
		}
		if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
			t.Fatal("dependency failure created output directory")
		}
		r["mock"] = true
		value, _, err := fn("run", providerRequest(t, r))
		if i == 0 {
			// The video mock is made with FFmpeg, which this PATH lacks.
			if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
				t.Fatalf("mock video without ffmpeg: %v %v", value, err)
			}
			continue
		}
		if err != nil || value.(map[string]any)["mock"] != true {
			t.Fatalf("explicit mock: %v %v", value, err)
		}
	}
}

func TestGFlowEstimateAndValidation(t *testing.T) {
	for _, fn := range []func(string, []byte) (any, []string, error){doGFlowVideo, doGFlowImage} {
		value, _, err := fn("estimate", []byte(`{"prompt":"test"}`))
		if err != nil || value.(map[string]any)["estimated_cost"] != nil || value.(map[string]any)["cost_known"] != false {
			t.Fatalf("real cost must be unknown: %v %v", value, err)
		}
		for _, field := range []string{"timeout_seconds", "aspect_ratio", "model"} {
			r := map[string]any{"prompt": "test", field: "invalid"}
			if field == "timeout_seconds" {
				r[field] = -1
			}
			if _, _, err := fn("estimate", providerRequest(t, r)); err == nil {
				t.Fatalf("accepted invalid %s", field)
			}
		}
	}
	for _, duration := range []float64{-1, 1.5, 5, 100} {
		if _, _, err := doGFlowVideo("estimate", providerRequest(t, map[string]any{"prompt": "test", "duration": duration})); err == nil {
			t.Fatalf("accepted duration %v", duration)
		}
	}
	for _, count := range []int{-1, 5} {
		if _, _, err := doGFlowImage("estimate", providerRequest(t, map[string]any{"prompt": "test", "count": count})); err == nil {
			t.Fatalf("accepted count %v", count)
		}
	}
}

func TestGFlowOutputSafety(t *testing.T) {
	log := fakeGFlow(t, "success")
	dir := t.TempDir()
	for _, path := range []string{dir, filepath.Join(dir, "link.mp4")} {
		if path != dir {
			if err := os.Symlink(filepath.Join(dir, "untouched.mp4"), path); err != nil {
				t.Logf("symlink unavailable: %v", err)
				continue
			}
		}
		if _, _, err := doGFlowVideo("run", providerRequest(t, map[string]any{"prompt": "test", "output_path": path})); err == nil {
			t.Fatalf("accepted unsafe output %s", path)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("provider invoked before output validation")
	}
}

func TestGFlowRelativeResponse(t *testing.T) {
	fakeGFlow(t, "relative")
	out := filepath.Join(t.TempDir(), "final.png")
	if _, _, err := doGFlowImage("run", providerRequest(t, map[string]any{"prompt": "test", "output_path": out})); err != nil {
		t.Fatal(err)
	}
}
