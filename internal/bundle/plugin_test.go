package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kayiik/facet/internal/toolbox"
)

var pluginTargets = []Target{TargetClaude, TargetCodex, TargetCopilot}

func readJSONFile(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func stringList(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

// A plugin is the same guidance as every other layout plus the CLI's plugin
// manifest and an MCP registration that runs facet from PATH.
func TestPluginLayoutsCarryGuidanceManifestAndServer(t *testing.T) {
	for _, target := range pluginTargets {
		dir := filepath.Join(t.TempDir(), string(target))
		m := build(t, target, ScopePlugin, dir)
		if m.Scope != ScopePlugin || m.InstallBase != "plugin" || m.InstallRoot != "" {
			t.Errorf("%s: manifest identity = %+v", target, m)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "skills", "facet", "SKILL.md")); err != nil || !bytes.Equal(got, canonical(t, "skills/facet/SKILL.md")) {
			t.Errorf("%s: the core skill is not at the plugin root: %v", target, err)
		}
		if _, err := Verify(dir, Expect{Target: target, Scope: ScopePlugin, Version: testVersion, Tools: testTools, Current: true}); err != nil {
			t.Errorf("%s: a fresh plugin failed verification: %v", target, err)
		}

		manifest := readJSONFile(t, filepath.Join(dir, filepath.FromSlash(PluginManifestPath(target))))
		if manifest["name"] != PluginName || manifest["version"] != testVersion || manifest["license"] != PluginLicense ||
			manifest["repository"] != PluginRepository || manifest["homepage"] != PluginWebsite {
			t.Errorf("%s: plugin manifest = %v", target, manifest)
		}
		if desc, _ := manifest["description"].(string); !strings.Contains(desc, "Facet Toolkit") || !strings.Contains(desc, PluginWebsite) {
			t.Errorf("%s: the description does not say the Toolkit is needed: %q", target, desc)
		}

		servers, _ := readJSONFile(t, filepath.Join(dir, ".mcp.json"))["mcpServers"].(map[string]any)
		server, _ := servers[MCPServerName].(map[string]any)
		if len(servers) != 1 || server["command"] != PluginCommand || !reflect.DeepEqual(stringList(server["args"]), MCPServerArgs()) {
			t.Fatalf("%s: .mcp.json servers = %v", target, servers)
		}
		switch target {
		case TargetClaude:
			// Claude Code starts plugin servers in the project and offers it
			// as the MCP root; a cwd would only move them.
			if _, ok := server["cwd"]; ok || len(server) != 2 {
				t.Errorf("claude: server entry = %v", server)
			}
		case TargetCopilot:
			// Copilot CLI would start the server in the plugin directory.
			if server["cwd"] != "." || server["type"] != "local" || !reflect.DeepEqual(stringList(server["tools"]), []string{"*"}) {
				t.Errorf("copilot: server entry = %v", server)
			}
			if msg, _ := manifest["postInstallMessage"].(string); !strings.Contains(msg, PluginWebsite) {
				t.Errorf("copilot: the post-install message does not point at the Toolkit: %q", msg)
			}
			if !reflect.DeepEqual(stringList(manifest["skills"]), []string{"skills/"}) || !reflect.DeepEqual(stringList(manifest["agents"]), []string{"agents/"}) {
				t.Errorf("copilot: component paths = %v %v", manifest["skills"], manifest["agents"])
			}
		case TargetCodex:
			if manifest["skills"] != "./skills/" || manifest["mcpServers"] != "./.mcp.json" {
				t.Errorf("codex: component paths = %v %v", manifest["skills"], manifest["mcpServers"])
			}
			if ui, _ := manifest["interface"].(map[string]any); ui["displayName"] != "Facet" || len(stringList(ui["defaultPrompt"])) > 3 {
				t.Errorf("codex: interface = %v", ui)
			}
			if !reflect.DeepEqual(stringList(server["env_vars"]), toolbox.EnvVars()) {
				t.Errorf("codex: env_vars = %v, want %v", server["env_vars"], toolbox.EnvVars())
			}
			if server["tool_timeout_sec"] != float64(CodexToolTimeoutSec) {
				t.Errorf("codex: tool_timeout_sec = %v", server["tool_timeout_sec"])
			}
			tools, _ := server["tools"].(map[string]any)
			var asked []string
			for tool, cfg := range tools {
				if mode, _ := cfg.(map[string]any)["approval_mode"].(string); mode == "prompt" {
					asked = append(asked, tool)
				}
			}
			sort.Strings(asked)
			if paid := toolbox.PaidTools(); len(paid) == 0 || !reflect.DeepEqual(asked, paid) || len(tools) != len(paid) {
				t.Errorf("codex: asks before %v, want every paid tool %v", asked, paid)
			}
		}
		_, err := os.Stat(filepath.Join(dir, "hooks", "hooks.json"))
		if hasHooks := err == nil; hasHooks != (target == TargetClaude) {
			t.Errorf("%s: hooks/hooks.json present = %v; only the Claude Code plugin registers a hook", target, hasHooks)
		}
	}
}

// A Claude Code plugin cannot add permission rules, so its PreToolUse hook
// makes Claude Code ask before every paid Facet tool, through MCP under the
// plugin's server name (and facet wire's) and through either shell.
func TestClaudePluginHookAsksBeforeEveryPaidTool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	build(t, TargetClaude, ScopePlugin, dir)
	doc := readJSONFile(t, filepath.Join(dir, "hooks", "hooks.json"))
	events, _ := doc["hooks"].(map[string]any)
	matchers, _ := events["PreToolUse"].([]any)
	if len(events) != 1 || len(matchers) != 1 {
		t.Fatalf("hooks = %v", doc["hooks"])
	}
	entry, _ := matchers[0].(map[string]any)
	hooks, _ := entry["hooks"].([]any)
	hook, _ := hooks[0].(map[string]any)
	if len(hooks) != 1 || hook["type"] != "command" || hook["command"] != "facet hook claude" || hook["timeout"] != float64(claudeHookTimeout) {
		t.Fatalf("hook = %v", hooks)
	}
	matcher, _ := entry["matcher"].(string)
	// Only letters, digits, `_` and `|`: Claude Code reads it as a list of
	// exact tool names, not as a regular expression.
	if !regexp.MustCompile(`^[A-Za-z0-9_|]+$`).MatchString(matcher) {
		t.Fatalf("matcher %q is not a list of exact names", matcher)
	}
	names := map[string]bool{}
	for _, name := range strings.Split(matcher, "|") {
		names[name] = true
	}
	want := []string{"Bash", "PowerShell"}
	for _, tool := range toolbox.PaidTools() {
		want = append(want, "mcp__plugin_facet_facet__"+tool, "mcp__facet__"+tool)
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("the hook does not run for %s", name)
		}
	}
	if len(names) != len(want) {
		t.Errorf("the hook runs for %d tools, want %d: %s", len(names), len(want), matcher)
	}
}

func TestPluginScopeIsOnlyForCLIsWithAPluginFormat(t *testing.T) {
	for _, target := range Targets() {
		want := target == TargetClaude || target == TargetCodex || target == TargetCopilot
		if SupportsScope(target, ScopePlugin) != want {
			t.Errorf("SupportsScope(%s, plugin) = %v", target, !want)
		}
		if _, _, err := Plan(Options{Target: target, Scope: ScopePlugin, Version: testVersion}); (err == nil) != want {
			t.Errorf("Plan(%s, plugin) error = %v", target, err)
		}
	}
	// facet wire installs at user or project scope; only the bundle has a
	// plugin layout.
	if _, err := ParseScope("plugin"); err == nil {
		t.Error("ParseScope accepted plugin, which facet wire would then try to install")
	}
	if s, err := ParseBundleScope("Plugin"); err != nil || s != ScopePlugin {
		t.Errorf("ParseBundleScope(Plugin) = %q, %v", s, err)
	}
	if _, err := ParseBundleScope("global"); err == nil {
		t.Error("ParseBundleScope accepted an unknown scope")
	}

	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := CLI([]string{"--target", "all", "--scope", "plugin", "--out", out}, &stdout, &stderr, testVersion); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var built []string
	for _, e := range entries {
		built = append(built, e.Name())
	}
	if strings.Join(built, ",") != "claude,codex,copilot" {
		t.Errorf("all at plugin scope built %v", built)
	}
	stderr.Reset()
	if code := CLI([]string{"--target", "opencode", "--scope", "plugin", "--out", out}, io.Discard, &stderr, testVersion); code != 2 || !strings.Contains(stderr.String(), "no plugin layout") {
		t.Errorf("opencode at plugin scope: exit %d, %s", code, stderr.String())
	}
}

// The plugins in the repository are what the marketplaces serve. They must
// be exactly what this facet projects at the release version in
// package.json, so a plugin never carries guidance the Toolkit of the same
// release does not.
//
// To regenerate them after changing guidance, the plugin layout or the
// version: FACET_UPDATE_PLUGINS=1 go test ./internal/bundle -run TestCommittedPluginsMatchThisFacet
func TestCommittedPluginsMatchThisFacet(t *testing.T) {
	repo := filepath.Join("..", "..")
	var pkg struct {
		Version string `json:"version"`
		License string `json:"license"`
	}
	data, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Version == "" {
		t.Fatalf("package.json: version %q, %v", pkg.Version, err)
	}
	if pkg.License != PluginLicense {
		t.Errorf("plugins declare license %s, package.json %s", PluginLicense, pkg.License)
	}
	tools := toolbox.Names()
	update := os.Getenv("FACET_UPDATE_PLUGINS") == "1"
	for _, target := range pluginTargets {
		committed := filepath.Join(repo, "plugins", string(target))
		if update {
			if _, err := Build(Options{Target: target, Scope: ScopePlugin, Version: pkg.Version, Tools: tools}, committed); err != nil {
				t.Fatalf("regenerating %s: %v", committed, err)
			}
		}
		want := filepath.Join(t.TempDir(), string(target))
		if _, err := Build(Options{Target: target, Scope: ScopePlugin, Version: pkg.Version, Tools: tools}, want); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(committed, Expect{Target: target, Scope: ScopePlugin, Version: pkg.Version, Tools: tools, Current: true}); err != nil {
			t.Errorf("plugins/%s: %v; regenerate with FACET_UPDATE_PLUGINS=1 go test ./internal/bundle -run TestCommittedPluginsMatchThisFacet", target, err)
			continue
		}
		got, wantFiles := snapshot(t, committed), snapshot(t, want)
		if !reflect.DeepEqual(got, wantFiles) {
			for name := range wantFiles {
				if got[name] != wantFiles[name] {
					t.Errorf("plugins/%s/%s differs from this facet's projection (a CRLF checkout? see .gitattributes)", target, name)
				}
			}
			for name := range got {
				if _, ok := wantFiles[name]; !ok {
					t.Errorf("plugins/%s/%s is not part of the plugin", target, name)
				}
			}
		}
	}
	// No other directory may pose as a plugin.
	entries, err := os.ReadDir(filepath.Join(repo, "plugins"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !SupportsScope(Target(e.Name()), ScopePlugin) {
			t.Errorf("plugins/%s is not a Facet plugin", e.Name())
		}
	}
}

// The marketplaces in the repository serve the plugins of one published
// release: each entry pins the same release tag, which moves only after that
// release is published, so the plugins and the Toolkit move together and a
// marketplace never points at an unpublished release. Between releases
// package.json names the next one, so the pin is never newer than it.
func TestMarketplacesServeThePinnedRelease(t *testing.T) {
	repo := filepath.Join("..", "..")
	claude := readJSONFile(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"))
	meta, _ := claude["metadata"].(map[string]any)
	pinned, _ := meta["version"].(string)
	next, _ := readJSONFile(t, filepath.Join(repo, "package.json"))["version"].(string)
	next, _, _ = strings.Cut(next, "-") // a prerelease of the next release
	if !releaseVersion.MatchString(pinned) || !releaseVersion.MatchString(next) || !notNewer(pinned, next) {
		t.Fatalf("the marketplaces pin release %q; package.json names %q, and the pin must not be newer", pinned, next)
	}
	tag := "v" + pinned
	gitURL := PluginRepository + ".git"
	repoName := strings.TrimPrefix(PluginRepository, "https://github.com/")

	type entry struct {
		target Target
		file   string
		source map[string]string
	}
	for _, want := range []entry{
		{TargetClaude, ".claude-plugin/marketplace.json", map[string]string{"source": "git-subdir", "url": gitURL, "path": "plugins/claude", "ref": tag}},
		{TargetCopilot, ".github/plugin/marketplace.json", map[string]string{"source": "github", "repo": repoName, "path": "plugins/copilot", "ref": tag}},
		{TargetCodex, ".agents/plugins/marketplace.json", map[string]string{"source": "git-subdir", "url": gitURL, "path": "plugins/codex", "ref": tag}},
	} {
		doc := readJSONFile(t, filepath.Join(repo, filepath.FromSlash(want.file)))
		plugins, _ := doc["plugins"].([]any)
		if doc["name"] != PluginName || len(plugins) != 1 {
			t.Errorf("%s: marketplace %v lists %d plugins", want.file, doc["name"], len(plugins))
			continue
		}
		plugin, _ := plugins[0].(map[string]any)
		source, _ := plugin["source"].(map[string]any)
		got := map[string]string{}
		for k, v := range source {
			got[k], _ = v.(string)
		}
		if plugin["name"] != PluginName || !reflect.DeepEqual(got, want.source) {
			t.Errorf("%s: plugin %v source %v, want %v", want.file, plugin["name"], source, want.source)
		}
		if v, ok := plugin["version"]; ok && v != pinned {
			t.Errorf("%s: plugin version %v, want the pinned %s", want.file, v, pinned)
		}
		if meta, ok := doc["metadata"].(map[string]any); ok {
			if desc, _ := meta["description"].(string); desc == "" || meta["version"] != pinned {
				t.Errorf("%s: metadata %v", want.file, meta)
			}
		} else if want.target != TargetCodex {
			// claude plugin validate --strict wants a marketplace description.
			t.Errorf("%s has no metadata", want.file)
		}
		if want.target == TargetCodex {
			policy, _ := plugin["policy"].(map[string]any)
			if category, _ := plugin["category"].(string); policy["installation"] != "AVAILABLE" || policy["authentication"] != "ON_INSTALL" || category == "" {
				t.Errorf("%s: Codex needs policy and category: %v", want.file, plugin)
			}
		}
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(want.source["path"]), filepath.FromSlash(PluginManifestPath(want.target)))); err != nil {
			t.Errorf("%s: %s holds no %s plugin: %v", want.file, want.source["path"], want.target, err)
		}
	}
}

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// notNewer reports whether release version a is not newer than b; both are
// MAJOR.MINOR.PATCH.
func notNewer(a, b string) bool {
	x, y := releaseVersion.FindStringSubmatch(a), releaseVersion.FindStringSubmatch(b)
	for i := 1; i < 4; i++ {
		m, _ := strconv.Atoi(x[i])
		n, _ := strconv.Atoi(y[i])
		if m != n {
			return m < n
		}
	}
	return true
}
