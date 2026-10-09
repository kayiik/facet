package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeKlingQueue is a fal queue with one Kling request that runs until it is
// completed. Its status, when not given by the submission, is addressed under
// the queue application, as a resumed call addresses it.
type fakeKlingQueue struct {
	server  *httptest.Server
	id      string
	mu      sync.Mutex
	done    bool
	submits int
}

func newFakeKlingQueue(t *testing.T) *fakeKlingQueue {
	f := &fakeKlingQueue{id: "5e1f-kling-req"}
	requests := "/fal-ai/kling-video/requests/" + f.id
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(code int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(value)
		}
		switch {
		case r.URL.Path == "/video.mp4":
			_, _ = w.Write([]byte("kling-bytes"))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/fal-ai/kling-video/v1/"):
			f.submits++
			reply(http.StatusOK, map[string]any{"request_id": f.id})
		case r.URL.Path == requests+"/status" && f.done:
			reply(http.StatusOK, map[string]any{"status": "COMPLETED"})
		case r.URL.Path == requests+"/status":
			reply(http.StatusAccepted, map[string]any{"status": "IN_PROGRESS"})
		case r.URL.Path == requests:
			reply(http.StatusOK, map[string]any{"video": map[string]any{"url": f.server.URL + "/video.mp4"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	t.Setenv("FAL_KEY", "test-key")
	t.Setenv("FAL_QUEUE_BASE_URL", f.server.URL)
	return f
}

// A paid call the client cuts off while it waits has already named its
// provider job in a progress notification; calling again with that id
// collects the job without a second submission, inside the root, and the
// artifact provenance names the job.
func TestPaidCallCutOffOverMCPResumesFromItsProgressNotification(t *testing.T) {
	queue := newFakeKlingQueue(t)
	root := t.TempDir()
	jobs := make(chan string, 16)
	cs := connect(t, Options{Root: root}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			if strings.Contains(req.Params.Message, queue.id) {
				jobs <- req.Params.Message
			}
		},
	})

	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	callErr := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      "kling_video",
			Arguments: map[string]any{"prompt": "waves at dusk", "output_path": "clip.mp4"},
			Meta:      mcp.Meta{"progressToken": "kling-1"},
		})
		callErr <- err
	}()
	select {
	case message := <-jobs:
		if !strings.Contains(message, "resume_job_id") {
			t.Errorf("the milestone does not say how to resume: %q", message)
		}
	case <-time.After(testTimeout):
		t.Fatal("no progress notification named the provider job")
	}
	cancel() // the harness gives up while the provider is still working
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool returned %v, want context.Canceled", err)
	}

	queue.mu.Lock()
	queue.done = true
	queue.mu.Unlock()
	res, env := call(t, cs, "kling_video", map[string]any{
		"prompt": "waves at dusk", "output_path": "clip.mp4", "resume_job_id": queue.id,
	})
	if res.IsError || !env.OK {
		t.Fatalf("the resumed call failed: %+v", env.Error)
	}
	queue.mu.Lock()
	submits := queue.submits
	queue.mu.Unlock()
	if submits != 1 {
		t.Fatalf("the provider saw %d submissions, want 1", submits)
	}
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["provider_job_id"] != queue.id || result["resumed"] != true {
		t.Errorf("the result does not record the resumed job: %v", result)
	}
	if data, err := os.ReadFile(filepath.Join(root, "clip.mp4")); err != nil || string(data) != "kling-bytes" {
		t.Fatalf("the video was not collected inside the root: %q, %v", data, err)
	}
	if len(env.Artifacts) != 1 || env.Artifacts[0].ProviderJobID != queue.id {
		t.Errorf("the artifact does not name its provider job: %+v", env.Artifacts)
	}
}
