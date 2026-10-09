package toolbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func composeRuntimeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func isolateComposeRuntime(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv(ComposerDirEnv, "")
	// The executable-relative runtime must be a fixture too, not wherever the
	// test binary happens to be built.
	fakeRuntime(t)
	t.Chdir(workspace)
	return home, workspace
}

// Which composer renders must not depend on where a call runs or on the
// user's home: none of the retired locations is searched any more.
func TestFindComposerDirIgnoresTheHomeAndTheWorkingDirectory(t *testing.T) {
	home, workspace := isolateComposeRuntime(t)
	nested := filepath.Join(workspace, "a", "b")
	for _, retired := range []string{
		filepath.Join(home, ".facet", "bundle", "remotion-composer"),
		filepath.Join(workspace, "remotion-composer"),
		filepath.Join(workspace, "packs", "explainer", "runtime"),
		filepath.Join(nested, "remotion-composer"),
	} {
		composeRuntimeFixture(t, filepath.Join(retired, "package.json"), "{}")
	}
	// Configuration files are not read either.
	composeRuntimeFixture(t, filepath.Join(workspace, ".facet.yaml"), "paths:\n  remotion_composer: '"+filepath.ToSlash(filepath.Join(workspace, "remotion-composer"))+"'\n")
	composeRuntimeFixture(t, filepath.Join(home, ".config", "facet", "config.yaml"), "paths:\n  remotion_composer: '"+filepath.ToSlash(filepath.Join(workspace, "remotion-composer"))+"'\n")
	t.Chdir(nested)
	if got, err := findComposerDir(); err == nil {
		t.Fatalf("a composer outside the runtime was used: %q", got)
	} else if !strings.Contains(err.Error(), ComposerDirEnv) {
		t.Fatalf("the error does not name the override: %v", err)
	}
}

// An installed Facet finds the composer it shipped beside its executable,
// whatever the working directory.
func TestFindComposerDirBesideExecutable(t *testing.T) {
	isolateComposeRuntime(t)
	root := fakeRuntime(t)
	want := filepath.Join(root, "dependencies", "remotion-composer")
	composeRuntimeFixture(t, filepath.Join(want, "package.json"), "{}")
	got, err := findComposerDir()
	if err != nil || !sameFile(got, want) {
		t.Fatalf("findComposerDir() = %q, %v; want %q", got, err, want)
	}
	// The retired runtime folder name is not searched.
	if err := os.RemoveAll(want); err != nil {
		t.Fatal(err)
	}
	composeRuntimeFixture(t, filepath.Join(root, "bundle", "remotion-composer", "package.json"), "{}")
	if got, err := findComposerDir(); err == nil {
		t.Fatalf("the retired <runtime>/bundle folder was used: %q", got)
	}
}

// %LOCALAPPDATA%\Facet\runtimes was a retired install location; a composer
// left there must not be picked up in preference to reporting it missing.
func TestFindComposerDirIgnoresRetiredRuntimeLocation(t *testing.T) {
	home, _ := isolateComposeRuntime(t)
	t.Setenv("LOCALAPPDATA", home)
	for _, retired := range []string{filepath.Join(home, "Facet", "runtimes", "remotion"), filepath.Join(home, "Facet", "runtimes", "remotion", "current")} {
		composeRuntimeFixture(t, filepath.Join(retired, "package.json"), "{}")
	}
	if got, err := findComposerDir(); err == nil {
		t.Fatalf("a retired runtime location was used: %q", got)
	}
}

// FACET_REMOTION_COMPOSER is the one explicit override, and it wins over the
// runtime's own composer.
func TestFindComposerDirOverride(t *testing.T) {
	home, _ := isolateComposeRuntime(t)
	root := fakeRuntime(t)
	composeRuntimeFixture(t, filepath.Join(root, "dependencies", "remotion-composer", "package.json"), "{}")
	want := filepath.Join(home, "checkout", "remotion-composer")
	composeRuntimeFixture(t, filepath.Join(want, "package.json"), "{}")
	t.Setenv(ComposerDirEnv, want)
	got, err := findComposerDir()
	if err != nil || got != want {
		t.Fatalf("findComposerDir() = %q, %v; want %q", got, err, want)
	}
}

// An override naming no composer fails; it never silently selects another.
func TestFindComposerDirOverrideMustExist(t *testing.T) {
	_, workspace := isolateComposeRuntime(t)
	root := fakeRuntime(t)
	composeRuntimeFixture(t, filepath.Join(root, "dependencies", "remotion-composer", "package.json"), "{}")
	t.Setenv(ComposerDirEnv, filepath.Join(workspace, "missing-composer"))
	if got, err := findComposerDir(); err == nil || got != "" {
		t.Fatalf("a missing override must not silently select another composer: %q, %v", got, err)
	}
}

func TestRemotionRenderFailsClosed(t *testing.T) {
	for _, missing := range []string{"cli", "node", "failed-cli"} {
		t.Run(missing, func(t *testing.T) {
			if missing == "failed-cli" {
				if _, err := exec.LookPath("node"); err != nil {
					t.Skip("node is needed to test a failing CLI without rendering")
				}
			}
			home, workspace := isolateComposeRuntime(t)
			composer := filepath.Join(home, "composer", "remotion-composer")
			t.Setenv(ComposerDirEnv, composer)
			composeRuntimeFixture(t, filepath.Join(composer, "package.json"), "{}")
			composeRuntimeFixture(t, filepath.Join(composer, "src", "index.tsx"), "")
			if missing != "cli" {
				composeRuntimeFixture(t, filepath.Join(composer, "node_modules", "@remotion", "cli", "remotion-cli.js"), "process.stderr.write('intentional CLI failure'); process.exit(23);\n")
			}
			if missing == "node" {
				t.Setenv("PATH", t.TempDir())
			}
			outPath := filepath.Join(workspace, "output.mp4")
			result, _, err := doRemotionRender(composeRequest{}, outPath, time.Second*10)
			var failure *toolFailure
			if result != nil || !errors.As(err, &failure) {
				t.Fatalf("expected runtime failure, got result=%v err=%v", result, err)
			}
			wantCode, wantMessage := "dependency_missing", "Remotion render CLI"
			if missing == "node" {
				wantMessage = "node"
			} else if missing == "failed-cli" {
				wantCode, wantMessage = "command_failed", "node failed"
				if !strings.Contains(failure.err.Details["stderr"].(string), "intentional CLI failure") {
					t.Fatalf("CLI diagnostics lost: %v", failure.err.Details)
				}
			}
			if failure.err.Code != wantCode || !strings.Contains(failure.err.Message, wantMessage) {
				t.Fatalf("runtime error replaced by fallback: %+v", failure.err)
			}
			for _, path := range []string{outPath, filepath.Join(workspace, ".remotion_props.json")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("unexpected render artifact %s: %v", path, err)
				}
			}
		})
	}
}

func TestRemotionTimeoutRetainsSafeProgress(t *testing.T) {
	workspace := composeDeliveryFixture(t, `
const fs = require('fs');
fs.writeFileSync(process.argv[5], 'partial render');
fs.writeSync(1, 'Bundling 100%\nConcurrency          8x\nRendered 12/60, time remaining: 1m 31s\n');
fs.writeSync(1, 'https://user:secret@example.test/media?token=private\n{"password":"private"}\n');
fs.writeSync(2, 'Bearer private\nsource code and user props\n');
setInterval(() => {}, 1000);
`)
	output := filepath.Join(workspace, "output.mp4")
	composeRuntimeFixture(t, output, "previous delivery")
	result, _, err := doRemotionRender(composeRequest{}, output, 2*time.Second)
	var failed *toolFailure
	if result != nil || !errors.As(err, &failed) || failed.err.Code != "command_timeout" {
		t.Fatalf("expected timeout without fallback: result=%v err=%v", result, err)
	}
	stdout, _ := failed.err.Details["output"].(string)
	for _, want := range []string{"Bundling 100%", "Concurrency          8x", "Rendered 12/60, time remaining: 1m 31s", "REDACTED"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing timeout progress %q: %v", want, failed.err.Details)
		}
	}
	for _, diagnostic := range []string{stdout, failed.err.Details["stderr"].(string)} {
		if len(diagnostic) > maxDiagnostic {
			t.Fatal("unbounded timeout diagnostic")
		}
		for _, secret := range []string{"private", "secret", "example.test", "password", "source code", "user props"} {
			if strings.Contains(diagnostic, secret) {
				t.Fatalf("sensitive renderer diagnostic exposed: %q", diagnostic)
			}
		}
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "previous delivery" {
		t.Fatalf("previous delivery lost: %q, %v", data, err)
	}
	for _, pattern := range []string{".facet-*", ".remotion_props.json"} {
		leftovers, err := filepath.Glob(filepath.Join(workspace, pattern))
		if err != nil || len(leftovers) != 0 {
			t.Fatalf("temporary artifacts left behind: %v, %v", leftovers, err)
		}
	}
}

func TestRemotionTimeoutDiagnosticBound(t *testing.T) {
	var log strings.Builder
	for i := 0; i <= 1000; i++ {
		fmt.Fprintf(&log, "Rendered %d/1000\r", i)
	}
	log.WriteString("Rendered 1/2, time remaining: credential\nConcurrency 8x token=private\n")
	got := remotionTimeoutDiagnostic(log.String())
	if len(got) > maxDiagnostic || !strings.Contains(got, "Rendered 1000/1000") || !strings.Contains(got, "REDACTED") || strings.Contains(got, "private") || strings.Contains(got, "credential") {
		t.Fatalf("unsafe or incomplete bounded progress: %q", got)
	}
}
