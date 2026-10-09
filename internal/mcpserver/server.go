// Package mcpserver serves Facet's tool registry to an agent harness over the
// Model Context Protocol. It is the agent-facing transport for every harness;
// the `facet` CLI remains the interface for people and scripts.
//
// The server is a stateless adapter. The harness owns models, approvals and
// permissions: Facet declares each tool's effects (annotations and
// _meta.facet) and executes the calls it receives. The one policy the server
// enforces itself is path confinement, because a file written outside the
// allowed root cannot be taken back by any later decision.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kayiik/facet/internal/toolbox"
)

// Options configure one server instance.
type Options struct {
	// Root confines tool paths. Empty means: client-supplied roots, else the
	// working directory.
	Root string
	// Version is the Facet release version reported to clients.
	Version string
	In      io.Reader
	Out     io.Writer
}

// runTool executes one Facet tool run. Cancelling ctx must stop the run; the
// toolbox kills the whole process tree. Tests replace it.
var runTool = toolbox.RunContext

// progressInterval is how often a running call reports progress when the
// client sent a progress token. Tests shorten it.
var progressInterval = 5 * time.Second

// rootsTimeout bounds the roots/list request a call may make.
const rootsTimeout = 10 * time.Second

const instructions = "Facet is a stateless media toolbox. Each tool runs one operation and returns " +
	"a JSON envelope (ok, result or error, warnings, execution, artifacts) as structured content. " +
	"Effects are declared in each tool's annotations and _meta.facet (may_charge, network, " +
	"external_write, deterministic, read_only); approval and consent belong to the harness. " +
	"Relative paths resolve against the allowed root, paths outside it are refused, and a " +
	"default output is written inside it. A call that may outlast this client's MCP time " +
	"limit (an estimate reports prefer_shell) runs better from a shell: facet tools run."

// Run serves MCP over the given streams (newline-delimited JSON-RPC, as on
// stdio) until the client disconnects or ctx ends. Both end in-flight runs,
// killing their processes, and both return nil; an error means the stream or
// the configuration was unusable. Run closes In, if it is an io.Closer, when
// the session ends, to release the reader; Out is left open.
func Run(ctx context.Context, opts Options) error {
	if opts.In == nil || opts.Out == nil {
		return errors.New("mcp: input and output streams are required")
	}
	// Runs derive from this context rather than from the session: a call's
	// own context only ends on an explicit cancellation, so without it a
	// client that hangs up would leave its render running to the end.
	runs, stopRuns := context.WithCancel(ctx)
	defer stopRuns()
	srv, err := newServer(runs, opts)
	if err != nil {
		return err
	}
	input := &hangupReader{r: opts.In, hangup: stopRuns}
	transport := &mcp.IOTransport{Reader: input, Writer: keepOpen{opts.Out}}
	err = srv.mcp.Run(ctx, transport)
	if ctx.Err() != nil || input.ended.Load() {
		// Stopped by the caller, or the client hung up. A hang-up during a
		// call also surfaces as a failure to deliver that call's result,
		// which is the expected consequence rather than a fault.
		return nil
	}
	return err
}

// hangupReader ends every run as soon as the client's stream ends. The
// session can no longer deliver a result once its input is gone, so a run
// that continued would only burn time and money.
type hangupReader struct {
	r      io.Reader
	hangup func()
	ended  atomic.Bool // the client closed its end of the stream
}

func (h *hangupReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			h.ended.Store(true)
		}
		h.hangup()
	}
	return n, err
}

func (h *hangupReader) Close() error {
	h.hangup()
	if c, ok := h.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// keepOpen leaves the caller's output stream open when the session closes.
type keepOpen struct{ io.Writer }

func (keepOpen) Close() error { return nil }

// server is one MCP server and the policy shared by its sessions.
type server struct {
	mcp      *mcp.Server
	base     context.Context // ends every run when done
	run      func(context.Context, string, []byte) toolbox.Envelope
	interval time.Duration
	root     string // fixed allowed root (Options.Root); empty: per session
	workdir  func() (string, error)

	mu    sync.Mutex
	roots map[*mcp.ServerSession]*sessionRoot
}

// sessionRoot caches a session's allowed root until its roots change.
type sessionRoot struct {
	generation uint64
	root       string
	known      bool
}

func newServer(base context.Context, opts Options) (*server, error) {
	s := &server{
		base:     base,
		run:      runTool,
		interval: progressInterval,
		roots:    map[*mcp.ServerSession]*sessionRoot{},
		workdir: sync.OnceValues(func() (string, error) {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			return resolveRoot(wd)
		}),
	}
	if opts.Root != "" {
		root, err := resolveRoot(opts.Root)
		if err != nil {
			return nil, fmt.Errorf("mcp: root %s is not an accessible directory: %w", opts.Root, err)
		}
		s.root = root
	}
	version := opts.Version
	if version == "" {
		version = toolbox.ProductVersion()
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "facet", Version: version}, &mcp.ServerOptions{
		Instructions: instructions,
		// The tool list is fixed for the life of the process, and the server
		// sends no log messages.
		Capabilities:            &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		RootsListChangedHandler: s.rootsChanged,
	})
	for _, t := range s.tools() {
		s.mcp.AddTool(t.tool, s.handler(t.tool.Name, t.call))
	}
	return s, nil
}

// operation performs one tool call and returns its envelope.
type operation func(ctx context.Context, req *mcp.CallToolRequest) outcome

// outcome is a call's envelope and whether it succeeded.
type outcome struct {
	envelope any
	ok       bool
}

// handler adapts an operation to the SDK: it ties the call to the server's
// lifetime, reports progress, and turns even a panic into a tool error.
func (s *server) handler(name string, op operation) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stopAfter := context.AfterFunc(s.base, cancel)
		defer stopAfter()

		milestone, stop := s.reportProgress(ctx, req, name)
		defer stop()
		ctx = toolbox.WithProgress(ctx, milestone)
		defer func() {
			if p := recover(); p != nil {
				tool, op := name, "run"
				if _, isFacetTool := facetToolName(name); !isFacetTool {
					tool, op = "", name
				}
				result, err = toolResult(failure(tool, op, "internal_error",
					fmt.Sprintf("Facet failed internally while handling %s: %v", name, p), nil)), nil
			}
		}()
		return toolResult(op(ctx, req)), nil
	}
}

// reportProgress sends progress notifications while a call runs, when the
// client asked for progress: one every interval, and one at once for each
// milestone the tool reports, such as a provider job it submitted, whose id
// later notifications repeat. The returned stop waits until no further
// notification can be sent, so none follows the result.
func (s *server) reportProgress(ctx context.Context, req *mcp.CallToolRequest, name string) (milestone func(toolbox.Progress), stop func()) {
	if req == nil || req.Params == nil || req.Session == nil || s.interval <= 0 {
		return nil, func() {}
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &progressReporter{ctx: ctx, session: req.Session, token: token, start: time.Now()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				message := fmt.Sprintf("%s running for %s", name, time.Since(p.start).Round(time.Second))
				if job := p.providerJob(); job != "" {
					message += "; provider job " + job
				}
				p.send(message)
			}
		}
	}()
	return p.milestone, func() {
		cancel()
		<-done
	}
}

// progressReporter sends one call's progress notifications. Progress is the
// elapsed time in seconds, kept strictly increasing across both senders.
type progressReporter struct {
	ctx     context.Context
	session *mcp.ServerSession
	token   any
	start   time.Time

	mu   sync.Mutex
	last float64
	job  string
}

func (p *progressReporter) milestone(m toolbox.Progress) {
	if m.ProviderJobID != "" {
		p.mu.Lock()
		p.job = m.ProviderJobID
		p.mu.Unlock()
	}
	p.send(m.Message)
}

func (p *progressReporter) providerJob() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.job
}

func (p *progressReporter) send(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return
	}
	progress := math.Round(time.Since(p.start).Seconds()*1000) / 1000
	if progress <= p.last {
		progress = p.last + 0.001
	}
	p.last = progress
	_ = p.session.NotifyProgress(p.ctx, &mcp.ProgressNotificationParams{
		ProgressToken: p.token,
		// Elapsed seconds: increases with every notification while the
		// total stays unknown.
		Progress: progress,
		Message:  message,
	})
}

// toolResult renders an envelope as a tool result: the JSON as text, for
// clients that read text, and the same object as structured content.
func toolResult(o outcome) *mcp.CallToolResult {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(o.envelope); err != nil {
		o = failure("", "", "internal_error", "Facet could not encode the result: "+err.Error(), nil)
		buf.Reset()
		_ = encoder.Encode(o.envelope)
	}
	data := bytes.TrimRight(buf.Bytes(), "\n")
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: json.RawMessage(data),
		IsError:           !o.ok,
	}
}

// failure builds a Facet error envelope for a call the server refused or
// could not complete, shaped like the toolbox's own.
func failure(tool, op, code, message string, details map[string]any) outcome {
	if details == nil {
		details = map[string]any{}
	}
	return outcome{envelope: toolbox.Envelope{
		OK: false, Tool: tool, Operation: op,
		Error:     &toolbox.ToolError{Code: code, Message: message, Details: details},
		Warnings:  []string{},
		Execution: execution(tool, op),
	}}
}

// execution reports what the tool would have done, as the toolbox's error
// envelopes do: its provider and declared network, write and cost facts.
func execution(tool, op string) toolbox.Execution {
	zero, actual := 0.0, 0.0
	e := toolbox.Execution{Provider: "local", EstimatedCost: &zero, ActualCost: &actual}
	facts, ok := describedFacts()[tool]
	if !ok {
		return e
	}
	effects := toolbox.EffectsFor(tool)
	if facts.provider != "" {
		e.Provider = facts.provider
	}
	e.Network = effects.Network
	e.ExternalWrite = op == "run" && effects.ExternalWrite
	if effects.MayCharge {
		// A chargeable operation's amount is unknown, never zero.
		e.EstimatedCost, e.ActualCost = nil, nil
	}
	return e
}

// facetRequest turns tool-call arguments into the JSON a Facet tool receives,
// with every path argument resolved inside the allowed root. Arguments that
// are not an object are passed on unchanged, so the tool rejects them in its
// own words; a missing or null object is an empty request.
func (s *server) facetRequest(root rootSource, tool, op string, raw json.RawMessage) ([]byte, *outcome) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber() // numbers reach the tool exactly as sent
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.More() {
		return trimmed, nil
	}
	args, isObject := value.(map[string]any)
	if !isObject {
		return trimmed, nil
	}
	confined, refusal := confineArguments(root, args)
	if refusal != nil {
		o := failure(tool, op, refusal.code(), refusal.message(), refusal.details())
		return nil, &o
	}
	data, err := json.Marshal(confined)
	if err != nil {
		o := failure(tool, op, "invalid_request", "arguments could not be encoded: "+err.Error(), nil)
		return nil, &o
	}
	return data, nil
}

// lazyRoot resolves a call's allowed root the first time a path argument
// needs it.
func (s *server) lazyRoot(ctx context.Context, ss *mcp.ServerSession) rootSource {
	return sync.OnceValues(func() (string, error) { return s.allowedRoot(ctx, ss) })
}

// allowedRoot is the directory a call's paths must stay inside: Options.Root
// when set, else the client's first file root when it supports roots, else
// the working directory.
func (s *server) allowedRoot(ctx context.Context, ss *mcp.ServerSession) (string, error) {
	if s.root != "" {
		return s.root, nil
	}
	if !supportsRoots(ss) {
		return s.workdir()
	}
	s.mu.Lock()
	entry := s.roots[ss]
	if entry == nil {
		entry = &sessionRoot{}
		s.roots[ss] = entry
	}
	if entry.known {
		root := entry.root
		s.mu.Unlock()
		return root, nil
	}
	generation := entry.generation
	s.mu.Unlock()

	root, cacheable, err := s.clientRoot(ctx, ss)
	if err != nil {
		return "", err
	}
	if cacheable {
		s.mu.Lock()
		if entry.generation == generation {
			entry.root, entry.known = root, true
		}
		s.mu.Unlock()
	}
	return root, nil
}

// clientRoot asks the client for its roots and resolves the first file root.
// A client that cannot answer, or has no file root, is treated like one
// without roots. A file root that is unusable here is an error rather than a
// reason to fall back: the agent resolves relative paths against the root it
// was shown, and silently confining it elsewhere would misplace its files.
func (s *server) clientRoot(ctx context.Context, ss *mcp.ServerSession) (root string, cacheable bool, err error) {
	listCtx, cancel := context.WithTimeout(ctx, rootsTimeout)
	defer cancel()
	result, err := ss.ListRoots(listCtx, nil)
	if err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err() // this call was cancelled, not the roots request
		}
		root, err := s.workdir()
		return root, err == nil, err
	}
	for _, r := range result.Roots {
		if r == nil {
			continue
		}
		if scheme, _ := urlScheme(r.URI); scheme != "file" {
			continue
		}
		path, err := fileURLPath(r.URI)
		if err == nil {
			root, err = resolveRoot(path)
		}
		if err != nil {
			return "", false, fmt.Errorf("the client's root %s is not an accessible local directory (facet mcp --root DIR sets the root explicitly): %w", r.URI, err)
		}
		return root, true, nil
	}
	root, err = s.workdir()
	return root, err == nil, err
}

func supportsRoots(ss *mcp.ServerSession) bool {
	if ss == nil {
		return false
	}
	params := ss.InitializeParams()
	return params != nil && params.Capabilities != nil && params.Capabilities.RootsV2 != nil
}

// rootsChanged forgets a session's root when the client's roots change.
func (s *server) rootsChanged(_ context.Context, req *mcp.RootsListChangedRequest) {
	if req == nil || req.Session == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.roots[req.Session]; entry != nil {
		entry.generation++
		entry.known = false
	}
}

// strictDecode decodes a call's arguments into dst, rejecting unknown fields
// as the tool's input schema declares. Missing or null arguments are empty.
func strictDecode(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("unexpected data after the arguments object")
	}
	return nil
}

// argumentsError describes a decoding failure in JSON terms, without Go
// type names.
func argumentsError(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field == "" {
			return "invalid arguments: expected a JSON object, got " + typeErr.Value
		}
		return fmt.Sprintf("invalid arguments: %s must be %s, not %s", typeErr.Field, jsonKind(typeErr.Type), typeErr.Value)
	}
	return "invalid arguments: " + strings.TrimPrefix(err.Error(), "json: ")
}

func jsonKind(t reflect.Type) string {
	if t == nil {
		return "a different JSON value"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a different JSON value"
	}
}
