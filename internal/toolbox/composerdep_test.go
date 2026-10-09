package toolbox

import "testing"

// video_compose must declare the Remotion composer it needs to render.
//
// It declared only ffmpeg. Verified on a fresh install: the tool reported
// configured:true while the composer had no node_modules, and the very next
// render failed with dependency_missing. A tool that cannot render must not
// report itself ready — that is the whole purpose of `configured`.
func TestVideoComposeDeclaresTheComposer(t *testing.T) {
	for _, tool := range []string{"video_compose"} {
		deps, ok := summary(tool)["dependencies"].([]any)
		if !ok {
			t.Fatalf("%s declares no dependencies", tool)
		}
		var names []string
		for _, d := range deps {
			if m, ok := d.(map[string]any); ok {
				n, _ := m["name"].(string)
				names = append(names, n)
			}
		}
		for _, want := range []string{"ffmpeg", "node", "remotion-composer"} {
			found := false
			for _, n := range names {
				if n == want {
					found = true
				}
			}
			if !found {
				t.Errorf("%s does not declare %q; it declares %v", tool, want, names)
			}
		}
	}
}

// A composer whose dependencies are absent is present and UNUSABLE. Reporting
// the directory as available is what let a broken install claim readiness.
func TestComposerAvailabilityMeansItCanRender(t *testing.T) {
	dep := composerDependency()
	if dep["type"] != "runtime" {
		t.Errorf("type = %v, want runtime; it is a directory, not a binary on PATH", dep["type"])
	}
	// Availability must be decided by the render CLI existing, not by the
	// directory existing. On this machine the composer is installed, so the
	// assertion is that a reported-available composer really can be executed.
	if available, _ := dep["available"].(bool); available {
		dir, err := findComposerDir()
		if err != nil || dir == "" {
			t.Fatal("composer reported available with no resolvable directory")
		}
		if !fileExists(dir + "/node_modules/@remotion/cli/remotion-cli.js") {
			t.Error("composer reported available but its render CLI is absent")
		}
	}
}

// A tool is configured only when every declared dependency is available.
// Otherwise `configured` answers a different question than the one a caller
// is asking.
func TestConfiguredRequiresEveryDeclaredDependency(t *testing.T) {
	entry := summary("video_compose")
	deps, _ := entry["dependencies"].([]any)
	allAvailable := true
	for _, d := range deps {
		if m, ok := d.(map[string]any); ok {
			if a, _ := m["available"].(bool); !a {
				allAvailable = false
			}
		}
	}
	configured, _ := entry["configured"].(bool)
	if configured != allAvailable {
		t.Errorf("configured = %v but dependencies all available = %v; "+
			"configured must mean the tool can actually run", configured, allAvailable)
	}
}
