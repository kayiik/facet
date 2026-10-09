// Package doctor reports what a Facet installation can do: the active
// runtime, the wirings, Facet 1.x leftovers, the media programs the tools
// would run, the agentic CLIs on PATH, the provider credentials the tools
// read, and every tool's readiness.
//
// It only reads. Programs are resolved exactly as the tools resolve them, so
// the report can never disagree with what a tool would run; there is no
// configuration file to pin anything else.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kayiik/facet/internal/facethome"
	"github.com/kayiik/facet/internal/toolbox"
	"github.com/kayiik/facet/internal/wire"
)

// CheckStatus represents the health/discovery status of a component.
type CheckStatus string

const (
	StatusOK       CheckStatus = "OK"
	StatusFound    CheckStatus = "Found"
	StatusNotFound CheckStatus = "Not found"
	StatusWarning  CheckStatus = "Warning"
	StatusError    CheckStatus = "Error"
)

// FacetRuntimeCheck reports the running facet and the active runtime at
// current in Facet's home folder, whose executable is what facet wire
// registers.
type FacetRuntimeCheck struct {
	Running    string      `json:"running_version"`
	Executable string      `json:"executable,omitempty"`
	Current    string      `json:"current,omitempty"`
	Target     string      `json:"current_target,omitempty"`
	Version    string      `json:"current_version,omitempty"`
	Status     CheckStatus `json:"status"`
	Details    string      `json:"details,omitempty"`
}

// WiringCheck summarizes one wiring recorded by facet wire.
type WiringCheck struct {
	Label   string      `json:"label"`
	Status  CheckStatus `json:"status"`
	Details string      `json:"details"`
	Fix     string      `json:"fix,omitempty"`
}

// LegacyCheck is a Facet 1.x per-project installation whose skill copy
// shadows the user-wide skill.
type LegacyCheck struct {
	Project string   `json:"project"`
	Host    string   `json:"host,omitempty"`
	Files   []string `json:"files"`
	Cleanup string   `json:"cleanup"`
}

// RuntimeCheck represents the status of an external or built-in runtime.
type RuntimeCheck struct {
	Name      string      `json:"name"`
	Status    CheckStatus `json:"status"`
	Path      string      `json:"path,omitempty"`
	Version   string      `json:"version,omitempty"`
	Details   string      `json:"details,omitempty"`
	Available bool        `json:"available"`
}

// AgentCLICheck represents the discovery status of an agent CLI.
type AgentCLICheck struct {
	Name      string      `json:"name"`
	Status    CheckStatus `json:"status"`
	Path      string      `json:"path,omitempty"`
	Available bool        `json:"available"`
}

// EnvVarCheck represents the masked presence of an environment variable.
type EnvVarCheck struct {
	Name    string `json:"name"`
	IsSet   bool   `json:"is_set"`
	Display string `json:"display"` // "set" or "not set"
}

// ToolCheck represents the configuration status of a toolbox tool.
type ToolCheck struct {
	Name        string   `json:"name"`
	Capability  string   `json:"capability"`
	Configured  bool     `json:"configured"`
	MissingDeps []string `json:"missing_dependencies,omitempty"`
}

// DoctorReport contains the aggregated discovery and health report.
type DoctorReport struct {
	Facet       FacetRuntimeCheck `json:"facet"`
	Wiring      []WiringCheck     `json:"wiring"`
	WiringError string            `json:"wiring_error,omitempty"`
	Legacy      []LegacyCheck     `json:"legacy_v1"`
	Runtimes    []RuntimeCheck    `json:"runtimes"`
	CLIs        []AgentCLICheck   `json:"agent_clis"`
	EnvVars     []EnvVarCheck     `json:"env_vars"`
	Tools       []ToolCheck       `json:"tools"`
}

// EnvProbes returns the environment variables probed: the credentials Facet's
// own provider-backed tools read, including the alternative names some tools
// accept. The harness's model credentials are the harness's concern and are
// not probed.
func EnvProbes() []string {
	return []string{
		"OPENAI_API_KEY",
		"ELEVENLABS_API_KEY",
		"FAL_KEY",
		"FLUX_API_KEY",
		"KLING_API_KEY",
		"PEXELS_API_KEY",
		"PIXABAY_API_KEY",
	}
}

// Run executes every check and prints the formatted report to w.
func Run(w io.Writer) (*DoctorReport, error) {
	report := Generate()
	formatDoctorReport(report, w)
	return report, nil
}

// Generate inspects the environment and produces a structured report.
// It only reads: nothing is installed, wired, or repaired.
func Generate() *DoctorReport {
	report := &DoctorReport{
		Wiring:   make([]WiringCheck, 0),
		Legacy:   make([]LegacyCheck, 0),
		Runtimes: make([]RuntimeCheck, 0),
		CLIs:     make([]AgentCLICheck, 0),
		EnvVars:  make([]EnvVarCheck, 0),
		Tools:    make([]ToolCheck, 0),
	}

	// 0. Facet itself: the active runtime, the wirings, and 1.x leftovers.
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	running := toolbox.ProductVersion()
	report.Facet = probeFacetRuntime(home, running)
	report.Wiring, report.WiringError = wiringChecks(running)
	report.Legacy = legacyChecks(runtime.GOOS, cwd, home)

	// 1. The programs the tools would run, resolved as the tools resolve
	// them: the runtime's private copy first, then PATH.
	report.Runtimes = append(report.Runtimes, probeProgram("FFmpeg", "ffmpeg", "-version"))
	report.Runtimes = append(report.Runtimes, probeProgram("FFprobe", "ffprobe", "-version"))
	report.Runtimes = append(report.Runtimes, probeProgram("Node", "node", "-v"))
	report.Runtimes = append(report.Runtimes, probeRemotionComposer())
	report.Runtimes = append(report.Runtimes, probeEdgeTTS())

	// 2. Agentic CLIs, found on PATH as facet wire finds them.
	report.CLIs = append(report.CLIs, probeAgentCLI("Claude Code", "claude"))
	report.CLIs = append(report.CLIs, probeAgentCLI("OpenCode", "opencode"))
	report.CLIs = append(report.CLIs, probeAgentCLI("GitHub Copilot", "copilot"))
	report.CLIs = append(report.CLIs, probeAgentCLI("OpenAI Codex", "codex"))
	report.CLIs = append(report.CLIs, probeAgentCLI("Compa", "compa-kernel"))

	// 3. Environment Variables
	for _, envVar := range EnvProbes() {
		isSet := strings.TrimSpace(os.Getenv(envVar)) != ""
		display := "not set"
		if isSet {
			display = "set"
		}
		report.EnvVars = append(report.EnvVars, EnvVarCheck{Name: envVar, IsSet: isSet, Display: display})
	}

	// 4. Every toolbox tool.
	envList, ok := toolbox.CLI([]string{"tools", "list"})
	if ok && envList.Result != nil {
		if resMap, ok := envList.Result.(map[string]any); ok {
			if toolList, ok := resMap["tools"].([]any); ok {
				for _, item := range toolList {
					if tMap, ok := item.(map[string]any); ok {
						report.Tools = append(report.Tools, toolCheck(tMap))
					}
				}
			}
		}
	}

	return report
}

func toolCheck(tMap map[string]any) ToolCheck {
	name, _ := tMap["name"].(string)
	capab, _ := tMap["capability"].(string)
	conf, _ := tMap["configured"].(bool)
	var missing []string
	if deps, ok := tMap["dependencies"].([]any); ok {
		for _, d := range deps {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			if avail, ok := dm["available"].(bool); ok && !avail {
				depName, _ := dm["name"].(string)
				switch alternatives := dm["alternatives"].(type) {
				case []string:
					if len(alternatives) > 0 {
						depName = strings.Join(alternatives, "|")
					}
				case []any:
					names := make([]string, 0, len(alternatives))
					for _, a := range alternatives {
						names = append(names, fmt.Sprint(a))
					}
					if len(names) > 0 {
						depName = strings.Join(names, "|")
					}
				}
				if depType, _ := dm["type"].(string); depType == "env" {
					missing = append(missing, fmt.Sprintf("env:%s", depName))
				} else {
					missing = append(missing, depName)
				}
			}
		}
	}
	return ToolCheck{Name: name, Capability: capab, Configured: conf, MissingDeps: missing}
}

// facetVersionOf runs `<exe> version` with a hard timeout and returns the
// reported version. Tests replace it.
var facetVersionOf = func(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if m := facetVersionPattern.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

var facetVersionPattern = regexp.MustCompile(`facet v(\S+)`)

// versionFromRuntimeDir reads <version>-<os>-<arch>, the installer's
// runtime directory name.
func versionFromRuntimeDir(dir string) string {
	parts := strings.Split(filepath.Base(dir), "-")
	if len(parts) < 3 {
		return ""
	}
	switch parts[len(parts)-2] {
	case "windows", "linux", "darwin":
	default:
		return ""
	}
	switch parts[len(parts)-1] {
	case "amd64", "arm64":
	default:
		return ""
	}
	return strings.Join(parts[:len(parts)-2], "-")
}

// probeFacetRuntime reports the running facet and the active runtime.
func probeFacetRuntime(home, running string) FacetRuntimeCheck {
	check := FacetRuntimeCheck{Running: running}
	if exe, err := os.Executable(); err == nil {
		check.Executable = exe
	}
	facetHome := facethome.For(home)
	if facetHome == "" {
		check.Status = StatusWarning
		check.Details = "the home directory is unknown, so the active runtime cannot be checked; set " + facethome.EnvVar
		return check
	}
	current := filepath.Join(facetHome, "current")
	if _, err := os.Lstat(current); err != nil {
		check.Status = StatusNotFound
		check.Details = "no active runtime at " + current + "; facet wire registers the running executable instead"
		return check
	}
	check.Current = current
	check.Target = current
	if target, err := filepath.EvalSymlinks(current); err == nil {
		check.Target = target
	}
	exe := wire.CurrentExecutable(home)
	if exe == "" {
		check.Status = StatusWarning
		check.Details = current + " has no bin/facet executable; reinstall Facet"
		return check
	}
	check.Version = facetVersionOf(exe)
	if check.Version == "" {
		check.Version = versionFromRuntimeDir(check.Target)
	}
	switch {
	case check.Version == "":
		check.Status = StatusWarning
		check.Details = "could not determine the version of " + exe
	case check.Version != running:
		check.Status = StatusWarning
		check.Details = fmt.Sprintf("the active runtime is facet v%s, but this is facet v%s", check.Version, running)
	default:
		check.Status = StatusOK
	}
	return check
}

// wiringChecks summarizes the wirings recorded in wiring.json.
func wiringChecks(running string) ([]WiringCheck, string) {
	checks := make([]WiringCheck, 0)
	report, err := wire.Inspect(running)
	if err != nil {
		return checks, err.Error()
	}
	for _, s := range report.Wirings {
		check := WiringCheck{Label: s.Label(), Status: StatusOK,
			Details: fmt.Sprintf("facet v%s, %d files, MCP %s", s.FacetVersion, s.Files, s.MCPState)}
		if s.MCPMethod != "" {
			check.Details += " (" + s.MCPMethod + ")"
		}
		if s.Drifted() {
			check.Status = StatusWarning
			check.Details = strings.Join(s.Problems(running), "; ")
			check.Fix = s.RewireCommand()
		}
		checks = append(checks, check)
	}
	return checks, ""
}

// legacyChecks finds Facet 1.x project installations in the current and
// home directories. Their skill copies shadow the user-wide skill.
func legacyChecks(goos string, dirs ...string) []LegacyCheck {
	checks := make([]LegacyCheck, 0)
	for _, v := range wire.FindV1Integrations(dirs...) {
		checks = append(checks, LegacyCheck{Project: v.Project, Host: v.Host, Files: v.Files, Cleanup: v.CleanupCommand(goos)})
	}
	return checks
}

// probeProgram reports the program the tools would run for program.
func probeProgram(name, program, versionArg string) RuntimeCheck {
	target, err := toolbox.ResolveProgram(program)
	if err != nil || target == "" {
		return RuntimeCheck{
			Name:      name,
			Status:    StatusNotFound,
			Available: false,
			Details:   fmt.Sprintf("%s not found beside this facet or on PATH", program),
		}
	}
	versionStr := ""
	if versionArg != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, target, versionArg).Output(); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) > 0 {
				versionStr = strings.TrimSpace(lines[0])
			}
		}
	}
	return RuntimeCheck{Name: name, Status: StatusOK, Path: target, Version: versionStr, Available: true}
}

// probeRemotionComposer reports the composer video_compose renders with.
func probeRemotionComposer() RuntimeCheck {
	dir, usable, err := toolbox.ComposerStatus()
	switch {
	case err != nil || dir == "":
		details := "not found"
		if err != nil {
			details = err.Error()
		}
		return RuntimeCheck{Name: "Remotion Composer", Status: StatusNotFound, Available: false, Details: details}
	case !usable:
		// A composer without its dependencies cannot render, so it is a
		// WARNING rather than OK, and Available stays false: a caller asking
		// whether the composer is usable is asking whether it can render.
		return RuntimeCheck{
			Name:   "Remotion Composer",
			Status: StatusWarning,
			Path:   dir,
			Details: dir + " (dependencies not installed; reinstall Facet with the remotion component, " +
				"or run `npm ci` there before rendering)",
			Available: false,
		}
	}
	return RuntimeCheck{Name: "Remotion Composer", Status: StatusOK, Path: dir, Details: dir, Available: true}
}

func probeEdgeTTS() RuntimeCheck {
	return RuntimeCheck{
		Name:      "Edge-TTS",
		Status:    StatusOK,
		Details:   "built-in keyless network client ready; service reachability not tested",
		Available: true,
	}
}

// probeAgentCLI finds an agentic CLI on PATH, where facet wire runs it.
func probeAgentCLI(displayName, binary string) AgentCLICheck {
	if path, err := exec.LookPath(binary); err == nil && path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return AgentCLICheck{Name: displayName, Status: StatusFound, Path: path, Available: true}
	}
	return AgentCLICheck{Name: displayName, Status: StatusNotFound, Available: false}
}

func formatDoctorReport(report *DoctorReport, w io.Writer) {
	fmt.Fprintln(w, "=== Facet System Doctor ===")
	fmt.Fprintln(w)

	// 0. Facet runtime, wiring, and 1.x leftovers.
	fmt.Fprintln(w, "[Facet]")
	running := "facet v" + report.Facet.Running
	if report.Facet.Executable != "" {
		running += " (" + report.Facet.Executable + ")"
	}
	fmt.Fprintf(w, "  ✓ %-18s : %s\n", "Running", running)
	symbol := "✓"
	if report.Facet.Status != StatusOK {
		symbol = "✗"
		if report.Facet.Status == StatusNotFound {
			symbol = "-"
		}
	}
	active := string(report.Facet.Status)
	if report.Facet.Current != "" {
		active = report.Facet.Current
		if report.Facet.Target != "" && report.Facet.Target != report.Facet.Current {
			active += " -> " + report.Facet.Target
		}
		if report.Facet.Version != "" {
			active += " (facet v" + report.Facet.Version + ")"
		}
	}
	fmt.Fprintf(w, "  %s %-18s : %s\n", symbol, "Active runtime", active)
	if report.Facet.Details != "" {
		fmt.Fprintf(w, "    %s\n", report.Facet.Details)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[Wiring]")
	switch {
	case report.WiringError != "":
		fmt.Fprintf(w, "  ✗ %s\n", report.WiringError)
	case len(report.Wiring) == 0:
		fmt.Fprintln(w, "  - No wirings recorded. Run `facet wire <cli>` to wire Facet into claude, codex, copilot, opencode, or compa.")
	}
	for _, c := range report.Wiring {
		symbol := "✓"
		if c.Status != StatusOK {
			symbol = "✗"
		}
		fmt.Fprintf(w, "  %s %s: %s\n", symbol, c.Label, c.Details)
		if c.Fix != "" {
			fmt.Fprintf(w, "    fix: %s\n", c.Fix)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "[Facet 1.x Integrations]")
	if len(report.Legacy) == 0 {
		fmt.Fprintln(w, "  ✓ none found in the current or home directory")
	}
	for _, l := range report.Legacy {
		fmt.Fprintf(w, "  ✗ A Facet 1.x installation for %s shadows the user-wide Facet skill:\n", l.Project)
		for _, f := range l.Files {
			fmt.Fprintf(w, "      %s\n", f)
		}
		fmt.Fprintln(w, "    Remove it with the 1.1.0 installer, run from its extracted folder:")
		fmt.Fprintf(w, "      %s\n", l.Cleanup)
	}
	fmt.Fprintln(w)

	// 1. Runtimes
	fmt.Fprintln(w, "[System Runtimes]")
	for _, r := range report.Runtimes {
		symbol := "✓"
		if !r.Available {
			symbol = "✗"
		}
		info := string(r.Status)
		if r.Version != "" {
			info = fmt.Sprintf("%s (%s)", r.Status, r.Version)
		} else if r.Details != "" {
			info = fmt.Sprintf("%s (%s)", r.Status, r.Details)
		} else if r.Path != "" {
			info = fmt.Sprintf("%s (%s)", r.Status, r.Path)
		}
		fmt.Fprintf(w, "  %s %-18s : %s\n", symbol, r.Name, info)
	}
	fmt.Fprintln(w)

	// 2. Agent CLIs
	fmt.Fprintln(w, "[Agent CLIs]")
	for _, cli := range report.CLIs {
		symbol := "✓"
		if !cli.Available {
			symbol = "-"
		}
		info := string(cli.Status)
		if cli.Path != "" {
			info = fmt.Sprintf("%s (%s)", cli.Status, cli.Path)
		}
		fmt.Fprintf(w, "  %s %-18s : %s\n", symbol, cli.Name, info)
	}
	fmt.Fprintln(w)

	// 3. Environment Variables
	fmt.Fprintln(w, "[Environment Variables]")
	for _, ev := range report.EnvVars {
		symbol := "✓"
		if !ev.IsSet {
			symbol = "-"
		}
		fmt.Fprintf(w, "  %s %-20s : %s\n", symbol, ev.Name, ev.Display)
	}
	fmt.Fprintln(w)

	// 4. Toolbox Tools
	fmt.Fprintf(w, "[Toolbox Tools (%d tools)]\n", len(report.Tools))
	for _, t := range report.Tools {
		symbol := "✓"
		statusStr := "[configured]"
		if !t.Configured {
			symbol = "-"
			if len(t.MissingDeps) > 0 {
				statusStr = fmt.Sprintf("[missing: %s]", strings.Join(t.MissingDeps, ", "))
			} else {
				statusStr = "[unconfigured]"
			}
		}
		fmt.Fprintf(w, "  %s %-22s %-25s (%s)\n", symbol, t.Name, statusStr, t.Capability)
	}
	fmt.Fprintln(w)
}

// JSON serialization helper for report
func (r *DoctorReport) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
