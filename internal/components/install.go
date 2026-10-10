package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	facet "github.com/kayiik/facet"
	"github.com/kayiik/facet/internal/toolbox"
)

const hyperframesVersion = "0.8.46"

// Options say how to install.
type Options struct {
	// From is a folder that may hold the downloads already, by their names
	// (offline installs, or a second computer).
	From string
	// Offline refuses to download what From lacks.
	Offline bool
	// Force reinstalls a component Facet installed, and installs Facet's own
	// copy beside one already on the computer.
	Force bool
	// Version is this facet's version, sent with downloads.
	Version string
	// Log receives progress for people; nil discards it.
	Log io.Writer
}

// logMu keeps progress lines whole when a heartbeat and a step print at once.
var logMu sync.Mutex

func (o Options) logf(format string, args ...any) {
	if o.Log != nil {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(o.Log, format+"\n", args...)
	}
}

// receipt records what `facet install` put in a component's folder; only a
// folder with one is replaced or removed.
type receipt struct {
	Component   string    `json:"component"`
	Version     string    `json:"version"`
	Platform    string    `json:"platform"`
	InstalledAt time.Time `json:"installed_at"`
	Sources     []string  `json:"sources,omitempty"`
	// Also lists other paths under dependencies the component owns.
	Also []string `json:"also,omitempty"`
}

const receiptName = ".facet-component.json"

func readReceipt(dir string) (receipt, bool) {
	var r receipt
	data, err := os.ReadFile(filepath.Join(dir, receiptName))
	if err != nil || json.Unmarshal(data, &r) != nil || r.Component == "" {
		return receipt{}, false
	}
	return r, true
}

func receiptVersion(c Component) string {
	if dir := toolbox.RuntimeDependency(c.Dir); dir != "" {
		if r, ok := readReceipt(dir); ok {
			return r.Version
		}
	}
	return ""
}

func writeReceipt(dir string, r receipt) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, receiptName), append(data, '\n'), 0o644)
}

func composerFile(name string) ([]byte, error) {
	return facet.Composer.ReadFile("remotion-composer/" + name)
}

// Plan returns what installing ids does, in order: each requested component
// and each missing one it needs, without what is there already (unless
// Force).
func Plan(ids []string, force bool) ([]Status, error) {
	want := map[string]bool{}
	var visit func(id string, requested bool) error
	visit = func(id string, requested bool) error {
		c, ok := Lookup(id)
		if !ok {
			return fmt.Errorf("unknown component %q; components are %s", id, strings.Join(IDs(), ", "))
		}
		if want[c.ID] {
			return nil
		}
		for _, n := range c.Needs {
			if err := visit(n, false); err != nil {
				return err
			}
		}
		s := StatusOf(c)
		switch {
		case s.State == Unavailable:
			if requested {
				return errors.New(s.Detail)
			}
		case s.State == Missing, requested && force:
			want[c.ID] = true
		}
		return nil
	}
	for _, id := range ids {
		if err := visit(id, true); err != nil {
			return nil, err
		}
	}
	var out []Status
	for _, c := range All() {
		if want[c.ID] {
			out = append(out, StatusOf(c))
		}
	}
	return out, nil
}

// Install installs ids and the missing components they need, in order.
func Install(ctx context.Context, ids []string, opts Options) error {
	root := toolbox.RuntimeRoot()
	if root == "" {
		return errors.New("cannot locate this facet's runtime folder")
	}
	plan, err := Plan(ids, opts.Force)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if c, ok := Lookup(id); ok {
			if s := StatusOf(c); !opts.Force && s.State != Missing {
				opts.logf("%s is %s (%s); nothing to do.", c.Name, describe(s.State), firstNonEmpty(s.Found, s.Path))
			}
		}
	}
	deps := filepath.Join(root, "dependencies")
	if err := os.MkdirAll(deps, 0o755); err != nil {
		return err
	}
	for _, s := range plan {
		opts.logf("Installing %s %s: %s", s.Name, s.Version, s.Enables)
		if err := install(ctx, s.Component, deps, opts); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		after := StatusOf(s.Component)
		if after.State != Installed {
			return fmt.Errorf("%s was installed, but Facet's tools do not find it (%s)", s.Name, firstNonEmpty(after.Detail, string(after.State)))
		}
		opts.logf("  %s %s is installed.", s.Name, firstNonEmpty(after.Found, s.Version))
	}
	return nil
}

func describe(s State) string {
	switch s {
	case Installed:
		return "installed"
	case System:
		return "already on this computer"
	}
	return string(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func install(ctx context.Context, c Component, deps string, opts Options) error {
	dest := filepath.Join(deps, c.Dir)
	if err := replaceable(dest, opts.Force); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(deps, ".install-"+c.ID+"-")
	if err != nil {
		return err
	}
	defer removeTree(stage)
	tree := filepath.Join(stage, "tree")
	downloads := filepath.Join(stage, "downloads")
	for _, dir := range []string{tree, downloads} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	r := receipt{Component: c.ID, Version: c.Version, Platform: platform, InstalledAt: time.Now().UTC()}
	switch c.ID {
	case "node", "ffmpeg", "browser":
		pins := map[string]map[string][]download{"node": nodePins, "ffmpeg": ffmpegPins, "browser": browserPins}[c.ID]
		for _, d := range pins[platform] {
			file, err := fetch(ctx, d, downloads, opts)
			if err != nil {
				return err
			}
			if err := unpack(file, tree, d); err != nil {
				return err
			}
			r.Sources = append(r.Sources, d.URL)
		}
	case "remotion":
		if err := writeComposer(tree); err != nil {
			return err
		}
		if err := npmCI(ctx, tree, opts); err != nil {
			return err
		}
	case "hyperframes":
		for _, name := range []string{"package.json", "package-lock.json"} {
			data, err := hyperframesFiles.ReadFile("hyperframes/" + name)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(tree, name), data, 0o644); err != nil {
				return err
			}
		}
		if err := npmCI(ctx, tree, opts); err != nil {
			return err
		}
	case "piper":
		// A Python environment cannot move once made, so Piper is built in
		// place, with the previous one kept aside until it works.
		return installPiper(ctx, c, deps, stage, opts)
	default:
		return fmt.Errorf("no installer for %s", c.ID)
	}
	if err := writeReceipt(tree, r); err != nil {
		return err
	}
	return replaceDir(dest, tree, stage)
}

// replaceable refuses to replace what Facet did not install: a link (a
// development layout) or a folder without a receipt.
func replaceable(dest string, force bool) error {
	info, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return fmt.Errorf("%s is a link, such as a development layout; Facet leaves it alone", dest)
	}
	if _, ok := readReceipt(dest); !ok && !force {
		return fmt.Errorf("%s exists and was not installed by facet install; remove it, or install with --force to replace it", dest)
	}
	return nil
}

// replaceDir moves tree into dest, keeping the previous dest until tree is in
// place, and putting it back when the move fails.
func replaceDir(dest, tree, stage string) error {
	if _, err := os.Lstat(dest); err == nil {
		old := filepath.Join(stage, "previous")
		if err := rename(dest, old); err != nil {
			return fmt.Errorf("cannot move the previous %s aside (is a program using it?): %w", filepath.Base(dest), err)
		}
		if err := rename(tree, dest); err != nil {
			_ = rename(old, dest)
			return err
		}
		return nil
	}
	return rename(tree, dest)
}

// rename retries for a while: on Windows, a scanner that has just opened a
// fresh file blocks moving its folder.
func rename(from, to string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		if runtime.GOOS != "windows" {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func removeTree(dir string) {
	for attempt := 0; attempt < 10; attempt++ {
		if err := os.RemoveAll(dir); err == nil || runtime.GOOS != "windows" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func writeComposer(tree string) error {
	return fs.WalkDir(facet.Composer, "remotion-composer", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := facet.Composer.ReadFile(name)
		if err != nil {
			return err
		}
		dest := filepath.Join(tree, filepath.FromSlash(strings.TrimPrefix(name, "remotion-composer/")))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// Remove removes components Facet installed; one already on the computer,
// or a folder Facet did not make, is left alone.
func Remove(ids []string, opts Options) error {
	for _, id := range ids {
		c, ok := Lookup(id)
		if !ok {
			return fmt.Errorf("unknown component %q; components are %s", id, strings.Join(IDs(), ", "))
		}
		dir := toolbox.RuntimeDependency(c.Dir)
		if dir == "" {
			return errors.New("cannot locate this facet's runtime folder")
		}
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			opts.logf("%s is not installed beside this facet; nothing to remove.", c.Name)
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return fmt.Errorf("%s is a link, such as a development layout; Facet leaves it alone", dir)
		}
		r, ok := readReceipt(dir)
		if !ok {
			return fmt.Errorf("%s was not installed by facet install; Facet leaves it alone", dir)
		}
		for _, other := range All() {
			for _, n := range other.Needs {
				if n == c.ID && StatusOf(other).State == Installed {
					opts.logf("Note: %s runs on %s; it will use one on this computer, if there is one.", other.Name, c.Name)
				}
			}
		}
		trash := dir + ".removing"
		if err := rename(dir, trash); err != nil {
			return fmt.Errorf("cannot remove %s (is a program using it?): %w", dir, err)
		}
		removeTree(trash)
		for _, also := range r.Also {
			if path, err := within(filepath.Dir(dir), also); err == nil {
				os.Remove(path)
			}
		}
		opts.logf("Removed %s.", c.Name)
	}
	return nil
}

// nodeAndNPM returns the node a component's packages install with, and npm's
// own script beside it.
func nodeAndNPM() (string, string, error) {
	node, err := toolbox.ResolveProgram("node")
	if err != nil {
		return "", "", errors.New("Node.js is missing; install the node component first")
	}
	var candidates []string
	for _, n := range []string{node, evalSymlinks(node)} {
		dir := filepath.Dir(n)
		candidates = append(candidates,
			filepath.Join(dir, "node_modules", "npm", "bin", "npm-cli.js"),
			filepath.Join(filepath.Dir(dir), "lib", "node_modules", "npm", "bin", "npm-cli.js"))
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			return node, c, nil
		}
	}
	return "", "", fmt.Errorf("npm is not beside %s; reinstall Node.js with npm, or install Facet's node component", node)
}

func evalSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// npmCI installs the packages dir's lockfile pins, with the node Facet runs
// first on PATH so install scripts use it too.
func npmCI(ctx context.Context, dir string, opts Options) error {
	node, npm, err := nodeAndNPM()
	if err != nil {
		return err
	}
	opts.logf("  Installing npm packages with Node.js %s (npm ci; this can take a few minutes)", strings.TrimPrefix(firstLine(node, "--version"), "v"))
	cmd := exec.CommandContext(ctx, node, npm, "ci", "--no-audit", "--no-fund", "--loglevel=error")
	cmd.Dir = dir
	cmd.Env = append(withPath(os.Environ(), filepath.Dir(node)), "npm_config_update_notifier=false")
	started := time.Now()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				opts.logf("    still installing (%d min so far)", int(time.Since(started).Minutes()))
			}
		}
	}()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm ci failed: %v\n%s", err, tail(string(out), 40))
	}
	return nil
}

// withPath puts dir first on PATH in env.
func withPath(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	path := ""
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "PATH") {
			path = value
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+dir+string(os.PathListSeparator)+path)
}

func tail(text string, lines int) string {
	all := strings.Split(strings.TrimSpace(text), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}
