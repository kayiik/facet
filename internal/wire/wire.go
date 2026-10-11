// Package wire installs the Facet bundle into an agentic CLI's native
// user-level or project-level locations and registers Facet's MCP server
// through that CLI's own configuration mechanism.
//
// Wiring is the user's decision, like installing a plugin: nothing here runs
// unless `facet wire` is invoked. The CLI keeps ownership of approvals and
// permissions, and its instruction files (AGENTS.md, CLAUDE.md, ...) are never
// edited. Everything facet wire writes is recorded with its digest in
// wiring.json in Facet's home folder (~/.facet, or FACET_HOME); only recorded,
// unmodified items are ever replaced or removed.
package wire

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kayiik/facet/internal/bundle"
	"github.com/kayiik/facet/internal/toolbox"
)

const usage = `Usage:
  facet wire <cli>[,<cli>...]|all [--scope user|project] [--project DIR] [--exe PATH] [--dry-run]
  facet wire --remove <cli>[,<cli>...]|all [--scope user|project] [--project DIR] [--dry-run]
  facet wire --refresh [--dry-run]
  facet wire --status

Wires Facet into an agentic CLI: installs the facet skill (the guide), and
the facet producer and role agents where the CLI supports agents, then
registers the MCP server "facet" (the facet executable
started with "mcp") through the CLI's own configuration. Instruction files such
as AGENTS.md or CLAUDE.md are never edited.

CLIs: claude, codex, copilot, opencode, compa, or all. compa is Compa's agent
engine, wired at user scope in its config.json (COMPA_HOME moves it); all
includes it when Compa is set up.

Options:
  --scope user|project  where the CLI should find Facet (default: user)
  --project DIR         the project for project scope (default: the current
                        directory); implies --scope project
  --exe PATH            the facet executable to register as the MCP server
                        (default: this facet)
  --dry-run             print the exact actions without changing anything
  --remove              reverse a wiring; only recorded, unmodified items are
                        removed
  --refresh             bring every recorded wiring to this facet's version;
                        files changed since facet wire wrote them are never
                        overwritten
  --status              report every wiring and any drift

Everything written is recorded in wiring.json in Facet's home folder
(~/.facet, or FACET_HOME when it is set). Existing files that facet wire did
not write, or that changed since it wrote them, are never overwritten or
deleted.
`

// commandTimeout bounds every CLI subprocess.
const commandTimeout = 60 * time.Second

// env is the process context wiring runs in.
type env struct {
	home   string // the user's home directory: user-scope CLI locations
	cwd    string
	getenv func(string) string
	exe    string // facet executable registered as the MCP server
	// explicitExe is set when --exe chose the executable: a later --status
	// does not call it drift, and --refresh keeps it.
	explicitExe bool
	version     string
	out         io.Writer
	errOut      io.Writer
	now         func() time.Time
}

func newEnv(version string, stdout, stderr io.Writer, exeOverride string) (*env, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("cannot determine the home directory: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	exe, explicit := "", false
	if exeOverride != "" {
		if exe, err = explicitExecutable(exeOverride); err != nil {
			return nil, err
		}
		explicit = true
	} else if exe = toolbox.Executable(); exe == "" {
		return nil, errors.New("cannot determine the facet executable")
	}
	return &env{
		home:        filepath.Clean(home),
		cwd:         cwd,
		getenv:      os.Getenv,
		exe:         exe,
		explicitExe: explicit,
		version:     version,
		out:         stdout,
		errOut:      stderr,
		now:         func() time.Time { return time.Now().UTC() },
	}, nil
}

// explicitExecutable checks an --exe path: it must name an existing regular
// file, and is recorded as an absolute path.
func explicitExecutable(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("--exe %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--exe %s: %w", abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("--exe %s is not a regular file", abs)
	}
	return filepath.Clean(abs), nil
}

func (e *env) registryPath() string { return RegistryPath(e.home) }

type mode int

const (
	modeInstall mode = iota
	modeRemove
	modeStatus
	modeRefresh
)

type request struct {
	mode     mode
	targets  []bundle.Target
	scope    bundle.Scope
	scopeSet bool
	project  string
	exe      string
	dryRun   bool
	// named are the targets named explicitly rather than through "all".
	named map[bundle.Target]bool
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

// parseArgs accepts options before or after the CLI names.
func parseArgs(args []string) (*request, bool, error) {
	req := &request{mode: modeInstall, scope: bundle.ScopeUser}
	var names []string
	projectSet := false
	value := func(i *int, flag string) (string, error) {
		if *i+1 >= len(args) {
			return "", usageError{flag + " requires a value"}
		}
		*i++
		return args[*i], nil
	}
	removeSet, statusSet, refreshSet := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "-") {
			names = append(names, arg)
			continue
		}
		switch strings.TrimLeft(name, "-") {
		case "h", "help":
			return nil, true, nil
		case "dry-run":
			if hasInline {
				return nil, false, usageError{"--dry-run takes no value"}
			}
			req.dryRun = true
		case "remove":
			if hasInline {
				return nil, false, usageError{"--remove takes no value"}
			}
			removeSet = true
		case "status":
			if hasInline {
				return nil, false, usageError{"--status takes no value"}
			}
			statusSet = true
		case "refresh":
			if hasInline {
				return nil, false, usageError{"--refresh takes no value"}
			}
			refreshSet = true
		case "scope":
			v := inline
			if !hasInline {
				var err error
				if v, err = value(&i, "--scope"); err != nil {
					return nil, false, err
				}
			}
			scope, err := bundle.ParseScope(v)
			if err != nil {
				return nil, false, usageError{err.Error()}
			}
			req.scope, req.scopeSet = scope, true
		case "project":
			v := inline
			if !hasInline {
				var err error
				if v, err = value(&i, "--project"); err != nil {
					return nil, false, err
				}
			}
			if strings.TrimSpace(v) == "" {
				return nil, false, usageError{"--project requires a directory"}
			}
			req.project, projectSet = v, true
		case "exe":
			v := inline
			if !hasInline {
				var err error
				if v, err = value(&i, "--exe"); err != nil {
					return nil, false, err
				}
			}
			if strings.TrimSpace(v) == "" {
				return nil, false, usageError{"--exe requires a path"}
			}
			req.exe = v
		default:
			return nil, false, usageError{fmt.Sprintf("unknown option %s", arg)}
		}
	}
	switch {
	case statusSet && (removeSet || refreshSet || req.dryRun || len(names) > 0 || req.scopeSet || projectSet || req.exe != ""):
		return nil, false, usageError{"--status takes no other arguments"}
	case statusSet:
		req.mode = modeStatus
		return req, false, nil
	case refreshSet && (removeSet || len(names) > 0 || req.scopeSet || projectSet || req.exe != ""):
		return nil, false, usageError{"--refresh takes only --dry-run; it refreshes every recorded wiring as it was made"}
	case refreshSet:
		req.mode = modeRefresh
		return req, false, nil
	case removeSet && req.exe != "":
		return nil, false, usageError{"--exe applies only when wiring; --remove reverses what was recorded"}
	case removeSet:
		req.mode = modeRemove
	}
	if len(names) == 0 {
		return nil, false, usageError{"name the CLI to wire: claude, codex, copilot, opencode, compa, or all"}
	}
	targets, err := bundle.ParseTargets(names...)
	if err != nil {
		return nil, false, usageError{err.Error()}
	}
	req.targets = targets
	req.named = map[bundle.Target]bool{}
	for _, list := range names {
		for _, name := range strings.Split(list, ",") {
			if t, err := bundle.ParseTarget(name); err == nil {
				req.named[t] = true
			}
		}
	}
	if projectSet {
		if req.scopeSet && req.scope != bundle.ScopeProject {
			return nil, false, usageError{"--project applies only to --scope project"}
		}
		req.scope = bundle.ScopeProject
	}
	// A target that has no project scope is an error when named, and left
	// out of "all".
	var kept []bundle.Target
	for _, t := range req.targets {
		switch {
		case bundle.SupportsScope(t, req.scope):
			kept = append(kept, t)
		case req.named[t]:
			return nil, false, usageError{fmt.Sprintf("%s is wired at user scope only", t)}
		}
	}
	req.targets = kept
	return req, false, nil
}

// CLI implements `facet wire`. It returns the process exit code.
func CLI(args []string, stdout, stderr io.Writer, version string) int {
	req, help, err := parseArgs(args)
	if help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "wire: %v\n\n%s", err, usage)
		return 2
	}
	e, err := newEnv(version, stdout, stderr, req.exe)
	if err != nil {
		fmt.Fprintf(stderr, "wire: %v\n", err)
		return 1
	}
	switch req.mode {
	case modeStatus:
		return e.status()
	case modeRemove:
		return e.remove(req)
	case modeRefresh:
		return e.refresh(req)
	default:
		return e.install(req)
	}
}

// resolveProject returns the absolute project directory for project scope.
// Removal accepts a project directory that no longer exists, so a wiring can
// always be reversed.
func (e *env) resolveProject(req *request, mustExist bool) (string, error) {
	if req.scope != bundle.ScopeProject {
		return "", nil
	}
	dir := req.project
	if dir == "" {
		dir = e.cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	switch {
	case err != nil && (mustExist || !errors.Is(err, fs.ErrNotExist)):
		return "", fmt.Errorf("project directory %s: %w", abs, err)
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("project directory %s is not a directory", abs)
	}
	if samePath(abs, e.home) {
		return "", errors.New("the project directory is your home directory, where project-scope files would act user-wide; use --scope user instead")
	}
	return abs, nil
}

func (e *env) install(req *request) int {
	project, err := e.resolveProject(req, true)
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	reg, err := LoadRegistry(e.registryPath())
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	var plans []*plan
	failed := false
	for _, t := range req.targets {
		if t == bundle.TargetCompa && !req.named[t] && !e.compaSetUp() {
			// "all" includes Compa only where it is set up.
			continue
		}
		p := e.planInstall(reg, t, req.scope, project)
		plans = append(plans, p)
		if len(p.problems) > 0 {
			failed = true
		}
	}
	if req.dryRun {
		fmt.Fprintln(e.out, "Dry run: nothing will be changed.")
	}
	if e.temporaryExecutable() {
		fmt.Fprintf(e.errOut, "wire: warning: %s looks temporary; the CLI will lose the MCP server when it is deleted. Install Facet and rerun facet wire from the installed copy.\n", e.exe)
	}
	if failed {
		for _, p := range plans {
			if len(p.problems) == 0 {
				continue
			}
			fmt.Fprintf(e.errOut, "%s: cannot wire:\n", p.label())
			for _, problem := range p.problems {
				fmt.Fprintf(e.errOut, "  - %s\n", problem)
			}
		}
		fmt.Fprintln(e.errOut, "Nothing was changed.")
		return 1
	}
	for _, p := range plans {
		e.printPlan(p)
		if req.dryRun {
			for _, note := range p.notes {
				fmt.Fprintf(e.out, "  note:      %s\n", note)
			}
			continue
		}
		before := len(p.mcp.warnings)
		if err := e.applyInstall(reg, p); err != nil {
			fmt.Fprintf(e.errOut, "%s: %v\n", p.label(), err)
			return 1
		}
		for _, warning := range p.mcp.warnings[before:] {
			fmt.Fprintf(e.errOut, "  warning: %s\n", warning)
		}
		fmt.Fprintf(e.out, "%s: wired; MCP server %q runs %s\n", p.label(), bundle.MCPServerName, commandLine(append([]string{e.exe}, bundle.MCPServerArgs()...)))
		for _, note := range p.notes {
			fmt.Fprintf(e.out, "  note:      %s\n", note)
		}
	}
	return 0
}

func (e *env) remove(req *request) int {
	project, err := e.resolveProject(req, false)
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	reg, err := LoadRegistry(e.registryPath())
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	if req.dryRun {
		fmt.Fprintln(e.out, "Dry run: nothing will be changed.")
	}
	code := 0
	for _, t := range req.targets {
		w := reg.find(string(t), string(req.scope), project)
		label := (&Wiring{CLI: string(t), Scope: string(req.scope), Project: project}).label()
		if w == nil {
			fmt.Fprintf(e.out, "%s: not wired by facet wire; nothing to remove\n", label)
			continue
		}
		p := e.planRemove(w)
		e.printPlan(p)
		if req.dryRun {
			continue
		}
		if err := e.applyRemove(reg, p); err != nil {
			fmt.Fprintf(e.errOut, "%s: %v\n", label, err)
			code = 1
			continue
		}
		fmt.Fprintf(e.out, "%s: removed\n", label)
	}
	return code
}

// refresh re-applies every recorded wiring with this facet: the skills,
// agent and MCP registration move to this version, exactly as `facet wire
// <cli>` would install them. The installer runs it after every upgrade, so
// what each CLI holds always matches the installed facet.
// A wiring made with --exe keeps its executable.
func (e *env) refresh(req *request) int {
	reg, err := LoadRegistry(e.registryPath())
	if err != nil {
		fmt.Fprintf(e.errOut, "wire: %v\n", err)
		return 1
	}
	if len(reg.Wirings) == 0 {
		fmt.Fprintln(e.out, "No wirings recorded; nothing to refresh.")
		return 0
	}
	if req.dryRun {
		fmt.Fprintln(e.out, "Dry run: nothing will be changed.")
	}
	// Applying a plan replaces records in reg, so walk a snapshot.
	recorded := append([]*Wiring(nil), reg.Wirings...)
	code := 0
	for _, w := range recorded {
		we := *e
		if w.ExplicitExecutable {
			we.exe, we.explicitExe = w.Executable, true
		}
		label := w.label()
		if w.Scope == string(bundle.ScopeProject) {
			if info, err := os.Stat(w.Project); err != nil || !info.IsDir() {
				fmt.Fprintf(e.errOut, "%s: the project directory is gone; remove the wiring with `%s`\n", label, removeCommand(w))
				code = 1
				continue
			}
		}
		target, err := bundle.ParseTargets(w.CLI)
		if err != nil || len(target) != 1 {
			fmt.Fprintf(e.errOut, "%s: not a CLI this facet wires; remove the wiring with `%s`\n", label, removeCommand(w))
			code = 1
			continue
		}
		p := we.planInstall(reg, target[0], bundle.Scope(w.Scope), w.Project)
		if len(p.problems) > 0 {
			fmt.Fprintf(e.errOut, "%s: cannot refresh; nothing of it was changed:\n", label)
			for _, problem := range p.problems {
				fmt.Fprintf(e.errOut, "  - %s\n", problem)
			}
			code = 1
			continue
		}
		if !p.changed(&we) {
			fmt.Fprintf(e.out, "%s: up to date (facet v%s)\n", label, we.version)
			continue
		}
		we.printPlan(p)
		if req.dryRun {
			continue
		}
		before := len(p.mcp.warnings)
		if err := we.applyInstall(reg, p); err != nil {
			fmt.Fprintf(e.errOut, "%s: %v\n", label, err)
			code = 1
			continue
		}
		for _, warning := range p.mcp.warnings[before:] {
			fmt.Fprintf(e.errOut, "  warning: %s\n", warning)
		}
		fmt.Fprintf(e.out, "%s: refreshed to facet v%s\n", label, we.version)
	}
	return code
}

// removeCommand is the command that reverses w.
func removeCommand(w *Wiring) string {
	cmd := "facet wire --remove " + w.CLI
	if w.Scope == string(bundle.ScopeProject) {
		cmd += " --scope project --project " + commandLine([]string{w.Project})
	}
	return cmd
}

// temporaryExecutable reports whether the registered executable lives in the
// temporary directory, as a `go run` build does.
func (e *env) temporaryExecutable() bool {
	tmp := os.TempDir()
	if tmp == "" {
		return false
	}
	rel, err := filepath.Rel(pathKey(tmp), pathKey(e.exe))
	return err == nil && !strings.HasPrefix(rel, "..") && rel != "."
}

// commandLine renders argv for people, quoting arguments with spaces.
func commandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
