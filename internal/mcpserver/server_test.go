package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kayiik/facet/internal/toolbox"
)

const testTimeout = 60 * time.Second

var planningTools = []string{"tools_list", "describe", "estimate", "capabilities", "pipelines_list", "pipeline_describe", "guidance"}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// setRunner replaces the tool runner for servers created by this test.
func setRunner(t *testing.T, run func(context.Context, string, []byte) toolbox.Envelope) {
	t.Helper()
	previous := runTool
	runTool = run
	t.Cleanup(func() { runTool = previous })
}

func setProgressInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := progressInterval
	progressInterval = interval
	t.Cleanup(func() { progressInterval = previous })
}

// within fails the test if fn does not return in time.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Errorf("timed out waiting for %s", what)
	}
}

// connect serves a fresh server to an in-memory client.
func connect(t *testing.T, opts Options, clientOpts *mcp.ClientOptions, roots ...*mcp.Root) *mcp.ClientSession {
	t.Helper()
	cs, _ := connectServer(t, opts, clientOpts, roots...)
	return cs
}

func connectServer(t *testing.T, opts Options, clientOpts *mcp.ClientOptions, roots ...*mcp.Root) (*mcp.ClientSession, *server) {
	t.Helper()
	base, stopRuns := context.WithCancel(context.Background())
	srv, err := newServer(base, opts)
	if err != nil {
		stopRuns()
		t.Fatalf("newServer: %v", err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := testContext(t)
	ss, err := srv.mcp.Connect(ctx, serverTransport, nil)
	if err != nil {
		stopRuns()
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "facet-test", Version: "test"}, clientOpts)
	client.AddRoots(roots...)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		stopRuns()
		_ = ss.Close()
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		stopRuns() // ends any run still in flight
		within(t, "the client to close", func() { _ = cs.Close() })
		within(t, "the server session to end", func() { _ = ss.Wait() })
	})
	return cs, srv
}

// countProgress counts the progress notifications a server delivers.
func countProgress(srv *server) *atomic.Int64 {
	var sent atomic.Int64
	srv.mcp.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if method == "notifications/progress" && err == nil {
				sent.Add(1)
			}
			return result, err
		}
	})
	return &sent
}

// envelope is the decoded structured content of a tool result.
type envelope struct {
	OK        bool            `json:"ok"`
	Tool      string          `json:"tool"`
	Operation string          `json:"operation"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
	Warnings  []string           `json:"warnings"`
	Execution map[string]any     `json:"execution"`
	Artifacts []toolbox.Artifact `json:"artifacts"`
}

// decode checks that a result carries one envelope twice, as text and as
// structured content, with isError agreeing with ok.
func decode(t *testing.T, res *mcp.CallToolResult) envelope {
	t.Helper()
	if res == nil {
		t.Fatal("no result")
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("structured content: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(structured, &env); err != nil {
		t.Fatalf("structured content is not an envelope: %v\n%s", err, structured)
	}
	if len(res.Content) != 1 {
		t.Fatalf("want one content block, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(text.Text), &fromText); err != nil {
		t.Fatalf("text content is not JSON: %v", err)
	}
	_ = json.Unmarshal(structured, &fromStructured)
	if a, b := mustJSON(t, fromText), mustJSON(t, fromStructured); a != b {
		t.Errorf("text and structured content differ:\n%s\n%s", a, b)
	}
	if res.IsError == env.OK {
		t.Errorf("isError=%v but ok=%v", res.IsError, env.OK)
	}
	return env
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, envelope) {
	t.Helper()
	res, err := cs.CallTool(testContext(t), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res, decode(t, res)
}

// recorder is a runner that records what each tool received.
type recorder struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (r *recorder) run(_ context.Context, tool string, data []byte) toolbox.Envelope {
	var args map[string]any
	_ = json.Unmarshal(data, &args)
	r.mu.Lock()
	r.calls = append(r.calls, args)
	r.mu.Unlock()
	return toolbox.Envelope{OK: true, Tool: tool, Operation: "run", Result: map[string]any{"received": args}, Warnings: []string{}}
}

func (r *recorder) received(t *testing.T) []map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.calls...)
}

func TestToolsListDeclaresEveryToolAndItsEffects(t *testing.T) {
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	res, err := cs.ListTools(testContext(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		if tools[tool.Name] != nil {
			t.Errorf("%s is listed twice", tool.Name)
		}
		tools[tool.Name] = tool
	}
	names := toolbox.Names()
	if len(res.Tools) != len(names)+len(planningTools) {
		t.Errorf("listed %d tools, want %d registry tools and %d planning tools", len(res.Tools), len(names), len(planningTools))
	}
	check := func(tool *mcp.Tool, want toolbox.Effects) {
		t.Helper()
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s has no annotations", tool.Name)
			return
		}
		if a.ReadOnlyHint != want.ReadOnly || a.IdempotentHint != want.Deterministic {
			t.Errorf("%s: readOnlyHint=%v idempotentHint=%v, want %v %v", tool.Name, a.ReadOnlyHint, a.IdempotentHint, want.ReadOnly, want.Deterministic)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint != want.Network {
			t.Errorf("%s: openWorldHint=%v, want %v", tool.Name, a.OpenWorldHint, want.Network)
		}
		if a.DestructiveHint == nil || *a.DestructiveHint != !want.ReadOnly {
			t.Errorf("%s: destructiveHint=%v, want explicitly %v (file-writing tools may replace files)", tool.Name, a.DestructiveHint, !want.ReadOnly)
		}
		meta, _ := tool.Meta["facet"].(map[string]any)
		for field, value := range map[string]bool{
			"may_charge": want.MayCharge, "network": want.Network, "external_write": want.ExternalWrite,
			"deterministic": want.Deterministic, "read_only": want.ReadOnly,
		} {
			if meta[field] != value {
				t.Errorf("%s: _meta.facet.%s = %v, want %v", tool.Name, field, meta[field], value)
			}
		}
		if !strings.Contains(tool.Description, fmt.Sprintf("Effects: may_charge=%t, network=%t, external_write=%t, deterministic=%t, read_only=%t.",
			want.MayCharge, want.Network, want.ExternalWrite, want.Deterministic, want.ReadOnly)) {
			t.Errorf("%s: the description does not state its effects: %q", tool.Name, tool.Description)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("%s: input schema type = %v", tool.Name, schema["type"])
		}
		for _, keyword := range topLevelCombinators {
			if _, ok := schema[keyword]; ok {
				t.Errorf("%s: input schema has top-level %s", tool.Name, keyword)
			}
		}
	}
	for _, name := range names {
		tool := tools[name]
		if tool == nil {
			t.Errorf("registry tool %s is not served", name)
			continue
		}
		check(tool, toolbox.EffectsFor(name))
		if !strings.HasPrefix(tool.Description, toolbox.Description(name)) {
			t.Errorf("%s: description %q does not start with the capability", name, tool.Description)
		}
		described, _ := toolbox.Describe(name)
		cost, _ := described["cost"].(map[string]any)
		if meta := tool.Meta["facet"].(map[string]any); meta["cost_known"] != cost["known"] {
			t.Errorf("%s: _meta.facet.cost_known = %v, describe says %v", name, meta["cost_known"], cost["known"])
		}
		// The served schema keeps every request field the registry declares.
		served, _ := json.Marshal(tool.InputSchema.(map[string]any)["properties"])
		declared, _ := json.Marshal(toolbox.Parameters(name)["properties"])
		var a, b any
		_ = json.Unmarshal(served, &a)
		_ = json.Unmarshal(declared, &b)
		if mustJSON(t, a) != mustJSON(t, b) {
			t.Errorf("%s: served request properties differ from the registry's", name)
		}
	}
	for _, name := range planningTools {
		tool := tools[name]
		if tool == nil {
			t.Errorf("planning tool %s is not served", name)
			continue
		}
		check(tool, toolbox.Effects{ReadOnly: true})
		if meta := tool.Meta["facet"].(map[string]any); meta["cost_known"] != true {
			t.Errorf("%s: a free planning tool must declare its cost known", name)
		}
	}
}

// A request the tool rejects is a tool error carrying the toolbox's own
// message, never a protocol error or a panic.
func TestInvalidRequestsAreToolErrorsInTheToolboxsWords(t *testing.T) {
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	ctx := testContext(t)
	for _, c := range []struct {
		name string
		args any
		raw  string // what the tool receives
	}{
		{"missing input", map[string]any{}, "{}"},
		{"unknown field", map[string]any{"input": "a.mp4", "bogus": true}, ""},
		{"wrong type", map[string]any{"input": 42}, `{"input":42}`},
		{"not an object", json.RawMessage(`[1,2]`), `[1,2]`},
		{"a string", json.RawMessage(`"probe it"`), `"probe it"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, env := call(t, cs, "media_probe", c.args)
			if !res.IsError || env.OK || env.Error == nil {
				t.Fatalf("want a tool error, got %+v", env)
			}
			if c.raw != "" {
				want := toolbox.RunContext(ctx, "media_probe", []byte(c.raw))
				if want.Error == nil || env.Error.Message != want.Error.Message || env.Error.Code != want.Error.Code {
					t.Errorf("error = %s %q, the toolbox says %+v", env.Error.Code, env.Error.Message, want.Error)
				}
			}
			if env.Tool != "media_probe" {
				t.Errorf("tool = %q", env.Tool)
			}
		})
	}
}

func TestPathEscapesAreRefusedBeforeTheToolRuns(t *testing.T) {
	rec := &recorder{}
	setRunner(t, rec.run)
	dir := t.TempDir()
	cs := connect(t, Options{Root: dir}, nil)
	root, _ := resolveRoot(dir)
	parent := filepath.Dir(root)
	for _, args := range []map[string]any{
		{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": "../escaped.srt"},
		{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": filepath.Join(parent, "escaped.srt")},
		{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": "renders/../../escaped.srt"},
	} {
		res, env := call(t, cs, "subtitle_gen", args)
		if !res.IsError || env.Error == nil {
			t.Fatalf("an escaping path was accepted: %v", args["output_path"])
		}
		if env.Error.Code != "invalid_request" || !strings.Contains(env.Error.Message, root) {
			t.Errorf("the refusal must be invalid_request naming the root %s: %s %q", root, env.Error.Code, env.Error.Message)
		}
		if env.Error.Details["root"] != root || env.Error.Details["argument"] != "output_path" {
			t.Errorf("details = %#v", env.Error.Details)
		}
		if env.Tool != "subtitle_gen" || env.Operation != "run" || env.Execution["provider"] == nil {
			t.Errorf("the refusal is not a complete envelope: %+v", env)
		}
	}
	if got := rec.received(t); len(got) != 0 {
		t.Fatalf("the tool ran despite the refusal: %v", got)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped.srt")); err == nil {
		t.Error("a file was written outside the root")
	}
}

func TestRelativePathsResolveInsideTheRoot(t *testing.T) {
	rec := &recorder{}
	setRunner(t, rec.run)
	dir := t.TempDir()
	cs := connect(t, Options{Root: dir}, nil)
	root, _ := resolveRoot(dir)

	res, env := call(t, cs, "source_edit", map[string]any{
		"segments": []any{map[string]any{"input": "media/a.mp4", "start": 0, "end": 1.25}},
		"target":   map[string]any{"width": 1280, "height": 720},
		"output":   "renders/out.mp4",
		"title":    "../prose stays prose",
	})
	if res.IsError {
		t.Fatalf("refused: %+v", env.Error)
	}
	got := rec.received(t)
	if len(got) != 1 {
		t.Fatalf("the tool ran %d times", len(got))
	}
	args := got[0]
	segment := args["segments"].([]any)[0].(map[string]any)
	if !samePath(segment["input"].(string), filepath.Join(root, "media", "a.mp4")) {
		t.Errorf("segments[0].input = %v", segment["input"])
	}
	if !samePath(args["output"].(string), filepath.Join(root, "renders", "out.mp4")) {
		t.Errorf("output = %v", args["output"])
	}
	if args["title"] != "../prose stays prose" || segment["end"] != 1.25 {
		t.Errorf("non-path values changed: %#v", args)
	}
}

// A real, fast tool end to end: subtitle_gen needs no external program and
// writes a file, so its envelope carries an artifact.
func TestARealRunReturnsItsArtifacts(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{Root: dir}, nil)
	root, _ := resolveRoot(dir)
	res, env := call(t, cs, "subtitle_gen", map[string]any{
		"segments":    []any{map[string]any{"text": "Hello from Facet", "start": 0, "end": 1.5}},
		"format":      "srt",
		"output_path": "captions/hello.srt",
	})
	if res.IsError || !env.OK {
		t.Fatalf("subtitle_gen failed: %+v", env.Error)
	}
	want := filepath.Join(root, "captions", "hello.srt")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("the subtitle file is not inside the root: %v", err)
	}
	if len(env.Artifacts) != 1 {
		t.Fatalf("artifacts = %+v", env.Artifacts)
	}
	artifact := env.Artifacts[0]
	digest := sha256.Sum256(data)
	if !samePath(artifact.Path, want) || artifact.SHA256 != hex.EncodeToString(digest[:]) || artifact.SizeBytes != int64(len(data)) {
		t.Errorf("artifact = %+v, want %s with sha256 %x", artifact, want, digest)
	}
	var result struct {
		Output   string `json:"output"`
		CueCount int    `json:"cue_count"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil || !samePath(result.Output, want) || result.CueCount != 1 {
		t.Errorf("result = %s", env.Result)
	}
}

func TestMediaProbeOnARealFile(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not on PATH")
	}
	dir := t.TempDir()
	ctx := testContext(t)
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=64x48:rate=10:duration=0.5",
		"-c:v", "mpeg4", "-pix_fmt", "yuv420p", filepath.Join(dir, "tiny.mp4"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not make a fixture: %v %s", err, out)
	}
	cs := connect(t, Options{Root: dir}, nil)
	res, env := call(t, cs, "media_probe", map[string]any{"input": "tiny.mp4"})
	if res.IsError || !env.OK {
		t.Fatalf("media_probe failed: %+v", env.Error)
	}
	var result struct {
		Input        string `json:"input"`
		VideoStreams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"video_streams"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.VideoStreams) != 1 || result.VideoStreams[0].Width != 64 || result.VideoStreams[0].Height != 48 {
		t.Errorf("result = %s", env.Result)
	}
	if env.Execution["provider"] != "ffprobe" {
		t.Errorf("execution = %v", env.Execution)
	}
}

func TestClientCancellationReachesTheRunner(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan error, 1)
	setRunner(t, func(ctx context.Context, tool string, _ []byte) toolbox.Envelope {
		close(started)
		select {
		case <-ctx.Done():
			observed <- ctx.Err()
		case <-time.After(testTimeout):
			observed <- errors.New("the run was never cancelled")
		}
		return toolbox.Envelope{OK: false, Tool: tool, Operation: "run", Error: &toolbox.ToolError{Code: "cancelled", Message: "cancelled"}, Warnings: []string{}}
	})
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	callCtx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{Name: "video_compose", Arguments: map[string]any{"output": "out.mp4"}})
		callErr <- err
	}()
	select {
	case <-started:
	case <-time.After(testTimeout):
		t.Fatal("the run never started")
	}
	cancel()
	select {
	case err := <-observed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the runner saw %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("cancellation never reached the runner")
	}
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Errorf("CallTool returned %v", err)
	}
}

func TestProgressIsReportedWhileAToolRuns(t *testing.T) {
	const interval = 20 * time.Millisecond
	setProgressInterval(t, interval)
	release := make(chan struct{})
	setRunner(t, func(ctx context.Context, tool string, _ []byte) toolbox.Envelope {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return toolbox.Envelope{OK: true, Tool: tool, Operation: "run", Result: map[string]any{}, Warnings: []string{}}
	})
	var mu sync.Mutex
	var notes []*mcp.ProgressNotificationParams
	arrived := make(chan struct{}, 1024)
	cs, srv := connectServer(t, Options{Root: t.TempDir()}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			notes = append(notes, req.Params)
			mu.Unlock()
			arrived <- struct{}{}
		},
	})
	sent := countProgress(srv)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(notes)
	}

	ctx := testContext(t)
	results := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "media_probe", Arguments: map[string]any{"input": "a.mp4"},
			Meta: mcp.Meta{"progressToken": "probe-1"},
		})
		if err != nil {
			t.Errorf("CallTool: %v", err)
		}
		results <- res
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-arrived:
		case <-time.After(testTimeout):
			t.Fatalf("only %d progress notifications arrived", count())
		}
	}
	close(release)
	select {
	case res := <-results:
		if res == nil || res.IsError {
			t.Fatalf("the call failed: %+v", res)
		}
	case <-time.After(testTimeout):
		t.Fatal("the call never finished")
	}

	// The server stops reporting before it answers, so by the time the
	// result is here the count is final.
	final := sent.Load()
	time.Sleep(10 * interval)
	if after := sent.Load(); after != final {
		t.Errorf("%d progress notifications were sent after the result", after-final)
	}
	deadline := time.Now().Add(testTimeout)
	for int64(count()) < final && time.Now().Before(deadline) {
		time.Sleep(interval)
	}
	mu.Lock()
	received := append([]*mcp.ProgressNotificationParams(nil), notes...)
	mu.Unlock()
	if int64(len(received)) != final {
		t.Fatalf("the client received %d of %d progress notifications", len(received), final)
	}
	for i, note := range received {
		if note.ProgressToken != "probe-1" {
			t.Errorf("notification %d has token %v", i, note.ProgressToken)
		}
		if i > 0 && note.Progress <= received[i-1].Progress {
			t.Errorf("progress did not increase: %v then %v", received[i-1].Progress, note.Progress)
		}
		if !strings.Contains(note.Message, "media_probe") {
			t.Errorf("message %q does not name the tool", note.Message)
		}
	}

	// Without a token, no progress is sent however long the call runs.
	setRunner(t, func(_ context.Context, tool string, _ []byte) toolbox.Envelope {
		time.Sleep(10 * interval)
		return toolbox.Envelope{OK: true, Tool: tool, Operation: "run", Result: map[string]any{}, Warnings: []string{}}
	})
	quiet, quietServer := connectServer(t, Options{Root: t.TempDir()}, nil)
	quietSent := countProgress(quietServer)
	if res, _ := call(t, quiet, "media_probe", map[string]any{"input": "a.mp4"}); res.IsError {
		t.Fatal("the call failed")
	}
	if n := quietSent.Load(); n != 0 {
		t.Errorf("%d progress notifications were sent without a progress token", n)
	}
}

func TestAPanicInAToolIsAToolError(t *testing.T) {
	setRunner(t, func(context.Context, string, []byte) toolbox.Envelope { panic("boom") })
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	res, env := call(t, cs, "color_grade", map[string]any{"input_path": "a.mp4", "output_path": "b.mp4"})
	if !res.IsError || env.Error == nil || env.Error.Code != "internal_error" || !strings.Contains(env.Error.Message, "boom") {
		t.Fatalf("want an internal_error tool result, got %+v", env)
	}
	if _, err := cs.ListTools(testContext(t), nil); err != nil {
		t.Errorf("the session did not survive: %v", err)
	}
}

// Garbage arguments never break the server: every tool answers with a
// result, and the session keeps working.
func TestMalformedArgumentsNeverBreakTheServer(t *testing.T) {
	rec := &recorder{}
	setRunner(t, rec.run)
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	garbage := []json.RawMessage{
		json.RawMessage(`[]`), json.RawMessage(`"x"`), json.RawMessage(`42`), json.RawMessage(`true`), json.RawMessage(`null`),
		json.RawMessage(`{"tool":5,"arguments":[1],"request":"x","method":{"a":1}}`),
		json.RawMessage(`{"tool":"media_probe","arguments":{"input":{"nested":["../x"]}}}`),
		json.RawMessage(`{"input":["a.mp4",{"path":7}],"output_path":null,"clips":[null,1,"x.mp4"]}`),
	}
	res, err := cs.ListTools(testContext(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		for _, args := range garbage {
			res, err := cs.CallTool(testContext(t), &mcp.CallToolParams{Name: tool.Name, Arguments: args})
			if err != nil {
				t.Fatalf("%s %s: protocol error %v", tool.Name, args, err)
			}
			decode(t, res)
		}
	}
	if _, err := cs.ListTools(testContext(t), nil); err != nil {
		t.Errorf("the session did not survive: %v", err)
	}
}

func TestClientRootsConfineWhenNoRootIsSet(t *testing.T) {
	rec := &recorder{}
	setRunner(t, rec.run)
	first, second := t.TempDir(), t.TempDir()
	firstRoot, _ := resolveRoot(first)
	secondRoot, _ := resolveRoot(second)

	client := mcp.NewClient(&mcp.Implementation{Name: "facet-test", Version: "test"}, nil)
	client.AddRoots(&mcp.Root{URI: fileURI(first)})
	base, stopRuns := context.WithCancel(context.Background())
	defer stopRuns()
	srv, err := newServer(base, Options{})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := testContext(t)
	ss, err := srv.mcp.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		within(t, "the client to close", func() { _ = cs.Close() })
		within(t, "the server session to end", func() { _ = ss.Wait() })
	}()

	if res, env := call(t, cs, "media_probe", map[string]any{"input": "clip.mp4"}); res.IsError {
		t.Fatalf("refused: %+v", env.Error)
	}
	if got := rec.received(t); len(got) != 1 || !samePath(got[0]["input"].(string), filepath.Join(firstRoot, "clip.mp4")) {
		t.Fatalf("the client root was not used: %v", got)
	}
	res, env := call(t, cs, "subtitle_gen", map[string]any{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": "../escaped.srt"})
	if !res.IsError || !strings.Contains(env.Error.Message, firstRoot) {
		t.Fatalf("an escape from the client root was allowed or not explained: %+v", env.Error)
	}

	// Changing the roots changes the allowed root for later calls.
	client.RemoveRoots(fileURI(first))
	client.AddRoots(&mcp.Root{URI: fileURI(second)})
	if res, env := call(t, cs, "media_probe", map[string]any{"input": "clip.mp4"}); res.IsError {
		t.Fatalf("refused: %+v", env.Error)
	}
	if got := rec.received(t); len(got) != 2 || !samePath(got[1]["input"].(string), filepath.Join(secondRoot, "clip.mp4")) {
		t.Fatalf("the changed roots were not used: %v", got)
	}
}

func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

// The root is only resolved when a call needs it: for a path argument, or for
// a tool that may write, whose default output lands inside the root. An
// unusable client root fails exactly those calls, saying which root it was.
func TestOnlyCallsThatNeedTheRootResolveIt(t *testing.T) {
	foreign := "file://fileserver/share/project" // a remote host is never a local root off Windows
	if runtime.GOOS == "windows" {
		foreign = "file:///home/me/project" // no drive: not a local absolute path on Windows
	}
	for _, uri := range []string{fileURI(filepath.Join(t.TempDir(), "not-here")), foreign} {
		rec := &recorder{}
		setRunner(t, rec.run)
		cs := connect(t, Options{}, nil, &mcp.Root{URI: uri})
		if res, env := call(t, cs, "video_selector", map[string]any{"query": "hello"}); res.IsError {
			t.Fatalf("%s: a read-only call without paths failed on the client's root: %+v", uri, env.Error)
		}
		for _, args := range []map[string]any{{"text": "hello"}, {"text": "hello", "output_path": "hello.mp3"}} {
			res, env := call(t, cs, "edge_tts", args)
			if !res.IsError || env.Error.Code != "root_unavailable" || !strings.Contains(env.Error.Message, uri) {
				t.Errorf("%s %v: want root_unavailable naming the client's root, got %+v", uri, args, env.Error)
			}
		}
		if got := rec.received(t); len(got) != 1 {
			t.Errorf("%s: tools ran %d times, want once (the read-only one)", uri, len(got))
		}
	}
}

// A default output (the file a tool names itself when a request names none)
// lands inside the allowed root, never in the server's working directory.
func TestDefaultOutputsLandInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	cs := connect(t, Options{Root: root}, nil)
	resolved, err := resolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool, file string
		args       map[string]any
	}{
		{"subtitle_gen", "subtitles.srt", map[string]any{"segments": []any{map[string]any{"text": "Hello", "start": 0, "end": 1}}}},
		{"openai_image", "openai_image.png", map[string]any{"prompt": "a test card", "mock": true}},
		{"flux_image", "flux_image.png", map[string]any{"prompt": "a test card", "mock": true}},
	} {
		res, env := call(t, cs, c.tool, c.args)
		if res.IsError || !env.OK {
			t.Fatalf("%s failed: %+v", c.tool, env.Error)
		}
		want := filepath.Join(resolved, c.file)
		if _, err := os.Stat(want); err != nil {
			t.Errorf("%s: the default output is not inside the root: %v", c.tool, err)
		}
		if len(env.Artifacts) != 1 || !samePath(env.Artifacts[0].Path, want) {
			t.Errorf("%s: artifacts = %+v, want %s", c.tool, env.Artifacts, want)
		}
	}
	// Every tool, called with the least it accepts, writes nothing beside the
	// server either: most refuse the request, none may escape the root.
	for _, name := range toolbox.Names() {
		_, _ = call(t, cs, name, map[string]any{})
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("tools wrote into the server's working directory: %v", entries)
	}
}

func TestWithoutRootsTheWorkingDirectoryConfines(t *testing.T) {
	rec := &recorder{}
	setRunner(t, rec.run)
	cs := connect(t, Options{}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := resolveRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	if res, env := call(t, cs, "media_probe", map[string]any{"input": "no-such-dir/clip.mp4"}); res.IsError {
		t.Fatalf("refused: %+v", env.Error)
	}
	got := rec.received(t)
	if len(got) != 1 || !samePath(got[0]["input"].(string), filepath.Join(root, "no-such-dir", "clip.mp4")) {
		t.Fatalf("the working directory was not the root: %v", got)
	}
	res, env := call(t, cs, "subtitle_gen", map[string]any{"segments": []any{map[string]any{"text": "hi", "start": 0, "end": 1}}, "output_path": filepath.Join(filepath.Dir(root), "x.srt")})
	if !res.IsError || !strings.Contains(env.Error.Message, root) {
		t.Errorf("a path outside the working directory was allowed: %+v", env)
	}
}

func TestEstimateValidatesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{Root: dir}, nil)
	root, _ := resolveRoot(dir)
	segments := []any{map[string]any{"text": "hi", "start": 0, "end": 1}}

	res, env := call(t, cs, "estimate", map[string]any{"tool": "subtitle_gen", "arguments": map[string]any{"segments": segments, "output_path": "subs/a.srt"}})
	if res.IsError || env.Operation != "estimate" || env.Tool != "subtitle_gen" {
		t.Fatalf("estimate failed: %+v", env)
	}
	if _, err := os.Stat(filepath.Join(root, "subs")); err == nil {
		t.Error("an estimate wrote output")
	}
	res, env = call(t, cs, "estimate", map[string]any{"tool": "subtitle_gen", "arguments": map[string]any{"segments": segments, "output_path": "../a.srt"}})
	if !res.IsError || !strings.Contains(env.Error.Message, root) || env.Operation != "estimate" {
		t.Errorf("an escaping estimate was allowed: %+v", env)
	}
	res, env = call(t, cs, "estimate", map[string]any{"tool": "no_such_tool"})
	if !res.IsError || env.Error.Code != "unknown_tool" {
		t.Errorf("unknown tool: %+v", env)
	}
	res, env = call(t, cs, "estimate", map[string]any{"tool": "media_probe", "arguments": map[string]any{}})
	if !res.IsError || env.Tool != "media_probe" || env.Error.Code != "invalid_request" {
		t.Errorf("an estimate must report the tool's validation: %+v", env)
	}
}

// Argument errors are phrased in JSON terms an agent can act on.
func TestArgumentErrorsSpeakJSON(t *testing.T) {
	cs := connect(t, Options{Root: t.TempDir()}, nil)
	for _, c := range []struct{ args, want string }{
		{`{"tool":5}`, "tool must be a string, not number"},
		{`[1]`, "expected a JSON object, got array"},
		{`{"tool":"media_probe","bogus":1}`, `unknown field "bogus"`},
	} {
		res, env := call(t, cs, "describe", json.RawMessage(c.args))
		if !res.IsError || env.Error == nil {
			t.Fatalf("%s was accepted", c.args)
		}
		if !strings.Contains(env.Error.Message, c.want) || strings.Contains(env.Error.Message, "Go value") || strings.Contains(env.Error.Message, "struct") {
			t.Errorf("%s: message %q, want it to say %q in JSON terms", c.args, env.Error.Message, c.want)
		}
		if env.Operation != "describe" || env.Error.Code != "invalid_request" {
			t.Errorf("%s: envelope %+v", c.args, env)
		}
	}
}

func TestPlanningTools(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{Root: dir}, nil)

	res, env := call(t, cs, "tools_list", nil)
	var listing struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if res.IsError || json.Unmarshal(env.Result, &listing) != nil || len(listing.Tools) != len(toolbox.Names()) {
		t.Errorf("tools_list: %+v", env)
	}

	res, env = call(t, cs, "describe", map[string]any{"tool": "media_probe"})
	var description map[string]any
	if res.IsError || json.Unmarshal(env.Result, &description) != nil || description["request_schema"] == nil || env.Tool != "media_probe" {
		t.Errorf("describe: %+v", env)
	}
	for _, args := range []map[string]any{{"tool": "nope"}, {"tool": "--help"}, {}, {"tool": "media_probe", "extra": 1}} {
		if res, env := call(t, cs, "describe", args); !res.IsError || env.Error == nil {
			t.Errorf("describe %v succeeded: %+v", args, env)
		}
	}

	if res, env := call(t, cs, "capabilities", map[string]any{}); res.IsError {
		t.Errorf("capabilities: %+v", env.Error)
	}
	if res, env := call(t, cs, "pipelines_list", map[string]any{}); res.IsError {
		t.Errorf("pipelines_list: %+v", env.Error)
	}
	if res, env := call(t, cs, "pipeline_describe", map[string]any{"name": "animated-explainer", "stage": "script"}); res.IsError {
		t.Errorf("pipeline_describe: %+v", env.Error)
	}
	if res, _ := call(t, cs, "pipeline_describe", map[string]any{"name": "no-such-pipeline"}); !res.IsError {
		t.Error("pipeline_describe accepted an unknown pipeline")
	}
	if res, env := call(t, cs, "guidance", map[string]any{"path": "guidance/stages/script.md"}); res.IsError {
		t.Errorf("guidance: %+v", env.Error)
	}
	if res, _ := call(t, cs, "guidance", map[string]any{"path": "../go.mod"}); !res.IsError {
		t.Error("guidance served a file outside the bundle")
	}
}
