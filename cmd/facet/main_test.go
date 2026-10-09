package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kayiik/facet/internal/toolbox"
)

// Run the actual entry point in a child so exit codes and accidental writes are tested.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("FACET_CLI_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"facet"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func runCLI(t *testing.T, dir, home, path string, args ...string) (string, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FACET_CLI_TEST_PROCESS=1", "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "PATH="+path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatal(err)
	return "", -1
}

func TestHelpAndInvalidInputsDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		ok   bool
		want string
	}{
		{"help", []string{"--help"}, true, "Usage:"},
		{"doctor-help", []string{"doctor", "--help"}, true, "Usage of doctor"},
		{"mcp-help", []string{"mcp", "--help"}, true, "Usage of mcp"},
		{"command", []string{"bogus"}, false, "Unknown command"},
		{"removed-init", []string{"init"}, false, "Unknown command"},
		{"removed-ui", []string{"ui"}, false, "Unknown command"},
		{"removed-module", []string{"module", "describe"}, false, "Unknown command"},
		{"doctor-flag", []string{"doctor", "--bogus"}, false, "flag provided but not defined"},
		{"mcp-flag", []string{"mcp", "--bogus"}, false, "flag provided but not defined"},
		{"mcp-argument", []string{"mcp", "bogus"}, false, "unexpected arguments"},
		{"version-flag", []string{"version", "--bogus"}, false, "unexpected arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, home := t.TempDir(), t.TempDir()
			out, code := runCLI(t, dir, home, "", tc.args...)
			if (code == 0) != tc.ok || !strings.Contains(out, tc.want) {
				t.Fatalf("exit=%d output=%s", code, out)
			}
			for _, root := range []string{dir, home} {
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("unexpected writes in %s: %v (%v)", root, entries, err)
				}
			}
		})
	}
}

func TestHelpDerivesCanonicalToolCount(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	out, code := runCLI(t, dir, home, "", "--help")
	if code != 0 {
		t.Fatalf("help exit=%d output=%s", code, out)
	}
	want := fmt.Sprintf("and %d tools", len(toolbox.Names()))
	if !strings.Contains(out, want) {
		t.Fatalf("help output does not contain live canonical count %q:\n%s", want, out)
	}
}

func TestVersionComesFromTheSingleVariable(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	out, code := runCLI(t, dir, home, "", "version")
	if code != 0 || strings.TrimSpace(out) != "facet v"+Version {
		t.Fatalf("version exit=%d output=%q want facet v%s", code, out, Version)
	}
}
