package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compaConfig is a Compa config.json as onboarding writes it, reduced to
// the parts wiring touches: strict JSON, two-space indent, the default
// approval rules written out, MCP off with no servers.
func compaConfig(t *testing.T, workspace, version string) string {
	t.Helper()
	ws, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{
  "agents": {
    "defaults": {
      "workspace": %s,
      "restrict_to_workspace": true
    }
  },
  "tools": {
    "approval": {
      "default": "allow",
      "rules": [
        {
          "source": "module:*",
          "hints": [
            "cost_unknown",
            "network",
            "external_writes"
          ],
          "action": "ask"
        },
        {
          "tool": "install_skill",
          "action": "ask"
        }
      ]
    },
    "mcp": {
      "enabled": false,
      "discovery": {
        "enabled": false,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true,
        "use_regex": false
      },
      "max_inline_text_chars": 16384
    }
  },
  "build_info": {
    "version": %q,
    "git_commit": "5cc8c906"
  }
}
`, ws, version)
}

type compaSandbox struct {
	*sandbox
	home, config, workspace string
}

func newCompaSandbox(t *testing.T, version string) *compaSandbox {
	t.Helper()
	s := newSandbox(t)
	t.Setenv("COMPA_HOME", "")
	t.Setenv("COMPA_CONFIG", "")
	t.Setenv("COMPA_AGENTS_DEFAULTS_WORKSPACE", "")
	c := &compaSandbox{sandbox: s, home: filepath.Join(s.home, ".compa")}
	c.config = filepath.Join(c.home, "config.json")
	c.workspace = filepath.Join(c.home, "workspace")
	if version != "" {
		if err := os.MkdirAll(c.workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.config, []byte(compaConfig(t, c.workspace, version)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func (c *compaSandbox) doc(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(c.config)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("config.json is no longer strict JSON:\n%s", data)
	}
	return readJSON(t, c.config)
}

func compaRuleList(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	list, _ := dig(doc, "tools", "approval", "rules").([]any)
	var out []map[string]any
	for _, item := range list {
		rule, _ := item.(map[string]any)
		out = append(out, rule)
	}
	return out
}

// Wiring Compa installs the skills in its workspace, registers the server as
// `compa-kernel mcp add --trusted` would, turns MCP on, raises the MCP call
// limit, and puts one ask rule per paid tool first; --remove restores the
// file byte for byte.
func TestWireCompaEditsItsConfigurationAndRemovesExactly(t *testing.T) {
	c := newCompaSandbox(t, "0.0.1")
	original, _ := os.ReadFile(c.config)

	out := mustRun(t, "compa")
	for _, want := range []string{"compa (user) -> " + c.workspace, "turn MCP on", "MCP call limit 600 s", "ask before each of 8 paid tools", "Restart Service", "/approve"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, name := range []string{"facet"} {
		if _, err := os.Stat(filepath.Join(c.workspace, "skills", name, "SKILL.md")); err != nil {
			t.Errorf("skill %s not in the workspace: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(c.home, "skills")); !os.IsNotExist(err) {
		t.Error("skills were written to the global folder, which Compa's agent cannot read")
	}

	doc := c.doc(t)
	server, _ := dig(doc, "tools", "mcp", "servers", "facet").(map[string]any)
	if server["command"] != c.exe || server["type"] != "stdio" || server["trusted"] != true || server["enabled"] != true {
		t.Fatalf("facet server = %v", server)
	}
	if args, _ := server["args"].([]any); len(args) != 1 || args[0] != "mcp" {
		t.Fatalf("facet server args = %v", server["args"])
	}
	if dig(doc, "tools", "mcp", "enabled") != true || dig(doc, "tools", "mcp", "call_timeout_seconds") != 600.0 {
		t.Fatalf("tools.mcp = %v", dig(doc, "tools", "mcp"))
	}
	rules := compaRuleList(t, doc)
	if len(rules) != 10 {
		t.Fatalf("rules = %v", rules)
	}
	for i, tool := range paidTools() {
		if rules[i]["tool"] != "mcp_facet_"+tool || rules[i]["source"] != "mcp:facet" || rules[i]["action"] != "ask" || len(rules[i]) != 3 {
			t.Errorf("rule %d = %v, want an ask rule for %s first", i, rules[i], tool)
		}
	}
	if rules[8]["source"] != "module:*" || rules[9]["tool"] != "install_skill" {
		t.Errorf("the default rules moved: %v", rules[8:])
	}

	status := mustRun(t, "--status")
	if !strings.Contains(status, "compa (user): ok") || !strings.Contains(status, "ask:      ok") {
		t.Fatalf("status after wiring:\n%s", status)
	}
	// Running it again changes nothing.
	if out := mustRun(t, "compa"); !strings.Contains(out, "unchanged") {
		t.Errorf("a second run was not idempotent:\n%s", out)
	}

	mustRun(t, "--remove", "compa")
	if after, _ := os.ReadFile(c.config); !bytes.Equal(after, original) {
		t.Fatalf("--remove did not restore config.json:\n%s\nwant:\n%s", after, original)
	}
	if _, err := os.Stat(filepath.Join(c.workspace, "skills", "facet")); !os.IsNotExist(err) {
		t.Error("--remove left the facet skill")
	}
}

// Compa rewrites its whole file on every save. Drift is decided on values,
// and --remove still takes back exactly facet's entries.
func TestWireCompaSurvivesCompaRewritingItsFile(t *testing.T) {
	c := newCompaSandbox(t, "0.0.1")
	mustRun(t, "compa")
	var doc map[string]any
	if err := json.Unmarshal(mustRead(t, c.config), &doc); err != nil {
		t.Fatal(err)
	}
	rewritten, err := json.MarshalIndent(doc, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.config, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	if status := mustRun(t, "--status"); !strings.Contains(status, "compa (user): ok") {
		t.Fatalf("a rewrite by Compa was reported as drift:\n%s", status)
	}
	mustRun(t, "--remove", "compa")
	after := c.doc(t)
	if dig(after, "tools", "mcp", "servers") != nil || dig(after, "tools", "mcp", "enabled") != false || dig(after, "tools", "mcp", "call_timeout_seconds") != nil {
		t.Fatalf("tools.mcp after --remove = %v", dig(after, "tools", "mcp"))
	}
	if rules := compaRuleList(t, after); len(rules) != 2 {
		t.Fatalf("rules after --remove = %v", rules)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// What the person decided stays theirs: their rule for a paid tool, a rule
// that denies facet tools, their own MCP call limit, and MCP left on for
// their other servers.
func TestWireCompaKeepsThePersonsChoices(t *testing.T) {
	c := newCompaSandbox(t, "0.0.1")
	text := strings.Replace(compaConfig(t, c.workspace, "0.0.1"), `"rules": [`, `"rules": [
        {
          "tool": "mcp_facet_sora_video",
          "action": "allow"
        },
        {
          "tool": "mcp_facet_*_image",
          "action": "deny"
        },`, 1)
	text = strings.Replace(text, `"enabled": false,
      "discovery"`, `"enabled": true,
      "call_timeout_seconds": 120,
      "servers": {
        "other": {
          "enabled": true,
          "command": "other-server",
          "type": "stdio"
        }
      },
      "discovery"`, 1)
	if err := os.WriteFile(c.config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := run(t, "compa")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{`mcp_facet_sora_video ("allow")`, `mcp_facet_openai_image ("deny")`, "call_timeout_seconds", "is 120"} {
		if !strings.Contains(out+errOut, want) {
			t.Errorf("no warning containing %q:\n%s%s", want, out, errOut)
		}
	}
	doc := c.doc(t)
	if dig(doc, "tools", "mcp", "call_timeout_seconds") != 120.0 {
		t.Error("the person's MCP call limit was changed")
	}
	added := map[string]bool{}
	for _, rule := range compaRuleList(t, doc) {
		if rule["source"] == "mcp:facet" {
			added[rule["tool"].(string)] = true
		}
	}
	for _, kept := range []string{"mcp_facet_sora_video", "mcp_facet_openai_image", "mcp_facet_flux_image", "mcp_facet_gflow_image"} {
		if added[kept] {
			t.Errorf("an ask rule was added for %s although the person decided it", kept)
		}
	}
	if !added["mcp_facet_kling_video"] || !added["mcp_facet_elevenlabs_tts"] {
		t.Errorf("ask rules missing: %v", added)
	}

	mustRun(t, "--remove", "compa")
	after := c.doc(t)
	if dig(after, "tools", "mcp", "enabled") != true || dig(after, "tools", "mcp", "servers", "other") == nil || dig(after, "tools", "mcp", "servers", "facet") != nil {
		t.Fatalf("tools.mcp after --remove = %v", dig(after, "tools", "mcp"))
	}
	if !bytes.Equal(mustRead(t, c.config), []byte(text)) {
		t.Fatalf("--remove did not restore config.json:\n%s", mustRead(t, c.config))
	}
}

func TestWireCompaRefusesWhatItCannotOwn(t *testing.T) {
	t.Run("not set up", func(t *testing.T) {
		newCompaSandbox(t, "")
		out, errOut, code := run(t, "compa")
		if code == 0 || !strings.Contains(errOut, "Compa is not set up") || !strings.Contains(errOut, "Nothing was changed") {
			t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
		}
	})
	t.Run("project scope", func(t *testing.T) {
		newCompaSandbox(t, "0.0.1")
		if _, errOut, code := run(t, "compa", "--scope", "project"); code != 2 || !strings.Contains(errOut, "user scope only") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	})
	t.Run("another facet server", func(t *testing.T) {
		c := newCompaSandbox(t, "0.0.1")
		text := strings.Replace(compaConfig(t, c.workspace, "0.0.1"), `"max_inline_text_chars": 16384`, `"max_inline_text_chars": 16384,
      "servers": {
        "facet": {
          "enabled": true,
          "command": "somewhere-else",
          "type": "stdio"
        }
      }`, 1)
		if err := os.WriteFile(c.config, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, errOut, code := run(t, "compa"); code == 0 || !strings.Contains(errOut, "compa-kernel mcp remove facet") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if !bytes.Equal(mustRead(t, c.config), []byte(text)) {
			t.Fatal("a refused wiring changed config.json")
		}
	})
	t.Run("a version that is not a release number", func(t *testing.T) {
		newCompaSandbox(t, "dev")
		if _, errOut, code := run(t, "compa"); code == 0 || !strings.Contains(errOut, "supports Compa 0.0.1 or later") {
			t.Fatalf("exit %d: %s", code, errOut)
		}
	})
}

// "all" includes Compa only where it is set up; COMPA_HOME moves both the
// configuration and the default workspace.
func TestWireAllIncludesCompaWhereItIsSetUp(t *testing.T) {
	c := newCompaSandbox(t, "")
	c.installFake(t, "claude")
	out := mustRun(t, "all", "--dry-run")
	if strings.Contains(out, "compa (") {
		t.Fatalf("all wired Compa although it is not set up:\n%s", out)
	}

	moved := filepath.Join(c.root, "app-compa")
	workspace := filepath.Join(moved, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moved, "config.json"), []byte(compaConfig(t, workspace, "0.0.1")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPA_HOME", moved)
	out = mustRun(t, "all", "--dry-run")
	if !strings.Contains(out, "compa (user) -> "+workspace) || !strings.Contains(out, filepath.Join(moved, "config.json")) {
		t.Fatalf("all did not include the Compa set up under COMPA_HOME:\n%s", out)
	}
}

func TestCompaGlobAndVersions(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"mcp_facet_*", "mcp_facet_sora_video", true},
		{"mcp_facet_*_image", "mcp_facet_flux_image", true},
		{"mcp_facet_*_image", "mcp_facet_sora_video", false},
		{"mcp:?acet", "mcp:facet", true},
		{"MCP:facet", "mcp:facet", false},
		{"*", "", true},
		{"", "x", false},
	} {
		if got := compaGlob(c.pattern, c.s); got != c.want {
			t.Errorf("compaGlob(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
	for v, want := range map[string]bool{"2.1.1": true, "v2.1.0": true, "2.0.9": false, "3.0.0": true, "1.9": false, "2.1.0-rc1": true, "": false} {
		if got := versionAtLeast(v, 2, 1); got != want {
			t.Errorf("versionAtLeast(%q, 2, 1) = %v", v, got)
		}
	}
	for v, want := range map[string]bool{"0.0.1": true, "v0.0.1": true, "0.1.0": true, "2.1.1": true, "1.9": true, "0.0.1-dev": true, "dev": false, "": false} {
		if got := versionAtLeast(v, 0, 0); got != want {
			t.Errorf("versionAtLeast(%q, 0, 0) = %v", v, got)
		}
	}
}
