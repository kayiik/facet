package journeys

import "path/filepath"

// Location is a resolved project: where it is on disk, and the media
// reference its files are reported under.
type Location struct {
	// Slug is the canonical slug, as GetProjectDetails reports it.
	Slug string `json:"slug"`
	// Dir is the canonical project directory, which ReviewRender,
	// RecordDecision and ImportMedia take.
	Dir string `json:"dir"`
	// Ref begins the media references of the project's files:
	// "projects/<slug>" for a workspace project, "catalog/<id>" for a
	// catalog project.
	Ref string `json:"ref"`
}

// LocateProject resolves slug as GetProjectDetails does, without reading the
// project's evidence.
func LocateProject(opts Options, slug string) (Location, error) {
	project, _, err := findProject(opts, slug)
	if err != nil {
		return Location{}, err
	}
	kind := "projects"
	if project.catalog {
		kind = "catalog"
	}
	return Location{Slug: project.slug, Dir: project.canonicalProject, Ref: kind + "/" + project.slug}, nil
}

// MediaRef returns the media reference of a file in the project, named by a
// project-relative path or by an absolute path inside the project, such as
// one a tool reported. It reports false for a path that does not stay inside
// the project. A reference only names a location: ResolveMediaRef decides
// whether it may be served.
func (l Location) MediaRef(name string) (string, bool) {
	if l.Dir == "" || l.Ref == "" {
		return "", false
	}
	rel, err := projectRelative(l.Dir, l.Dir, name)
	if err != nil {
		return "", false
	}
	return l.Ref + "/" + filepath.ToSlash(rel), true
}

// MediaURL returns ref with each segment URL-escaped under prefix, as the
// evidence URL fields are filled.
func MediaURL(prefix, ref string) string { return mediaURL(prefix, ref) }
