package toolbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Runtime layout.
//
// A user-wide install keeps everything Facet ships or installs under one
// runtime directory, the parent of the directory holding the executable:
//
//	<runtime>/bin/facet[.exe]
//	<runtime>/dependencies/remotion-composer/  composer sources (+ node_modules)
//	<runtime>/dependencies/node/          optional private Node (node.exe, or bin/node)
//	<runtime>/dependencies/piper/         Python venv (Scripts\piper.exe, or bin/piper)
//	<runtime>/dependencies/voices/<voice>.onnx (+ .onnx.json)
//	<runtime>/dependencies/hyperframes/node_modules/hyperframes/bin/hyperframes.mjs
//
// Resolving these relative to the executable makes an installed Facet
// independent of PATH, of the working directory and of whatever launcher
// started it, while a development build still finds tools on PATH.

// executablePath locates the running executable. A variable so tests can
// place a fake runtime around a fake executable.
var executablePath = os.Executable

// runtimeRoot returns <runtime>, or "" when the executable cannot be located.
func runtimeRoot() string {
	exe, err := executablePath()
	if err != nil || exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(filepath.Dir(exe))
}

// runtimeDependency joins parts under <runtime>/dependencies, or returns ""
// when the runtime directory is unknown.
func runtimeDependency(parts ...string) string {
	root := runtimeRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(append([]string{root, "dependencies"}, parts...)...)
}

// runtimeBinary returns the private copy of program installed under
// <runtime>/dependencies, or "" when there is none. Only programs the
// installer actually places there are looked up.
func runtimeBinary(program string) string {
	var candidate string
	switch strings.ToLower(strings.TrimSpace(program)) {
	case "node":
		if runtime.GOOS == "windows" {
			candidate = runtimeDependency("node", "node.exe")
		} else {
			candidate = runtimeDependency("node", "bin", "node")
		}
	case "piper":
		if runtime.GOOS == "windows" {
			candidate = runtimeDependency("piper", "Scripts", "piper.exe")
		} else {
			candidate = runtimeDependency("piper", "bin", "piper")
		}
	}
	if candidate == "" || !executableFile(candidate) {
		return ""
	}
	return candidate
}

// executableFile reports whether path is a regular file this process could
// execute. Windows has no execute bit, so a regular file is enough there.
func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

// lookPath resolves a program: the runtime's private copy first, then PATH.
//
// This is the single resolution point for the whole toolbox, so a tool, its
// dependency report and `tools describe` can never disagree about which
// binary runs.
func lookPath(program string) (string, error) {
	if path := runtimeBinary(program); path != "" {
		return path, nil
	}
	return exec.LookPath(program)
}

// ResolveProgram returns the program the toolbox runs for name, resolved
// exactly as its tools resolve it, so a report (facet doctor) can never
// disagree with what a tool would run.
func ResolveProgram(name string) (string, error) { return lookPath(name) }

// ComposerStatus reports the Remotion composer this facet renders with and
// whether it can render: its render CLI must be installed, not merely its
// directory present.
func ComposerStatus() (dir string, usable bool, err error) {
	dir, err = findComposerDir()
	if err != nil || dir == "" {
		return dir, false, err
	}
	return dir, fileExists(filepath.Join(dir, "node_modules", "@remotion", "cli", "remotion-cli.js")), nil
}
