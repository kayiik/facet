package components

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
)

// CLI runs `facet components`, `facet install`, `facet remove` and
// `facet setup`. args starts with the command name.
func CLI(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "components":
		return componentsCLI(args[1:], stdout, stderr)
	case "install":
		return installCLI(ctx, args[1:], stdout, stderr, version)
	case "remove":
		return removeCLI(args[1:], stdout, stderr)
	case "setup":
		return setupCLI(ctx, args[1:], stdin, stdout, stderr, version)
	}
	fmt.Fprintf(stderr, "unknown command %q\n", args[0])
	return 1
}

func componentsCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("components", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: facet components [--json]")
		return 1
	}
	statuses := Statuses()
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"ok": true, "operation": "components", "result": map[string]any{"components": statuses}, "warnings": []string{}}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	printTable(stdout, statuses)
	var missing []string
	for _, s := range statuses {
		if s.State == Missing {
			missing = append(missing, s.ID)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stdout, "\nInstall what is missing with: facet install %s\n(or facet setup, which asks about each)\n", strings.Join(Minimal(missing), " "))
	}
	return 0
}

func label(s State) string {
	switch s {
	case System:
		return "on this computer"
	}
	return string(s)
}

func printTable(w io.Writer, statuses []Status) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tSTATE\tFOUND\tWHAT IT ENABLES")
	for _, s := range statuses {
		found := s.Found
		switch {
		case s.State == Missing:
			found = s.Size
		case found == "":
			found = s.Path
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.ID, label(s.State), shorten(found, 48), s.Enables)
	}
	tw.Flush()
}

func shorten(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n-1] + "…"
}

// expand turns the words people use into component ids.
func expand(words []string) ([]string, error) {
	var ids []string
	for _, w := range words {
		for _, part := range strings.Split(w, ",") {
			switch part = strings.ToLower(strings.TrimSpace(part)); part {
			case "":
			case "defaults", "default":
				for _, c := range All() {
					if c.Default {
						ids = append(ids, c.ID)
					}
				}
			case "all":
				ids = append(ids, IDs()...)
			default:
				if _, ok := Lookup(part); !ok {
					return nil, fmt.Errorf("unknown component %q; components are %s, or defaults, or all", part, strings.Join(IDs(), ", "))
				}
				ids = append(ids, part)
			}
		}
	}
	return ids, nil
}

func installFlags(name string, stderr io.Writer) (*flag.FlagSet, *Options) {
	opts := &Options{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.From, "from", "", "a folder that holds the downloads already")
	fs.BoolVar(&opts.Offline, "offline", false, "download nothing; take every file from --from")
	fs.BoolVar(&opts.Force, "force", false, "reinstall, and install Facet's own copy beside one already on this computer")
	return fs, opts
}

// flagsFirst moves flags ahead of the component names, so that
// `facet install browser --from DIR` reads like `facet install --from DIR
// browser`. valued names the flags that take a value.
func flagsFirst(args []string, valued ...string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			rest = append(rest, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if !strings.Contains(name, "=") && i+1 < len(args) {
			for _, v := range valued {
				if name == v {
					i++
					flags = append(flags, args[i])
				}
			}
		}
	}
	return append(flags, rest...)
}

func installCLI(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	fs, opts := installFlags("install", stderr)
	if fs.Parse(flagsFirst(args, "from")) != nil || fs.NArg() == 0 {
		fmt.Fprintf(stderr, "usage: facet install [--from DIR] [--offline] [--force] <component>...\ncomponents: %s, or defaults, or all\n", strings.Join(IDs(), ", "))
		return 1
	}
	if opts.Offline && opts.From == "" {
		fmt.Fprintln(stderr, "facet install: --offline needs --from")
		return 1
	}
	ids, err := expand(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, "facet install:", err)
		return 1
	}
	opts.Version, opts.Log = version, stdout
	if err := Install(ctx, ids, *opts); err != nil {
		fmt.Fprintln(stderr, "facet install:", err)
		return 1
	}
	return 0
}

func removeCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "usage: facet remove <component>...\ncomponents: %s\n", strings.Join(IDs(), ", "))
		return 1
	}
	ids, err := expand(args)
	if err != nil {
		fmt.Fprintln(stderr, "facet remove:", err)
		return 1
	}
	if err := Remove(ids, Options{Log: stdout}); err != nil {
		fmt.Fprintln(stderr, "facet remove:", err)
		return 1
	}
	return 0
}

func setupCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	fs, opts := installFlags("setup", stderr)
	yes := fs.Bool("yes", false, "install the default components without asking")
	all := fs.Bool("all", false, "with --yes, install the optional components too")
	if fs.Parse(flagsFirst(args, "from")) != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: facet setup [--yes [--all]] [--from DIR] [--offline]")
		return 1
	}
	if !*yes && !interactive(stdin) {
		fmt.Fprintln(stderr, "facet setup asks about each component; without a terminal, run facet setup --yes or facet install <component>")
		return 1
	}
	statuses := Statuses()
	fmt.Fprintln(stdout, "Facet's tools run on these components. Facet installs them beside itself;")
	fmt.Fprintln(stdout, "one already on this computer is used instead.")
	fmt.Fprintln(stdout)
	printTable(stdout, statuses)
	fmt.Fprintln(stdout)
	reader := bufio.NewReader(stdin)
	var chosen []string
	for _, s := range statuses {
		if s.State != Missing {
			continue
		}
		take := s.Default || *all
		if !*yes {
			hint := "[Y/n]"
			if !s.Default {
				hint = "[y/N]"
			}
			fmt.Fprintf(stdout, "Install %s (%s; %s)? %s ", s.Name, s.Enables, s.Size, hint)
			line, _ := reader.ReadString('\n')
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y", "yes":
				take = true
			case "n", "no":
				take = false
			}
		}
		if take {
			chosen = append(chosen, s.ID)
		}
	}
	if len(chosen) == 0 {
		fmt.Fprintln(stdout, "Nothing to install.")
		return 0
	}
	opts.Version, opts.Log = version, stdout
	if err := Install(ctx, chosen, *opts); err != nil {
		fmt.Fprintln(stderr, "facet setup:", err)
		return 1
	}
	fmt.Fprintln(stdout)
	printTable(stdout, Statuses())
	return 0
}

// interactive reports whether r is a terminal a person can answer in.
func interactive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
