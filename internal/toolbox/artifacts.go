package toolbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Artifact describes one file a successful run produced.
//
// It is provenance a caller can check without trusting the tool: the digest
// and size are measured from the bytes on disk after the run, and the media
// type is taken from the file's own signature where that is unambiguous.
type Artifact struct {
	// Path is the absolute path of the file.
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	// Tool is the canonical name of the tool that produced the file.
	Tool string `json:"tool"`
	// FacetVersion is the release that produced the file.
	FacetVersion string `json:"facet_version"`
	// ProviderJobID is the provider job that generated the file, when a
	// provider ran one.
	ProviderJobID string `json:"provider_job_id,omitempty"`
}

// Result fields that name produced files. Extraction is central so no tool
// has to remember to report its artifacts, and a result schema never has to
// carry them.
var (
	// artifactFields are top-level string fields holding one produced file.
	artifactFields = []string{"output", "output_path"}
	// artifactLists are top-level string arrays of produced files.
	artifactLists = []string{"files"}
	// artifactArrays are top-level arrays of objects; artifactItemFields are
	// the item fields that name produced files. They cover generated media
	// (outputs), extracted and evidence frames (samples, frames), and
	// downloads with their thumbnails (clips).
	artifactArrays     = []string{"outputs", "samples", "frames", "clips"}
	artifactItemFields = []string{"output", "path", "thumbnail"}
)

// collectArtifacts describes every file a run of tool reported producing.
//
// A read-only tool produces nothing, so paths in its result (music_library's
// tracks, say) are inputs and are never described. A reported path that is
// not a regular file after the run — an empty optional output, a directory —
// is skipped. A file that cannot be read is reported as a warning rather
// than failing a run that already succeeded.
func collectArtifacts(tool string, result any, warnings []string) ([]Artifact, []string) {
	if result == nil || readOnlyTools[tool] {
		return nil, warnings
	}
	var artifacts []Artifact
	seen := map[string]bool{}
	job := resultProviderJobID(result)
	for _, candidate := range artifactPaths(result) {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		key := filepath.Clean(path)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		artifact, err := describeArtifact(tool, path)
		if err != nil {
			warnings = append(warnings, "artifact "+path+" could not be described: "+bounded(err.Error()))
			continue
		}
		artifact.ProviderJobID = job
		artifacts = append(artifacts, artifact)
	}
	return artifacts, warnings
}

// resultProviderJobID is the provider job a result reports generating its
// files, or "".
func resultProviderJobID(result any) string {
	if fields, ok := result.(map[string]any); ok {
		if id, ok := fields["provider_job_id"].(string); ok {
			return id
		}
	}
	return ""
}

// artifactPaths lists the produced-file paths a result names, in a stable
// order. The result is read through its JSON form, which is what callers
// receive, so typed slices and structs are handled alike.
func artifactPaths(result any) []string {
	data, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil {
		return nil
	}
	var paths []string
	add := func(value any) {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			paths = append(paths, text)
		}
	}
	for _, name := range artifactFields {
		add(fields[name])
	}
	for _, name := range artifactLists {
		list, _ := fields[name].([]any)
		for _, item := range list {
			add(item)
		}
	}
	for _, name := range artifactArrays {
		list, _ := fields[name].([]any)
		for _, item := range list {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range artifactItemFields {
				add(object[field])
			}
		}
	}
	return paths
}

// describeArtifact measures one produced file.
func describeArtifact(tool, path string) (Artifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return Artifact{}, err
	}
	defer file.Close()
	hash := sha256.New()
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Artifact{}, err
	}
	head = head[:n]
	hash.Write(head)
	rest, err := io.Copy(hash, file)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		Path:         path,
		MediaType:    artifactMediaType(path, head),
		SizeBytes:    int64(n) + rest,
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		Tool:         tool,
		FacetVersion: ProductVersion(),
	}, nil
}

// mediaTypesByExtension names the types Facet's tools write.
var mediaTypesByExtension = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm", ".mkv": "video/x-matroska",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".aac": "audio/aac", ".flac": "audio/flac", ".ogg": "audio/ogg", ".opus": "audio/opus",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif",
	".srt": "application/x-subrip", ".vtt": "text/vtt", ".json": "application/json", ".html": "text/html", ".txt": "text/plain",
}

// artifactMediaType reports a file's media type.
//
// Image signatures are unambiguous and the bytes are evidence: a provider has
// returned JPEG data under a .png name. Container signatures are not — an
// .m4a sniffs as video/mp4 — so for everything else the extension the tool
// chose wins, and the signature is the fallback when there is none.
func artifactMediaType(path string, head []byte) string {
	sniffed := http.DetectContentType(head)
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	if byExtension, ok := mediaTypesByExtension[strings.ToLower(filepath.Ext(path))]; ok {
		return byExtension
	}
	if i := strings.Index(sniffed, ";"); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	return sniffed
}
