package bundle

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kayiik/facet/internal/toolbox"
)

const bundleUsage = `Usage: facet bundle --target <claude|codex|copilot|opencode|compa|all> [--scope user|project|plugin] --out DIR

Writes Facet's guidance in each CLI's native layout for packaging: the facet
skill (the guide), and where the CLI's agent format is supported the facet
producer agent and the role agents, and a facet-bundle.json
manifest with the Facet version and every file's digest.

Each target is written to DIR/<target>. At user and project scope it is laid
out relative to the scope's base directory (the home directory for user scope,
the project for project scope), and the MCP server is registered by facet
wire, not by a bundle. Compa is laid out at user scope only.

--scope plugin writes a plugin for a marketplace to serve, for claude, codex,
and copilot: the guidance plus the CLI's plugin manifest and an MCP server
registration that runs facet mcp, with facet from PATH; the person installs
the Facet Toolkit for it. The Codex plugin also forwards the environment
variables Facet reads and asks before every paid tool.

An existing DIR/<target> is replaced only if every file in it was written,
unmodified, by facet bundle. all leaves out the targets a scope does not
support.

Options:
  --target LIST   claude, codex, copilot, opencode, compa, or all (comma-separated)
  --scope SCOPE   user (default), project, or plugin
  --out DIR       output directory
`

// CLI implements `facet bundle`. It returns the process exit code.
func CLI(args []string, stdout, stderr io.Writer, version string) int {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "-help" {
			fmt.Fprint(stdout, bundleUsage)
			return 0
		}
	}
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, bundleUsage) }
	targetList := fs.String("target", "", "claude | codex | copilot | opencode | compa | all")
	scopeName := fs.String("scope", string(ScopeUser), "user | project | plugin")
	out := fs.String("out", "", "output directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "bundle: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if strings.TrimSpace(*targetList) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprintln(stderr, "bundle: --target and --out are required")
		fmt.Fprint(stderr, bundleUsage)
		return 2
	}
	targets, err := ParseTargets(*targetList)
	if err != nil {
		fmt.Fprintf(stderr, "bundle: %v\n", err)
		return 2
	}
	scope, err := ParseBundleScope(*scopeName)
	if err != nil {
		fmt.Fprintf(stderr, "bundle: %v\n", err)
		return 2
	}
	// "all" means every target the scope supports; a target named
	// explicitly at a scope it does not support is an error.
	explicit := map[Target]bool{}
	for _, name := range strings.Split(*targetList, ",") {
		if t, err := ParseTarget(name); err == nil {
			explicit[t] = true
		}
	}
	var supported []Target
	for _, t := range targets {
		switch {
		case SupportsScope(t, scope):
			supported = append(supported, t)
		case explicit[t] && scope == ScopePlugin:
			fmt.Fprintf(stderr, "bundle: %s has no plugin layout; plugins exist for claude, codex, and copilot\n", t)
			return 2
		case explicit[t]:
			fmt.Fprintf(stderr, "bundle: %s is laid out at user scope only\n", t)
			return 2
		}
	}
	targets = supported
	if strings.TrimSpace(version) == "" {
		fmt.Fprintln(stderr, "bundle: this facet has no version; a bundle without identity cannot be compared or upgraded")
		return 1
	}
	tools := toolbox.Names()

	// Check every target before writing any, so one conflict does not leave
	// the others half-updated.
	failed := false
	for _, t := range targets {
		if err := Check(filepath.Join(*out, string(t))); err != nil {
			fmt.Fprintf(stderr, "bundle %s: %v\n", t, err)
			failed = true
		}
	}
	if failed {
		return 1
	}
	for _, t := range targets {
		dir := filepath.Join(*out, string(t))
		built, err := Build(Options{Target: t, Scope: scope, Version: version, Tools: tools}, dir)
		if err != nil {
			fmt.Fprintf(stderr, "bundle %s: %v\n", t, err)
			return 1
		}
		for _, warning := range built.Warnings {
			fmt.Fprintf(stderr, "bundle %s: warning: %s\n", t, warning)
		}
		// Verify what was written rather than trusting the build.
		m, err := Verify(dir, Expect{Target: t, Scope: scope, Version: version, Tools: tools, Current: true})
		if err != nil {
			fmt.Fprintf(stderr, "bundle %s failed verification: %v\n", t, err)
			return 1
		}
		fmt.Fprintf(stdout, "%-9s %-8s %2d files  %s  %s\n", t, scope, len(m.Files), shortDigest(m.BundleDigest), dir)
	}
	return 0
}

func shortDigest(d string) string {
	if len(d) > 19 {
		return d[:19]
	}
	return d
}
