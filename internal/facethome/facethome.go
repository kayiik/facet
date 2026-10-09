// Package facethome locates Facet's home folder: where the installer keeps
// runtimes and the active one (current), and where facet wire keeps its
// wiring record.
//
// FACET_HOME moves it. The Facet App sets it for its own pinned Toolkit, so
// that Toolkit never reads or writes the user-wide one beside it.
package facethome

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvVar names the variable that moves Facet's home folder.
const EnvVar = "FACET_HOME"

// For returns Facet's home folder for the user whose home directory is
// userHome: FACET_HOME when it is set (made absolute), else <userHome>/.facet.
// It returns "" when neither is known.
func For(userHome string) string {
	if dir := strings.TrimSpace(os.Getenv(EnvVar)); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return filepath.Clean(dir)
	}
	if userHome == "" {
		return ""
	}
	return filepath.Join(userHome, ".facet")
}

// Dir returns Facet's home folder for the current user.
func Dir() string {
	userHome, _ := os.UserHomeDir()
	return For(userHome)
}
