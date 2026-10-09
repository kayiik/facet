package facethome

import (
	"path/filepath"
	"testing"
)

func TestDefaultsToDotFacetInTheUserHome(t *testing.T) {
	t.Setenv(EnvVar, "")
	home := t.TempDir()
	if got, want := For(home), filepath.Join(home, ".facet"); got != want {
		t.Fatalf("For(%q) = %q, want %q", home, got, want)
	}
	if got := For(""); got != "" {
		t.Fatalf("For(\"\") = %q, want \"\"", got)
	}
}

func TestFacetHomeMovesIt(t *testing.T) {
	moved := t.TempDir()
	t.Setenv(EnvVar, moved)
	if got := For(t.TempDir()); got != moved {
		t.Fatalf("For() = %q, want %q", got, moved)
	}

	// A relative value is made absolute once, so every reader agrees.
	t.Chdir(moved)
	t.Setenv(EnvVar, "app-home")
	if got, want := For(""), filepath.Join(moved, "app-home"); got != want {
		t.Fatalf("relative FACET_HOME: For() = %q, want %q", got, want)
	}
}
