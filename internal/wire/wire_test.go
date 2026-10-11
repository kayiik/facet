package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/kayiik/facet/internal/bundle"
	"github.com/kayiik/facet/internal/toolbox"
)

const testVersion = "2.0.0-test"

// The test binary doubles as fake agentic CLIs: copied onto a temporary PATH
// as claude[.exe] or codex[.exe], it records its argv and emulates the
// configuration writes of `mcp add` and `mcp remove`.
//
// Per-test fakes are hard links to one copy of the test binary made here:
// Windows cannot delete a link to the image of a running process, so they
// must not link to the test binary itself.
var fakeSource string

func TestMain(m *testing.M) {
	if logPath := os.Getenv("FACET_WIRE_FAKE_LOG"); logPath != "" {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
		switch name {
		case "claude", "codex", "copilot", "opencode":
			os.Exit(fakeCLI(name, os.Args[1:], logPath))
		}
	}
	dir, err := os.MkdirTemp("", "facet-wire-fake-")
	if err == nil {
		if self, err := os.Executable(); err == nil {
			dest := filepath.Join(dir, "fake")
			if runtime.GOOS == "windows" {
				dest += ".exe"
			}
			if copyFile(self, dest) == nil {
				fakeSource = dest
			}
		}
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type fakeCall struct {
	CLI  string   `json:"cli"`
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
}

func fakeCLI(name string, args []string, logPath string) int {
	dir, _ := os.Getwd()
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 9
	}
	_ = json.NewEncoder(f).Encode(fakeCall{CLI: name, Args: args, Dir: dir})
	f.Close()
	if os.Getenv("FACET_WIRE_FAKE_FAIL") == name {
		fmt.Fprintln(os.Stderr, "simulated failure")
		return 3
	}
	if len(args) < 3 || args[0] != "mcp" {
		return 0
	}
	scope := "local"
	var rest []string
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--scope", "-s":
			scope = args[i+1]
			i++
		case "--transport", "-t":
			i++
		case "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		default:
			rest = append(rest, args[i])
		}
	}
	home, _ := os.UserHomeDir()
	server := rest[0]
	switch name {
	case "claude":
		file := filepath.Join(home, ".claude.json")
		if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
			file = filepath.Join(d, ".claude.json")
		}
		if scope == "project" {
			file = filepath.Join(dir, ".mcp.json")
		}
		doc := map[string]any{}
		if data, err := os.ReadFile(file); err == nil {
			_ = json.Unmarshal(data, &doc)
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch args[1] {
		case "add":
			if _, exists := servers[server]; exists {
				fmt.Fprintf(os.Stderr, "MCP server %s already exists\n", server)
				return 1
			}
			servers[server] = map[string]any{"type": "stdio", "command": rest[1], "args": rest[2:], "env": map[string]any{}}
		case "remove":
			if _, exists := servers[server]; !exists {
				fmt.Fprintf(os.Stderr, "No MCP server found with name: %s\n", server)
				return 1
			}
			delete(servers, server)
		}
		doc["mcpServers"] = servers
		data, _ := json.MarshalIndent(doc, "", "  ")
		_ = os.WriteFile(file, data, 0o644)
	case "codex":
		file := filepath.Join(home, ".codex", "config.toml")
		if d := os.Getenv("CODEX_HOME"); d != "" {
			file = filepath.Join(d, "config.toml")
		}
		data, _ := os.ReadFile(file)
		content := fakeRemoveTable(string(data), "mcp_servers."+server)
		switch args[1] {
		case "add":
			quoted := make([]string, len(rest[2:]))
			for i, a := range rest[2:] {
				quoted[i] = tomlString(a)
			}
			content += fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n", server, tomlString(rest[1]), strings.Join(quoted, ", "))
		case "remove":
			if !inspectTOMLServer(string(data), server).defined {
				fmt.Fprintf(os.Stderr, "No MCP server named '%s' found\n", server)
				return 1
			}
		}
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		_ = os.WriteFile(file, []byte(content), 0o644)
	}
	return 0
}

func fakeRemoveTable(content, header string) string {
	var out []string
	skipping := false
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			skipping = trimmed == "["+header+"]"
		}
		if !skipping && line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

type sandbox struct {
	root, home, project, bin, log string
	exe                           string
}

func newSandbox(t *testing.T, clis ...string) *sandbox {
	t.Helper()
	root := t.TempDir()
	s := &sandbox{
		root:    root,
		home:    filepath.Join(root, "home"),
		project: filepath.Join(root, "work", "demo"),
		bin:     filepath.Join(root, "bin"),
		log:     filepath.Join(root, "calls.jsonl"),
	}
	for _, dir := range []string{s.home, s.project, s.bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", s.home)
	t.Setenv("USERPROFILE", s.home)
	t.Setenv("APPDATA", filepath.Join(s.home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(s.home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(s.home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("FACET_HOME", "")
	t.Setenv("PATH", s.bin)
	t.Setenv("FACET_WIRE_FAKE_LOG", s.log)
	t.Setenv("FACET_WIRE_FAKE_FAIL", "")
	for _, cli := range clis {
		s.installFake(t, cli)
	}
	// A fixed running executable makes registrations deterministic: the facet
	// an installer put in its programs folder.
	name := "facet"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	s.exe = filepath.Join(root, "Programs", "Facet", "bin", name)
	if err := os.MkdirAll(filepath.Dir(s.exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.exe, []byte("installed facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	// facet wire registers the running facet with its links resolved, which on
	// Windows also expands a short (8.3) temporary folder such as RUNNER~1.
	s.exe = resolved(t, s.exe)
	t.Cleanup(toolbox.UseExecutable(s.exe))
	t.Chdir(s.project)
	return s
}

// resolved returns path with its links and Windows short names resolved.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func (s *sandbox) installFake(t *testing.T, cli string) {
	t.Helper()
	if fakeSource == "" {
		t.Fatal("no copy of the test binary is available for fake CLIs")
	}
	dest := filepath.Join(s.bin, cli)
	if runtime.GOOS == "windows" {
		dest += ".exe"
	}
	if err := os.Link(fakeSource, dest); err == nil {
		return
	}
	if err := copyFile(fakeSource, dest); err != nil {
		t.Fatal(err)
	}
}

func (s *sandbox) calls(t *testing.T) []fakeCall {
	t.Helper()
	f, err := os.Open(s.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var calls []fakeCall
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var c fakeCall
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := CLI(args, &stdout, &stderr, testVersion)
	return stdout.String(), stderr.String(), code
}

func runVersion(t *testing.T, version string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := CLI(args, &stdout, &stderr, version)
	return stdout.String(), stderr.String(), code
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := run(t, args...)
	if code != 0 {
		t.Fatalf("facet wire %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, name)
		if entry.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(plainJSON(data), &doc); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	return doc
}

func dig(doc map[string]any, keys ...string) any {
	var cur any = doc
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func expectFiles(t *testing.T, root string, target bundle.Target) {
	t.Helper()
	files, err := bundle.Files(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("%s: %s not installed: %v", target, f.Path, err)
			continue
		}
		if !bytes.Equal(got, f.Content) {
			t.Errorf("%s: %s differs from the projection", target, f.Path)
		}
	}
}

func loadRegistry(t *testing.T, home string) *Registry {
	t.Helper()
	reg, err := LoadRegistry(RegistryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// expectAskRules checks that every CLI that has an approval setting asks
// before each tool that may charge.
func expectAskRules(t *testing.T, s *sandbox, scope bundle.Scope, project string) {
	t.Helper()
	paid := paidTools()
	if len(paid) == 0 {
		t.Fatal("no tool declares may_charge")
	}
	claudeFile := filepath.Join(s.home, ".claude", "settings.json")
	codexFile := filepath.Join(s.home, ".codex", "config.toml")
	opencodeFile := filepath.Join(s.home, ".config", "opencode", "opencode.json")
	if scope == bundle.ScopeProject {
		claudeFile = filepath.Join(project, ".claude", "settings.json")
		codexFile = filepath.Join(project, ".codex", "config.toml")
		opencodeFile = filepath.Join(project, "opencode.json")
	}

	ask := map[string]bool{}
	if list, ok := dig(readJSON(t, claudeFile), "permissions", "ask").([]any); ok {
		for _, item := range list {
			ask[fmt.Sprint(item)] = true
		}
	}
	codex, err := os.ReadFile(codexFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codex), "tool_timeout_sec = 600") {
		t.Errorf("%s sets no MCP call limit for facet:\n%s", codexFile, codex)
	}
	// Codex passes MCP servers only a short allowlist of variables; without
	// env_vars a provider key the person exported never reaches facet.
	for _, name := range toolbox.EnvVars() {
		if !strings.Contains(string(codex), `"`+name+`"`) || !strings.Contains(string(codex), "\nenv_vars = [") {
			t.Errorf("%s does not forward %s to facet:\n%s", codexFile, name, codex)
		}
	}
	data, err := os.ReadFile(opencodeFile)
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseJSONC(data)
	if err != nil {
		t.Fatal(err)
	}
	perm, _ := root.member("permission")
	if perm == nil || perm.value.kind != '{' {
		t.Fatalf("%s has no permission object:\n%s", opencodeFile, data)
	}
	members := perm.value.members
	if len(members) < len(paid) {
		t.Fatalf("%s permission = %s", opencodeFile, data[perm.value.start:perm.value.end])
	}
	// OpenCode lets the last matching rule win: Facet's rules come last.
	last := map[string]string{}
	for _, m := range members[len(members)-len(paid):] {
		value, _ := stringValue(data, m.value)
		last[m.key] = value
	}
	for _, tool := range paid {
		if !ask["mcp__facet__"+tool] || !ask["Bash(facet tools run "+tool+" *)"] {
			t.Errorf("claude does not ask before %s: %v", tool, ask)
		}
		if !strings.Contains(string(codex), "[mcp_servers.facet.tools."+tool+"]\napproval_mode = \"prompt\"") {
			t.Errorf("codex does not ask before %s:\n%s", tool, codex)
		}
		if last["facet_"+tool] != "ask" {
			t.Errorf("opencode does not ask before %s: %v", tool, last)
		}
	}
}

func TestWireUserScopeUsesEachCLIsNativeLocationsAndRegistration(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	out := mustRun(t, "all")

	roots := map[bundle.Target]string{
		bundle.TargetClaude:   filepath.Join(s.home, ".claude"),
		bundle.TargetCodex:    filepath.Join(s.home, ".codex"),
		bundle.TargetCopilot:  filepath.Join(s.home, ".copilot"),
		bundle.TargetOpenCode: filepath.Join(s.home, ".config", "opencode"),
	}
	for target, root := range roots {
		expectFiles(t, root, target)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".claude", "agents", "facet-critic.md")); err != nil {
		t.Error("Claude Code role agents not installed as native agents")
	}
	if _, err := os.Stat(filepath.Join(s.home, ".copilot", "agents", "facet.agent.md")); err != nil {
		t.Error("Copilot producer agent not installed as a native agent")
	}
	if _, err := os.Stat(filepath.Join(s.home, ".codex", "agents")); !os.IsNotExist(err) {
		t.Error("a persona was installed for Codex, whose agent format is not validated")
	}

	calls := s.calls(t)
	want := map[string][]string{
		"claude": {"mcp", "add", "--scope", "user", "--transport", "stdio", "facet", "--", s.exe, "mcp"},
	}
	if len(calls) != 1 {
		t.Fatalf("CLI calls = %+v, want one registration for claude; codex is registered by its config block", calls)
	}
	for _, c := range calls {
		if strings.Join(c.Args, "\x00") != strings.Join(want[c.CLI], "\x00") {
			t.Errorf("%s argv = %q, want %q", c.CLI, c.Args, want[c.CLI])
		}
	}
	if got := dig(readJSON(t, filepath.Join(s.home, ".claude.json")), "mcpServers", "facet", "command"); got != s.exe {
		t.Errorf("claude registration command = %v", got)
	}
	codexConfig, err := os.ReadFile(filepath.Join(s.home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if st := inspectTOMLServer(string(codexConfig), "facet"); !st.parsed || st.command != s.exe || strings.Join(st.args, " ") != "mcp" {
		t.Errorf("codex user config = %q", codexConfig)
	}
	copilot := readJSON(t, filepath.Join(s.home, ".copilot", "mcp-config.json"))
	if dig(copilot, "mcpServers", "facet", "command") != s.exe || dig(copilot, "mcpServers", "facet", "type") != "local" {
		t.Errorf("copilot registration = %v", copilot)
	}
	opencode := readJSON(t, filepath.Join(s.home, ".config", "opencode", "opencode.json"))
	if cmd, _ := dig(opencode, "mcp", "facet", "command").([]any); len(cmd) != 2 || cmd[0] != s.exe || cmd[1] != "mcp" {
		t.Errorf("opencode registration = %v", opencode)
	}

	reg := loadRegistry(t, s.home)
	if len(reg.Wirings) != 4 {
		t.Fatalf("registry wirings = %d, want 4", len(reg.Wirings))
	}
	methods := map[string]string{}
	for _, w := range reg.Wirings {
		if w.FacetVersion != testVersion || w.Executable != s.exe || w.Pending || w.MCP == nil || len(w.Files) == 0 {
			t.Errorf("incomplete record %+v", w)
			continue
		}
		methods[w.CLI] = w.MCP.Method
		for _, f := range w.Files {
			if got, err := fileDigest(f.Path); err != nil || got != f.Digest {
				t.Errorf("recorded digest of %s does not match the file", f.Path)
			}
		}
	}
	if methods["claude"] != MethodCommand || methods["codex"] != MethodTOML || methods["copilot"] != MethodJSON || methods["opencode"] != MethodJSON {
		t.Errorf("registration methods = %v", methods)
	}
	expectAskRules(t, s, bundle.ScopeUser, "")

	// Facet never writes instruction files.
	for name := range tree(t, s.home) {
		switch filepath.Base(name) {
		case "AGENTS.md", "CLAUDE.md", "copilot-instructions.md", "AGENT.md":
			t.Errorf("wrote instruction file %s", name)
		}
	}
	if !strings.Contains(out, "wired") {
		t.Errorf("output does not confirm the wiring:\n%s", out)
	}
}

func TestWireIsIdempotent(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	mustRun(t, "all")
	before := tree(t, s.home)
	calls := len(s.calls(t))
	out := mustRun(t, "claude,codex,copilot,opencode")
	if after := tree(t, s.home); len(after) != len(before) {
		t.Fatalf("second run changed the tree: %d vs %d entries", len(after), len(before))
	} else {
		for name, content := range before {
			if after[name] != content {
				t.Errorf("second run changed %s", name)
			}
		}
	}
	if got := len(s.calls(t)); got != calls {
		t.Errorf("second run invoked a CLI %d more time(s)", got-calls)
	}
	if strings.Contains(out, "create") || strings.Contains(out, " run ") {
		t.Errorf("second run planned changes:\n%s", out)
	}
}

func TestWireProjectScope(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	homeBefore := tree(t, s.home)
	mustRun(t, "all", "--scope", "project")

	roots := map[bundle.Target]string{
		bundle.TargetClaude:   filepath.Join(s.project, ".claude"),
		bundle.TargetCodex:    filepath.Join(s.project, ".agents"),
		bundle.TargetCopilot:  filepath.Join(s.project, ".github"),
		bundle.TargetOpenCode: filepath.Join(s.project, ".opencode"),
	}
	for target, root := range roots {
		expectFiles(t, root, target)
	}
	calls := s.calls(t)
	if len(calls) != 1 || calls[0].CLI != "claude" || !samePath(calls[0].Dir, s.project) ||
		strings.Join(calls[0].Args, " ") != strings.Join([]string{"mcp", "add", "--scope", "project", "--transport", "stdio", "facet", "--", s.exe, "mcp"}, " ") {
		t.Fatalf("project-scope CLI calls = %+v; only Claude has a project-scope command", calls)
	}
	if dig(readJSON(t, filepath.Join(s.project, ".mcp.json")), "mcpServers", "facet", "command") != s.exe {
		t.Error("Claude project registration missing from .mcp.json")
	}
	toml, err := os.ReadFile(filepath.Join(s.project, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if st := inspectTOMLServer(string(toml), "facet"); !st.parsed || st.command != s.exe || strings.Join(st.args, " ") != "mcp" {
		t.Errorf("codex project config = %q", toml)
	}
	if dig(readJSON(t, filepath.Join(s.project, ".github", "mcp.json")), "mcpServers", "facet", "command") != s.exe {
		t.Error("Copilot project registration missing")
	}
	if dig(readJSON(t, filepath.Join(s.project, "opencode.json")), "mcp", "facet", "enabled") != true {
		t.Error("OpenCode project registration missing")
	}
	expectAskRules(t, s, bundle.ScopeProject, s.project)
	// Only the registry changes in the home directory.
	homeAfter := tree(t, s.home)
	for name := range homeAfter {
		if _, ok := homeBefore[name]; !ok && name != ".facet/" && name != ".facet/wiring.json" {
			t.Errorf("project-scope wiring wrote %s in the home directory", name)
		}
	}
	reg := loadRegistry(t, s.home)
	for _, w := range reg.Wirings {
		if w.Scope != "project" || !samePath(w.Project, s.project) {
			t.Errorf("record %s/%s/%s", w.CLI, w.Scope, w.Project)
		}
	}

	// --project implies project scope and names the same wiring.
	out := mustRun(t, "opencode", "--project", s.project)
	if !strings.Contains(out, "unchanged") {
		t.Errorf("rewiring the same project planned changes:\n%s", out)
	}
}

func TestDryRunHasNoSideEffects(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	before := tree(t, s.root)
	out := mustRun(t, "all", "--dry-run")
	if after := tree(t, s.root); len(after) != len(before) {
		t.Fatalf("dry run changed the file system")
	}
	if calls := s.calls(t); len(calls) != 0 {
		t.Fatalf("dry run invoked CLIs: %+v", calls)
	}
	for _, want := range []string{
		"Dry run",
		"create",
		commandLine([]string{"claude", "mcp", "add", "--scope", "user", "--transport", "stdio", "facet", "--", s.exe, "mcp"}),
		filepath.Join(s.home, ".codex", "config.toml"),
		filepath.Join(s.home, ".claude", "settings.json") + " (ask before each of",
		filepath.Join(s.home, ".copilot", "mcp-config.json"),
		filepath.Join(s.home, ".config", "opencode", "opencode.json"),
		RegistryPath(s.home),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output omits %q:\n%s", want, out)
		}
	}

	mustRun(t, "claude")
	before = tree(t, s.root)
	calls := len(s.calls(t))
	out = mustRun(t, "--remove", "claude", "--dry-run")
	if after := tree(t, s.root); len(after) != len(before) {
		t.Fatal("dry-run removal changed the file system")
	}
	if len(s.calls(t)) != calls || !strings.Contains(out, "mcp remove --scope user facet") {
		t.Fatalf("dry-run removal output:\n%s", out)
	}
}

func TestRemoveReversesOnlyRecordedUnmodifiedItems(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	// Pre-existing user content that must survive.
	ownSkill := filepath.Join(s.home, ".claude", "skills", "mine", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(ownSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownSkill, []byte("---\nname: mine\ndescription: mine\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copilotConfig := filepath.Join(s.home, ".copilot", "mcp-config.json")
	if err := os.MkdirAll(filepath.Dir(copilotConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\n  \"mcpServers\": {\n    \"other\": {\n      \"type\": \"http\",\n      \"url\": \"http://127.0.0.1:9/mcp\"\n    }\n  }\n}\n")
	if err := os.WriteFile(copilotConfig, original, 0o600); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "all")
	modified := filepath.Join(s.home, ".config", "opencode", "agents", "facet-critic.md")
	if err := os.WriteFile(modified, []byte("my edits"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, "--remove", "all")
	if !strings.Contains(out, "left in place") {
		t.Errorf("the kept modified file is not reported:\n%s", out)
	}
	if data, err := os.ReadFile(modified); err != nil || string(data) != "my edits" {
		t.Error("a modified file was deleted")
	}
	if _, err := os.Stat(ownSkill); err != nil {
		t.Error("a skill facet wire did not install was removed")
	}
	if data, err := os.ReadFile(copilotConfig); err != nil || !bytes.Equal(data, original) {
		t.Errorf("copilot configuration was not restored exactly:\n%s", data)
	}
	for _, gone := range []string{
		filepath.Join(s.home, ".claude", "skills", "facet"),
		filepath.Join(s.home, ".claude", "agents"),
		filepath.Join(s.home, ".claude", "settings.json"),
		filepath.Join(s.home, ".codex", "skills"),
		filepath.Join(s.home, ".codex", "config.toml"),
		filepath.Join(s.home, ".copilot", "skills"),
		filepath.Join(s.home, ".copilot", "agents"),
		filepath.Join(s.home, ".config", "opencode", "opencode.json"),
		filepath.Join(s.home, ".config", "opencode", "skills", "facet"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived removal", gone)
		}
	}
	if dig(readJSON(t, filepath.Join(s.home, ".claude.json")), "mcpServers", "facet") != nil {
		t.Error("claude registration survived removal")
	}
	var removes []string
	for _, c := range s.calls(t) {
		if len(c.Args) > 1 && c.Args[1] == "remove" {
			removes = append(removes, c.CLI+" "+strings.Join(c.Args, " "))
		}
	}
	sort.Strings(removes)
	if strings.Join(removes, "|") != "claude mcp remove --scope user facet" {
		t.Errorf("unregister commands = %v", removes)
	}
	if reg := loadRegistry(t, s.home); len(reg.Wirings) != 0 {
		t.Errorf("registry still records %d wirings", len(reg.Wirings))
	}
	// Removing again is a no-op.
	if out := mustRun(t, "--remove", "claude"); !strings.Contains(out, "nothing to remove") {
		t.Errorf("second removal output:\n%s", out)
	}
}

func TestWireRefusesWhatItDoesNotOwn(t *testing.T) {
	t.Run("unowned skill file", func(t *testing.T) {
		s := newSandbox(t, "claude")
		skill := filepath.Join(s.home, ".claude", "skills", "facet", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skill, []byte("my facet notes"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := tree(t, s.root)
		_, errOut, code := run(t, "claude")
		if code != 1 || !strings.Contains(errOut, "was not installed by facet wire") || !strings.Contains(errOut, "Nothing was changed") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if after := tree(t, s.root); len(after) != len(before) || after[".claude/skills/facet/SKILL.md"] != before[".claude/skills/facet/SKILL.md"] {
			t.Fatal("a refused wiring changed files")
		}
		if len(s.calls(t)) != 0 {
			t.Fatal("a refused wiring ran the CLI")
		}
	})
	t.Run("modified owned file", func(t *testing.T) {
		s := newSandbox(t)
		mustRun(t, "copilot")
		skill := filepath.Join(s.home, ".copilot", "skills", "facet", "SKILL.md")
		if err := os.WriteFile(skill, []byte("edited"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := run(t, "copilot")
		if code != 1 || !strings.Contains(errOut, "changed after facet wire installed it") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if data, _ := os.ReadFile(skill); string(data) != "edited" {
			t.Fatal("a modified file was overwritten")
		}
	})
	t.Run("foreign MCP entry", func(t *testing.T) {
		s := newSandbox(t)
		config := filepath.Join(s.home, ".config", "opencode", "opencode.json")
		if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
			t.Fatal(err)
		}
		original := []byte(`{"mcp": {"facet": {"type": "local", "command": ["other-facet", "mcp"]}}}`)
		if err := os.WriteFile(config, original, 0o644); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := run(t, "opencode")
		if code != 1 || !strings.Contains(errOut, "already defines mcp.facet") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if data, _ := os.ReadFile(config); !bytes.Equal(data, original) {
			t.Fatal("a foreign registration was changed")
		}
		if _, err := os.Stat(filepath.Join(s.home, ".config", "opencode", "skills")); !os.IsNotExist(err) {
			t.Fatal("skills were installed although the wiring was refused")
		}
	})
	t.Run("foreign claude registration", func(t *testing.T) {
		s := newSandbox(t, "claude")
		config := filepath.Join(s.home, ".claude.json")
		if err := os.WriteFile(config, []byte(`{"mcpServers": {"facet": {"type": "stdio", "command": "elsewhere", "args": []}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := run(t, "claude")
		if code != 1 || !strings.Contains(errOut, "claude mcp remove --scope user facet") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if len(s.calls(t)) != 0 {
			t.Fatal("the CLI ran for a refused wiring")
		}
	})
	t.Run("missing CLI", func(t *testing.T) {
		s := newSandbox(t)
		_, errOut, code := run(t, "claude")
		if code != 1 || !strings.Contains(errOut, "claude was not found on PATH") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(s.home, ".claude")); !os.IsNotExist(err) {
			t.Fatal("files were written although the CLI is missing")
		}
	})
	t.Run("project is the home directory", func(t *testing.T) {
		s := newSandbox(t)
		_, errOut, code := run(t, "opencode", "--project", s.home)
		if code != 1 || !strings.Contains(errOut, "home directory") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	})
	t.Run("v1 skill copy", func(t *testing.T) {
		s := newSandbox(t)
		skill := filepath.Join(s.project, ".opencode", "skills", "facet", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(skill, []byte("# Facet\n\n## This installation\n- Invoke Facet through run-facet.sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := run(t, "opencode", "--scope", "project")
		if code != 1 || !strings.Contains(errOut, "Facet 1.x installation") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	})
}

func TestCLIFailureKeepsSkillsAndReportsDrift(t *testing.T) {
	s := newSandbox(t, "claude")
	t.Setenv("FACET_WIRE_FAKE_FAIL", "claude")
	_, errOut, code := run(t, "claude")
	if code != 1 || !strings.Contains(errOut, "simulated failure") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	reg := loadRegistry(t, s.home)
	if len(reg.Wirings) != 1 || reg.Wirings[0].MCP != nil || len(reg.Wirings[0].Files) == 0 {
		t.Fatalf("record after a failed registration = %+v", reg.Wirings)
	}
	out := mustRun(t, "--status")
	if !strings.Contains(out, "drift") || !strings.Contains(out, "no MCP server registration") {
		t.Errorf("status does not report the missing registration:\n%s", out)
	}
	t.Setenv("FACET_WIRE_FAKE_FAIL", "")
	mustRun(t, "claude")
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") {
		t.Errorf("rewiring did not repair the registration:\n%s", out)
	}
}

func TestStatusReportsDrift(t *testing.T) {
	s := newSandbox(t, "claude", "codex")
	if out := mustRun(t, "--status"); !strings.Contains(out, "No wirings recorded") {
		t.Fatalf("empty status:\n%s", out)
	}
	mustRun(t, "all")
	out := mustRun(t, "--status")
	if strings.Contains(out, "drift") || strings.Count(out, ": ok") != 4 {
		t.Fatalf("fresh wirings are not ok:\n%s", out)
	}

	modified := filepath.Join(s.home, ".claude", "skills", "facet", "SKILL.md")
	if err := os.WriteFile(modified, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(s.home, ".codex", "skills", "facet", "SKILL.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(s.home, ".config", "opencode", "opencode.json")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, bytes.Replace(data, []byte(`"enabled": true`), []byte(`"enabled": false`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.home, ".copilot", "mcp-config.json")); err != nil {
		t.Fatal(err)
	}

	out, _, code := runVersion(t, "2.1.0", "--status")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	for _, want := range []string{
		"modified: " + modified,
		"missing:  " + missing,
		"mcp:      modified",
		"mcp:      missing",
		"wired by facet v" + testVersion + "; this is facet v2.1.0",
		"fix:      facet wire claude",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status omits %q:\n%s", want, out)
		}
	}

	report, err := Inspect(testVersion)
	if err != nil {
		t.Fatal(err)
	}
	drifted := 0
	for _, w := range report.Wirings {
		if w.Drifted() {
			drifted++
		}
	}
	if len(report.Wirings) != 4 || drifted != 4 {
		t.Errorf("Inspect: %d wirings, %d drifted", len(report.Wirings), drifted)
	}
}

// facet wire registers the facet that runs it; another facet's status calls
// that drift, and wiring again from it registers it instead.
func TestRewireFollowsTheRunningExecutable(t *testing.T) {
	s := newSandbox(t, "claude")
	mustRun(t, "claude")
	if got := dig(readJSON(t, filepath.Join(s.home, ".claude.json")), "mcpServers", "facet", "command"); got != s.exe {
		t.Fatalf("registered %v, want the running facet %s", got, s.exe)
	}
	other := filepath.Join(s.root, "other", "bin", filepath.Base(s.exe))
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("another facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	other = resolved(t, other)
	t.Cleanup(toolbox.UseExecutable(other))
	if out := mustRun(t, "--status"); !strings.Contains(out, "the MCP server runs "+s.exe+", not this facet") {
		t.Errorf("status does not report the other executable:\n%s", out)
	}
	mustRun(t, "claude")
	calls := s.calls(t)
	last := calls[len(calls)-2:]
	if last[0].Args[1] != "remove" || last[1].Args[1] != "add" || last[1].Args[len(last[1].Args)-2] != other {
		t.Fatalf("re-registration calls = %+v", last)
	}
	if got := dig(readJSON(t, filepath.Join(s.home, ".claude.json")), "mcpServers", "facet", "command"); got != other {
		t.Fatalf("registered %v after rewiring, want %s", got, other)
	}
}

func TestJSONCConfigIsMergedAndRestoredByteForByte(t *testing.T) {
	s := newSandbox(t)
	config := filepath.Join(s.home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("// my settings\n{\n\t\"theme\": \"dark\", // keep\n\t/* servers */\n\t\"mcp\": {\n\t\t\"other\": {\"type\": \"remote\", \"url\": \"http://127.0.0.1:9\"},\n\t},\n}\n")
	if err := os.WriteFile(config, original, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "opencode")
	merged, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"// my settings", "// keep", "/* servers */", `"other": {"type": "remote", "url": "http://127.0.0.1:9"},`} {
		if !bytes.Contains(merged, []byte(keep)) {
			t.Errorf("merge lost %q:\n%s", keep, merged)
		}
	}
	doc := readJSON(t, config)
	if dig(doc, "mcp", "facet", "type") != "local" || dig(doc, "mcp", "other", "type") != "remote" || dig(doc, "theme") != "dark" {
		t.Errorf("merged document = %v", doc)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config), "opencode.json")); !os.IsNotExist(err) {
		t.Error("a second configuration file was created beside the existing one")
	}
	mustRun(t, "--remove", "opencode")
	restored, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Errorf("removal did not restore the file:\n%s\nwant:\n%s", restored, original)
	}
}

func TestCodexProjectConfigIsAppendedAndRestored(t *testing.T) {
	s := newSandbox(t)
	config := filepath.Join(s.project, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("model = \"o3\" # mine\n\n[mcp_servers.other]\ncommand = \"other\"\nargs = []")
	if err := os.WriteFile(config, original, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "codex", "--scope", "project")
	merged, _ := os.ReadFile(config)
	if !bytes.HasPrefix(merged, original) {
		t.Fatalf("existing TOML was rewritten:\n%s", merged)
	}
	if st := inspectTOMLServer(string(merged), "facet"); !st.parsed || st.command != s.exe {
		t.Fatalf("appended table not readable: %+v\n%s", st, merged)
	}
	if st := inspectTOMLServer(string(merged), "other"); !st.parsed || st.command != "other" {
		t.Fatalf("existing table damaged: %+v", st)
	}
	mustRun(t, "--remove", "codex", "--scope", "project")
	restored, _ := os.ReadFile(config)
	if !bytes.Equal(restored, original) {
		t.Fatalf("removal did not restore the file:\n%q\nwant:\n%q", restored, original)
	}
	if _, err := os.Stat(filepath.Join(s.project, ".agents")); !os.IsNotExist(err) {
		t.Error("the project skill directory created by facet wire survived removal")
	}
}

func TestInterruptedRunIsRecoverable(t *testing.T) {
	s := newSandbox(t)
	mustRun(t, "copilot")
	reg := loadRegistry(t, s.home)
	w := reg.Wirings[0]
	// Simulate an interrupted update: the record names a newer digest and
	// keeps the on-disk digest only as an accepted alternative.
	for i := range w.Files {
		w.Files[i].Alt = []string{w.Files[i].Digest}
		w.Files[i].Digest = "sha256:" + strings.Repeat("0", 64)
	}
	w.Pending = true
	if err := reg.Save(RegistryPath(s.home)); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "--status"); !strings.Contains(out, "did not finish") {
		t.Errorf("status does not report the interrupted run:\n%s", out)
	}
	mustRun(t, "copilot")
	reg = loadRegistry(t, s.home)
	if reg.Wirings[0].Pending {
		t.Fatal("rewiring did not complete the interrupted run")
	}
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") {
		t.Errorf("drift after recovery:\n%s", out)
	}
}

func TestApplyRefusesFilesThatAppearAfterPlanning(t *testing.T) {
	s := newSandbox(t)
	e, err := newEnv(testVersion, io.Discard, io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := &Registry{Schema: RegistrySchema}
	p := e.planInstall(reg, bundle.TargetOpenCode, bundle.ScopeUser, "")
	if len(p.problems) != 0 || len(p.files) == 0 || p.files[0].op != "create" {
		t.Fatalf("plan = %+v", p)
	}
	late := p.files[0].path
	if err := os.MkdirAll(filepath.Dir(late), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(late, []byte("written meanwhile"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.applyInstall(reg, p); err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatalf("apply = %v", err)
	}
	if data, _ := os.ReadFile(late); string(data) != "written meanwhile" {
		t.Fatal("a file that appeared after planning was overwritten")
	}
	if _, err := os.Stat(filepath.Join(s.home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("the MCP server was registered although applying failed")
	}
}

func TestRemoveWorksAfterTheProjectIsGone(t *testing.T) {
	s := newSandbox(t)
	mustRun(t, "opencode", "--scope", "project")
	// Not t.Chdir: it holds the previous directory open until cleanup, and
	// Windows cannot delete an open directory. The sandbox's t.Chdir still
	// restores the original working directory.
	if err := os.Chdir(s.root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.project); err != nil {
		t.Fatal(err)
	}
	if _, _, code := run(t, "opencode", "--project", s.project); code != 1 {
		t.Fatal("wiring a missing project directory succeeded")
	}
	mustRun(t, "--remove", "opencode", "--project", s.project)
	if reg := loadRegistry(t, s.home); len(reg.Wirings) != 0 {
		t.Fatalf("registry still records %+v", reg.Wirings)
	}
}

func TestArgumentHandling(t *testing.T) {
	s := newSandbox(t)
	stdout, _, code := run(t, "--help")
	if code != 0 || !strings.Contains(stdout, "facet wire --status") {
		t.Fatalf("help exit %d: %s", code, stdout)
	}
	for name, args := range map[string][]string{
		"no CLI":              {},
		"unknown CLI":         {"app"},
		"unknown option":      {"claude", "--bogus"},
		"bad scope":           {"claude", "--scope", "global"},
		"project with user":   {"claude", "--scope", "user", "--project", s.project},
		"status with others":  {"--status", "claude"},
		"status with remove":  {"--status", "--remove"},
		"scope without value": {"claude", "--scope"},
		"remove without CLI":  {"--remove"},
	} {
		if _, errOut, code := run(t, args...); code != 2 || !strings.Contains(errOut, "Usage:") {
			t.Errorf("%s: exit %d: %s", name, code, errOut)
		}
	}
	if entries, _ := os.ReadDir(s.home); len(entries) != 0 {
		t.Errorf("argument errors wrote to the home directory: %v", entries)
	}
	req, _, err := parseArgs([]string{"--dry-run", "opencode,claude", "--project=" + s.project})
	if err != nil || !req.dryRun || req.scope != bundle.ScopeProject || len(req.targets) != 2 {
		t.Fatalf("parseArgs = %+v, %v", req, err)
	}
	for name, args := range map[string][]string{
		"refresh with CLI":     {"--refresh", "claude"},
		"refresh with scope":   {"--refresh", "--scope", "user"},
		"refresh with remove":  {"--refresh", "--remove", "claude"},
		"refresh with exe":     {"--refresh", "--exe", s.exe},
		"status with refresh":  {"--status", "--refresh"},
		"status with exe":      {"--status", "--exe", s.exe},
		"remove with exe":      {"--remove", "claude", "--exe", s.exe},
		"exe without value":    {"claude", "--exe"},
		"refresh with a value": {"--refresh=yes"},
	} {
		if _, errOut, code := run(t, args...); code != 2 || !strings.Contains(errOut, "Usage:") {
			t.Errorf("%s: exit %d: %s", name, code, errOut)
		}
	}
	req, _, err = parseArgs([]string{"--refresh", "--dry-run"})
	if err != nil || req.mode != modeRefresh || !req.dryRun {
		t.Fatalf("parseArgs(--refresh --dry-run) = %+v, %v", req, err)
	}
}

// FACET_HOME moves the wiring record, so a second Toolkit (the Facet App's)
// keeps its own wirings apart from the user-wide one. The registered command
// is still the facet that runs facet wire.
func TestFacetHomeMovesTheRegistry(t *testing.T) {
	s := newSandbox(t)
	moved := filepath.Join(s.root, "app", "facet-home")
	t.Setenv("FACET_HOME", moved)
	mustRun(t, "copilot")

	if _, err := os.Stat(filepath.Join(moved, "wiring.json")); err != nil {
		t.Fatalf("the wiring was not recorded under FACET_HOME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".facet", "wiring.json")); !os.IsNotExist(err) {
		t.Fatal("the user-wide wiring record was written although FACET_HOME is set")
	}
	if got := dig(readJSON(t, filepath.Join(s.home, ".copilot", "mcp-config.json")), "mcpServers", "facet", "command"); got != s.exe {
		t.Fatalf("registered %v, want the running facet %s", got, s.exe)
	}
}

// --exe registers a chosen executable; it is the wiring's own, so status does
// not call it drift and --refresh keeps it.
func TestExplicitExecutableIsKeptAndIsNotDrift(t *testing.T) {
	s := newSandbox(t)
	chosen := filepath.Join(s.root, "app", "bin", filepath.Base(s.exe))
	if err := os.MkdirAll(filepath.Dir(chosen), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chosen, []byte("app facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run(t, "copilot", "--exe", filepath.Join(s.root, "missing", "facet")); code != 1 || !strings.Contains(errOut, "--exe") {
		t.Fatalf("a missing --exe: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(s.home, ".copilot")); !os.IsNotExist(err) {
		t.Fatal("a missing --exe still wired")
	}

	if _, errOut, code := runVersion(t, "1.9.0", "copilot", "--exe", chosen); code != 0 {
		t.Fatalf("wire --exe: exit %d: %s", code, errOut)
	}
	config := filepath.Join(s.home, ".copilot", "mcp-config.json")
	if got := dig(readJSON(t, config), "mcpServers", "facet", "command"); got != chosen {
		t.Fatalf("registered %v, want %s", got, chosen)
	}
	reg := loadRegistry(t, s.home)
	if len(reg.Wirings) != 1 || !reg.Wirings[0].ExplicitExecutable {
		t.Fatalf("registry = %+v", reg.Wirings)
	}
	if out, _, _ := runVersion(t, "1.9.0", "--status"); strings.Contains(out, "drift") {
		t.Errorf("an explicit executable is reported as drift:\n%s", out)
	}

	out := mustRun(t, "--refresh")
	if !strings.Contains(out, "refreshed to facet v"+testVersion) {
		t.Fatalf("refresh output:\n%s", out)
	}
	if got := dig(readJSON(t, config), "mcpServers", "facet", "command"); got != chosen {
		t.Fatalf("refresh replaced the explicit executable with %v", got)
	}
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") {
		t.Errorf("status after refresh:\n%s", out)
	}
	// Removing the chosen executable is drift, and the fix keeps --exe.
	if err := os.Remove(chosen); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "--status"); !strings.Contains(out, "no longer exists") || !strings.Contains(out, "--exe") {
		t.Errorf("status with the chosen executable gone:\n%s", out)
	}
}

// --refresh brings every recorded wiring to the running version, leaves what
// a person changed alone, and keeps going past a wiring it cannot refresh.
func TestRefreshBringsEveryWiringToThisVersion(t *testing.T) {
	s := newSandbox(t, "claude")
	for _, cli := range []string{"copilot", "opencode", "claude"} {
		if _, errOut, code := runVersion(t, "1.9.0", cli); code != 0 {
			t.Fatalf("wire %s: exit %d: %s", cli, code, errOut)
		}
	}
	if out := mustRun(t, "--status"); !strings.Contains(out, "wired by facet v1.9.0; this is facet v"+testVersion) {
		t.Fatalf("status does not report the older version:\n%s", out)
	}

	// A person edited one of opencode's skills: that wiring must not be
	// touched, and the others still refresh.
	edited := filepath.Join(s.home, ".config", "opencode", "skills", "facet", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(s.calls(t))
	out, errOut, code := run(t, "--refresh")
	if code != 1 || !strings.Contains(errOut, "opencode (user): cannot refresh") {
		t.Fatalf("refresh with an edited file: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if data, _ := os.ReadFile(edited); string(data) != "my notes" {
		t.Fatal("refresh overwrote an edited file")
	}
	// The registration still runs the same executable, so no CLI is called.
	if calls := s.calls(t); len(calls) != before {
		t.Errorf("refresh re-registered an unchanged MCP server: %+v", calls[before:])
	}
	for _, w := range loadRegistry(t, s.home).Wirings {
		want := testVersion
		if w.CLI == "opencode" {
			want = "1.9.0"
		}
		if w.FacetVersion != want || w.Pending {
			t.Errorf("%s: facet_version %s pending %v, want %s", w.CLI, w.FacetVersion, w.Pending, want)
		}
	}

	if err := os.Remove(edited); err != nil {
		t.Fatal(err)
	}
	out = mustRun(t, "--refresh")
	if !strings.Contains(out, "opencode (user): refreshed") || !strings.Contains(out, "copilot (user): up to date") {
		t.Fatalf("second refresh:\n%s", out)
	}
	if status := mustRun(t, "--status"); strings.Contains(status, "drift") {
		t.Errorf("drift after refresh:\n%s", status)
	}
	expectFiles(t, filepath.Join(s.home, ".config", "opencode"), bundle.TargetOpenCode)
}

func TestRefreshDryRunAndEmptyRegistry(t *testing.T) {
	s := newSandbox(t)
	if out := mustRun(t, "--refresh"); !strings.Contains(out, "nothing to refresh") {
		t.Fatalf("empty refresh:\n%s", out)
	}
	if _, errOut, code := runVersion(t, "1.9.0", "copilot"); code != 0 {
		t.Fatal(errOut)
	}
	out := mustRun(t, "--refresh", "--dry-run")
	if !strings.Contains(out, "Dry run") {
		t.Fatalf("dry run output:\n%s", out)
	}
	if w := loadRegistry(t, s.home).Wirings[0]; w.FacetVersion != "1.9.0" {
		t.Fatalf("a dry run changed the record: %+v", w)
	}
}

func TestRefreshReportsAProjectThatIsGone(t *testing.T) {
	s := newSandbox(t)
	mustRun(t, "copilot", "--scope", "project")
	if err := os.Chdir(s.root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.project); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := run(t, "--refresh")
	if code != 1 || !strings.Contains(errOut, "project directory is gone") || !strings.Contains(errOut, "facet wire --remove copilot --scope project --project") {
		t.Fatalf("refresh with a missing project: exit %d: %s", code, errOut)
	}
}

// Rules the person wrote stay theirs: facet wire adds only what is missing,
// records only what it added, and --remove restores their files exactly.
func TestAskRulesKeepWhatThePersonWrote(t *testing.T) {
	s := newSandbox(t, "claude")
	claudeSettings := filepath.Join(s.home, ".claude", "settings.json")
	opencodeConfig := filepath.Join(s.home, ".config", "opencode", "opencode.json")
	claudeOriginal := []byte("{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(git *)\"\n    ],\n    \"ask\": [\n      \"mcp__facet__sora_video\"\n    ]\n  },\n  \"theme\": \"dark\"\n}\n")
	opencodeOriginal := []byte("{\n  // mine\n  \"permission\": {\n    \"facet_*\": \"allow\",\n    \"facet_kling_video\": \"deny\"\n  }\n}\n")
	for path, content := range map[string][]byte{claudeSettings: claudeOriginal, opencodeConfig: opencodeOriginal} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := mustRun(t, "claude,opencode")
	if !strings.Contains(out, `permission.facet_kling_video in `) || !strings.Contains(out, `is already "deny"`) {
		t.Errorf("the rule the person set is not reported:\n%s", out)
	}
	reg := loadRegistry(t, s.home)
	for _, w := range reg.Wirings {
		if w.Rules == nil {
			t.Fatalf("%s: no rules recorded", w.CLI)
		}
		for _, rule := range w.Rules.Added {
			if rule == "mcp__facet__sora_video" || rule == "facet_kling_video" {
				t.Errorf("%s: recorded the person's own rule %s as facet wire's", w.CLI, rule)
			}
		}
		if w.Rules.CreatedFile || w.Rules.CreatedParent {
			t.Errorf("%s: claims to have created what existed: %+v", w.CLI, w.Rules)
		}
	}
	data, _ := os.ReadFile(opencodeConfig)
	if !strings.Contains(string(data), "// mine") || !strings.Contains(string(data), `"facet_kling_video": "deny"`) {
		t.Errorf("the person's OpenCode settings changed:\n%s", data)
	}
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") {
		t.Errorf("drift right after wiring:\n%s", out)
	}

	mustRun(t, "--remove", "claude,opencode")
	if data, _ := os.ReadFile(claudeSettings); !bytes.Equal(data, claudeOriginal) {
		t.Errorf("claude settings not restored exactly:\n%s", data)
	}
	if data, _ := os.ReadFile(opencodeConfig); !bytes.Equal(data, opencodeOriginal) {
		t.Errorf("opencode config not restored exactly:\n%s", data)
	}
}

// A missing ask rule is drift, and rewiring puts it back.
func TestMissingAskRuleIsDrift(t *testing.T) {
	s := newSandbox(t, "claude")
	mustRun(t, "claude")
	settings := filepath.Join(s.home, ".claude", "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "\"mcp__facet__sora_video\",", "", 1)
	if edited == string(data) {
		t.Fatalf("fixture rule not found:\n%s", data)
	}
	if err := os.WriteFile(settings, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "--status"); !strings.Contains(out, "ask rules for paid tools are missing") {
		t.Fatalf("status does not report the missing rule:\n%s", out)
	}
	mustRun(t, "claude")
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") {
		t.Errorf("drift after rewiring:\n%s", out)
	}
	expectClaude := readJSON(t, settings)
	found := false
	for _, item := range dig(expectClaude, "permissions", "ask").([]any) {
		found = found || item == "mcp__facet__sora_video"
	}
	if !found {
		t.Error("rewiring did not put the rule back")
	}
}

// OpenCode's permission set to one action for every tool takes no rules; that
// is noted once, not reported as drift forever.
func TestOpenCodeBlanketPermissionIsANote(t *testing.T) {
	s := newSandbox(t)
	config := filepath.Join(s.home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("{\"permission\": \"allow\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, "opencode")
	if !strings.Contains(out, "applies one action to every tool") {
		t.Errorf("the blanket permission is not reported:\n%s", out)
	}
	if out := mustRun(t, "--status"); strings.Contains(out, "drift") || !strings.Contains(out, "not added") {
		t.Errorf("status:\n%s", out)
	}
}

// A user wiring an earlier facet wire made with `codex mcp add` keeps that
// registration rather than being refused for its own table.
func TestEarlierCodexCommandRegistrationIsKept(t *testing.T) {
	s := newSandbox(t, "codex")
	config := filepath.Join(s.home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	table := fmt.Sprintf("[mcp_servers.facet]\ncommand = %s\nargs = [\"mcp\"]\n", tomlString(s.exe))
	if err := os.WriteFile(config, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := (&env{home: s.home, getenv: os.Getenv, exe: s.exe}).codexSpec()
	reg := &Registry{Schema: RegistrySchema, Wirings: []*Wiring{{
		CLI: "codex", Scope: "user", Root: filepath.Join(s.home, ".codex"), FacetVersion: testVersion, Executable: s.exe,
		MCP: &MCPRecord{Method: MethodCommand, Name: "facet", Command: s.exe, Args: []string{"mcp"}, File: config,
			CLI: "codex", AddArgv: spec.add, RemoveArgv: spec.remove, Dir: s.home},
	}}}
	if err := reg.Save(RegistryPath(s.home)); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "codex")
	if w := loadRegistry(t, s.home).Wirings[0]; w.MCP.Method != MethodCommand {
		t.Fatalf("the earlier registration was replaced: %+v", w.MCP)
	}
	if data, _ := os.ReadFile(config); string(data) != table {
		t.Errorf("the earlier registration changed:\n%s", data)
	}
	if calls := s.calls(t); len(calls) != 0 {
		t.Errorf("rewiring an unchanged registration ran codex: %+v", calls)
	}
}

// Facet 2.0.0 wrote its Codex block without env_vars. Rewiring (as the
// installers' facet wire --refresh does after an update) replaces that block
// in place, and --remove still restores the file exactly.
func TestCodexBlockWithoutEnvVarsIsUpdatedInPlace(t *testing.T) {
	s := newSandbox(t)
	config := filepath.Join(s.home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("model = \"o3\"\n")
	if err := os.WriteFile(config, original, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "codex")
	reg := loadRegistry(t, s.home)
	rec := reg.Wirings[0].MCP
	current, _ := os.ReadFile(config)
	envLine := regexp.MustCompile(`\nenv_vars = \[[^\n]*\]`)
	if !envLine.MatchString(rec.Block) {
		t.Fatalf("the recorded block has no env_vars line:\n%s", rec.Block)
	}
	old := envLine.ReplaceAllString(rec.Block, "")
	if err := os.WriteFile(config, []byte(strings.Replace(string(current), rec.Block, old, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Block = old
	if err := reg.Save(RegistryPath(s.home)); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "codex")
	updated, _ := os.ReadFile(config)
	if !bytes.HasPrefix(updated, original) || strings.Count(string(updated), "[mcp_servers.facet]") != 1 || !envLine.Match(updated) {
		t.Fatalf("the earlier block was not replaced in place:\n%s", updated)
	}
	mustRun(t, "--remove", "codex")
	if restored, _ := os.ReadFile(config); !bytes.Equal(restored, original) {
		t.Fatalf("removal did not restore the file:\n%q\nwant:\n%q", restored, original)
	}
}
