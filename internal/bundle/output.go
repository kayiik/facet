package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// ManifestName is the manifest written at the top of every bundle.
	ManifestName = "facet-bundle.json"
	// ManifestSchema identifies the manifest format. It is versioned
	// separately from Facet: a reader needs the manifest's shape before it
	// knows anything else about the bundle.
	ManifestSchema = "kayiik.facet.bundle/v2"
	legacySchema   = "kayiik.facet.bundle/v1"
)

// Entry is one file of a bundle, relative to the bundle directory.
type Entry struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
	Kind   string `json:"kind"`
}

// MCPServer is the server registration the guidance expects: the facet
// executable started with Args, registered under Name. A plugin bundle also
// carries that registration in the CLI's own format.
type MCPServer struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

// Manifest is a bundle's identity, provenance, and content.
type Manifest struct {
	Schema         string `json:"schema"`
	CapabilityID   string `json:"capability_id"`
	FacetVersion   string `json:"facet_version"`
	AdapterVersion string `json:"adapter_version"`
	Target         Target `json:"target"`
	Scope          Scope  `json:"scope"`
	// InstallBase is "home" for user scope, "project" for project scope and
	// "plugin" for plugin scope. Files are laid out relative to that base, so
	// copying the bundle directory's contents onto it installs the bundle; a
	// plugin bundle is the plugin directory itself.
	InstallBase string `json:"install_base"`
	// InstallRoot is the target's root relative to InstallBase, before any
	// environment override the CLI honours.
	InstallRoot string    `json:"install_root"`
	MCPServer   MCPServer `json:"mcp_server"`
	// Tools is the public vocabulary of the facet binary that built the
	// bundle. A bundle naming tools the running binary lacks is stale.
	Tools        []string `json:"tools,omitempty"`
	Files        []Entry  `json:"files"`
	BundleDigest string   `json:"bundle_digest"`
}

// Options select what Build projects.
type Options struct {
	Target  Target
	Scope   Scope
	Version string   // Facet version being projected
	Tools   []string // public tool vocabulary served by `facet mcp`
}

// Built reports a completed build.
type Built struct {
	Dir      string
	Manifest *Manifest
	Warnings []string
}

// Plan returns the manifest and bundle-relative files Build would write.
func Plan(opts Options) (*Manifest, []File, error) {
	if !opts.Target.valid() {
		return nil, nil, fmt.Errorf("unsupported target %q", opts.Target)
	}
	if !opts.Scope.valid() {
		return nil, nil, fmt.Errorf("unsupported scope %q", opts.Scope)
	}
	if !SupportsScope(opts.Target, opts.Scope) {
		if opts.Scope == ScopePlugin {
			return nil, nil, fmt.Errorf("%s has no plugin layout; plugins exist for claude, codex, and copilot", opts.Target)
		}
		return nil, nil, fmt.Errorf("%s is installed at user scope only; its MCP servers and approval rules live in its own configuration, not in a project", opts.Target)
	}
	if strings.TrimSpace(opts.Version) == "" {
		return nil, nil, fmt.Errorf("no Facet version supplied; a bundle without identity cannot be compared or upgraded")
	}
	files, err := Files(opts.Target)
	if err != nil {
		return nil, nil, err
	}
	if opts.Scope == ScopePlugin {
		extra, err := pluginFiles(opts.Target, opts.Version)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, extra...)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}
	root := DefaultRoot(opts.Target, opts.Scope)
	out := make([]File, len(files))
	entries := make([]Entry, len(files))
	for i, f := range files {
		if root != "" {
			f.Path = root + "/" + f.Path
		}
		out[i] = f
		entries[i] = Entry{Path: f.Path, Bytes: int64(len(f.Content)), Digest: f.Digest(), Kind: f.Kind}
	}
	base := "home"
	switch opts.Scope {
	case ScopeProject:
		base = "project"
	case ScopePlugin:
		base = "plugin"
	}
	tools := append([]string(nil), opts.Tools...)
	sort.Strings(tools)
	m := &Manifest{
		Schema:         ManifestSchema,
		CapabilityID:   CapabilityID,
		FacetVersion:   opts.Version,
		AdapterVersion: AdapterVersion,
		Target:         opts.Target,
		Scope:          opts.Scope,
		InstallBase:    base,
		InstallRoot:    root,
		MCPServer:      MCPServer{Name: MCPServerName, Args: MCPServerArgs()},
		Tools:          tools,
		Files:          entries,
	}
	m.BundleDigest = bundleDigest(entries)
	return m, out, nil
}

func bundleDigest(entries []Entry) string {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, e := range sorted {
		fmt.Fprintf(h, "%s\n%s\n", e.Path, e.Digest)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ConflictError reports files in an output directory that a build would
// have to delete or overwrite without owning them.
type ConflictError struct {
	Dir      string
	Problems []string
}

func (e *ConflictError) Error() string {
	shown := e.Problems
	if len(shown) > 10 {
		shown = append(append([]string(nil), shown[:10]...), fmt.Sprintf("... and %d more", len(e.Problems)-10))
	}
	return fmt.Sprintf("refusing to replace %s; it holds files facet bundle does not own:\n  %s\nMove them aside or choose another --out directory",
		e.Dir, strings.Join(shown, "\n  "))
}

// ownership is what an existing output directory's manifest says this
// package wrote there.
type ownership struct {
	exists bool
	owned  map[string]string // bundle-relative slash path -> digest
}

// Check reports whether dir can receive a bundle: it must be absent, empty,
// or hold only unmodified files recorded by an earlier facet bundle manifest.
func Check(dir string) error {
	_, err := inspectOutput(filepath.Clean(dir))
	return err
}

func inspectOutput(dir string) (*ownership, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return &ownership{owned: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s exists and is not a directory", dir)
	}
	own := &ownership{exists: true, owned: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	switch {
	case err == nil:
		recorded, derr := decodeOwnership(raw)
		if derr != nil {
			return nil, fmt.Errorf("refusing to replace %s: its %s cannot be read (%v)", dir, ManifestName, derr)
		}
		own.owned = recorded
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	var problems []string
	err = filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == dir || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !entry.Type().IsRegular() {
			problems = append(problems, rel+" (not a regular file)")
			return nil
		}
		if rel == ManifestName {
			return nil
		}
		want, ok := own.owned[rel]
		if !ok {
			problems = append(problems, rel+" (not written by facet bundle)")
			return nil
		}
		got, err := fileDigest(name)
		if err != nil {
			return err
		}
		if got != want {
			problems = append(problems, rel+" (modified since facet bundle wrote it)")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, &ConflictError{Dir: dir, Problems: problems}
	}
	return own, nil
}

// decodeOwnership reads the file list of a current or 1.x manifest.
func decodeOwnership(raw []byte) (map[string]string, error) {
	var doc struct {
		Schema  string  `json:"schema"`
		Files   []Entry `json:"files"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var entries []Entry
	switch doc.Schema {
	case ManifestSchema:
		entries = doc.Files
	case legacySchema:
		entries = doc.Entries
	default:
		return nil, fmt.Errorf("unknown manifest schema %q", doc.Schema)
	}
	owned := map[string]string{}
	for _, e := range entries {
		if !safeRel(e.Path) {
			return nil, fmt.Errorf("manifest entry %q is not a safe relative path", e.Path)
		}
		owned[e.Path] = e.Digest
	}
	return owned, nil
}

// safeRel reports whether p is a clean, relative, slash-separated path that
// stays inside its base.
func safeRel(p string) bool {
	if p == "" || strings.Contains(p, "\\") || path.IsAbs(p) || filepath.IsAbs(filepath.FromSlash(p)) || filepath.VolumeName(filepath.FromSlash(p)) != "" {
		return false
	}
	clean := path.Clean(p)
	return clean == p && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func fileDigest(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Build writes the bundle selected by opts into dir.
//
// The bundle is assembled in a staging directory beside dir and moved into
// place with renames, so dir always holds either the previous complete bundle
// or the new one. An existing dir is replaced only when every file in it is
// recorded, unmodified, by an earlier facet bundle manifest; anything else is
// refused before a byte is written.
func Build(opts Options, dir string) (*Built, error) {
	m, files, err := Plan(opts)
	if err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	own, err := inspectOutput(dir)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", parent, err)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".facet-stage-")
	if err != nil {
		return nil, fmt.Errorf("creating a staging directory: %w", err)
	}
	staged := true
	defer func() {
		if staged {
			// The staging directory is fresh and holds only what this call wrote.
			_ = os.RemoveAll(stage)
		}
	}()
	for _, f := range files {
		name := filepath.Join(stage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(name, f.Content, 0o644); err != nil {
			return nil, err
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, ManifestName), append(raw, '\n'), 0o644); err != nil {
		return nil, err
	}

	built := &Built{Dir: dir, Manifest: m}
	if !own.exists {
		if err := os.Rename(stage, dir); err != nil {
			return nil, fmt.Errorf("moving the bundle into %s: %w", dir, err)
		}
		staged = false
		return built, nil
	}
	backup, err := freeSibling(dir, ".facet-old-")
	if err != nil {
		return nil, err
	}
	if err := os.Rename(dir, backup); err != nil {
		return nil, fmt.Errorf("replacing %s: %w", dir, err)
	}
	if err := os.Rename(stage, dir); err != nil {
		if rerr := os.Rename(backup, dir); rerr != nil {
			return nil, fmt.Errorf("moving the bundle into %s: %v; restoring the previous bundle also failed (%v), it is in %s", dir, err, rerr, backup)
		}
		return nil, fmt.Errorf("moving the bundle into %s: %w", dir, err)
	}
	staged = false
	if left := removeOwnedTree(backup, own.owned); len(left) > 0 {
		built.Warnings = append(built.Warnings, fmt.Sprintf("kept %s because it holds files facet bundle does not own: %s", backup, strings.Join(left, ", ")))
	}
	return built, nil
}

// freeSibling returns an unused hidden path beside dir.
func freeSibling(dir, infix string) (string, error) {
	parent, base := filepath.Dir(dir), filepath.Base(dir)
	for i := 0; i < 100; i++ {
		name := filepath.Join(parent, "."+base+infix+strconv.FormatInt(time.Now().UnixNano(), 36)+strconv.Itoa(i))
		if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no free name beside %s", dir)
}

// removeOwnedTree deletes the manifest and the files whose digests still
// match the record, then any directories left empty. It returns the files it
// kept.
func removeOwnedTree(dir string, owned map[string]string) []string {
	var kept []string
	var dirs []string
	_ = filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		rel, _ := filepath.Rel(dir, name)
		rel = filepath.ToSlash(rel)
		if entry.Type().IsRegular() {
			if rel == ManifestName {
				if os.Remove(name) == nil {
					return nil
				}
			} else if want, ok := owned[rel]; ok {
				if got, err := fileDigest(name); err == nil && got == want && os.Remove(name) == nil {
					return nil
				}
			}
		}
		kept = append(kept, rel)
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // succeeds only for directories left empty
	}
	sort.Strings(kept)
	return kept
}

// ReadManifest decodes a bundle's manifest without verifying the bundle.
func ReadManifest(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("reading the manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("the manifest does not decode: %w", err)
	}
	return &m, nil
}

// Expect states what a verified bundle must match beyond its own integrity.
// Zero fields are not checked.
type Expect struct {
	Target Target
	Scope  Scope
	// Version is the running Facet version. A bundle built by another
	// version is stale for this binary.
	Version string
	// Tools is the running binary's tool vocabulary. A bundle naming a tool
	// outside it documents tools that cannot be called.
	Tools []string
	// Current requires the bundle content to equal what the running binary
	// projects for the bundle's target and scope.
	Current bool
}

// Verify checks a bundle against its manifest and against want.
//
// It answers "is this bundle intact, complete, and right for this facet",
// which is a different claim from "the build returned without error".
func Verify(dir string, want Expect) (*Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}
	switch {
	case m.Schema == legacySchema:
		return nil, fmt.Errorf("the bundle was built by Facet 1.x (%s); rebuild it with this facet", legacySchema)
	case m.Schema != ManifestSchema:
		return nil, fmt.Errorf("manifest schema %q, want %q", m.Schema, ManifestSchema)
	case m.CapabilityID != CapabilityID:
		return nil, fmt.Errorf("capability %q, want %q", m.CapabilityID, CapabilityID)
	case !m.Target.valid():
		return nil, fmt.Errorf("unsupported bundle target %q", m.Target)
	case !m.Scope.valid():
		return nil, fmt.Errorf("unsupported bundle scope %q", m.Scope)
	case want.Target != "" && m.Target != want.Target:
		return nil, fmt.Errorf("bundle target %q, want %q", m.Target, want.Target)
	case want.Scope != "" && m.Scope != want.Scope:
		return nil, fmt.Errorf("bundle scope %q, want %q", m.Scope, want.Scope)
	case len(m.Files) == 0:
		return nil, fmt.Errorf("the manifest declares no files")
	}
	declared := map[string]bool{}
	for _, e := range m.Files {
		if !safeRel(e.Path) {
			return nil, fmt.Errorf("manifest entry %q is not a safe relative path", e.Path)
		}
		declared[e.Path] = true
		name := filepath.Join(dir, filepath.FromSlash(e.Path))
		info, err := os.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("declared file %s is missing: %w", e.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("declared file %s is not a regular file", e.Path)
		}
		if info.Size() != e.Bytes {
			return nil, fmt.Errorf("%s is %d bytes, the manifest says %d", e.Path, info.Size(), e.Bytes)
		}
		got, err := fileDigest(name)
		if err != nil {
			return nil, err
		}
		if got != e.Digest {
			return nil, fmt.Errorf("%s digest %s, the manifest says %s", e.Path, got, e.Digest)
		}
	}
	if got := bundleDigest(m.Files); got != m.BundleDigest {
		return nil, fmt.Errorf("bundle digest %s, the manifest says %s", got, m.BundleDigest)
	}
	var undeclared []string
	err = filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, name)
		rel = filepath.ToSlash(rel)
		if rel != ManifestName && !declared[rel] {
			undeclared = append(undeclared, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return nil, fmt.Errorf("files not declared in the manifest: %s", strings.Join(undeclared, ", "))
	}
	if want.Version != "" && m.FacetVersion != want.Version {
		return nil, fmt.Errorf("the bundle was built by facet v%s but this is facet v%s; rebuild it", m.FacetVersion, want.Version)
	}
	if want.Tools != nil {
		running := map[string]bool{}
		for _, name := range want.Tools {
			running[name] = true
		}
		for _, name := range m.Tools {
			if !running[name] {
				return nil, fmt.Errorf("the bundle names tool %q, which this facet does not provide", name)
			}
		}
	}
	if want.Current {
		current, _, err := Plan(Options{Target: m.Target, Scope: m.Scope, Version: m.FacetVersion})
		if err != nil {
			return nil, err
		}
		if current.BundleDigest != m.BundleDigest {
			return nil, fmt.Errorf("the bundle content differs from what this facet projects for %s (%s scope); rebuild it", m.Target, m.Scope)
		}
	}
	return m, nil
}
