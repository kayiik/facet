package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	facet "github.com/kayiik/facet"
)

const testVersion = "2.0.0-test"

var testTools = []string{"media_probe", "output_review", "video_compose"}

func canonical(t *testing.T, name string) []byte {
	t.Helper()
	data, err := facet.Assets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

func filesByPath(t *testing.T, target Target) map[string]File {
	t.Helper()
	files, err := Files(target)
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	out := map[string]File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

// Every target receives the guide as the core skill, byte for byte, read from
// the embedded canonical assets, and nothing else as a skill.
func TestFilesProjectCanonicalGuidanceForEveryTarget(t *testing.T) {
	for _, target := range Targets() {
		files := filesByPath(t, target)
		core, ok := files["skills/facet/SKILL.md"]
		if !ok || !bytes.Equal(core.Content, canonical(t, "skills/facet/SKILL.md")) || core.Kind != KindSkill {
			t.Errorf("%s: core skill missing or drifted from the canonical asset", target)
		}
		for path, f := range files {
			if f.Kind == KindSkill && !strings.HasPrefix(path, "skills/facet/") {
				t.Errorf("%s: unexpected skill %s; pipelines and guidance are served by facet, not installed", target, path)
			}
			for _, banned := range []string{"package.json", "facet-pack.json", "AGENTS.md", "CLAUDE.md", "AGENT.md", "copilot-instructions.md"} {
				if filepath.Base(path) == banned {
					t.Errorf("%s: projection contains %s; Facet never writes instruction files", target, path)
				}
			}
		}
	}
}

// The role agents go to every target with a validated agent format; the
// producer agent, generated from the guide, only where it can be the main
// agent (never as a Claude Code subagent).
func TestAgentsAreProjectedOnlyInValidatedFormats(t *testing.T) {
	want := map[Target][]string{
		TargetClaude:   {"agents/facet-critic.md", "agents/facet-reference-analyst.md", "agents/facet-researcher.md"},
		TargetCopilot:  {"agents/facet-critic.agent.md", "agents/facet-reference-analyst.agent.md", "agents/facet-researcher.agent.md", "agents/facet.agent.md"},
		TargetOpenCode: {"agents/facet-critic.md", "agents/facet-reference-analyst.md", "agents/facet-researcher.md", "agents/facet.md"},
	}
	for _, target := range Targets() {
		var got []string
		for path, f := range filesByPath(t, target) {
			if f.Kind != KindAgent {
				continue
			}
			got = append(got, path)
			fields, ok := Frontmatter(f.Content)
			if !ok || fields["description"] == "" {
				t.Errorf("%s: %s lacks a description", target, path)
			}
			if target == TargetOpenCode {
				if _, present := fields["name"]; present {
					t.Errorf("opencode agent %s keeps `name`, which OpenCode would pass to the model provider", path)
				}
				mode := "subagent"
				if path == "agents/facet.md" {
					mode = "primary"
				}
				if fields["mode"] != mode {
					t.Errorf("opencode agent %s mode = %q, want %q", path, fields["mode"], mode)
				}
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want[target], ",") {
			t.Errorf("%s: agents = %v, want %v", target, got, want[target])
		}
		if HasAgents(target) != (len(want[target]) > 0) {
			t.Errorf("%s: HasAgents = %v", target, HasAgents(target))
		}
	}
}

// Skill loaders reject a skill whose name differs from its directory, and a
// loader that splits on "\n" would read a CRLF name as "facet\r".
func TestProjectedSkillsSatisfyEveryLoader(t *testing.T) {
	for _, target := range Targets() {
		for path, f := range filesByPath(t, target) {
			if bytes.Contains(f.Content, []byte("\r")) {
				t.Errorf("%s: %s contains CR bytes", target, path)
			}
			if strings.HasSuffix(path, "/SKILL.md") {
				fields, ok := Frontmatter(f.Content)
				dir := filepath.Base(filepath.Dir(filepath.FromSlash(path)))
				if !ok || fields["name"] != dir || fields["description"] == "" {
					t.Errorf("%s: %s frontmatter %v does not name its directory %q", target, path, fields, dir)
				}
			}
		}
	}
}

// The projected guide points to bundled guidance by its served path, never to a
// repository path that no CLI installs.
func TestProjectedGuidanceReferencesNoRepositoryPaths(t *testing.T) {
	for _, target := range Targets() {
		for path, f := range filesByPath(t, target) {
			if strings.Contains(string(f.Content), "packs/") {
				t.Errorf("%s: %s still references a removed pack path", target, path)
			}
		}
	}
}

func TestRootsFollowEachCLIsConventions(t *testing.T) {
	want := map[Scope]map[Target]string{
		ScopeUser:    {TargetClaude: ".claude", TargetCodex: ".codex", TargetCopilot: ".copilot", TargetOpenCode: ".config/opencode", TargetCompa: ".compa/workspace"},
		ScopeProject: {TargetClaude: ".claude", TargetCodex: ".agents", TargetCopilot: ".github", TargetOpenCode: ".opencode"},
	}
	for scope, roots := range want {
		for target, root := range roots {
			if got := DefaultRoot(target, scope); got != root {
				t.Errorf("DefaultRoot(%s, %s) = %q, want %q", target, scope, got, root)
			}
		}
	}
	base := filepath.Join(t.TempDir(), "home")
	env := map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(base, "claude-config"), "CODEX_HOME": filepath.Join(base, "codex-home"), "XDG_CONFIG_HOME": filepath.Join(base, "xdg"),
		"COMPA_HOME": filepath.Join(base, "compa-home")}
	getenv := func(k string) string { return env[k] }
	for target, want := range map[Target]string{
		TargetClaude:   env["CLAUDE_CONFIG_DIR"],
		TargetCodex:    env["CODEX_HOME"],
		TargetCopilot:  filepath.Join(base, ".copilot"),
		TargetOpenCode: filepath.Join(env["XDG_CONFIG_HOME"], "opencode"),
		TargetCompa:    filepath.Join(env["COMPA_HOME"], "workspace"),
	} {
		if got := Root(target, ScopeUser, base, getenv); got != want {
			t.Errorf("Root(%s, user) = %q, want %q", target, got, want)
		}
		if !SupportsScope(target, ScopeProject) {
			continue
		}
		// Project scope never follows user configuration overrides.
		if got, want := Root(target, ScopeProject, base, getenv), filepath.Join(base, filepath.FromSlash(DefaultRoot(target, ScopeProject))); got != want {
			t.Errorf("Root(%s, project) = %q, want %q", target, got, want)
		}
	}
}

func TestParseTargetsAndScopes(t *testing.T) {
	got, err := ParseTargets("opencode,claude", "claude")
	if err != nil || len(got) != 2 || got[0] != TargetClaude || got[1] != TargetOpenCode {
		t.Fatalf("ParseTargets = %v, %v", got, err)
	}
	if all, err := ParseTargets("all"); err != nil || len(all) != 5 || all[4] != TargetCompa {
		t.Fatalf("all = %v, %v", all, err)
	}
	for _, bad := range []string{"app", "studio", ""} {
		if _, err := ParseTargets(bad); err == nil {
			t.Errorf("target %q accepted", bad)
		}
	}
	if _, err := ParseScope("global"); err == nil {
		t.Error("unknown scope accepted")
	}
}

func build(t *testing.T, target Target, scope Scope, dir string) *Manifest {
	t.Helper()
	built, err := Build(Options{Target: target, Scope: scope, Version: testVersion, Tools: testTools}, dir)
	if err != nil {
		t.Fatalf("build %s/%s: %v", target, scope, err)
	}
	return built.Manifest
}

func TestBuildWritesNativeLayoutWithVerifiableManifest(t *testing.T) {
	for _, scope := range []Scope{ScopeUser, ScopeProject} {
		for _, target := range Targets() {
			dir := filepath.Join(t.TempDir(), string(target))
			if !SupportsScope(target, scope) {
				if _, err := Build(Options{Target: target, Scope: scope, Version: testVersion, Tools: testTools}, dir); err == nil {
					t.Errorf("%s was built at %s scope, which it does not support", target, scope)
				}
				continue
			}
			m := build(t, target, scope, dir)
			root := DefaultRoot(target, scope)
			if m.FacetVersion != testVersion || m.Target != target || m.Scope != scope || m.InstallRoot != root {
				t.Errorf("%s/%s manifest identity = %+v", target, scope, m)
			}
			if m.MCPServer.Name != "facet" || strings.Join(m.MCPServer.Args, " ") != "mcp" {
				t.Errorf("%s/%s MCP server = %+v", target, scope, m.MCPServer)
			}
			for _, e := range m.Files {
				if !strings.HasPrefix(e.Path, root+"/") || !strings.HasPrefix(e.Digest, "sha256:") {
					t.Errorf("%s/%s entry %+v", target, scope, e)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(root), "skills", "facet", "SKILL.md")); err != nil {
				t.Errorf("%s/%s core skill not at its native path: %v", target, scope, err)
			}
			if _, err := Verify(dir, Expect{Target: target, Scope: scope, Version: testVersion, Tools: testTools, Current: true}); err != nil {
				t.Errorf("%s/%s: a fresh bundle failed verification: %v", target, scope, err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil || decoded["facet_version"] != testVersion || decoded["files"] == nil {
				t.Errorf("manifest JSON lacks facet_version or files: %s", raw)
			}
		}
	}
}

func TestVerifyComparesTheRunningFacet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	build(t, TargetClaude, ScopeUser, dir)
	_, err := Verify(dir, Expect{Version: "2.1.0"})
	if err == nil || !strings.Contains(err.Error(), testVersion) || !strings.Contains(err.Error(), "2.1.0") {
		t.Fatalf("version mismatch not reported with both versions: %v", err)
	}
	if _, err := Verify(dir, Expect{Tools: []string{"media_probe"}}); err == nil {
		t.Fatal("a bundle naming tools the running binary lacks passed verification")
	}
	if _, err := Verify(dir, Expect{Target: TargetCodex}); err == nil {
		t.Fatal("target mismatch passed verification")
	}
	if _, err := Verify(dir, Expect{Scope: ScopeProject}); err == nil {
		t.Fatal("scope mismatch passed verification")
	}
}

// A verifier that cannot fail certifies whatever it is given.
func TestVerifyDetectsTamperingMissingAndUndeclaredFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opencode")
	m := build(t, TargetOpenCode, ScopeProject, dir)
	victim := filepath.Join(dir, filepath.FromSlash(m.Files[0].Path))
	orig, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), orig...)
	corrupt[0] ^= 1
	if err := os.WriteFile(victim, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, Expect{}); err == nil {
		t.Error("altered content passed verification")
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, Expect{}); err == nil {
		t.Error("a missing declared file passed verification")
	}
	if err := os.WriteFile(victim, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, Expect{}); err == nil || !strings.Contains(err.Error(), "extra.md") {
		t.Errorf("an undeclared file passed verification: %v", err)
	}
}

func TestBuildIsReproducible(t *testing.T) {
	a := build(t, TargetCodex, ScopeUser, filepath.Join(t.TempDir(), "codex"))
	b := build(t, TargetCodex, ScopeUser, filepath.Join(t.TempDir(), "codex"))
	if a.BundleDigest != b.BundleDigest || len(a.Files) != len(b.Files) {
		t.Fatalf("two builds differ: %s vs %s", a.BundleDigest, b.BundleDigest)
	}
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, name)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRebuildReplacesOnlyOwnedContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	build(t, TargetClaude, ScopeUser, dir)
	// An unmodified, owned bundle is replaced, including on a scope change.
	m := build(t, TargetClaude, ScopeProject, dir)
	if _, err := Verify(dir, Expect{Scope: ScopeProject}); err != nil {
		t.Fatalf("rebuilt bundle failed verification: %v", err)
	}

	// A modified owned file is refused and kept.
	victim := filepath.Join(dir, filepath.FromSlash(m.Files[0].Path))
	if err := os.WriteFile(victim, []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	_, err := Build(Options{Target: TargetClaude, Scope: ScopeProject, Version: testVersion, Tools: testTools}, dir)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified owned file not refused: %v", err)
	}
	if after := snapshot(t, dir); len(after) != len(before) || after[m.Files[0].Path] != "local edit" {
		t.Fatal("a refused build changed the directory")
	}
	// A missing owned file is no obstacle; an unowned file is.
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}

	// An unowned file is refused and kept.
	note := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(note, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Build(Options{Target: TargetClaude, Scope: ScopeProject, Version: testVersion, Tools: testTools}, dir)
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("unowned file not refused: %v", err)
	}
	if data, err := os.ReadFile(note); err != nil || string(data) != "mine" {
		t.Fatal("an unowned file was changed or deleted")
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "claude" {
			t.Errorf("a refused build left %s behind", entry.Name())
		}
	}
}

func TestBuildRefusesUnownedDirectoriesAndFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "codex")
	if err := os.MkdirAll(filepath.Join(dir, ".codex", "skills", "facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, ".codex", "skills", "facet", "SKILL.md")
	if err := os.WriteFile(mine, []byte("my own skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Options{Target: TargetCodex, Scope: ScopeUser, Version: testVersion}, dir); err == nil {
		t.Fatal("a directory without a manifest was replaced")
	}
	if data, _ := os.ReadFile(mine); string(data) != "my own skill" {
		t.Fatal("an unowned file was overwritten")
	}
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Options{Target: TargetCodex, Scope: ScopeUser, Version: testVersion}, file); err == nil {
		t.Fatal("a regular file was replaced by a bundle directory")
	}
	// An empty directory is not owned by anyone and may receive a bundle.
	empty := filepath.Join(t.TempDir(), "copilot")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	build(t, TargetCopilot, ScopeUser, empty)
}

// Output from the 1.x builder is owned through its manifest and may be
// replaced; it is never verified as a current bundle.
func TestBuildReplacesAnUnmodifiedLegacyBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	if err := os.MkdirAll(filepath.Join(dir, "skills", "facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := []byte("# legacy\n")
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), old, 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(map[string]any{"schema": legacySchema, "entries": []Entry{{Path: "CLAUDE.md", Bytes: int64(len(old)), Digest: Digest(old)}}})
	if err := os.WriteFile(filepath.Join(dir, ManifestName), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir, Expect{}); err == nil {
		t.Fatal("a 1.x bundle verified as current")
	}
	build(t, TargetClaude, ScopeUser, dir)
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the legacy instruction file survived the rebuild")
	}
}

func TestDropFrontmatterKeyKeepsEverythingElse(t *testing.T) {
	doc := []byte("---\nname: x\ndescription: d: e\n---\n\n# Body\nname: not frontmatter\n")
	got, err := dropFrontmatterKey("name")(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "---\ndescription: d: e\n---\n\n# Body\nname: not frontmatter\n" {
		t.Fatalf("got %q", got)
	}
	fields, ok := Frontmatter([]byte("---\r\nname: facet\r\ndescription: \"quoted\"\r\n---\r\nbody"))
	if !ok || fields["name"] != "facet" || fields["description"] != "quoted" {
		t.Fatalf("CRLF frontmatter = %v, %v", fields, ok)
	}
	if _, ok := Frontmatter([]byte("# no frontmatter")); ok {
		t.Fatal("frontmatter found in a document without one")
	}
}

func TestCLIBuildsEveryTargetAndRejectsWithdrawnOnes(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := CLI([]string{"--target", "all", "--scope", "project", "--out", out}, &stdout, &stderr, testVersion); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, target := range Targets() {
		m, err := Verify(filepath.Join(out, string(target)), Expect{Target: target, Scope: ScopeProject, Version: testVersion})
		if !SupportsScope(target, ScopeProject) {
			// "all" leaves out a target the scope does not support.
			if err == nil {
				t.Errorf("%s was built at project scope", target)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if len(m.Tools) == 0 {
			t.Errorf("%s: manifest records no tool vocabulary", target)
		}
		if !strings.Contains(stdout.String(), string(target)) {
			t.Errorf("summary omits %s: %s", target, stdout.String())
		}
	}
	// Rebuilding the same output is allowed: every file is owned.
	if code := CLI([]string{"--target", "claude,codex", "--out", out}, io.Discard, &stderr, testVersion); code != 0 {
		t.Fatalf("rebuild exit %d: %s", code, stderr.String())
	}

	for name, args := range map[string][]string{
		"withdrawn app target":   {"--target", "app", "--out", out},
		"missing out":            {"--target", "claude"},
		"missing target":         {"--out", out},
		"bad scope":              {"--target", "claude", "--scope", "global", "--out", out},
		"positional":             {"--target", "claude", "--out", out, "extra"},
		"unknown flag":           {"--bogus"},
		"compa at project scope": {"--target", "compa", "--scope", "project", "--out", out},
	} {
		stderr.Reset()
		if code := CLI(args, io.Discard, &stderr, testVersion); code != 2 {
			t.Errorf("%s: exit %d, want 2 (%s)", name, code, stderr.String())
		}
	}

	help := t.TempDir()
	stdout.Reset()
	if code := CLI([]string{"--help", "--out", help}, &stdout, io.Discard, testVersion); code != 0 || !strings.Contains(stdout.String(), "Usage: facet bundle") {
		t.Fatalf("help exit %d: %s", code, stdout.String())
	}
	if entries, _ := os.ReadDir(help); len(entries) != 0 {
		t.Fatal("help wrote files")
	}
}
