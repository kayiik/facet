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
//	<runtime>/dependencies/ffmpeg/bin/        private FFmpeg and FFprobe
//	<runtime>/dependencies/node/              optional private Node (node.exe, or bin/node)
//	<runtime>/dependencies/browser/           Chrome headless shell both renderers use
//	<runtime>/dependencies/remotion-composer/ composer sources (+ node_modules)
//	<runtime>/dependencies/piper/             Python venv (Scripts\piper.exe, or bin/piper)
//	<runtime>/dependencies/voices/<voice>.onnx (+ .onnx.json)
//	<runtime>/dependencies/hyperframes/node_modules/hyperframes/bin/hyperframes.mjs
//
// `facet install` puts the components there. Resolving them relative to the
// executable makes an installed Facet independent of PATH, of the working
// directory and of whatever launcher started it, while a development build
// still finds tools on PATH.

// executablePath locates the running executable. A variable so tests can
// place a fake runtime around a fake executable.
var executablePath = os.Executable

// Executable returns the running facet, resolved through links, or "" when it
// cannot be located. An installed facet lives in its runtime, which upgrades
// replace in place, so CLIs can register this path for good.
func Executable() string {
	exe, err := executablePath()
	if err != nil || exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Clean(exe)
}

// runtimeRoot returns <runtime>, or "" when the executable cannot be located.
func runtimeRoot() string {
	exe := Executable()
	if exe == "" {
		return ""
	}
	return filepath.Dir(filepath.Dir(exe))
}

// RuntimeRoot returns the runtime directory this facet belongs to, or "".
func RuntimeRoot() string { return runtimeRoot() }

// UseExecutable makes the toolbox resolve its runtime as if it ran from exe,
// and returns the function that undoes it. Tests of packages built on the
// toolbox use it to lay out a runtime of their own.
func UseExecutable(exe string) (restore func()) {
	previous := executablePath
	executablePath = func() (string, error) { return exe, nil }
	return func() { executablePath = previous }
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

// RuntimeDependency joins parts under <runtime>/dependencies, or returns "".
func RuntimeDependency(parts ...string) string { return runtimeDependency(parts...) }

// runtimeBinary returns the private copy of program installed under
// <runtime>/dependencies, or "" when there is none. Only programs the
// installer actually places there are looked up.
func runtimeBinary(program string) string {
	var candidate string
	switch name := strings.ToLower(strings.TrimSpace(program)); name {
	case "ffmpeg", "ffprobe":
		if runtime.GOOS == "windows" {
			candidate = runtimeDependency("ffmpeg", "bin", name+".exe")
		} else {
			candidate = runtimeDependency("ffmpeg", "bin", name)
		}
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

// privateBrowser returns the Chrome headless shell installed under
// <runtime>/dependencies/browser, or "". Chrome for Testing names it
// chrome-headless-shell; the Linux arm64 build calls it headless_shell.
func privateBrowser() string {
	names := []string{"chrome-headless-shell", "headless_shell"}
	if runtime.GOOS == "windows" {
		names = []string{"chrome-headless-shell.exe"}
	}
	for _, name := range names {
		if candidate := runtimeDependency("browser", name); candidate != "" && executableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// BrowserExecutable returns the browser a Remotion render uses, resolved
// exactly as the render resolves it, or "" when there is none.
func BrowserExecutable() string { return findBrowserExecutable() }

// PrivateBrowser returns the browser Facet installed, or "".
func PrivateBrowser() string { return privateBrowser() }

// HyperFramesEntry returns the HyperFrames CLI a hyperframes_compose run
// uses, resolved exactly as the run resolves it.
func HyperFramesEntry() (string, error) { return hyperframesEntry() }

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
