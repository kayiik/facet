package journeys

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mediaTypes is the closed list of file types a host may serve from a
// project, with the content type each is served as.
var mediaTypes = map[string]string{
	".mp4":  "video/mp4",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".aac":  "audio/aac",
	".m4a":  "audio/mp4",
	".ogg":  "audio/ogg",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".json": "application/json",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".md":   "text/markdown; charset=utf-8",
	".srt":  "text/plain; charset=utf-8",
	".vtt":  "text/vtt; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
}

// MediaContentType reports the content type a project file with this name is
// served as, and whether its type is served at all.
func MediaContentType(name string) (string, bool) {
	contentType, ok := mediaTypes[strings.ToLower(filepath.Ext(name))]
	return contentType, ok
}

// Media is a project file a host may serve.
type Media struct {
	// Project is the canonical project directory.
	Project string
	// Name is the file's project-relative path with forward slashes, as
	// requested; ReviewRender and RecordDecision accept it.
	Name string
	// Path is the canonical location of the file to serve.
	Path        string
	ContentType string
	Size        int64
	ModTime     time.Time
}

// projectTarget is a regular file confined to a project.
type projectTarget struct {
	root string // canonical project directory
	name string // project-relative path, forward slashes
	path string // canonical file path
	info os.FileInfo
}

// ResolveMedia resolves name to a file of a served type inside projectDir.
//
// name is project-relative, with forward slashes or the platform separator,
// or an absolute path inside the project. It is refused with
// ErrOutsideProject when it contains "..", names a drive or volume, is an
// absolute path elsewhere, or leads out of the project through a link,
// junction or mount point; with ErrUnsupportedMedia when the requested file
// or the file it resolves to has a type outside the served list; and with
// ErrNotFound when it is missing, not a regular file, or an empty MP4.
func ResolveMedia(projectDir, name string) (Media, error) {
	lexicalRoot, canonicalRoot, err := projectRoots(projectDir)
	if err != nil {
		return Media{}, err
	}
	rel, err := projectRelative(lexicalRoot, canonicalRoot, name)
	if err != nil {
		return Media{}, err
	}
	if _, ok := MediaContentType(rel); !ok {
		return Media{}, fmt.Errorf("%w: %s", ErrUnsupportedMedia, filepath.ToSlash(rel))
	}
	target, err := statProjectFile(canonicalRoot, rel)
	if err != nil {
		return Media{}, err
	}
	contentType, ok := MediaContentType(target.path)
	if !ok {
		return Media{}, fmt.Errorf("%w: %s resolves to a file that is not served", ErrUnsupportedMedia, target.name)
	}
	if strings.EqualFold(filepath.Ext(target.path), ".mp4") && target.info.Size() == 0 {
		return Media{}, fmt.Errorf("%w: %s is an empty video", ErrNotFound, target.name)
	}
	return Media{
		Project:     target.root,
		Name:        target.name,
		Path:        target.path,
		ContentType: contentType,
		Size:        target.info.Size(),
		ModTime:     target.info.ModTime(),
	}, nil
}

// ResolveMediaRef resolves a media reference reported by ListProjects or
// GetProjectDetails: "projects/<slug>/<file>" names a file in a direct child
// of the workspace's projects/ directory, and "catalog/<id>/<file>" a file in
// the catalog project with that ID. The reference is the decoded path a host
// received under its media route; it uses forward slashes and has no empty,
// "." or ".." segment. A reference that names no known project is refused
// with ErrOutsideProject; the file itself is resolved by ResolveMedia.
func ResolveMediaRef(opts Options, ref string) (Media, error) {
	segments, err := mediaRefSegments(ref)
	if err != nil {
		return Media{}, err
	}
	slug, name := segments[1], strings.Join(segments[2:], "/")
	if validateProjectSlug(slug) != nil {
		return Media{}, fmt.Errorf("%w: %q does not name a project", ErrOutsideProject, slug)
	}

	var projectDir string
	switch segments[0] {
	case "projects":
		if strings.TrimSpace(opts.Workspace) == "" {
			return Media{}, fmt.Errorf("%w: no workspace is configured", ErrOutsideProject)
		}
		workspace, err := resolveProjectsScope(opts.Workspace, opts.MediaURLPrefix)
		if err != nil || workspace.canonicalProjects == "" {
			return Media{}, fmt.Errorf("%w: the workspace has no projects directory", ErrOutsideProject)
		}
		project, _, err := workspace.projectEntry(slug)
		if err != nil {
			return Media{}, fmt.Errorf("%w: %q is not a workspace project", ErrOutsideProject, slug)
		}
		projectDir = project.projectPath
	case "catalog":
		if strings.TrimSpace(opts.CatalogPath) == "" {
			return Media{}, fmt.Errorf("%w: no catalog is configured", ErrOutsideProject)
		}
		cat, err := readCatalog(opts)
		if err != nil {
			return Media{}, err
		}
		for _, p := range cat.Projects {
			if catalogID(p) == slug {
				projectDir = p.Path
				break
			}
		}
		if projectDir == "" {
			return Media{}, fmt.Errorf("%w: %q is not a catalog project", ErrOutsideProject, slug)
		}
	default:
		return Media{}, fmt.Errorf("%w: %q is not a project media reference", ErrOutsideProject, ref)
	}
	return ResolveMedia(projectDir, name)
}

// mediaRefSegments splits a media reference into at least a kind, a project
// and one file segment.
func mediaRefSegments(ref string) ([]string, error) {
	if strings.ContainsAny(ref, "\\\x00") {
		return nil, fmt.Errorf("%w: %q is not a media reference", ErrOutsideProject, ref)
	}
	segments := strings.Split(ref, "/")
	if len(segments) < 3 {
		return nil, fmt.Errorf("%w: %q does not name a project file", ErrOutsideProject, ref)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, fmt.Errorf("%w: %q is not a media reference", ErrOutsideProject, ref)
		}
	}
	return segments, nil
}

// resolveProjectFile resolves name to a regular file of any type inside
// projectDir, under the same confinement as ResolveMedia.
func resolveProjectFile(projectDir, name string) (projectTarget, error) {
	lexicalRoot, canonicalRoot, err := projectRoots(projectDir)
	if err != nil {
		return projectTarget{}, err
	}
	rel, err := projectRelative(lexicalRoot, canonicalRoot, name)
	if err != nil {
		return projectTarget{}, err
	}
	return statProjectFile(canonicalRoot, rel)
}

// projectRoots returns the absolute and the canonical project directory.
func projectRoots(projectDir string) (string, string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", "", fmt.Errorf("%w: no project directory", ErrInvalidRequest)
	}
	lexical, err := filepath.Abs(projectDir)
	if err != nil {
		return "", "", err
	}
	canonical, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		if missing(err) {
			return "", "", fmt.Errorf("%w: project directory %s", ErrNotFound, projectDir)
		}
		return "", "", fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", "", fmt.Errorf("%w: project directory %s: %w", ErrNotFound, projectDir, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%w: %s is not a directory", ErrNotFound, projectDir)
	}
	return lexical, canonical, nil
}

// projectRelative validates name lexically and returns it as a clean,
// native, project-relative path.
func projectRelative(lexicalRoot, canonicalRoot, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%w: choose a file inside the project", ErrInvalidRequest)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: the path contains a NUL byte", ErrOutsideProject)
	}
	for _, segment := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return "", fmt.Errorf("%w: %q steps out with ..", ErrOutsideProject, name)
		}
	}
	native := filepath.FromSlash(name)
	if filepath.IsAbs(native) {
		for _, root := range []string{lexicalRoot, canonicalRoot} {
			if rel, err := filepath.Rel(root, native); err == nil && rel != "." && filepath.IsLocal(rel) {
				return rel, nil
			}
		}
		return "", fmt.Errorf("%w: %q is an absolute path outside the project", ErrOutsideProject, name)
	}
	if hasDriveLetter(name) || filepath.VolumeName(native) != "" {
		return "", fmt.Errorf("%w: %q names a drive", ErrOutsideProject, name)
	}
	if !filepath.IsLocal(native) {
		return "", fmt.Errorf("%w: %q is not a project-relative path", ErrOutsideProject, name)
	}
	clean := filepath.Clean(native)
	if clean == "." {
		return "", fmt.Errorf("%w: choose a file inside the project", ErrInvalidRequest)
	}
	return clean, nil
}

// hasDriveLetter reports a Windows drive prefix such as "C:" on any platform.
func hasDriveLetter(name string) bool {
	if len(name) < 2 || name[1] != ':' {
		return false
	}
	c := name[0]
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// statProjectFile resolves links under the canonical project root and accepts
// only a regular file that stays strictly inside it, reached through no
// junction or mount point.
func statProjectFile(root, rel string) (projectTarget, error) {
	name := filepath.ToSlash(rel)
	if junctionWithin(root, filepath.Dir(rel)) {
		return projectTarget{}, fmt.Errorf("%w: %s passes through a junction or mount point", ErrOutsideProject, name)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		if missing(err) {
			return projectTarget{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return projectTarget{}, fmt.Errorf("resolve %s: %w", name, err)
	}
	if !pathStrictlyWithin(root, resolved) {
		return projectTarget{}, fmt.Errorf("%w: %s leaves the project", ErrOutsideProject, name)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		if missing(err) {
			return projectTarget{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return projectTarget{}, fmt.Errorf("stat %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return projectTarget{}, fmt.Errorf("%w: %s is not a file", ErrNotFound, name)
	}
	return projectTarget{root: root, name: name, path: resolved, info: info}, nil
}
