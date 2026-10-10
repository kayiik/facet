package journeys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// v110MediaRoute is the media route the withdrawn 1.x app mounted; tests use
// it to compare reported URLs with the 1.x ones.
const v110MediaRoute = "/api/media/"

func workspaceOptions(root string) Options {
	return Options{Workspace: root, MediaURLPrefix: v110MediaRoute}
}

// catalogOptions places the catalog and the productions root in their own
// app-owned state directory, outside every project.
func catalogOptions(t *testing.T) Options {
	t.Helper()
	state := t.TempDir()
	return Options{
		CatalogPath:     filepath.Join(state, "catalog.json"),
		ProductionsRoot: filepath.Join(state, "productions"),
		MediaURLPrefix:  v110MediaRoute,
	}
}

func mustWriteTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func mustReadTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func makeTestSymlink(t *testing.T, target, link string) bool {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink %q -> %q: %v", link, target, err)
		return false
	}
	return true
}

// makeTestJunction creates a Windows directory junction, which, unlike a
// symbolic link, needs no privilege. Elsewhere the test is skipped.
func makeTestJunction(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("directory junctions are a Windows feature")
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(link), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create junction %q -> %q: %v %s", link, target, err, output)
	}
}

func sha256Hex(contents string) string {
	sum := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(sum[:])
}

func mustCanonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := canonicalExistingPath(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return resolved
}

// serveMediaURL plays the host: it decodes a reported media URL the way an
// HTTP router would, strips the media route and resolves the reference.
func serveMediaURL(t *testing.T, opts Options, mediaURL string) (Media, error) {
	t.Helper()
	parsed, err := url.Parse(mediaURL)
	if err != nil {
		t.Fatalf("parse media URL %q: %v", mediaURL, err)
	}
	ref, ok := strings.CutPrefix(parsed.Path, opts.MediaURLPrefix)
	if !ok {
		t.Fatalf("media URL %q is not under %q", mediaURL, opts.MediaURLPrefix)
	}
	return ResolveMediaRef(opts, ref)
}

// entryState is everything a write into a tree would change.
type entryState struct {
	Mode    fs.FileMode
	Size    int64
	ModTime time.Time
	Digest  string
}

// snapshotTree records every entry under root, root included.
func snapshotTree(t *testing.T, root string) map[string]entryState {
	t.Helper()
	states := make(map[string]entryState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		state := entryState{Mode: info.Mode(), ModTime: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.Size = info.Size()
			state.Digest = sha256Hex(string(data))
		}
		states[filepath.ToSlash(rel)] = state
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return states
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]entryState) {
	t.Helper()
	after := snapshotTree(t, root)
	for name, state := range after {
		previous, ok := before[name]
		if !ok {
			t.Errorf("%s was created inside %s", name, root)
			continue
		}
		if previous != state {
			t.Errorf("%s changed inside %s: before %+v, after %+v", name, root, previous, state)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			t.Errorf("%s was removed from %s", name, root)
		}
	}
}

// ageTree moves every modification time under root into the past, so a
// write during the test is visible even on coarse-grained file systems.
func ageTree(t *testing.T, root string) {
	t.Helper()
	past := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		return os.Chtimes(path, past, past)
	})
	if err != nil {
		t.Fatalf("age %s: %v", root, err)
	}
	// Directories last and deepest first: touching a child must not undo
	// its parent's time.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(dirs[i], past, past); err != nil {
			t.Fatalf("age %s: %v", dirs[i], err)
		}
	}
}
