package components

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// hyperframesFiles pin HyperFrames and every package under it, and the
// install scripts npm may run for them.
//
//go:embed hyperframes/package.json hyperframes/package-lock.json
var hyperframesFiles embed.FS

// findPython returns a command that runs Python 3.9 or newer, or nil.
func findPython(ctx context.Context) ([]string, error) {
	var candidates [][]string
	if runtime.GOOS == "windows" {
		candidates = append(candidates, []string{"py", "-3"}, []string{"python"}, []string{"python3"})
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			found, _ := filepath.Glob(filepath.Join(local, "Programs", "Python", "Python3*", "python.exe"))
			sort.Sort(sort.Reverse(sort.StringSlice(found)))
			for _, exe := range found {
				candidates = append(candidates, []string{exe})
			}
		}
	} else {
		candidates = append(candidates, []string{"python3"}, []string{"python"})
	}
	// Each candidate must run: on Windows a Microsoft Store alias exists
	// without a working interpreter.
	for _, c := range candidates {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, err := exec.CommandContext(ctx, c[0], append(c[1:], "-c", "import sys; assert sys.version_info >= (3, 9); print(sys.executable)")...).Output()
		cancel()
		if err == nil {
			if exe := strings.TrimSpace(string(out)); exe != "" {
				return []string{exe}, nil
			}
		}
	}
	return nil, errors.New("Piper needs Python 3.9 or newer, and none was found; install Python, then run facet install piper again")
}

func venvProgram(venv, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venv, "Scripts", name+".exe")
	}
	return filepath.Join(venv, "bin", name)
}

// installPiper makes a Python environment with Piper in place, and fetches
// the pinned voice beside it.
func installPiper(ctx context.Context, c Component, deps, stage string, opts Options) (err error) {
	python, err := findPython(ctx)
	if err != nil {
		return err
	}
	voices := filepath.Join(deps, "voices")
	downloads := filepath.Join(stage, "downloads")
	var files []string
	for _, d := range voicePins {
		file, err := fetch(ctx, d, downloads, opts)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	dest := filepath.Join(deps, c.Dir)
	previous := filepath.Join(stage, "previous")
	if _, statErr := os.Lstat(dest); statErr == nil {
		if err := rename(dest, previous); err != nil {
			return fmt.Errorf("cannot move the previous Piper aside (is it running?): %w", err)
		}
		defer func() {
			if err != nil {
				removeTree(dest)
				_ = rename(previous, dest)
			}
		}()
	} else {
		defer func() {
			if err != nil {
				removeTree(dest)
			}
		}()
	}
	opts.logf("  Making a Python environment with %s", python[0])
	if out, err := exec.CommandContext(ctx, python[0], append(python[1:], "-m", "venv", dest)...).CombinedOutput(); err != nil {
		return fmt.Errorf("python -m venv failed: %v\n%s", err, tail(string(out), 20))
	}
	opts.logf("  Installing piper-tts %s with pip", piperVersion)
	pip := exec.CommandContext(ctx, venvProgram(dest, "python"), "-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--only-binary=:all:", "piper-tts=="+piperVersion)
	if out, err := pip.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install piper-tts failed: %v\n%s", err, tail(string(out), 20))
	}
	if _, err := os.Stat(venvProgram(dest, "piper")); err != nil {
		return errors.New("pip installed piper-tts without its piper program")
	}
	r := receipt{Component: c.ID, Version: c.Version, Platform: platform, InstalledAt: time.Now().UTC()}
	for i, d := range voicePins {
		if err := unpack(files[i], voices, d); err != nil {
			return err
		}
		r.Sources = append(r.Sources, d.URL)
		r.Also = append(r.Also, "voices/"+d.Raw)
	}
	if err := writeReceipt(dest, r); err != nil {
		return err
	}
	return nil
}
