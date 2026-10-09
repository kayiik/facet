package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// facet tools run prints a run's milestones, such as the provider job it
// submitted, to stderr as they happen, and keeps stdout one envelope.
func TestToolsRunReportsTheProviderJobOnStderr(t *testing.T) {
	const id = "a1b2-kling-req"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/video.mp4":
			_, _ = w.Write([]byte("kling-bytes"))
		case r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"request_id": id})
		case strings.HasSuffix(r.URL.Path, "/requests/"+id+"/status"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
		case strings.HasSuffix(r.URL.Path, "/requests/"+id):
			_ = json.NewEncoder(w).Encode(map[string]any{"video": map[string]any{"url": server.URL + "/video.mp4"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_QUEUE_BASE_URL", server.URL)

	request, _ := json.Marshal(map[string]any{"prompt": "waves", "output_path": filepath.Join(t.TempDir(), "clip.mp4")})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"tools", "run", "kling_video", "--input", string(request)}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "facet: kling_video submitted provider job "+id) ||
		!strings.Contains(stderr.String(), "resume_job_id") {
		t.Errorf("stderr does not report the provider job: %q", stderr.String())
	}
	var envelope struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Result["provider_job_id"] != id {
		t.Errorf("the envelope does not record the provider job: %s", stdout.String())
	}
}
