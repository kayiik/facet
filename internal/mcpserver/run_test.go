package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kayiik/facet/internal/toolbox"
)

// pipes connects a client to Run in process: what the client writes, Run
// reads, and the other way round.
type pipes struct {
	serverIn  *io.PipeReader
	clientOut *io.PipeWriter
	clientIn  *io.PipeReader
	serverOut *io.PipeWriter
}

func newPipes(t *testing.T) *pipes {
	t.Helper()
	p := &pipes{}
	p.serverIn, p.clientOut = io.Pipe()
	p.clientIn, p.serverOut = io.Pipe()
	t.Cleanup(func() {
		_ = p.clientOut.Close()
		_ = p.serverOut.Close()
		_ = p.clientIn.Close()
		_ = p.serverIn.Close()
	})
	return p
}

// serve starts Run and returns the channel its result arrives on.
func (p *pipes) serve(ctx context.Context, opts Options) <-chan error {
	opts.In, opts.Out = p.serverIn, p.serverOut
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	return done
}

func waitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Run did not return")
	}
}

// The stream API speaks newline-delimited JSON-RPC, exactly as on stdio.
func TestRunAnswersInitializeAndToolsListOverAStream(t *testing.T) {
	p := newPipes(t)
	done := p.serve(testContext(t), Options{Root: t.TempDir(), Version: "9.8.7-test"})

	lines := make(chan []byte, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(p.clientIn)
		scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for scanner.Scan() {
			lines <- append([]byte(nil), scanner.Bytes()...)
		}
	}()
	send := func(message string) {
		t.Helper()
		written := make(chan error, 1)
		go func() {
			_, err := io.WriteString(p.clientOut, message+"\n")
			written <- err
		}()
		select {
		case err := <-written:
			if err != nil {
				t.Fatalf("write: %v", err)
			}
		case <-time.After(testTimeout):
			t.Fatal("the server did not read")
		}
	}
	receive := func(id int, result any) {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("the server closed its output")
			}
			var response struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      int             `json:"id"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(line, &response); err != nil {
				t.Fatalf("not a JSON-RPC line: %v\n%s", err, line)
			}
			if response.JSONRPC != "2.0" || response.ID != id || response.Error != nil {
				t.Fatalf("unexpected response: %s", line)
			}
			if err := json.Unmarshal(response.Result, result); err != nil {
				t.Fatalf("result: %v", err)
			}
		case <-time.After(testTimeout):
			t.Fatal("no response")
		}
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"stream-test","version":"0"}}}`)
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools   *struct{}       `json:"tools"`
			Logging json.RawMessage `json:"logging"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	receive(1, &initialized)
	if initialized.ServerInfo.Name != "facet" || initialized.ServerInfo.Version != "9.8.7-test" {
		t.Errorf("serverInfo = %+v", initialized.ServerInfo)
	}
	if initialized.ProtocolVersion != "2025-06-18" || initialized.Capabilities.Tools == nil || initialized.Instructions == "" {
		t.Errorf("initialize result = %+v", initialized)
	}
	if initialized.Capabilities.Logging != nil {
		t.Errorf("the server advertises logging it never sends: %s", initialized.Capabilities.Logging)
	}

	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var listed struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
			Annotations map[string]any `json:"annotations"`
			Meta        map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	receive(2, &listed)
	served := map[string]bool{}
	for _, tool := range listed.Tools {
		served[tool.Name] = true
		_, declaresDestructive := tool.Annotations["destructiveHint"].(bool)
		if tool.InputSchema["type"] != "object" || !declaresDestructive || tool.Meta["facet"] == nil {
			t.Errorf("%s is not fully declared on the wire: %+v", tool.Name, tool)
		}
	}
	for _, name := range append(toolbox.Names(), planningTools...) {
		if !served[name] {
			t.Errorf("tools/list over the stream is missing %s", name)
		}
	}
	if len(listed.Tools) != len(toolbox.Names())+len(planningTools) {
		t.Errorf("tools/list returned %d tools", len(listed.Tools))
	}

	// Hanging up is a clean end.
	_ = p.clientOut.Close()
	waitRun(t, done)
}

// A client that hangs up mid-run must not leave the run going: the session
// can no longer deliver its result.
func TestRunEndsRunsWhenTheClientHangsUp(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan struct{})
	setRunner(t, func(ctx context.Context, tool string, _ []byte) toolbox.Envelope {
		close(started)
		<-ctx.Done()
		close(ended)
		return toolbox.Envelope{OK: false, Tool: tool, Operation: "run", Error: &toolbox.ToolError{Code: "cancelled", Message: "cancelled"}, Warnings: []string{}}
	})
	p := newPipes(t)
	ctx := testContext(t)
	done := p.serve(ctx, Options{Root: t.TempDir()})

	client := mcp.NewClient(&mcp.Implementation{Name: "hangup-test", Version: "0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	cs, err := client.Connect(ctx, &mcp.IOTransport{Reader: p.clientIn, Writer: p.clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(ctx)
	t.Cleanup(func() {
		cancelCall()
		within(t, "the client to close", func() { _ = cs.Close() })
	})
	go func() {
		_, _ = cs.CallTool(callCtx, &mcp.CallToolParams{Name: "media_probe", Arguments: map[string]any{"input": "a.mp4"}})
	}()
	select {
	case <-started:
	case <-time.After(testTimeout):
		t.Fatal("the run never started")
	}

	_ = p.clientOut.Close() // the client hangs up: Run reads end of stream

	select {
	case <-ended:
	case <-time.After(testTimeout):
		t.Fatal("the run outlived the client")
	}
	waitRun(t, done)
}

func TestRunStopsWhenItsContextEnds(t *testing.T) {
	started := make(chan struct{})
	setRunner(t, func(ctx context.Context, tool string, _ []byte) toolbox.Envelope {
		close(started)
		<-ctx.Done()
		return toolbox.Envelope{OK: false, Tool: tool, Operation: "run", Error: &toolbox.ToolError{Code: "cancelled", Message: "cancelled"}, Warnings: []string{}}
	})
	p := newPipes(t)
	ctx, stop := context.WithCancel(testContext(t))
	defer stop()
	done := p.serve(ctx, Options{Root: t.TempDir()})

	client := mcp.NewClient(&mcp.Implementation{Name: "stop-test", Version: "0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	cs, err := client.Connect(testContext(t), &mcp.IOTransport{Reader: p.clientIn, Writer: p.clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(testContext(t))
	t.Cleanup(func() {
		cancelCall()
		_ = p.serverOut.Close()
		within(t, "the client to close", func() { _ = cs.Close() })
	})
	go func() {
		_, _ = cs.CallTool(callCtx, &mcp.CallToolParams{Name: "media_probe", Arguments: map[string]any{"input": "a.mp4"}})
	}()
	select {
	case <-started:
	case <-time.After(testTimeout):
		t.Fatal("the run never started")
	}
	stop() // the run in flight must not hold the server open
	waitRun(t, done)
}

func TestRunRejectsUnusableConfiguration(t *testing.T) {
	p := newPipes(t)
	ctx := testContext(t)
	if err := Run(ctx, Options{Out: p.serverOut}); err == nil {
		t.Error("Run accepted a missing input stream")
	}
	if err := Run(ctx, Options{In: p.serverIn}); err == nil {
		t.Error("Run accepted a missing output stream")
	}
	if err := Run(ctx, Options{Root: "this/root/does/not/exist", In: p.serverIn, Out: p.serverOut}); err == nil {
		t.Error("Run accepted a root that does not exist")
	}
}
