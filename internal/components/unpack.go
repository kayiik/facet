package components

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// maxUnpacked bounds what one archive may unpack to, against a malformed or
// hostile archive that passed its checksum only because its pin was wrong.
const maxUnpacked = 4 << 30

// unpack places what d takes from archive under dir.
func unpack(archive, dir string, d download) error {
	if d.Raw != "" {
		return placeRaw(archive, dir, d.Raw)
	}
	var err error
	switch {
	case strings.HasSuffix(d.Name, ".zip"):
		err = unzip(archive, dir, d)
	case strings.HasSuffix(d.Name, ".tar.gz"), strings.HasSuffix(d.Name, ".tgz"):
		err = untar(archive, dir, d)
	default:
		err = fmt.Errorf("%s: unknown archive type", d.Name)
	}
	if err != nil {
		return fmt.Errorf("unpack %s: %w", d.Name, err)
	}
	for want := range d.Files {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(d.Files[want]))); err != nil {
			return fmt.Errorf("unpack %s: it has no %s", d.Name, want)
		}
	}
	return nil
}

func placeRaw(archive, dir, name string) error {
	dest, err := within(dir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Rename(archive, dest)
}

// target maps an archive path to its place under dir, or "" to skip it.
func target(dir, name string, d download) (string, error) {
	name = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
	parts := strings.Split(name, "/")
	if len(parts) <= d.Strip {
		return "", nil
	}
	rel := strings.Join(parts[d.Strip:], "/")
	if d.Files != nil {
		mapped, ok := d.Files[rel]
		if !ok {
			return "", nil
		}
		rel = mapped
	}
	return within(dir, rel)
}

// within joins rel under dir and refuses a path that would leave it.
func within(dir, rel string) (string, error) {
	if rel == "" || !fs.ValidPath(rel) {
		return "", fmt.Errorf("refusing archive path %q", rel)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

func fileMode(mode fs.FileMode) fs.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func unzip(archive, dir string, d download) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	var written int64
	for _, f := range r.File {
		dest, err := target(dir, f.Name, d)
		if err != nil || dest == "" {
			if err != nil {
				return err
			}
			continue
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(rc, 4096))
			rc.Close()
			if err != nil {
				return err
			}
			if err := symlink(dir, dest, string(link)); err != nil {
				return err
			}
		default:
			rc, err := f.Open()
			if err != nil {
				return err
			}
			n, err := writeFile(dest, rc, fileMode(mode), maxUnpacked-written)
			rc.Close()
			if err != nil {
				return err
			}
			written += n
		}
	}
	return nil
}

func untar(archive, dir string, d download) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var written int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		dest, err := target(dir, h.Name, d)
		if err != nil {
			return err
		}
		if dest == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := symlink(dir, dest, h.Linkname); err != nil {
				return err
			}
		case tar.TypeLink:
			source, err := target(dir, h.Linkname, d)
			if err != nil || source == "" {
				return fmt.Errorf("hard link %s to %s leaves the archive", h.Name, h.Linkname)
			}
			in, err := os.Open(source)
			if err != nil {
				return err
			}
			n, err := writeFile(dest, in, fileMode(h.FileInfo().Mode()), maxUnpacked-written)
			in.Close()
			if err != nil {
				return err
			}
			written += n
		case tar.TypeReg:
			n, err := writeFile(dest, tr, fileMode(h.FileInfo().Mode()), maxUnpacked-written)
			if err != nil {
				return err
			}
			written += n
		}
	}
}

func writeFile(dest string, r io.Reader, mode fs.FileMode, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(r, budget+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > budget {
		err = errors.New("the archive unpacks to more than Facet allows")
	}
	return n, err
}

// symlink recreates a link whose target stays inside dir.
func symlink(dir, dest, link string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("the archive has a symbolic link (%s), which Facet does not unpack on Windows", dest)
	}
	if filepath.IsAbs(link) {
		return fmt.Errorf("refusing absolute link %s -> %s", dest, link)
	}
	resolved := filepath.Join(filepath.Dir(dest), link)
	if rel, err := filepath.Rel(dir, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing link %s -> %s, which leaves the archive", dest, link)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, dest)
}
