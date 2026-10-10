// Package toolkit runs the Facet Toolkit, the `facet` command, for the App's
// own screens.
//
// Design rule R1: the window calls the Toolkit directly only for tools that
// cannot charge and are read-only or local; everything else goes through the
// agent engine, where consent is asked. Runner enforces that from the
// Toolkit's own effect declarations (`facet tools list`), so a tool's
// declaration, not a list kept here, decides. Besides running those tools,
// Runner only reads: the tool list (Tools) and the version (Version).
package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// Envelope is the JSON every `facet tools` command prints.
type Envelope struct {
	OK        bool            `json:"ok"`
	Tool      string          `json:"tool,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *ToolError      `json:"error,omitempty"`
	Warnings  []string        `json:"warnings"`
	Artifacts []Artifact      `json:"artifacts,omitempty"`
}

// ToolError is a failed run's error.
type ToolError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// Artifact is a file a run produced.
type Artifact struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// Effects are a tool's declared effects, from `facet tools list`.
type Effects struct {
	MayCharge     bool `json:"may_charge"`
	Network       bool `json:"network"`
	ExternalWrite bool `json:"external_write"`
}

// Direct reports whether the window may run the tool itself (R1): it cannot
// charge, and it is read-only or local.
func (e Effects) Direct() bool { return !e.MayCharge && (!e.ExternalWrite || !e.Network) }

// ErrNotDirect refuses a tool the window must leave to the agent engine.
var ErrNotDirect = errors.New("this tool may charge, or writes over the network; the App runs it only through the agent, which asks first")

// Exec runs the facet executable; tests replace it.
type Exec func(ctx context.Context, dir string, args ...string) (stdout []byte, err error)

// Runner runs one Toolkit.
type Runner struct {
	Facet string // the facet executable
	Exec  Exec

	once    sync.Once
	effects map[string]Effects
	listErr error
}

// New returns a Runner for the facet executable at path.
func New(path string) *Runner { return &Runner{Facet: path} }

func (r *Runner) exec(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if r.Exec != nil {
		return r.Exec(ctx, dir, args...)
	}
	cmd := exec.CommandContext(ctx, r.Facet, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && stdout.Len() == 0 {
		return nil, fmt.Errorf("facet %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	// A failed tool still prints its envelope and exits non-zero.
	return stdout.Bytes(), nil
}

// Effects returns every tool's declared effects.
func (r *Runner) Effects(ctx context.Context) (map[string]Effects, error) {
	r.once.Do(func() {
		tools, err := r.list(ctx)
		if err != nil {
			r.listErr = err
			return
		}
		r.effects = map[string]Effects{}
		for _, t := range tools {
			r.effects[t.Name] = t.Effects
		}
	})
	return r.effects, r.listErr
}

// Tool is one tool as `facet tools list` reports it: what it does, whether
// it can run on this computer, what it needs, and its declared effects.
type Tool struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	// Configured reports that every dependency is available.
	Configured   bool         `json:"configured"`
	Dependencies []Dependency `json:"dependencies"`
	Cost         Cost         `json:"cost"`
	Effects
}

// Dependency is something a tool needs: a program (type "binary"), an
// environment variable such as an API key ("env"), or a runtime the Toolkit
// installs beside itself ("runtime").
type Dependency struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Available bool   `json:"available"`
	// Resolution is "satisfied", "unsatisfied", or "unknown" for a key that
	// is set but cannot be proven to work until a provider answers.
	Resolution string `json:"resolution"`
	// Alternatives are the names a credential is read under, any one of
	// which will do.
	Alternatives []string `json:"alternatives,omitempty"`
}

// Cost is a tool's declared cost; an amount is known only for tools that
// cannot charge.
type Cost struct {
	Known    bool     `json:"known"`
	Amount   *float64 `json:"amount"`
	Currency string   `json:"currency"`
}

// Tools runs `facet tools list` and returns every tool as the Toolkit
// reports it now. Unlike Effects it is read on every call, because readiness
// changes when a program is installed.
func (r *Runner) Tools(ctx context.Context) ([]Tool, error) { return r.list(ctx) }

func (r *Runner) list(ctx context.Context) ([]Tool, error) {
	out, err := r.exec(ctx, "", "tools", "list")
	if err != nil {
		return nil, err
	}
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil || !env.OK {
		return nil, fmt.Errorf("facet tools list did not return the tool list: %v", err)
	}
	return env.Result.Tools, nil
}

// Version returns the Toolkit's version, as `facet version` prints it
// ("facet v0.0.1" is "0.0.1").
func (r *Runner) Version(ctx context.Context) (string, error) {
	out, err := r.exec(ctx, "", "version")
	if err != nil {
		return "", err
	}
	name, version, ok := strings.Cut(strings.TrimSpace(string(out)), " ")
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !ok || name != "facet" || version == "" || strings.ContainsAny(version, " \n") {
		return "", fmt.Errorf("facet version printed %q, not a version", bytes.TrimSpace(out))
	}
	return version, nil
}

// PaidTools returns the tools whose effects may charge, sorted.
func (r *Runner) PaidTools(ctx context.Context) ([]string, error) {
	effects, err := r.Effects(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for name, e := range effects {
		if e.MayCharge {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Run runs a tool the window may run itself, in dir, and returns its envelope.
// A tool that ran and failed returns its envelope with OK false and a nil
// error; err reports a tool the window may not run, or a Toolkit that did not
// answer.
func (r *Runner) Run(ctx context.Context, dir, tool string, request any) (Envelope, error) {
	return r.call(ctx, dir, "run", tool, request)
}

// Estimate validates a request and reports its cost without running it.
func (r *Runner) Estimate(ctx context.Context, dir, tool string, request any) (Envelope, error) {
	return r.call(ctx, dir, "estimate", tool, request)
}

func (r *Runner) call(ctx context.Context, dir, op, tool string, request any) (Envelope, error) {
	effects, err := r.Effects(ctx)
	if err != nil {
		return Envelope{}, err
	}
	e, known := effects[tool]
	if !known {
		return Envelope{}, fmt.Errorf("facet has no tool %q", tool)
	}
	if op == "run" && !e.Direct() {
		return Envelope{}, fmt.Errorf("%s: %w", tool, ErrNotDirect)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Envelope{}, err
	}
	out, err := r.exec(ctx, dir, "tools", op, tool, "--input", string(data))
	if err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(bytes.TrimSpace(out), &env); err != nil {
		return Envelope{}, fmt.Errorf("facet tools %s %s printed no envelope: %v", op, tool, err)
	}
	return env, nil
}
