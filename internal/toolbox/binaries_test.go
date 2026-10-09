package toolbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRuntime lays out <runtime>/bin/facet[.exe] in a temporary directory and
// makes the toolbox believe it is that executable, so runtime-relative
// discovery is tested without touching a real install.
func fakeRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "bin", "facet"+exeSuffix())
	writeExecutable(t, exe)
	previous := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = previous })
	return root
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a real program"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// sameFile compares files rather than spellings, which differ across
// symlinked temporary directories.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

func privateNodePath(root string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "dependencies", "node", "node.exe")
	}
	return filepath.Join(root, "dependencies", "node", "bin", "node")
}

func privatePiperPath(root string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "dependencies", "piper", "Scripts", "piper.exe")
	}
	return filepath.Join(root, "dependencies", "piper", "bin", "piper")
}

// The runtime's private copies win over PATH, so an installed Facet runs the
// Node and Piper it installed rather than whatever the shell happens to have.
func TestLookPathPrefersRuntimeDependencies(t *testing.T) {
	root := fakeRuntime(t)
	onPath := t.TempDir()
	for _, name := range []string{"node", "piper"} {
		writeExecutable(t, filepath.Join(onPath, name+exeSuffix()))
	}
	t.Setenv("PATH", onPath)

	for name, private := range map[string]string{"node": privateNodePath(root), "piper": privatePiperPath(root)} {
		got, err := lookPath(name)
		if err != nil || !sameFile(got, filepath.Join(onPath, name+exeSuffix())) {
			t.Fatalf("without a private %s, lookPath = %q, %v; want the PATH copy", name, got, err)
		}
		writeExecutable(t, private)
		if got, err := lookPath(name); err != nil || !sameFile(got, private) {
			t.Errorf("lookPath(%q) = %q, %v; want the runtime's private copy %q", name, got, err, private)
		}
	}
}

// Only what the installer places under dependencies is looked up there; any
// other program still comes from PATH.
func TestLookPathUsesRuntimeOnlyForInstalledDependencies(t *testing.T) {
	root := fakeRuntime(t)
	writeExecutable(t, filepath.Join(root, "dependencies", "ffmpeg", "ffmpeg"+exeSuffix()))
	writeExecutable(t, filepath.Join(root, "dependencies", "ffmpeg", "bin", "ffmpeg"+exeSuffix()))
	t.Setenv("PATH", t.TempDir())
	if got, err := lookPath("ffmpeg"); err == nil {
		t.Fatalf("ffmpeg resolved to %q from an undeclared runtime location", got)
	}
	if _, err := lookPath("definitely-not-a-real-binary-xyz"); err == nil {
		t.Fatal("an unknown binary resolved")
	}
}

// A directory, or on POSIX a file without an execute bit, is not a program.
func TestRuntimeDependencyMustBeAnExecutableFile(t *testing.T) {
	root := fakeRuntime(t)
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(privateNodePath(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPath("node"); err == nil {
		t.Fatalf("a directory was resolved as node: %q", got)
	}
	if runtime.GOOS != "windows" {
		piper := privatePiperPath(root)
		if err := os.MkdirAll(filepath.Dir(piper), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(piper, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := lookPath("piper"); err == nil {
			t.Fatalf("a non-executable file was resolved as piper: %q", got)
		}
	}
}

// The listing reports the binary a run would actually use.
func TestListingReportsRuntimeNode(t *testing.T) {
	root := fakeRuntime(t)
	t.Setenv("PATH", t.TempDir())
	node := privateNodePath(root)
	writeExecutable(t, node)
	deps, _ := summary("video_compose")["dependencies"].([]any)
	for _, d := range deps {
		m := d.(map[string]any)
		if m["name"] == "node" {
			path, _ := m["path"].(string)
			if !sameFile(path, node) || m["available"] != true || m["resolution"] != ResolutionSatisfied {
				t.Fatalf("node dependency does not report the runtime copy: %#v", m)
			}
			return
		}
	}
	t.Fatal("video_compose does not declare node")
}

func TestRuntimeRootIsParentOfExecutableDirectory(t *testing.T) {
	root := fakeRuntime(t)
	if got := runtimeRoot(); !sameFile(got, root) {
		t.Fatalf("runtimeRoot() = %q, want %q", got, root)
	}
}
