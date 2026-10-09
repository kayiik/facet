package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Artifacts come from the result fields that name produced files, in a fixed
// order, without duplicates, and only for files that exist.
func TestCollectArtifactsReadsProducedFileFields(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	frame := filepath.Join(dir, "frames", "frame-0001.jpg")
	thumb := filepath.Join(dir, "thumbs", "clip.jpg")
	listed := filepath.Join(dir, "index.html")
	for _, path := range []string{output, frame, thumb, listed} {
		writeFile(t, path, []byte("bytes of "+filepath.Base(path)))
	}
	result := map[string]any{
		"output":    output,
		"input":     filepath.Join(dir, "never-an-artifact.mp4"),
		"files":     []string{listed, filepath.Join(dir, "missing.txt")},
		"samples":   []map[string]any{{"path": frame, "timestamp": 1.0}},
		"clips":     []map[string]any{{"path": output, "thumbnail": thumb}},
		"workspace": dir,
	}
	artifacts, warnings := collectArtifacts("video_compose", result, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	var got []string
	for _, a := range artifacts {
		got = append(got, filepath.Base(a.Path))
	}
	if strings.Join(got, ",") != "out.mp4,index.html,frame-0001.jpg,clip.jpg" {
		t.Fatalf("artifacts = %v", got)
	}
}

// A read-only tool produces nothing, so the paths in its result are inputs.
func TestReadOnlyToolsDescribeNoArtifacts(t *testing.T) {
	track := filepath.Join(t.TempDir(), "song.mp3")
	writeFile(t, track, []byte("audio"))
	result := map[string]any{"output": track, "tracks": []map[string]any{{"path": track}}}
	if artifacts, _ := collectArtifacts("music_library", result, nil); len(artifacts) != 0 {
		t.Fatalf("a read-only tool described artifacts: %+v", artifacts)
	}
}

// Image signatures are evidence: JPEG bytes under a .png name are JPEG.
// Containers are not, so an .m4a is audio even though it sniffs as MP4.
func TestArtifactMediaType(t *testing.T) {
	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.White)
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	mp4Header := []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}
	for _, c := range []struct {
		name string
		head []byte
		want string
	}{
		{"mislabelled.png", jpg.Bytes(), "image/jpeg"},
		{"voice.m4a", mp4Header, "audio/mp4"},
		{"render.mp4", mp4Header, "video/mp4"},
		{"captions.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), "application/x-subrip"},
		{"cues.caption.json", []byte(`{"cues":[]}`), "application/json"},
		{"noextension", mp4Header, "video/mp4"},
		{"notes", []byte("plain words"), "text/plain"},
	} {
		if got := artifactMediaType(c.name, c.head); got != c.want {
			t.Errorf("artifactMediaType(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// Artifacts are envelope-level: present for a run that produced files,
// absent from the JSON otherwise, and never part of a result schema.
func TestArtifactsAppearOnlyWhenProduced(t *testing.T) {
	SetProductVersion("9.9.9-test")
	t.Cleanup(func() { SetProductVersion("dev") })
	dir := t.TempDir()
	output := filepath.Join(dir, "subs.vtt")
	request, _ := json.Marshal(map[string]any{"segments": []any{map[string]any{"text": "hello world", "start": 0, "end": 1}}, "format": "vtt", "output_path": output})
	env := RunContext(context.Background(), "subtitle_gen", request)
	if !env.OK || len(env.Artifacts) != 1 {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	a := env.Artifacts[0]
	if a.FacetVersion != "9.9.9-test" || a.Tool != "subtitle_gen" || a.MediaType != "text/vtt" {
		t.Fatalf("unexpected descriptor: %+v", a)
	}
	assertArtifacts(t, "subtitle_gen", env, 1)
	encoded, _ := json.Marshal(env)
	if !strings.Contains(string(encoded), `"artifacts":[{"path":`) {
		t.Fatalf("artifacts missing from the envelope JSON: %s", encoded)
	}
	if _, inResult := env.Result.(map[string]any)["artifacts"]; inResult {
		t.Fatal("artifacts leaked into the tool result")
	}

	estimate := EstimateContext(context.Background(), "subtitle_gen", request)
	encoded, _ = json.Marshal(estimate)
	if len(estimate.Artifacts) != 0 || strings.Contains(string(encoded), `"artifacts"`) {
		t.Fatalf("an estimate described artifacts: %s", encoded)
	}
	failed := RunContext(context.Background(), "subtitle_gen", []byte(`{"segments":[]}`))
	if failed.OK || len(failed.Artifacts) != 0 {
		t.Fatalf("a failed run described artifacts: %+v", failed)
	}
}
