package toolbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every variable a tool reads must be in EnvVars: a CLI that filters the
// environment of its MCP servers forwards only that list, and a setting it
// drops does nothing while looking configured.
func TestEnvVarsCoverEveryVariableTheToolsRead(t *testing.T) {
	forwarded := map[string]bool{}
	for _, name := range EnvVars() {
		if forwarded[name] {
			t.Errorf("EnvVars lists %s twice", name)
		}
		forwarded[name] = true
	}
	literal := regexp.MustCompile(`(?:Getenv|LookupEnv)\("([^"]+)"\)`)
	dependency := regexp.MustCompile(`env(?:Any)?Dependency\(([^)]*)\)`)
	quoted := regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`)
	identifier := regexp.MustCompile(`(?:Getenv|LookupEnv)\(([A-Za-z_][A-Za-z0-9_]*)\)`)
	constants := map[string]string{"ComposerDirEnv": ComposerDirEnv, "hyperframesEnv": hyperframesEnv}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	check := func(file, name string) {
		reads++
		if !forwarded[name] {
			t.Errorf("%s reads %s, which EnvVars does not list; Codex would not forward it to facet mcp", file, name)
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, m := range literal.FindAllStringSubmatch(src, -1) {
			check(file, m[1])
		}
		// envDependency("X") and envAnyDependency("X", "Y") read their
		// arguments; the functions themselves read a parameter.
		for _, m := range dependency.FindAllStringSubmatch(src, -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				check(file, q[1])
			}
		}
		for _, m := range identifier.FindAllStringSubmatch(src, -1) {
			if m[1] == "name" {
				continue // inside envDependency and envAnyDependency
			}
			value, ok := constants[m[1]]
			if !ok {
				t.Errorf("%s reads the variable named by %s; add it to EnvVars and to this test", file, m[1])
				continue
			}
			check(file, value)
		}
	}
	// A scan that finds nothing and a scan that inspects nothing look alike.
	if reads < 20 {
		t.Fatalf("found only %d variable reads in the toolbox; the scan no longer sees them", reads)
	}
}
