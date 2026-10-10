package window

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kayiik/facet/internal/app/journeys"
	"github.com/kayiik/facet/internal/app/toolkit"
)

// The screens that need no model: projects and their evidence, media import
// and inspection, technical review and the person's decision
// (internal/journeys), and the Toolkit's readiness (internal/toolkit).
//
// Files are named by media references ("projects/<slug>/<file>" or
// "catalog/<id>/<file>"), which journeys resolves to one file inside one
// project or refuses; nothing outside a project is served, inspected,
// reviewed or written. The window runs tools only through the Runner, which
// refuses any tool that may charge or that writes over the network (R1), and
// no request names the tool it runs.

// mediaRoute serves project media: GET mediaRoute + reference.
const mediaRoute = "/api/media/"

// DefaultMaxUpload bounds one imported file when Projects.MaxUpload is zero.
const DefaultMaxUpload int64 = 4 << 30

// maxProjectName bounds a new project's name, in characters.
const maxProjectName = 120

// Projects configures the project screens.
type Projects struct {
	// Options locate the workspace, the catalog and the productions root new
	// projects go in. Media URLs always follow the window's own route,
	// whatever MediaURLPrefix says.
	Options journeys.Options
	// DataDir is the App's own data folder. No folder in it is opened as a
	// project: the projects there are listed from the workspace already,
	// and the rest is the agent's configuration and keys.
	DataDir string
	// MaxUpload bounds one imported file, in bytes; zero means
	// DefaultMaxUpload.
	MaxUpload int64
}

func (s *Server) opts() journeys.Options {
	opts := s.Projects.Options
	opts.MediaURLPrefix = mediaRoute
	return opts
}

func (s *Server) maxUpload() int64 {
	if s.Projects.MaxUpload > 0 {
		return s.Projects.MaxUpload
	}
	return DefaultMaxUpload
}

// inWorkspace reports whether dir is inside the agent's workspace, the only
// place the agent can read and write.
func (s *Server) inWorkspace(dir string) bool {
	workspace := s.Projects.Options.Workspace
	return workspace != "" && dir != "" && within(workspace, dir)
}

// within reports whether path is base or inside it, as written or once
// links are resolved.
func within(base, path string) bool {
	inside := func(base, path string) bool {
		rel, err := filepath.Rel(base, path)
		return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if inside(base, path) {
		return true
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && inside(resolvedBase, resolved)
}

// projectRow is a project as the list shows it.
type projectRow struct {
	journeys.ProjectSummary
	// InWorkspace reports that the project is inside the agent's workspace,
	// so the agent can work on it.
	InWorkspace bool `json:"in_workspace"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) error {
	opts := s.opts()
	projects, err := journeys.ListProjects(opts)
	if err != nil {
		return err
	}
	rows := make([]projectRow, 0, len(projects))
	for _, p := range projects {
		rows = append(rows, projectRow{ProjectSummary: p, InWorkspace: s.inWorkspace(p.Path)})
	}
	// The productions root is where new projects go; reading it needs no
	// catalog, whose folders may be slow to check.
	root := ""
	if opts.CatalogPath != "" && strings.TrimSpace(opts.ProductionsRoot) != "" {
		if abs, err := filepath.Abs(strings.TrimSpace(opts.ProductionsRoot)); err == nil {
			root = abs
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": rows, "productions_root": root})
	return nil
}

// createProject creates a project from its name alone: its folder always
// goes in the productions root, never where a request says.
func (s *Server) createProject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return badRequest{"name the project"}
	}
	if utf8.RuneCountInString(name) > maxProjectName {
		return badRequest{fmt.Sprintf("the name is too long; use at most %d characters", maxProjectName)}
	}
	project, err := journeys.CreateProject(s.opts(), journeys.NewProject{Name: name})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project})
	return nil
}

// openProject adds a folder the person typed to the catalog; journeys
// writes nothing into it.
func (s *Server) openProject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	dir, err := s.folderToOpen(req.Path)
	if err != nil {
		return err
	}
	project, err := journeys.OpenProject(s.opts(), dir, "")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
	return nil
}

// folderToOpen checks a folder path the person typed (a browser cannot pick
// folders). It must be a full path that steps through no "..", and neither a
// whole drive nor part of the App's own data. A folder that holds the App's
// data is refused by journeys, because the catalog would lie inside it.
func (s *Server) folderToOpen(typed string) (string, error) {
	folder := strings.TrimSpace(typed)
	// Windows Explorer's "Copy as path" wraps the path in quotes.
	if len(folder) >= 2 && folder[0] == '"' && folder[len(folder)-1] == '"' {
		folder = strings.TrimSpace(folder[1 : len(folder)-1])
	}
	if folder == "" {
		return "", badRequest{"type the folder's full path"}
	}
	if strings.ContainsRune(folder, 0) {
		return "", badRequest{"the path is not valid"}
	}
	for _, segment := range strings.FieldsFunc(folder, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return "", fmt.Errorf("%w: %s steps out with ..; type the folder's own path", journeys.ErrOutsideProject, folder)
		}
	}
	if folder == "~" || strings.HasPrefix(folder, "~/") || strings.HasPrefix(folder, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			folder = filepath.Join(home, folder[1:])
		}
	}
	if !filepath.IsAbs(folder) {
		return "", badRequest{"type the folder's full path, such as " + examplePath()}
	}
	if strings.HasPrefix(folder, `\\?\`) || strings.HasPrefix(folder, `\\.\`) || strings.HasPrefix(folder, `//?/`) || strings.HasPrefix(folder, `//./`) {
		return "", badRequest{"type an ordinary folder path"}
	}
	clean := filepath.Clean(folder)
	if filepath.Dir(clean) == clean {
		return "", fmt.Errorf("%w: a whole drive is not a project folder", journeys.ErrOutsideProject)
	}
	if s.Projects.DataDir != "" && within(s.Projects.DataDir, clean) {
		return "", fmt.Errorf("%w: %s is part of the App's own data; its projects are listed already", journeys.ErrOutsideProject, clean)
	}
	return clean, nil
}

func examplePath() string {
	if filepath.Separator == '\\' {
		return `C:\Users\user\Videos\My film`
	}
	return "/home/you/Videos/my-film"
}

// projectView is one project as its screen shows it: the evidence journeys
// reports, with the renders set apart and the review's frames mapped onto
// the project's media.
type projectView struct {
	Project     *journeys.ProjectDetails `json:"project"`
	Ref         string                   `json:"ref"`
	InWorkspace bool                     `json:"in_workspace"`
	// Renders are the videos in renders/, the files a review is about;
	// Files are the project's other artifacts.
	Renders   []journeys.MediaFile `json:"renders"`
	Files     []journeys.MediaFile `json:"files"`
	Review    *reviewEvidence      `json:"review,omitempty"`
	MaxUpload int64                `json:"max_upload"`
}

func (s *Server) projectDetails(w http.ResponseWriter, r *http.Request) error {
	opts := s.opts()
	slug := r.PathValue("slug")
	// Both calls resolve the same slug the same way, so they find one
	// project.
	loc, err := journeys.LocateProject(opts, slug)
	if err != nil {
		return err
	}
	details, err := journeys.GetProjectDetails(opts, slug)
	if err != nil {
		return err
	}
	view := projectView{Project: details, Ref: loc.Ref, InWorkspace: s.inWorkspace(loc.Dir),
		Renders: []journeys.MediaFile{}, Files: []journeys.MediaFile{}, MaxUpload: s.maxUpload()}
	for _, file := range details.Artifacts {
		if strings.HasPrefix(file.RelativePath, loc.Ref+"/renders/") && isVideo(file.Name) {
			view.Renders = append(view.Renders, file)
		} else {
			view.Files = append(view.Files, file)
		}
	}
	if details.ReviewReport != nil {
		view.Review = s.evidenceOf(loc, details.ReviewReport)
	}
	writeJSON(w, http.StatusOK, view)
	return nil
}

func isVideo(name string) bool {
	contentType, _ := journeys.MediaContentType(name)
	return strings.HasPrefix(contentType, "video/")
}

// reviewEvidence is what a technical review reported besides its gates:
// which file it reviewed, and the frames it sampled.
type reviewEvidence struct {
	// File is the reviewed file's project-relative path.
	File    string        `json:"file,omitempty"`
	Samples []frameSample `json:"samples"`
}

type frameSample struct {
	URL       string  `json:"url"`
	Timestamp float64 `json:"timestamp"`
}

// evidenceOf maps the paths a review reported, absolute ones, onto the
// project's media. A path outside the project, or a frame since removed, is
// left out.
func (s *Server) evidenceOf(loc journeys.Location, report any) *reviewEvidence {
	values, ok := report.(map[string]any)
	if !ok {
		return nil
	}
	if result, ok := values["result"].(map[string]any); ok {
		values = result
	}
	evidence := &reviewEvidence{Samples: []frameSample{}}
	if facts, ok := values["output_facts"].(map[string]any); ok {
		if input, ok := facts["input"].(string); ok {
			if ref, ok := loc.MediaRef(input); ok {
				evidence.File = strings.TrimPrefix(ref, loc.Ref+"/")
			}
		}
	}
	samples, _ := values["samples"].([]any)
	for _, raw := range samples {
		sample, _ := raw.(map[string]any)
		frame, _ := sample["path"].(string)
		ref, ok := loc.MediaRef(frame)
		if !ok {
			continue
		}
		if _, err := journeys.ResolveMediaRef(s.opts(), ref); err != nil {
			continue
		}
		timestamp, _ := sample["timestamp"].(float64)
		evidence.Samples = append(evidence.Samples, frameSample{URL: journeys.MediaURL(mediaRoute, ref), Timestamp: timestamp})
	}
	return evidence
}

// importMedia stores one file the page uploads (the request body) in the
// project's assets/ folder. The query names the file (name) and what to do
// when one of that name exists (if_exists: replace or keep; refused when
// absent).
func (s *Server) importMedia(w http.ResponseWriter, r *http.Request) error {
	loc, err := journeys.LocateProject(s.opts(), r.PathValue("slug"))
	if err != nil {
		return err
	}
	limit := s.maxUpload()
	if r.ContentLength > limit {
		return fmt.Errorf("%w: the file is larger than the %s a file may have", journeys.ErrTooLarge, sizeText(limit))
	}
	query := r.URL.Query()
	imported, err := journeys.ImportMedia(loc.Dir, journeys.ImportRequest{Name: query.Get("name"), IfExists: query.Get("if_exists"), MaxBytes: limit}, r.Body)
	if err != nil {
		return err
	}
	ref, _ := loc.MediaRef(imported.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"imported": imported, "ref": ref, "url": journeys.MediaURL(mediaRoute, ref)})
	return nil
}

// sizeText writes a byte count the way a person reads it.
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.3g GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.3g MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.3g KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// media serves one project file, named by its media reference after the
// route. The browser's video, audio and image elements load it, with the
// token in the address.
func (s *Server) media(w http.ResponseWriter, r *http.Request) error {
	m, err := journeys.ResolveMediaRef(s.opts(), r.PathValue("ref"))
	if err != nil {
		return err
	}
	f, err := os.Open(m.Path)
	if err != nil {
		return fmt.Errorf("%w: %s", journeys.ErrNotFound, m.Name)
	}
	defer f.Close()
	// The file opened must be the file resolved: one replaced by a link
	// since then is refused.
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if named, err := os.Lstat(m.Path); err != nil || !named.Mode().IsRegular() || !os.SameFile(opened, named) {
		return fmt.Errorf("%w: %s changed while it was opened", journeys.ErrOutsideProject, m.Name)
	}
	w.Header().Set("Content-Type", m.ContentType)
	// The address carries the run's token, so nothing keeps the response.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, path.Base(m.Name), opened.ModTime(), f)
	return nil
}

// mediaFile resolves the file a request names by its media reference, and
// the project it is in.
func (s *Server) mediaFile(ref string) (journeys.Media, journeys.Location, error) {
	m, err := journeys.ResolveMediaRef(s.opts(), ref)
	if err != nil {
		return journeys.Media{}, journeys.Location{}, err
	}
	// A reference that resolved begins with its kind and project.
	parts := strings.SplitN(ref, "/", 3)
	return m, journeys.Location{Slug: parts[1], Dir: m.Project, Ref: parts[0] + "/" + parts[1]}, nil
}

// probe inspects a video, audio or image file with media_probe.
func (s *Server) probe(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Ref string `json:"ref"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	m, _, err := s.mediaFile(req.Ref)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(m.ContentType, "video/") && !strings.HasPrefix(m.ContentType, "audio/") && !strings.HasPrefix(m.ContentType, "image/") {
		return fmt.Errorf("%w: only video, audio and image files are inspected", journeys.ErrUnsupportedMedia)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	envelope, err := s.toolkit.Run(ctx, m.Project, "media_probe", map[string]any{"input": m.Path})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, envelope)
	return nil
}

// reviewRequest is a technical review the person asks for: the render and
// what it is expected to be.
type reviewRequest struct {
	Ref               string  `json:"ref"`
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	FPS               float64 `json:"fps"`
	Duration          float64 `json:"duration"`
	DurationTolerance float64 `json:"duration_tolerance"`
	Audio             bool    `json:"audio"`
	VideoCodec        string  `json:"video_codec"`
	PixelFormat       string  `json:"pixel_format"`
	AudioCodec        string  `json:"audio_codec"`
	AudioSampleRate   int     `json:"audio_sample_rate"`
	AudioChannels     int     `json:"audio_channels"`
}

// review runs the technical review of a render (output_review). A review
// the tool could not finish is reported in the envelope, not as an error.
func (s *Server) review(w http.ResponseWriter, r *http.Request) error {
	var req reviewRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	m, loc, err := s.mediaFile(req.Ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	envelope, err := journeys.ReviewRender(ctx, m.Project, journeys.ReviewRequest{
		Path: m.Name, Width: req.Width, Height: req.Height, FPS: req.FPS,
		Duration: req.Duration, DurationTolerance: req.DurationTolerance, Audio: req.Audio,
		VideoCodec: req.VideoCodec, PixelFormat: req.PixelFormat,
		AudioCodec: req.AudioCodec, AudioSampleRate: req.AudioSampleRate, AudioChannels: req.AudioChannels,
	})
	if err != nil {
		return err
	}
	var result map[string]any
	_ = json.Unmarshal(envelope.Result, &result)
	writeJSON(w, http.StatusOK, map[string]any{"envelope": envelope, "review": s.evidenceOf(loc, result)})
	return nil
}

// decision records the person's decision about a file, against its digest.
func (s *Server) decision(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Ref      string `json:"ref"`
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	m, _, err := s.mediaFile(req.Ref)
	if err != nil {
		return err
	}
	record, err := journeys.RecordDecision(m.Project, journeys.DecisionRequest{Path: m.Name, Decision: req.Decision, Note: req.Note})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"decision": record})
	return nil
}

// readinessRow is one tool on the readiness screen.
type readinessRow struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	Configured bool   `json:"configured"`
	// Missing is what the tool needs and does not have; Unverified are keys
	// that are set but prove nothing until the provider answers.
	Missing    []toolkit.Dependency `json:"missing"`
	Unverified []string             `json:"unverified"`
	toolkit.Effects
	// Direct reports that the window may run the tool itself (R1).
	Direct bool `json:"direct"`
}

// readiness reports the Toolkit's version and, for every tool, whether it
// can run on this computer and what it is missing.
func (s *Server) readiness(w http.ResponseWriter, r *http.Request) error {
	tools, err := s.toolkit.Tools(r.Context())
	if err != nil {
		return err
	}
	// Direct follows the effects the Runner enforces, read when the App
	// started.
	effects, err := s.toolkit.Effects(r.Context())
	if err != nil {
		return err
	}
	rows := make([]readinessRow, 0, len(tools))
	for _, tool := range tools {
		row := readinessRow{Name: tool.Name, Capability: tool.Capability, Configured: tool.Configured,
			Missing: []toolkit.Dependency{}, Unverified: []string{}, Effects: tool.Effects}
		if declared, ok := effects[tool.Name]; ok {
			row.Direct = declared.Direct()
		}
		for _, dep := range tool.Dependencies {
			switch {
			case !dep.Available:
				row.Missing = append(row.Missing, dep)
			case dep.Resolution == "unknown":
				row.Unverified = append(row.Unverified, dep.Name)
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	out := map[string]any{"tools": rows}
	if version, err := s.toolkit.Version(r.Context()); err != nil {
		out["version_error"] = err.Error()
	} else {
		out["version"] = version
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
