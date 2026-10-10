package compa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/facet/internal/app/kernel"
)

// When the kernel starts this test binary as its hook process, it relays.
func TestMain(m *testing.M) {
	if os.Getenv("FACET_APP_TEST_HOOK") == "1" {
		if err := RunHookRelay(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// scriptedModel is an OpenAI-compatible endpoint whose replies a test
// scripts by the scenario named in the last user message.
type scriptedModel struct {
	calls map[string]map[string]any // scenario -> tool call {name, args}
}

var scenarioWord = regexp.MustCompile(`SCENARIO ([a-z0-9-]+)`)

func (s *scriptedModel) serve(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(s.handle)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String() + "/v1"
}

func (s *scriptedModel) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	body, _ := io.ReadAll(r.Body)
	json.Unmarshal(body, &req)
	scenario, last, lastContent := "", "", ""
	for _, m := range req.Messages {
		text, _ := m.Content.(string)
		if m.Role == "user" {
			if found := scenarioWord.FindStringSubmatch(text); found != nil {
				scenario = found[1]
			}
		}
		last, lastContent = m.Role, text
	}
	var text string
	var call map[string]any
	switch {
	case last == "tool":
		if len(lastContent) > 300 {
			lastContent = lastContent[:300]
		}
		text = "Done " + scenario + ". Tool said: " + lastContent
	case s.calls[scenario] != nil:
		call = s.calls[scenario]
	default:
		text = "Hello from the scripted model."
	}
	choice := map[string]any{"index": 0, "finish_reason": "stop"}
	message := map[string]any{"role": "assistant", "content": text}
	if call != nil {
		args, _ := json.Marshal(call["args"])
		message["content"] = nil
		message["tool_calls"] = []any{map[string]any{"id": "call_" + scenario, "type": "function",
			"function": map[string]any{"name": call["name"], "arguments": string(args)}}}
		choice["finish_reason"] = "tool_calls"
	}
	if !req.Stream {
		choice["message"] = message
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{choice}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	delta := map[string]any{"role": "assistant"}
	if call != nil {
		args, _ := json.Marshal(call["args"])
		delta["tool_calls"] = []any{map[string]any{"index": 0, "id": "call_" + scenario, "type": "function",
			"function": map[string]any{"name": call["name"], "arguments": string(args)}}}
	} else {
		delta["content"] = text
	}
	send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
	send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": choice["finish_reason"]}}})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// waitFor returns the first event after now that matches.
func waitFor(t *testing.T, events <-chan kernel.Event, what string, timeout time.Duration, match func(kernel.Event) bool) kernel.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("events closed while waiting for %s", what)
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s within %s", what, timeout)
		}
	}
}

// TestRealKernel drives a real compa-kernel and facet with a scripted model.
// It needs FACET_APP_KERNEL, FACET_APP_FACET and FFmpeg on PATH.
func TestRealKernel(t *testing.T) {
	kernelPath, facetPath := os.Getenv("FACET_APP_KERNEL"), os.Getenv("FACET_APP_FACET")
	if kernelPath == "" || facetPath == "" {
		t.Skip("FACET_APP_KERNEL and FACET_APP_FACET are not set")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	base := t.TempDir()
	home := filepath.Join(base, "compa")
	workspace := filepath.Join(home, "workspace")
	for _, dir := range []string{filepath.Join(workspace, "media"), filepath.Join(workspace, "renders")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	long := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30",
		"-t", "60", "-c:v", "libx264", "-preset", "ultrafast", filepath.Join(workspace, "media", "long.mp4"))
	if out, err := long.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	model := &scriptedModel{calls: map[string]map[string]any{
		"paid":       {"name": "mcp_facet_sora_video", "args": map[string]any{"prompt": "a red ball", "mock": true, "duration": 1, "output_path": "renders/paid.mp4"}},
		"shell-paid": {"name": "exec", "args": map[string]any{"action": "run", "command": "facet tools run sora_video --input {\"prompt\":\"x\",\"mock\":true,\"output_path\":\"renders/shell.mp4\"}"}},
		"long":       {"name": "mcp_facet_color_grade", "args": map[string]any{"input_path": "media/long.mp4", "output_path": "renders/graded.mp4", "overwrite": true}},
	}}
	endpoint := model.serve(t)
	self, _ := os.Executable()
	t.Setenv("FACET_APP_TEST_HOOK", "1") // inherited by the hook process only matters there
	logFile, _ := os.Create(filepath.Join(base, "kernel.log"))
	defer logFile.Close()

	k := New(Options{HomeOptions: HomeOptions{
		Kernel: kernelPath, Facet: facetPath, Home: home, FacetHome: filepath.Join(base, "facet"),
		HookCommand: []string{self},
		Agent:       "---\nname: Facet Studio\ndescription: Facet's video producer.\n---\nYou are Facet Studio.\n",
		Model:       &Model{Endpoint: endpoint, ID: "scripted"},
	}, PaidTools: []string{"sora_video", "kling_video"}, Log: logFile})
	ctx := context.Background()
	if err := k.Start(ctx); err != nil {
		log, _ := os.ReadFile(filepath.Join(base, "kernel.log"))
		t.Fatalf("start: %v\n%s", err, log)
	}
	stopped := false
	defer func() {
		if !stopped {
			k.Stop(ctx)
		}
	}()
	events := k.Events()
	waitFor(t, events, "ready", 10*time.Second, func(e kernel.Event) bool { return e.Kind == kernel.EventStatus && e.Status == "ready" })

	// Streamed text, and the turn's end.
	k.Send(ctx, "s1", "SCENARIO hello")
	waitFor(t, events, "the final message", time.Minute, func(e kernel.Event) bool {
		return e.Kind == kernel.EventMessage && e.Final && strings.Contains(e.Text, "Hello from the scripted model")
	})
	waitFor(t, events, "turn end", time.Minute, func(e kernel.Event) bool { return e.Kind == kernel.EventTurnEnd && e.Status == "completed" })

	// A paid tool waits for the window, and runs once approved.
	k.Send(ctx, "s1", "SCENARIO paid")
	req := waitFor(t, events, "an approval request", time.Minute, func(e kernel.Event) bool { return e.Kind == kernel.EventApprovalRequest })
	if !req.Approval.Tool.Paid || req.Approval.Tool.Facet != "sora_video" || req.Approval.Origin != "web" {
		t.Errorf("approval = %+v", req.Approval)
	}
	if err := k.Answer(req.Approval.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, "turn end", 2*time.Minute, func(e kernel.Event) bool { return e.Kind == kernel.EventTurnEnd })
	if _, err := os.Stat(filepath.Join(workspace, "renders", "paid.mp4")); err != nil {
		t.Errorf("the approved mock render is missing: %v", err)
	}

	// The shell cannot run a paid tool.
	k.Send(ctx, "s1", "SCENARIO shell-paid")
	final := waitFor(t, events, "the final message", time.Minute, func(e kernel.Event) bool {
		return e.Kind == kernel.EventMessage && e.Final && strings.Contains(e.Text, "Done shell-paid")
	})
	if !strings.Contains(final.Text, "mcp_facet_sora_video") {
		t.Errorf("the model was not told to use MCP: %s", final.Text)
	}
	if _, err := os.Stat(filepath.Join(workspace, "renders", "shell.mp4")); err == nil {
		t.Error("a paid tool ran from the shell")
	}

	// The window cancels a long render.
	k.Send(ctx, "s1", "SCENARIO long")
	waitFor(t, events, "the color_grade call", time.Minute, func(e kernel.Event) bool {
		return e.Kind == kernel.EventToolCall && e.Tool.Facet == "color_grade"
	})
	time.Sleep(2 * time.Second)
	if err := k.Cancel(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, "an aborted turn", time.Minute, func(e kernel.Event) bool { return e.Kind == kernel.EventTurnEnd && e.Status == "aborted" })

	if err := k.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	stopped = true
	waitFor(t, events, "stopped", 30*time.Second, func(e kernel.Event) bool { return e.Kind == kernel.EventStatus && e.Status == "stopped" })
	for range events {
	}
}
