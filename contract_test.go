package facet

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var productPathPattern = regexp.MustCompile("`((?:skills|packs|agents|schemas|styles|pipeline_defs|\\.agents)/[^`\\s,;:)]+)")
var documentedTargetInstallRoots = []string{".agents/skills"}

func TestRequiredRetainedPortLegalNotice(t *testing.T) {
	data, err := os.ReadFile("THIRD_PARTY_NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	notice := strings.Join(strings.Fields(string(data)), " ")
	required := []string{
		"cd9f3c1f03368be87b140af494914b8ee4e3c7a4",
		"AGPL-3.0",
		"toolbox contract/registry/process execution",
		"media probe/frame sampling/scene detection",
		"supplied-footage source editing",
		"audio mixing/normalization",
		"technical output review",
		"no donor skills, pipelines, schemas, fixtures, or product guidance",
		"composer implementation in `remotion-composer/src` is independently authored by Facet",
	}
	for _, text := range required {
		if !strings.Contains(notice, text) {
			t.Errorf("THIRD_PARTY_NOTICES.md omits required legal attribution %q", text)
		}
	}
}

func TestTrackedTextContainsNoPersonalWindowsUserPaths(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)[a-z]:[/\\]+users[/\\]+([^/\\\r\n"'<>]+)`)
	allowed := map[string]bool{"user": true, "test user": true}
	var violations []string
	for _, name := range trackedFiles(t) {
		if !isProductTextFile(name) {
			continue
		}
		data, err := os.ReadFile(filepath.FromSlash(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllSubmatch(data, -1) {
			username := strings.ToLower(strings.TrimSpace(string(match[1])))
			if !allowed[username] {
				violations = append(violations, name+": personal Windows user path")
			}
		}
	}
	if len(violations) != 0 {
		t.Fatalf("personal host paths remain:\n%s", strings.Join(violations, "\n"))
	}
}

func TestActiveProductDocsDescribeGuidanceNotWorkflowContracts(t *testing.T) {
	files := []string{
		"docs/index.html",
		"internal/toolbox/productmodel_test.go",
	}
	banned := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bcanonical\b[^\n]{0,80}\bpipelines?\b`),
		regexp.MustCompile(`(?i)\bpipeline definitions?\b`),
		regexp.MustCompile(`(?i)\b(?:pipeline|style) YAML files?\b`),
		regexp.MustCompile(`(?i)\bdirector skills?\b`),
		regexp.MustCompile(`(?i)\bprovider[-_ ]selection\b`),
		regexp.MustCompile(`(?i)\bworkflow schemas?\b`),
		regexp.MustCompile(`(?i)\bauthoring schemas?\b`),
		regexp.MustCompile(`(?i)\b(?:112|34)\s+files?\b`),
	}
	var violations []string
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range banned {
			if match := pattern.Find(data); match != nil {
				violations = append(violations, name+": "+string(match))
			}
		}
	}
	sort.Strings(violations)
	if len(violations) != 0 {
		t.Fatalf("active product docs advertise removed workflow contracts:\n%s",
			strings.Join(violations, "\n"))
	}
}

// The npm package is only the launcher: the guidance is compiled into the
// facet binary and the composer ships in the release archive, so an npm copy
// of either could only drift from what actually runs. npm always adds the
// README, which says the package is only a launcher.
func TestNpmPackageShipsOnlyTheLauncherReadmeAndNotices(t *testing.T) {
	command := exec.Command("npm", "pack", "--dry-run", "--json", "--ignore-scripts")
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("inspect npm package payload: %v", err)
	}
	var result []struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode npm package payload: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("npm pack returned %d payloads, want 1", len(result))
	}
	var shipped []string
	for _, file := range result[0].Files {
		shipped = append(shipped, filepath.ToSlash(file.Path))
	}
	sort.Strings(shipped)
	want := []string{"LICENSE", "README.md", "THIRD_PARTY_NOTICES.md", "bin/facet-cli.js", "package.json"}
	if strings.Join(shipped, "\n") != strings.Join(want, "\n") {
		t.Fatalf("npm package ships:\n%s\nwant:\n%s", strings.Join(shipped, "\n"), strings.Join(want, "\n"))
	}
}

// agentEngineModules are module path prefixes of agent engines and
// reasoning-model clients. Facet is never a harness: it links none of them.
// The Facet App runs Compa's kernel beside the Toolkit as a separate program,
// never as a Go dependency.
var agentEngineModules = []string{
	"github.com/kayiik/compa",
	"github.com/kayiik/llm-gateway",
	"github.com/openai/",
	"github.com/anthropics/",
	"github.com/sashabaranov/go-openai",
	"google.golang.org/genai",
	"github.com/google/generative-ai-go",
	"github.com/tmc/langchaingo",
	"github.com/cloudwego/eino",
}

func TestFacetLinksNoAgentEngine(t *testing.T) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		module := fields[0]
		if module == "require" && len(fields) > 1 {
			module = fields[1]
		}
		for _, engine := range agentEngineModules {
			if strings.HasPrefix(module, engine) {
				t.Errorf("go.mod requires agent engine module %s", module)
			}
		}
	}

	// Each binary's own import graph: what is actually linked.
	for _, command := range []string{"./cmd/facet", "./cmd/facet-app"} {
		for _, pkg := range linkedPackages(t, command) {
			for _, engine := range agentEngineModules {
				if strings.HasPrefix(pkg.module, engine) {
					t.Errorf("%s links agent engine package %s (module %s)", command, pkg.importPath, pkg.module)
				}
			}
		}
	}
}

// The Facet App and the Toolkit share this repository, its module and its
// release, not code. The App (cmd/facet-app and internal/app) reaches the
// Toolkit only through the facet command (design rule R1), and the facet
// binary links nothing of the App.
func TestToolkitAndAppStayApart(t *testing.T) {
	entries, err := os.ReadDir("cmd")
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, entry := range entries {
		commands = append(commands, entry.Name())
	}
	if got := strings.Join(commands, " "); got != "facet facet-app" {
		t.Errorf("cmd holds %s; this repository builds two commands, facet and facet-app", got)
	}
	const module = "github.com/kayiik/facet"
	inApp := func(importPath string) bool {
		return importPath == module+"/internal/app" || strings.HasPrefix(importPath, module+"/internal/app/")
	}
	for _, pkg := range linkedPackages(t, "./cmd/facet") {
		if inApp(pkg.importPath) {
			t.Errorf("cmd/facet links App package %s", pkg.importPath)
		}
	}
	for _, pkg := range linkedPackages(t, "./cmd/facet-app") {
		if pkg.module == module && pkg.importPath != module+"/cmd/facet-app" && !inApp(pkg.importPath) {
			t.Errorf("cmd/facet-app links Toolkit package %s; the App runs the facet command instead", pkg.importPath)
		}
	}
}

type goPackage struct{ importPath, module string }

// linkedPackages returns the packages a command links, with their modules.
func linkedPackages(t *testing.T, command string) []goPackage {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f",
		"{{.ImportPath}}|{{if .Module}}{{.Module.Path}}{{end}}", command).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", command, err)
	}
	var packages []goPackage
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		importPath, module, _ := strings.Cut(strings.TrimSpace(line), "|")
		packages = append(packages, goPackage{importPath, module})
	}
	return packages
}

// The Toolkit never calls a reasoning model: it calls media-generation
// providers only when such a tool runs. No Go source in the binary's tree may
// name a model-inference endpoint.
func TestToolkitCallsNoReasoningModel(t *testing.T) {
	forbidden := []string{
		"/chat/completions", "/v1/completions", "/v1/responses", "/v1/messages",
		"api.anthropic.com", "generativelanguage.googleapis.com", "bedrock-runtime",
	}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return err
			}
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			text := strings.ToLower(string(data))
			for _, endpoint := range forbidden {
				if strings.Contains(text, endpoint) {
					t.Errorf("%s names the model endpoint %q", filepath.ToSlash(name), endpoint)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestFacetCarriesNoRetiredKernelIdentity(t *testing.T) {
	retired := "facet" + "-studio"
	var violations []string
	for _, name := range trackedFiles(t) {
		if !isProductTextFile(name) {
			continue
		}
		data, err := os.ReadFile(filepath.FromSlash(name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(data)), retired) {
			violations = append(violations, name)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("retired kernel identity remains in Facet:\n%s", strings.Join(violations, "\n"))
	}
}

func TestProductTextGuardCoversShippingDefinitions(t *testing.T) {
	for _, name := range []string{".gitignore", "install.sh", "install.ps1", "scripts/build-release.py", ".github/workflows/release.yml", "Dockerfile"} {
		if !isProductTextFile(name) {
			t.Errorf("product text guard skips %s", name)
		}
	}
}

func TestMarkdownProductPathsDistinguishesTargetInstallRoots(t *testing.T) {
	tests := []struct {
		name           string
		reference      string
		wantViolations int
	}{
		{name: "target root", reference: ".agents/skills"},
		{name: "target descendant", reference: ".agents/skills/facet/SKILL.md"},
		{name: "dangling repository path", reference: "skills/missing-contract/SKILL.md", wantViolations: 1},
		{name: "adjacent agents path", reference: ".agents/missing-contract/SKILL.md", wantViolations: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "contract.md")
			if err := os.WriteFile(name, []byte("`"+test.reference+"`"), 0o600); err != nil {
				t.Fatal(err)
			}
			var violations []string
			checkMarkdownProductPaths(name, &violations)
			if len(violations) != test.wantViolations {
				t.Fatalf("checkMarkdownProductPaths(%q) found %d violations, want %d: %v",
					test.reference, len(violations), test.wantViolations, violations)
			}
		})
	}
}

func TestCanonicalGuidanceRequiresExplicitOutputReviewExpectations(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("skills", "facet", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"explicit expected profile",
		"duration",
		"audio presence",
		"`assumed`",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("canonical guidance does not explain output review expectation %q", required)
		}
	}
}

func checkPackPath(packDir, manifestPath, name string, violations *[]string) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		*violations = append(*violations, filepath.ToSlash(manifestPath)+": non-Facet path "+name)
		return
	}
	if _, err := os.Stat(filepath.Join(packDir, clean)); err != nil {
		*violations = append(*violations, filepath.ToSlash(manifestPath)+": missing "+name)
	}
}

func checkMarkdownProductPaths(name string, violations *[]string) {
	data, err := os.ReadFile(name)
	if err != nil {
		*violations = append(*violations, filepath.ToSlash(name)+": missing contract file")
		return
	}
	for _, match := range productPathPattern.FindAllStringSubmatch(string(data), -1) {
		ref := strings.TrimSuffix(match[1], "/")
		if strings.ContainsAny(ref, "*<>") {
			*violations = append(*violations, filepath.ToSlash(name)+": non-deterministic path "+match[1])
			continue
		}
		if isDocumentedTargetInstallPath(ref) {
			continue
		}
		if _, err := os.Stat(filepath.FromSlash(ref)); err != nil {
			*violations = append(*violations, filepath.ToSlash(name)+": missing "+match[1])
		}
	}
}

func isDocumentedTargetInstallPath(name string) bool {
	clean := path.Clean(name)
	for _, root := range documentedTargetInstallRoots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return false
}

func isProductTextFile(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case ".dockerignore", ".gitignore":
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return true
	}
	switch ext {
	case ".cjs", ".css", ".go", ".html", ".js", ".json", ".md", ".mjs", ".ps1", ".py", ".sh", ".ts", ".tsv", ".tsx", ".txt", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func trackedFiles(t *testing.T) []string {
	t.Helper()
	command := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}
	parts := strings.Split(string(raw), "\x00")
	files := make([]string, 0, len(parts))
	for _, name := range parts {
		if name != "" {
			if _, err := os.Stat(filepath.FromSlash(name)); os.IsNotExist(err) {
				continue
			}
			files = append(files, filepath.ToSlash(name))
		}
	}
	sort.Strings(files)
	return files
}

// The composer ships only the allowlisted source files; the allowlist is the
// provenance control for the independently authored renderer.
func TestComposerSourceMatchesAllowlist(t *testing.T) {
	var manifest struct {
		AllowedSourcePaths []string `json:"allowedSourcePaths"`
	}
	data, err := os.ReadFile(filepath.Join("remotion-composer", "composer-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var actual []string
	err = filepath.WalkDir(filepath.Join("remotion-composer", "src"), func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel("remotion-composer", name)
		if err != nil {
			return err
		}
		actual = append(actual, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(actual)
	sort.Strings(manifest.AllowedSourcePaths)
	if strings.Join(actual, "\n") != strings.Join(manifest.AllowedSourcePaths, "\n") {
		t.Fatalf("composer source differs from its allowlist:\ngot %v\nwant %v", actual, manifest.AllowedSourcePaths)
	}
}

// The end-to-end test installs the release with its own installer on a
// machine that has none of facet's components, so every one is facet's own
// download, and runs the journey with no network. It never builds or lays out
// facet by hand.
func TestUATInstallsTheReleaseOnACleanMachine(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "uat.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, line)
		}
	}
	instructions := strings.Join(lines, "\n")
	for _, required := range []string{
		"FROM ubuntu:24.04",
		"sh /release/install.sh --mode bundle --harness opencode --components defaults --distribution-dir /release",
		"/home/facet/.local/lib/facet/dependencies/node/bin/node",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("scripts/uat.Dockerfile does not contain %q", required)
		}
	}
	for _, forbidden := range []string{"ffmpeg", "nodejs", "npm ", "chromium", "google-chrome", "go build", "ln -s", ".facet/current"} {
		if strings.Contains(strings.ToLower(instructions), forbidden) {
			t.Errorf("scripts/uat.Dockerfile installs or lays out %q itself", forbidden)
		}
	}
	runner, err := os.ReadFile(filepath.Join("scripts", "uat.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runner), `"--network", "none"`) {
		t.Error("scripts/uat.py does not run the journey without a network")
	}
}

func TestUATJourneyDrivesTheInstalledFacet(t *testing.T) {
	data, err := os.ReadFile("scripts/uat-journey.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"['wire', 'opencode', '--scope', 'user']",
		"'video_compose'",
		"'output_review'",
		"opencode('mcp', 'list')",
	} {
		if !strings.Contains(string(data), required) {
			t.Errorf("UAT journey does not exercise %s", required)
		}
	}
}

// Surfaces removed in 2.0 stay removed: the browser preview, the module host,
// project initialization and the duplicate launchers.
func TestRemovedTwoPointZeroSurfacesStayRemoved(t *testing.T) {
	for _, name := range []string{
		"internal/studio", "internal/module", "cmd/facet-ui", "cmd/facet-module", "web",
		"pkg/viewdef", "internal/config/init.go", "scripts/package-windows.cjs", "bin/facet-ui-cli.js",
		// The Compa embedding and the 1.x configuration file are gone too.
		"pkg/provider", "internal/config",
	} {
		if _, err := os.Stat(filepath.FromSlash(name)); !os.IsNotExist(err) {
			t.Errorf("removed surface returned: %s", name)
		}
	}
	var pkg struct {
		Bin map[string]string `json:"bin"`
	}
	data, err := os.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Bin) != 1 || pkg.Bin["facet"] == "" {
		t.Errorf("npm package must expose only the facet launcher, got %v", pkg.Bin)
	}
}
