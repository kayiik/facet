package wire

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/kayiik/facet/internal/bundle"
)

// WiringStatus is the observed state of one recorded wiring.
type WiringStatus struct {
	CLI          string `json:"cli"`
	Scope        string `json:"scope"`
	Project      string `json:"project,omitempty"`
	Root         string `json:"root"`
	FacetVersion string `json:"facet_version"`
	Executable   string `json:"executable"`
	// ExplicitExecutable is set when --exe chose the executable.
	ExplicitExecutable bool     `json:"explicit_executable,omitempty"`
	WiredAt            string   `json:"wired_at"`
	Files              int      `json:"files"`
	Missing            []string `json:"missing,omitempty"`
	Modified           []string `json:"modified,omitempty"`
	MCPMethod          string   `json:"mcp_method,omitempty"`
	// MCPState is ok, missing, modified, unknown (the configuration could
	// not be read), or none (no registration recorded).
	MCPState string `json:"mcp_state"`
	MCPFile  string `json:"mcp_file,omitempty"`
	// RulesState says whether the CLI still asks before every paid tool:
	// ok, missing, unknown, skipped (the settings did not allow rules),
	// absent (none recorded), or none (the CLI keeps no separate rules).
	RulesState string `json:"rules_state"`
	RulesFile  string `json:"rules_file,omitempty"`
	RulesNote  string `json:"rules_note,omitempty"`
	// Stale is set when another Facet version wired it.
	Stale bool `json:"stale,omitempty"`
	// ExecutableChanged is set when the MCP server runs another facet than
	// this one; ExecutableMissing when the registered one is gone.
	ExecutableChanged bool `json:"executable_changed,omitempty"`
	ExecutableMissing bool `json:"executable_missing,omitempty"`
	Pending           bool `json:"pending,omitempty"`
}

// Drifted reports whether anything differs from what was wired.
func (s WiringStatus) Drifted() bool {
	return len(s.Missing) > 0 || len(s.Modified) > 0 || s.MCPState != "ok" || s.Stale ||
		s.ExecutableChanged || s.ExecutableMissing || s.Pending ||
		s.RulesState == "missing" || s.RulesState == "unknown" || s.RulesState == "absent"
}

// Label names the wiring for people.
func (s WiringStatus) Label() string {
	return (&Wiring{CLI: s.CLI, Scope: s.Scope, Project: s.Project}).label()
}

// RewireCommand is the command that brings the wiring up to date.
func (s WiringStatus) RewireCommand() string {
	cmd := "facet wire " + s.CLI
	if s.Scope == string(bundle.ScopeProject) {
		cmd += " --scope project --project " + commandLine([]string{s.Project})
	}
	if s.ExplicitExecutable {
		cmd += " --exe " + commandLine([]string{s.Executable})
	}
	return cmd
}

// Problems lists each drift in a sentence.
func (s WiringStatus) Problems(running string) []string {
	var out []string
	if len(s.Modified) > 0 {
		out = append(out, fmt.Sprintf("%d file(s) changed since facet wire installed them", len(s.Modified)))
	}
	if len(s.Missing) > 0 {
		out = append(out, fmt.Sprintf("%d file(s) missing", len(s.Missing)))
	}
	switch s.MCPState {
	case "missing":
		out = append(out, fmt.Sprintf("MCP server %q is not registered in %s", bundle.MCPServerName, s.MCPFile))
	case "modified":
		out = append(out, fmt.Sprintf("MCP server %q in %s changed since facet wire registered it", bundle.MCPServerName, s.MCPFile))
	case "unknown":
		out = append(out, fmt.Sprintf("could not read %s to check the MCP server", s.MCPFile))
	case "none":
		out = append(out, "no MCP server registration is recorded")
	}
	switch s.RulesState {
	case "missing":
		out = append(out, fmt.Sprintf("ask rules for paid tools are missing from %s", s.RulesFile))
	case "unknown":
		out = append(out, fmt.Sprintf("could not read %s to check the ask rules for paid tools", s.RulesFile))
	case "absent":
		out = append(out, "no ask rules for paid tools are recorded")
	}
	if s.Stale {
		out = append(out, fmt.Sprintf("wired by facet v%s; this is facet v%s", s.FacetVersion, running))
	}
	if s.ExecutableMissing {
		out = append(out, fmt.Sprintf("the registered executable %s no longer exists", s.Executable))
	} else if s.ExecutableChanged {
		out = append(out, fmt.Sprintf("the MCP server runs %s, not this facet", s.Executable))
	}
	if s.Pending {
		out = append(out, "a previous facet wire run did not finish")
	}
	return out
}

// Report is the state of every recorded wiring.
type Report struct {
	RegistryPath string         `json:"registry"`
	Running      string         `json:"running_version"`
	Executable   string         `json:"executable"`
	Wirings      []WiringStatus `json:"wirings"`
}

// Inspect reports every recorded wiring against the running facet without
// changing anything.
func Inspect(runningVersion string) (*Report, error) {
	e, err := newEnv(runningVersion, nil, nil, "")
	if err != nil {
		return nil, err
	}
	return e.inspect()
}

func (e *env) inspect() (*Report, error) {
	report := &Report{RegistryPath: e.registryPath(), Running: e.version, Executable: e.exe}
	reg, err := LoadRegistry(report.RegistryPath)
	if err != nil {
		return report, err
	}
	for _, w := range reg.Wirings {
		report.Wirings = append(report.Wirings, e.inspectWiring(w))
	}
	return report, nil
}

func (e *env) inspectWiring(w *Wiring) WiringStatus {
	s := WiringStatus{CLI: w.CLI, Scope: w.Scope, Project: w.Project, Root: w.Root, FacetVersion: w.FacetVersion,
		Executable: w.Executable, ExplicitExecutable: w.ExplicitExecutable, WiredAt: w.WiredAt, Files: len(w.Files), Pending: w.Pending}
	for _, f := range w.Files {
		got, err := fileDigest(f.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			s.Missing = append(s.Missing, f.Path)
		case err != nil || !f.owns(got):
			s.Modified = append(s.Modified, f.Path)
		}
	}
	s.Stale = w.FacetVersion != e.version
	if _, err := os.Stat(w.Executable); err != nil {
		s.ExecutableMissing = true
	}
	// An executable chosen with --exe is the wiring's own; only its absence
	// is drift.
	s.ExecutableChanged = !w.ExplicitExecutable && !samePath(w.Executable, e.exe)
	s.MCPState = "none"
	if w.MCP != nil {
		s.MCPMethod, s.MCPFile = w.MCP.Method, w.MCP.File
		s.MCPState = mcpState(w.MCP)
	}
	s.RulesState = rulesState(w.CLI, w.Rules)
	if w.Rules != nil {
		s.RulesFile, s.RulesNote = w.Rules.File, w.Rules.Skipped
	}
	return s
}

func mcpState(rec *MCPRecord) string {
	switch rec.Method {
	case MethodCommand:
		o := readCommandConfig(rec.CLI, rec.File)
		return observedState(o, rec)
	case MethodJSON:
		data, err := os.ReadFile(rec.File)
		if errors.Is(err, fs.ErrNotExist) {
			return "missing"
		}
		if err != nil || len(rec.KeyPath) != 2 {
			return "unknown"
		}
		root, err := parseJSONC(data)
		if err != nil || root.kind != '{' {
			return "unknown"
		}
		parent, _ := root.member(rec.KeyPath[0])
		if parent == nil || parent.value.kind != '{' {
			return "missing"
		}
		m, _ := parent.value.member(rec.KeyPath[1])
		switch {
		case m == nil:
			return "missing"
		case nodeEquals(data, m.value, rec.Value):
			return "ok"
		}
		return "modified"
	case MethodTOML:
		data, err := os.ReadFile(rec.File)
		if errors.Is(err, fs.ErrNotExist) {
			return "missing"
		}
		if err != nil {
			return "unknown"
		}
		switch content := string(data); {
		case rec.Block != "" && strings.Contains(content, rec.Block):
			return "ok"
		case inspectTOMLServer(content, rec.Name).defined:
			return "modified"
		}
		return "missing"
	case MethodCompa:
		return compaMCPState(rec)
	}
	return "unknown"
}

func observedState(o observed, rec *MCPRecord) string {
	switch {
	case !o.known:
		return "unknown"
	case !o.present:
		return "missing"
	case o.runs(rec.Command, rec.Args):
		return "ok"
	}
	return "modified"
}

// status prints the report for `facet wire --status`.
func (e *env) status() int {
	report, err := e.inspect()
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	fmt.Fprintf(e.out, "Wiring registry: %s\n", report.RegistryPath)
	fmt.Fprintf(e.out, "Running facet v%s; MCP executable: %s\n", report.Running, report.Executable)
	if len(report.Wirings) == 0 {
		fmt.Fprintln(e.out, "\nNo wirings recorded. Run `facet wire <cli>` to wire Facet into claude, codex, copilot, opencode, or compa.")
		return 0
	}
	for _, s := range report.Wirings {
		state := "ok"
		if s.Drifted() {
			state = "drift"
		}
		fmt.Fprintf(e.out, "\n%s: %s\n", s.Label(), state)
		fmt.Fprintf(e.out, "  root:     %s\n", s.Root)
		fmt.Fprintf(e.out, "  wired by: facet v%s at %s\n", s.FacetVersion, s.WiredAt)
		fmt.Fprintf(e.out, "  files:    %d recorded, %d modified, %d missing\n", s.Files, len(s.Modified), len(s.Missing))
		for _, path := range s.Modified {
			fmt.Fprintf(e.out, "    modified: %s\n", path)
		}
		for _, path := range s.Missing {
			fmt.Fprintf(e.out, "    missing:  %s\n", path)
		}
		if s.MCPState == "none" {
			fmt.Fprintln(e.out, "  mcp:      not registered")
		} else {
			fmt.Fprintf(e.out, "  mcp:      %s (%s, %s)\n", s.MCPState, s.MCPMethod, s.MCPFile)
		}
		switch s.RulesState {
		case "none":
		case "skipped":
			fmt.Fprintf(e.out, "  ask:      not added (%s)\n", s.RulesNote)
		case "absent":
			fmt.Fprintln(e.out, "  ask:      no rules for paid tools recorded")
		default:
			fmt.Fprintf(e.out, "  ask:      %s (%s)\n", s.RulesState, s.RulesFile)
		}
		fmt.Fprintf(e.out, "  runs:     %s\n", commandLine(append([]string{s.Executable}, bundle.MCPServerArgs()...)))
		if s.Drifted() {
			for _, problem := range s.Problems(report.Running) {
				fmt.Fprintf(e.out, "  drift:    %s\n", problem)
			}
			fmt.Fprintf(e.out, "  fix:      %s\n", s.RewireCommand())
		}
	}
	return 0
}
