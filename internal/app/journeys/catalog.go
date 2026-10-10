package journeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// catalogMu serializes every catalog read-modify-write in this process.
var catalogMu sync.Mutex

const catalogVersion = "1.0"

// Options names every location the journeys read or write. Nothing is derived
// from the environment, the user's home directory or OS application-data
// folders; relative paths resolve against the working directory, so hosts
// should pass absolute ones.
type Options struct {
	// Workspace is a directory whose projects/ subdirectories are listed as
	// projects. Empty lists catalog projects only.
	Workspace string
	// CatalogPath is the app-owned catalog file. It must live outside every
	// project. Empty means no catalog: listing skips it and catalog
	// operations fail.
	CatalogPath string
	// ProductionsRoot is the parent directory CreateProject uses when a
	// request names none.
	ProductionsRoot string
	// MediaURLPrefix, when set, fills the evidence URL fields with each media
	// reference URL-escaped under this prefix, for example "/api/media/". The
	// package never serves media; a host mounts ResolveMediaRef there.
	MediaURLPrefix string
}

// Catalog tracks the projects the app has created or opened.
type Catalog struct {
	Version string `json:"version"`
	// DefaultRoot is where a new project goes when a request names no
	// directory: Options.ProductionsRoot when set, else the stored value.
	DefaultRoot string           `json:"default_root"`
	Projects    []CatalogProject `json:"projects"`
}

// CatalogProject is one catalog entry. ID is unique within the catalog and is
// the slug the project is listed and served under.
type CatalogProject struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Engine       string    `json:"engine"`
	Packs        []string  `json:"packs"`
	CreatedAt    time.Time `json:"created_at"`
	LastOpenedAt time.Time `json:"last_opened_at"`
	// Exists reports whether Path was a directory when the catalog was loaded.
	Exists bool `json:"exists"`
}

// NewProject describes a project for CreateProject.
type NewProject struct {
	// Name is the display name; empty means "Untitled Production".
	Name string
	// Slug is the folder name and catalog ID; empty derives one from Name.
	Slug string
	// Dir is the parent directory; empty means Options.ProductionsRoot, then
	// the catalog's stored default root.
	Dir string
	// Engine optionally labels the harness that works on the project.
	Engine string
	// Packs optionally records production-method pack IDs.
	Packs []string
}

// projectLock is the part of a 1.x facet.lock.json that OpenProject reads.
type projectLock struct {
	Engine string   `json:"engine"`
	Packs  []string `json:"packs"`
}

func (o Options) catalogFile() (string, error) {
	if strings.TrimSpace(o.CatalogPath) == "" {
		return "", fmt.Errorf("%w: no catalog path is configured", ErrInvalidRequest)
	}
	return filepath.Abs(o.CatalogPath)
}

// catalogWithin reports whether the catalog file lies inside dir, where
// writing it would write into the project.
func (o Options) catalogWithin(dir string) (bool, error) {
	catalog, err := o.catalogFile()
	if err != nil {
		return false, err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	if pathWithin(absDir, catalog) {
		return true, nil
	}
	resolvedCatalog, err := resolvePathAllowMissing(catalog)
	if err != nil {
		return false, err
	}
	resolvedDir, err := resolvePathAllowMissing(absDir)
	if err != nil {
		return false, err
	}
	return pathWithin(resolvedDir, resolvedCatalog), nil
}

func catalogID(p CatalogProject) string {
	if p.ID != "" {
		return p.ID
	}
	return filepath.Base(p.Path)
}

// lookup finds a project by ID, then by folder name, ignoring case.
func (c *Catalog) lookup(slug string) (CatalogProject, bool) {
	for _, p := range c.Projects {
		if strings.EqualFold(catalogID(p), slug) {
			return p, true
		}
	}
	for _, p := range c.Projects {
		if strings.EqualFold(filepath.Base(p.Path), slug) {
			return p, true
		}
	}
	return CatalogProject{}, false
}

// LoadCatalog reads the catalog at Options.CatalogPath. A missing file is an
// empty catalog; a file that is not valid JSON is an error and is left
// untouched rather than replaced. Each project's Exists is checked after the
// catalog lock is released: a folder on an unreachable drive may take long
// to answer, and must not hold up every other catalog operation.
func LoadCatalog(opts Options) (*Catalog, error) {
	cat, err := readCatalog(opts)
	if err != nil {
		return nil, err
	}
	for i := range cat.Projects {
		cat.Projects[i].Exists = folderExists(cat.Projects[i].Path)
	}
	return cat, nil
}

// readCatalog reads the catalog without checking its folders, for a request
// that needs one project and can check that one itself.
func readCatalog(opts Options) (*Catalog, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return loadCatalog(opts)
}

// folderExists reports whether path is a directory now.
func folderExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// loadCatalog reads the catalog file; the caller holds catalogMu. It checks
// no folder: callers that need Exists set it themselves.
func loadCatalog(opts Options) (*Catalog, error) {
	path, err := opts.catalogFile()
	if err != nil {
		return nil, err
	}
	cat := &Catalog{Version: catalogVersion, Projects: []CatalogProject{}}
	data, err := os.ReadFile(path)
	if err != nil && !missing(err) {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, cat); err != nil {
			return nil, fmt.Errorf("catalog %s is not valid JSON and was left untouched: %w", path, err)
		}
	}
	if cat.Version == "" {
		cat.Version = catalogVersion
	}
	if cat.Projects == nil {
		cat.Projects = []CatalogProject{}
	}
	if root := strings.TrimSpace(opts.ProductionsRoot); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			cat.DefaultRoot = abs
		}
	}
	return cat, nil
}

// SaveCatalog writes the catalog atomically to Options.CatalogPath, creating
// its parent directory when needed.
func SaveCatalog(opts Options, cat *Catalog) error {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	return saveCatalog(opts, cat)
}

func saveCatalog(opts Options, cat *Catalog) error {
	if cat == nil {
		return fmt.Errorf("%w: no catalog to save", ErrInvalidRequest)
	}
	path, err := opts.catalogFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o644)
}

// RegisterProject adds an existing project directory to the catalog, or
// refreshes the entry already registered for it: LastOpenedAt is renewed and
// a non-empty name, engine or pack list replaces the stored one. A new entry's
// ID is derived from name (the folder name when empty) and made unique.
//
// When the catalog cannot be saved the entry is returned together with the
// error, so the caller sees what was registered and that it did not persist.
func RegisterProject(opts Options, name, dir, engine string, packs []string) (*CatalogProject, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	cat, err := loadCatalog(opts)
	if err != nil {
		return nil, err
	}
	return registerLocked(opts, cat, name, dir, engine, packs, "")
}

// registerLocked registers dir in cat and saves it. The caller holds catalogMu.
func registerLocked(opts Options, cat *Catalog, name, dir, engine string, packs []string, id string) (*CatalogProject, error) {
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: directory does not exist: %s", ErrNotFound, dir)
	}
	inside, err := opts.catalogWithin(absPath)
	if err != nil {
		return nil, err
	}
	if inside {
		return nil, fmt.Errorf("%w: the catalog %s lies inside the project %s; the catalog is app-owned and must live outside every project", ErrInvalidRequest, opts.CatalogPath, absPath)
	}

	now := time.Now().UTC()
	var found *CatalogProject
	for i := range cat.Projects {
		p := &cat.Projects[i]
		if sameProjectPath(p.Path, absPath) {
			p.LastOpenedAt = now
			if name != "" {
				p.Name = name
			}
			if engine != "" {
				p.Engine = engine
			}
			if len(packs) > 0 {
				p.Packs = packs
			}
			p.Exists = true
			found = p
			break
		}
	}

	if found == nil {
		if name == "" {
			name = filepath.Base(absPath)
		}
		if id == "" {
			id = uniqueCatalogID(cat, slugify(name))
		}
		entry := CatalogProject{
			ID:           id,
			Name:         name,
			Path:         absPath,
			Engine:       engine,
			Packs:        packs,
			CreatedAt:    now,
			LastOpenedAt: now,
			Exists:       true,
		}
		cat.Projects = append([]CatalogProject{entry}, cat.Projects...)
		found = &cat.Projects[0]
	}
	registered := *found

	// The save is the only thing that makes the registration outlive the
	// process, so its failure is the caller's to see: a project reported as
	// registered but absent from disk would vanish on restart.
	if err := saveCatalog(opts, cat); err != nil {
		return &registered, fmt.Errorf("project %q was registered but the catalog could not be saved: %w", registered.Name, err)
	}
	return &registered, nil
}

// CreateProject creates a new, empty project directory and registers it with
// its folder name as catalog ID. Nothing is written into the directory. An
// existing folder is never reused (open it instead), and when the catalog
// cannot be saved the new, still-empty directory is removed again.
func CreateProject(opts Options, req NewProject) (*CatalogProject, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Untitled Production"
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if !filepath.IsLocal(slug) || strings.ContainsAny(slug, `/\`) || slug == "." || validateProjectSlug(slug) != nil {
		return nil, fmt.Errorf("%w: project folder name must be a single local directory name", ErrInvalidRequest)
	}

	catalogMu.Lock()
	defer catalogMu.Unlock()
	cat, err := loadCatalog(opts)
	if err != nil {
		return nil, err
	}

	baseDir := strings.TrimSpace(req.Dir)
	if baseDir == "" {
		baseDir = cat.DefaultRoot
	}
	if baseDir == "" {
		return nil, fmt.Errorf("%w: no parent directory: name one or configure Options.ProductionsRoot", ErrInvalidRequest)
	}
	baseDir, err = filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	targetDir := filepath.Join(baseDir, slug)

	inside, err := opts.catalogWithin(targetDir)
	if err != nil {
		return nil, err
	}
	if inside {
		return nil, fmt.Errorf("%w: the catalog %s would lie inside the new project %s", ErrInvalidRequest, opts.CatalogPath, targetDir)
	}
	kept := cat.Projects[:0:0]
	for _, p := range cat.Projects {
		sameID := strings.EqualFold(catalogID(p), slug)
		samePath := sameProjectPath(p.Path, targetDir)
		// Only the entries the new project could clash with are checked on
		// disk, while the catalog is locked.
		exists := (sameID || samePath) && folderExists(p.Path)
		if sameID && exists {
			return nil, fmt.Errorf("%w: the catalog already has project %q at %s; open it or choose another folder name", ErrConflict, slug, p.Path)
		}
		if !exists && (sameID || samePath) {
			// The directory behind this entry is gone; the new project
			// takes over its ID and folder instead of being shadowed.
			continue
		}
		kept = append(kept, p)
	}

	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: a project folder with this name already exists; use Open to resume it", ErrConflict)
		}
		return nil, fmt.Errorf("failed to create project directory: %w", err)
	}

	cat.Projects = kept
	project, err := registerLocked(opts, cat, name, targetDir, strings.TrimSpace(req.Engine), req.Packs, slug)
	if err != nil {
		if removeErr := os.Remove(targetDir); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("the new project directory %s could not be removed: %w", targetDir, removeErr))
		}
		return nil, err
	}
	return project, nil
}

// OpenProject registers an existing folder; a new entry is named after the
// folder, and an existing entry keeps its name. It reads a 1.x
// facet.lock.json for the engine (when none is given) and packs, and writes
// nothing into the folder: the only write is the app-owned catalog, which
// must live outside it.
func OpenProject(opts Options, dir, engine string) (*CatalogProject, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: choose a project directory", ErrInvalidRequest)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: directory does not exist: %s", ErrNotFound, dir)
	}
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	engine = strings.TrimSpace(engine)
	var packs []string
	if lock, ok := readProjectLock(absPath); ok {
		if engine == "" {
			engine = lock.Engine
		}
		if len(lock.Packs) > 0 {
			packs = lock.Packs
		}
	}

	catalogMu.Lock()
	defer catalogMu.Unlock()
	cat, err := loadCatalog(opts)
	if err != nil {
		return nil, err
	}
	return registerLocked(opts, cat, "", absPath, engine, packs, "")
}

// readProjectLock reads facet.lock.json from the project root, ignoring a
// lock that is missing, invalid or a link leaving the project.
func readProjectLock(projectDir string) (projectLock, bool) {
	target, err := resolveProjectFile(projectDir, "facet.lock.json")
	if err != nil {
		return projectLock{}, false
	}
	data, err := os.ReadFile(target.path)
	if err != nil {
		return projectLock{}, false
	}
	var lock projectLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return projectLock{}, false
	}
	lock.Engine = strings.TrimSpace(lock.Engine)
	return lock, true
}

// sameProjectPath reports whether two catalog paths name the same directory.
func sameProjectPath(a, b string) bool {
	if a == b {
		return true
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(aInfo, bInfo)
	}
	return samePath(filepath.Clean(a), filepath.Clean(b))
}

// slugify derives a catalog ID or folder name: lower-case letters and digits
// separated by single hyphens.
func slugify(name string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
			continue
		}
		pendingHyphen = true
	}
	if b.Len() == 0 {
		return "project"
	}
	return b.String()
}

// uniqueCatalogID returns base, or base with the first free numeric suffix.
func uniqueCatalogID(cat *Catalog, base string) string {
	taken := make(map[string]bool, len(cat.Projects))
	for _, p := range cat.Projects {
		taken[strings.ToLower(catalogID(p))] = true
	}
	if !taken[strings.ToLower(base)] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}
