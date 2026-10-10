package journeys

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ImportDir is the project folder ImportMedia stores files in; project
// details list it as Assets.
const ImportDir = "assets"

// importExts are the file types ImportMedia accepts: video, audio and
// images. The assets/ listing uses the same list, so whatever is imported is
// listed.
var importExts = []string{".mp4", ".mov", ".webm", ".mp3", ".wav", ".ogg", ".m4a", ".aac", ".png", ".jpg", ".jpeg", ".webp"}

// maxStem bounds the part of an imported file's name before its extension,
// in characters.
const maxStem = 100

// ImportRequest says how ImportMedia stores one file.
type ImportRequest struct {
	// Name is the file's name on the person's disk; only a safe form of its
	// base name is kept (SafeMediaName).
	Name string
	// IfExists says what happens when assets/ already has a file of that
	// name: "" refuses with ErrConflict, "replace" replaces it, and "keep"
	// keeps both, storing the new file under the first free numbered name.
	IfExists string
	// MaxBytes bounds the file's size; a larger file is refused with
	// ErrTooLarge. Zero means no bound.
	MaxBytes int64
}

// Imported is the file ImportMedia stored.
type Imported struct {
	// Name is the stored file's project-relative path, assets/<file>.
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Replaced reports that an earlier file of that name was replaced.
	Replaced bool `json:"replaced"`
}

// ImportMedia stores a file the person brings into the project at
// projectDir: the contents read from r go into the project's assets/ folder,
// under a safe form of req.Name. Nothing is written anywhere else: assets/
// may not lead out of the project through a link, junction or mount point,
// and the file takes its name only once it is complete, so a refused or
// failed import leaves nothing behind. An existing file is replaced only
// when req.IfExists says so, and never when it is a link or a directory.
func ImportMedia(projectDir string, req ImportRequest, r io.Reader) (*Imported, error) {
	name, err := SafeMediaName(req.Name)
	if err != nil {
		return nil, err
	}
	switch req.IfExists {
	case "", "replace", "keep":
	default:
		return nil, fmt.Errorf("%w: for a file that exists, choose replace or keep, not %q", ErrInvalidRequest, req.IfExists)
	}
	if req.MaxBytes < 0 {
		return nil, fmt.Errorf("%w: the size limit is negative", ErrInvalidRequest)
	}
	_, root, err := projectRoots(projectDir)
	if err != nil {
		return nil, err
	}
	dir, err := ensureProjectDir(root, ImportDir)
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(dir, ".import-*.part")
	if err != nil {
		return nil, err
	}
	published := false
	defer func() {
		if !published {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	source := r
	if req.MaxBytes > 0 {
		// One byte past the limit is enough to know the file is over it.
		source = io.LimitReader(r, req.MaxBytes+1)
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), source)
	if err != nil {
		return nil, fmt.Errorf("the file did not arrive whole: %w", err)
	}
	if req.MaxBytes > 0 && size > req.MaxBytes {
		return nil, fmt.Errorf("%w: %s is larger than the %d bytes a file may have", ErrTooLarge, name, req.MaxBytes)
	}
	if size == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrInvalidRequest, name)
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	stored, replaced, err := publishImport(tmp.Name(), dir, name, req.IfExists)
	if err != nil {
		return nil, err
	}
	published = true
	return &Imported{Name: ImportDir + "/" + stored, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Replaced: replaced}, nil
}

// publishImport gives the complete file at tmp its name in dir, and returns
// the name and whether it replaced a file. A free name is claimed by creating
// it exclusively, so two imports, or an import and another program, never
// end up writing one file; the claimed, empty placeholder is then replaced
// by the complete file in a single rename.
func publishImport(tmp, dir, name, ifExists string) (string, bool, error) {
	if ifExists == "replace" {
		info, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case err == nil && !info.Mode().IsRegular():
			return "", false, fmt.Errorf("%w: %s/%s exists and is not a file", ErrConflict, ImportDir, name)
		case err == nil:
			if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
				return "", false, err
			}
			return name, true, nil
		case !missing(err):
			return "", false, err
		}
		// Nothing to replace: the name is claimed like any other.
	}
	candidate := name
	for n := 2; ; n++ {
		target := filepath.Join(dir, candidate)
		// Lstat first: an exclusive create is not trusted to treat a
		// dangling link as taken on every platform.
		_, err := os.Lstat(target)
		if missing(err) {
			var placeholder *os.File
			placeholder, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err == nil {
				placeholder.Close()
				if err := os.Rename(tmp, target); err != nil {
					os.Remove(target)
					return "", false, err
				}
				return candidate, false, nil
			}
		}
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return "", false, err
		}
		if ifExists != "keep" {
			return "", false, fmt.Errorf("%w: %s/%s is already in the project; replace it or keep both", ErrConflict, ImportDir, name)
		}
		if n > 999 {
			return "", false, fmt.Errorf("%w: the project has too many files named like %s", ErrConflict, name)
		}
		ext := filepath.Ext(name)
		candidate = strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(n) + ext
	}
}

// SafeMediaName returns the name ImportMedia stores a file under: the base
// name of name, every run of characters other than letters, digits and
// underscores turned into one hyphen, at most 100 characters before the
// extension, and the extension in lower case. Such a name cannot step out of
// a folder, hide (no leading dot), or name a Windows device. The extension
// must be a video, audio or image type ImportMedia accepts; anything else is
// refused with ErrUnsupportedMedia.
func SafeMediaName(name string) (string, error) {
	base := strings.TrimSpace(name)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if !utf8.ValidString(base) {
		return "", fmt.Errorf("%w: the file name is not valid text", ErrInvalidRequest)
	}
	ext := filepath.Ext(base)
	stemPart := base[:len(base)-len(ext)]
	ext = strings.ToLower(ext)
	if !containsExt(importExts, ext) {
		return "", fmt.Errorf("%w: %q is not a video, audio or image file the App imports (%s)", ErrUnsupportedMedia, base, strings.Join(importExts, " "))
	}
	var stem strings.Builder
	runes, hyphen := 0, false
	for _, r := range stemPart {
		if runes >= maxStem {
			break
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || (unicode.IsMark(r) && stem.Len() > 0 && !hyphen):
			if hyphen && stem.Len() > 0 {
				stem.WriteByte('-')
				runes++
			}
			hyphen = false
			stem.WriteRune(r)
			runes++
		default:
			hyphen = true
		}
	}
	safe := stem.String()
	if safe == "" {
		safe = "media"
	}
	if reservedDeviceName(safe) {
		safe += "-file"
	}
	return safe + ext, nil
}

// reservedDeviceName reports a name Windows gives to a device whatever its
// extension, such as CON, NUL or COM1. (The superscript-digit forms cannot
// survive SafeMediaName, which keeps only decimal digits.)
func reservedDeviceName(stem string) bool {
	upper := strings.ToUpper(stem)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && '0' <= upper[3] && upper[3] <= '9'
}
