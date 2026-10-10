package journeys

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Sentinel errors let a host map journey failures onto its own responses.
// Every error the package returns for these conditions wraps one of them, so
// test with errors.Is.
var (
	// ErrInvalidRequest reports a request that cannot be carried out as
	// stated: a missing or malformed value, an unknown decision, an unusable
	// project name.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrNotFound reports a project or project file that does not exist, is
	// not a regular file, or is an empty video.
	ErrNotFound = errors.New("not found")
	// ErrOutsideProject reports a path that does not stay inside a project:
	// "..", a drive letter, an absolute path elsewhere, a link that leaves
	// the project, or a media reference that names no known project.
	ErrOutsideProject = errors.New("path is outside the project")
	// ErrUnsupportedMedia reports a project file whose type is never served,
	// or an imported file whose type is not imported.
	ErrUnsupportedMedia = errors.New("file type is not served")
	// ErrConflict reports a project folder or catalog ID that already exists.
	ErrConflict = errors.New("already exists")
	// ErrTooLarge reports an imported file larger than its size limit.
	ErrTooLarge = errors.New("file is too large")
)

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// resolvePathAllowMissing resolves symbolic links like filepath.EvalSymlinks
// but tolerates a missing tail: the longest existing prefix is resolved and
// the missing components are appended unchanged.
func resolvePathAllowMissing(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	tail := ""
	current := abs
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if tail == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, tail), nil
		}
		if !missing(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		if tail == "" {
			tail = filepath.Base(current)
		} else {
			tail = filepath.Join(filepath.Base(current), tail)
		}
		current = parent
	}
}

// missing reports whether err means the path does not exist, including a
// path whose parent is a file.
func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func samePath(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	return err == nil && rel == "."
}

func pathWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathStrictlyWithin(base, target string) bool {
	return !samePath(base, target) && pathWithin(base, target)
}

// isJunction reports a Windows junction or mount point. Lstat reports one as
// an irregular non-directory and filepath.EvalSymlinks does not follow it, so
// a containment check on the resolved path alone would let it lead anywhere.
func isJunction(info fs.FileInfo) bool {
	return info.Mode()&fs.ModeIrregular != 0 && !info.IsDir()
}

// junctionWithin reports whether a component of dir, a path relative to root,
// is a junction or mount point.
func junctionWithin(root, dir string) bool {
	current := root
	for _, part := range strings.Split(filepath.Clean(dir), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return false
		}
		if isJunction(info) {
			return true
		}
	}
	return false
}

// writeFileAtomic replaces path with data. A crash leaves either the old file
// or the new one, never a torn mix, and an existing symbolic link at path is
// replaced rather than followed.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (returnErr error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if returnErr != nil {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}
