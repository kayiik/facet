package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/kayiik/facet/internal/bundle"
	"github.com/kayiik/facet/internal/components"
	"github.com/kayiik/facet/internal/doctor"
	"github.com/kayiik/facet/internal/hook"
	"github.com/kayiik/facet/internal/mcpserver"
	"github.com/kayiik/facet/internal/planning"
	"github.com/kayiik/facet/internal/toolbox"
	"github.com/kayiik/facet/internal/wire"
)

// Version is the Facet release version. Release builds set it with
// -ldflags "-X main.Version=<version>"; it is the only version source.
var Version = "0.0.1-dev"

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `Facet - a video production studio for agentic CLIs

Usage:
  facet <command> [arguments]

Commands:
  tools <op> ...     Run tool operations (list, describe, estimate, run)
  capabilities       Show what can be made now: each capability's tools, free first, and what is configured
  pipelines <op>     List the production pipelines, or describe one (and one stage)
  guidance [path]    Read Facet's bundled guidance, pipelines, styles and record schemas
  doctor             Inspect dependencies, runtimes, and %d tools
  components         Show the components Facet's tools run on, and which are missing
  install <name>...  Install components beside facet (pinned, checksum-verified)
  remove <name>...   Remove components facet install installed
  setup              Ask which components to install, and install them
  mcp                Serve Facet's tools to an agent over MCP (stdio)
  wire               Wire Facet into an agentic CLI at user or project scope
  bundle             Build harness-native bundle layouts for packaging
  hook claude        Answer Claude Code's tool hook: ask before tools that may charge
  version            Print the Facet version
  help               Show this help

Use "facet <command> --help" for command-specific help.
`, len(toolbox.Names()))
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	toolbox.SetProductVersion(Version)
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0

	case "version", "-v", "--version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "Version error: unexpected arguments")
			return 1
		}
		fmt.Fprintf(stdout, "facet v%s\n", Version)
		return 0

	case "tools":
		// Milestones such as a submitted provider job go to stderr as they
		// happen: a run cut off before its envelope still told its job id.
		result, ok := toolbox.CLIWithProgress(args, func(p toolbox.Progress) {
			fmt.Fprintln(stderr, "facet: "+p.Message)
		})
		return encode(stdout, stderr, result, ok, true)

	case "capabilities", "pipelines", "guidance":
		result, ok := planning.CLI(args)
		return encode(stdout, stderr, result, ok, true)

	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ExitOnError)
		fs.SetOutput(stderr)
		_ = fs.Parse(args[1:])
		if fs.NArg() != 0 {
			fmt.Fprintln(stderr, "Doctor error: unexpected arguments")
			return 1
		}
		if _, err := doctor.Run(stdout); err != nil {
			fmt.Fprintf(stderr, "Doctor error: %v\n", err)
			return 1
		}
		return 0

	case "components", "install", "remove", "setup":
		return components.CLI(args, os.Stdin, stdout, stderr, Version)

	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ExitOnError)
		fs.SetOutput(stderr)
		root := fs.String("root", "", "Directory that tool paths must stay inside (default: client roots, else the working directory)")
		_ = fs.Parse(args[1:])
		if fs.NArg() != 0 {
			fmt.Fprintln(stderr, "MCP error: unexpected arguments")
			return 1
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := mcpserver.Run(ctx, mcpserver.Options{Root: *root, Version: Version, In: os.Stdin, Out: os.Stdout}); err != nil {
			fmt.Fprintf(stderr, "MCP error: %v\n", err)
			return 1
		}
		return 0

	case "wire":
		return wire.CLI(args[1:], stdout, stderr, Version)

	case "bundle":
		return bundle.CLI(args[1:], stdout, stderr, Version)

	case "hook":
		return hook.CLI(args[1:], os.Stdin, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "Unknown command %q. Run 'facet help' for usage.\n", args[0])
		return 1
	}
}

func encode(stdout, stderr io.Writer, value any, ok, indent bool) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}
