package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kayiik/facet/internal/facethome"
	"github.com/kayiik/facet/internal/toolbox"
	"github.com/kayiik/facet/internal/wire"
)

// isolate points every home-directory lookup at a fresh temporary home and
// moves into an empty working directory, so the doctor reads nothing of the
// real user's environment.
func isolate(t *testing.T) (home, cwd string) {
	t.Helper()
	root := t.TempDir()
	home, cwd = filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv(facethome.EnvVar, "")
	t.Setenv(toolbox.ComposerDirEnv, "")
	t.Chdir(cwd)
	return home, cwd
}

func TestRunDoctor(t *testing.T) {
	home, cwd := isolate(t)
	var buf bytes.Buffer

	report, err := Run(&buf)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}

	runtimeNames := map[string]bool{}
	for _, r := range report.Runtimes {
		runtimeNames[r.Name] = true
	}
	for _, expected := range []string{"FFmpeg", "FFprobe", "Node", "Remotion Composer", "Edge-TTS"} {
		if !runtimeNames[expected] {
			t.Errorf("missing runtime check for %s", expected)
		}
	}

	cliNames := map[string]bool{}
	for _, c := range report.CLIs {
		cliNames[c.Name] = true
	}
	for _, expected := range []string{"Claude Code", "OpenCode", "GitHub Copilot", "OpenAI Codex", "Compa"} {
		if !cliNames[expected] {
			t.Errorf("missing CLI check for %s", expected)
		}
	}

	if len(report.Tools) != len(toolbox.Names()) {
		t.Errorf("expected %d toolbox tools, got %d", len(toolbox.Names()), len(report.Tools))
	}
	if len(report.EnvVars) != len(EnvProbes()) {
		t.Errorf("expected %d env vars in report, got %d", len(EnvProbes()), len(report.EnvVars))
	}

	// A fresh home has no runtime, no wiring, and no 1.x leftovers.
	if report.Facet.Status != StatusNotFound || len(report.Wiring) != 0 || report.WiringError != "" || len(report.Legacy) != 0 {
		t.Errorf("fresh home report: facet=%+v wiring=%v (%s) legacy=%v", report.Facet, report.Wiring, report.WiringError, report.Legacy)
	}

	out := buf.String()
	for _, section := range []string{"=== Facet System Doctor ===", "[Facet]", "[Wiring]", "[Facet 1.x Integrations]", "[System Runtimes]", "[Agent CLIs]", "[Environment Variables]", "[Toolbox Tools ("} {
		if !strings.Contains(out, section) {
			t.Errorf("output missing %s:\n%s", section, out)
		}
	}
	if !strings.Contains(out, "No wirings recorded") {
		t.Errorf("output does not explain the empty wiring:\n%s", out)
	}

	jsonBytes, err := report.JSON()
	if err != nil || len(jsonBytes) == 0 {
		t.Fatalf("report.JSON() = %d bytes, %v", len(jsonBytes), err)
	}

	// The doctor only reads.
	for _, dir := range []string{home, cwd} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("doctor wrote into %s: %v", dir, entries)
		}
	}
}

func TestDoctorProbesOnlyToolCredentials(t *testing.T) {
	probes := map[string]bool{}
	for _, name := range EnvProbes() {
		probes[name] = true
		if name == "ANTHROPIC_API_KEY" {
			t.Error("the doctor probes a model credential; models are the harness's concern")
		}
	}
	// The alternative names the provider tools accept are reported too.
	for _, name := range []string{"FAL_KEY", "FLUX_API_KEY", "KLING_API_KEY"} {
		if !probes[name] {
			t.Errorf("the doctor does not report %s, which a provider tool reads", name)
		}
	}
}

// The doctor resolves programs exactly as the tools do. A configuration file
// can no longer pin a program the tools would never run.
func TestDoctorAgreesWithTheToolbox(t *testing.T) {
	_, cwd := isolate(t)
	if err := os.WriteFile(filepath.Join(cwd, ".facet.yaml"), []byte("paths:\n  ffmpeg: /pinned/nowhere/ffmpeg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	report := Generate()
	for _, r := range report.Runtimes {
		if r.Name != "FFmpeg" {
			continue
		}
		want, err := toolbox.ResolveProgram("ffmpeg")
		if (err == nil) != r.Available || (err == nil && r.Path != want) {
			t.Errorf("doctor FFmpeg = %+v; the toolbox resolves %q (%v)", r, want, err)
		}
		if strings.Contains(r.Path, "pinned") {
			t.Errorf("doctor read a configuration file: %+v", r)
		}
	}

	composer := filepath.Join(cwd, "composer")
	if err := os.MkdirAll(composer, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composer, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(toolbox.ComposerDirEnv, composer)
	got := probeRemotionComposer()
	if got.Status != StatusWarning || got.Available || !strings.Contains(got.Details, "dependencies not installed") {
		t.Errorf("a composer without its dependencies: %+v", got)
	}
}

// The doctor reports the runtime this facet belongs to, and what the
// installer's receipt there records.
func TestDoctorReportsTheRuntime(t *testing.T) {
	isolate(t)
	place := filepath.Join(t.TempDir(), "Programs", "Facet")
	name := "facet"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(place, "bin", name)
	writeFixture(t, exe, "fake")
	t.Cleanup(toolbox.UseExecutable(exe))
	// The doctor reports paths with their links and Windows short names resolved.
	exe, place = resolved(t, exe), resolved(t, place)

	got := probeFacetRuntime("2.0.0")
	if got.Runtime != place || got.Executable != exe || got.Status != StatusNotFound || !strings.Contains(got.Details, "not installed by Facet's installer") {
		t.Fatalf("a runtime without a receipt: %+v", got)
	}

	receipt := filepath.Join(place, "install-receipt.tsv")
	writeFixture(t, receipt, "format\tfacet-install-v1\nversion\t2.0.0\ncommit\t"+strings.Repeat("a", 40)+
		"\nrepository\tkayiik/facet\ntarget\twindows/amd64\nroot\t"+place+"\nproduct\tcore\nproduct\tapp\nfile\troot\tLICENSE\t"+strings.Repeat("b", 64)+"\n")
	got = probeFacetRuntime("2.0.0")
	if got.Status != StatusOK || got.Installed != "2.0.0" || strings.Join(got.Products, ",") != "core,app" || got.Details != "" {
		t.Fatalf("an installed runtime: %+v", got)
	}
	var buf bytes.Buffer
	formatDoctorReport(&DoctorReport{Facet: got}, &buf)
	if !strings.Contains(buf.String(), place+" (installed: Facet 2.0.0, core and app)") {
		t.Errorf("formatted runtime:\n%s", buf.String())
	}

	got = probeFacetRuntime("2.1.0")
	if got.Status != StatusWarning || !strings.Contains(got.Details, "Facet 2.0.0") || !strings.Contains(got.Details, "facet v2.1.0") {
		t.Fatalf("a receipt for another release: %+v", got)
	}

	writeFixture(t, receipt, "format\tfacet-install-v0\nversion\t2.0.0\n")
	if got = probeFacetRuntime("2.0.0"); got.Status != StatusWarning || !strings.Contains(got.Details, "receipt") {
		t.Fatalf("a receipt of another format: %+v", got)
	}
}

// FACET_HOME moves Facet's records, not the runtime: that is wherever the
// running facet is.
func TestDoctorRuntimeIgnoresFacetHome(t *testing.T) {
	isolate(t)
	place := filepath.Join(t.TempDir(), "runtime")
	exe := filepath.Join(place, "bin", "facet")
	writeFixture(t, exe, "fake")
	t.Cleanup(toolbox.UseExecutable(exe))
	t.Setenv(facethome.EnvVar, filepath.Join(t.TempDir(), "app-facet"))
	if got := probeFacetRuntime("2.0.0"); got.Runtime != resolved(t, place) {
		t.Fatalf("FACET_HOME moved the runtime: %+v", got)
	}
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

func TestDoctorNamesV1IntegrationsAndTheirCleanup(t *testing.T) {
	home, cwd := isolate(t)
	marked := "# Facet\n\n## This installation\n- Invoke Facet through `run-facet.ps1` followed by the normal arguments.\n"
	writeFixture(t, filepath.Join(home, ".facet-install", "installation.json"), `{"schema":1,"version":"1.1.0","host":"opencode","components":["remotion"],"packs":[]}`)
	writeFixture(t, filepath.Join(home, ".opencode", "skills", "facet", "SKILL.md"), marked)
	writeFixture(t, filepath.Join(cwd, ".facet-install", "installation.tsv"), "version\t1.1.0\ninstallation\t/opt/facet\nhost\tclaude\ncomponents\tremotion\npacks\t\n")
	// A skill without both markers is not a 1.x copy.
	writeFixture(t, filepath.Join(cwd, ".agents", "skills", "facet", "SKILL.md"), "# Facet\n\n## This installation\n")
	writeFixture(t, filepath.Join(cwd, ".github", "skills", "facet", "SKILL.md"), "uses run-facet but no section\n")

	checks := legacyChecks("windows", cwd, home)
	if len(checks) != 2 {
		t.Fatalf("legacy checks = %+v", checks)
	}
	byProject := map[string]LegacyCheck{}
	for _, c := range checks {
		byProject[c.Project] = c
	}
	homeCheck, cwdCheck := byProject[home], byProject[cwd]
	if homeCheck.Host != "opencode" || len(homeCheck.Files) != 2 ||
		homeCheck.Files[0] != filepath.Join(home, ".facet-install", "installation.json") ||
		homeCheck.Files[1] != filepath.Join(home, ".opencode", "skills", "facet", "SKILL.md") {
		t.Errorf("home check = %+v", homeCheck)
	}
	if want := `.\install.ps1 -Action uninstall -Target opencode -ProjectDir '` + home + `'`; homeCheck.Cleanup != want {
		t.Errorf("windows cleanup = %q, want %q", homeCheck.Cleanup, want)
	}
	if cwdCheck.Host != "claude" || len(cwdCheck.Files) != 1 || cwdCheck.Files[0] != filepath.Join(cwd, ".facet-install", "installation.tsv") {
		t.Errorf("cwd check = %+v", cwdCheck)
	}
	unix := legacyChecks("linux", cwd)
	if want := "bash install.sh --action uninstall --target claude --project '" + cwd + "'"; len(unix) != 1 || unix[0].Cleanup != want {
		t.Errorf("unix cleanup = %+v, want %q", unix, want)
	}

	var buf bytes.Buffer
	report, err := Run(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Legacy) != 2 {
		t.Fatalf("doctor found %d 1.x installations", len(report.Legacy))
	}
	out := buf.String()
	for _, want := range append(append([]string{}, homeCheck.Files...), "Action uninstall", "action uninstall", "shadows the user-wide Facet skill") {
		if runtime.GOOS != "windows" && want == "Action uninstall" || runtime.GOOS == "windows" && want == "action uninstall" {
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("doctor output omits %q:\n%s", want, out)
		}
	}
}

func TestDoctorSummarizesWiring(t *testing.T) {
	home, _ := isolate(t)
	var stdout, stderr bytes.Buffer
	if code := wire.CLI([]string{"copilot"}, &stdout, &stderr, toolbox.ProductVersion()); code != 0 {
		t.Fatalf("wire copilot: %d\n%s%s", code, stdout.String(), stderr.String())
	}
	checks, errText := wiringChecks(toolbox.ProductVersion())
	if errText != "" || len(checks) != 1 || checks[0].Status != StatusOK || checks[0].Label != "copilot (user)" {
		t.Fatalf("wiring checks = %+v (%s)", checks, errText)
	}
	skill := filepath.Join(home, ".copilot", "skills", "facet", "SKILL.md")
	if err := os.WriteFile(skill, []byte("edited"), 0644); err != nil {
		t.Fatal(err)
	}
	checks, _ = wiringChecks("9.9.9")
	if len(checks) != 1 || checks[0].Status != StatusWarning || checks[0].Fix != "facet wire copilot" ||
		!strings.Contains(checks[0].Details, "changed since facet wire installed them") || !strings.Contains(checks[0].Details, "v9.9.9") {
		t.Fatalf("drifted wiring checks = %+v", checks)
	}
	var buf bytes.Buffer
	formatDoctorReport(&DoctorReport{Facet: FacetRuntimeCheck{Status: StatusNotFound}, Wiring: checks}, &buf)
	if !strings.Contains(buf.String(), "fix: facet wire copilot") {
		t.Errorf("formatted wiring:\n%s", buf.String())
	}

	if err := os.WriteFile(wire.RegistryPath(home), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, errText := wiringChecks("9.9.9"); errText == "" {
		t.Error("an unreadable registry was not reported")
	}
}

func TestEdgeTTSDoctorDistinguishesBuiltInClientFromServiceReachability(t *testing.T) {
	got := probeEdgeTTS()
	if !got.Available || got.Status != StatusOK {
		t.Fatalf("built-in Edge TTS client should be available: %#v", got)
	}
	for _, want := range []string{"built-in", "network", "not tested"} {
		if !strings.Contains(strings.ToLower(got.Details), want) {
			t.Errorf("Edge TTS details %q do not mention %q", got.Details, want)
		}
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
