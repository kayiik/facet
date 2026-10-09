package toolbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJobProvider is a provider that runs one job: it accepts a submission,
// reports the job as running until complete is set, then serves its video.
type fakeJobProvider struct {
	t       *testing.T
	jobID   string
	server  *httptest.Server
	mu      sync.Mutex
	done    bool
	failed  bool
	submits int
	polls   int
}

func (f *fakeJobProvider) complete() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = true
}

func (f *fakeJobProvider) fail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = true
}

func (f *fakeJobProvider) counts() (submits, polls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.submits, f.polls
}

func (f *fakeJobProvider) state() (done, failed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return f.done, f.failed
}

func (f *fakeJobProvider) submitted() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits++
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

// newFakeFal serves the fal queue for Kling under FAL_QUEUE_BASE_URL.
func newFakeFal(t *testing.T) *fakeJobProvider {
	f := &fakeJobProvider{t: t, jobID: "0f8b9c2e-req-1"}
	requests := "/" + klingQueueApp + "/requests/"
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/video.mp4" {
			_, _ = w.Write([]byte("kling-video-bytes"))
			return
		}
		if r.Header.Get("Authorization") != "Key test-fal-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/fal-ai/kling-video/v1/"):
			f.submitted()
			// No status or response URL: Facet must address the request
			// under the queue application, as it does when resuming.
			writeJSON(w, http.StatusOK, map[string]any{"request_id": f.jobID})
		case r.Method == http.MethodGet && r.URL.Path == requests+f.jobID+"/status":
			done, failed := f.state()
			switch {
			case failed:
				writeJSON(w, http.StatusOK, map[string]any{"status": "FAILED"})
			case done:
				writeJSON(w, http.StatusOK, map[string]any{"status": "COMPLETED"})
			default:
				writeJSON(w, http.StatusAccepted, map[string]any{"status": "IN_PROGRESS"})
			}
		case r.Method == http.MethodGet && r.URL.Path == requests+f.jobID:
			writeJSON(w, http.StatusOK, map[string]any{"video": map[string]any{"url": f.server.URL + "/video.mp4"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	t.Setenv("FAL_KEY", "test-fal-key")
	t.Setenv("KLING_API_KEY", "")
	t.Setenv("FAL_QUEUE_BASE_URL", f.server.URL)
	return f
}

// newFakeSora serves Sora video jobs under OPENAI_BASE_URL.
func newFakeSora(t *testing.T) *fakeJobProvider {
	f := &fakeJobProvider{t: t, jobID: "video_123abc"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sora.mp4" {
			_, _ = w.Write([]byte("sora-video-bytes"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos/generations":
			f.submitted()
			writeJSON(w, http.StatusOK, map[string]any{"id": f.jobID, "status": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/generations/"+f.jobID:
			done, failed := f.state()
			switch {
			case failed:
				writeJSON(w, http.StatusOK, map[string]any{"id": f.jobID, "status": "failed"})
			case done:
				writeJSON(w, http.StatusOK, map[string]any{"id": f.jobID, "status": "completed", "video_url": f.server.URL + "/sora.mp4"})
			default:
				writeJSON(w, http.StatusOK, map[string]any{"id": f.jobID, "status": "in_progress"})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_BASE_URL", f.server.URL)
	return f
}

func fastProviderPolling(t *testing.T) {
	t.Helper()
	previous := providerPollInterval
	providerPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { providerPollInterval = previous })
}

type resumeCase struct {
	tool    string
	fake    func(*testing.T) *fakeJobProvider
	content string
}

var resumeCases = []resumeCase{
	{tool: "kling_video", fake: newFakeFal, content: "kling-video-bytes"},
	{tool: "sora_video", fake: newFakeSora, content: "sora-video-bytes"},
}

func resumeRequestJSON(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	request := map[string]any{"prompt": "waves at dusk", "duration": 5}
	for key, value := range fields {
		request[key] = value
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The R2 contract: a paid call cut off while it waits names its provider job,
// as soon as the job exists and again in its error, and a rerun with that id
// collects the job without a second submission.
func TestPaidVideoCutOffWhileWaitingResumesWithoutResubmitting(t *testing.T) {
	fastProviderPolling(t)
	for _, c := range resumeCases {
		t.Run(c.tool, func(t *testing.T) {
			fake := c.fake(t)
			out := filepath.Join(t.TempDir(), "clip.mp4")

			// The first call times out while the provider is still working.
			var milestones []Progress
			ctx := WithProgress(context.Background(), func(p Progress) { milestones = append(milestones, p) })
			env := RunContext(ctx, c.tool, resumeRequestJSON(t, map[string]any{"output_path": out, "timeout_seconds": 1}))
			if env.OK || env.Error == nil || env.Error.Code != "command_timeout" {
				t.Fatalf("first call: want command_timeout, got %+v", env)
			}
			if got := env.Error.Details["provider_job_id"]; got != fake.jobID {
				t.Fatalf("the error names provider job %v, want %s", got, fake.jobID)
			}
			if env.Error.Retryable {
				t.Error("an error with a running paid job must not invite resending the same request")
			}
			if !strings.Contains(env.Error.Message, "resume_job_id") {
				t.Errorf("the error does not say how to collect the job: %q", env.Error.Message)
			}
			if len(milestones) != 1 || milestones[0].ProviderJobID != fake.jobID || milestones[0].Tool != c.tool ||
				!strings.Contains(milestones[0].Message, fake.jobID) {
				t.Fatalf("the job was not reported as soon as it existed: %+v", milestones)
			}

			// The rerun with the id collects the finished job.
			fake.complete()
			milestones = nil
			env = RunContext(ctx, c.tool, resumeRequestJSON(t, map[string]any{"output_path": out, "resume_job_id": fake.jobID}))
			if !env.OK {
				t.Fatalf("resumed call failed: %+v", env.Error)
			}
			if submits, _ := fake.counts(); submits != 1 {
				t.Fatalf("the provider saw %d submissions, want exactly 1", submits)
			}
			result := env.Result.(map[string]any)
			if result["provider_job_id"] != fake.jobID || result["resumed"] != true {
				t.Errorf("the result does not record the resumed job: %+v", result)
			}
			assertResultConforms(t, c.tool, result)
			if data, err := os.ReadFile(out); err != nil || string(data) != c.content {
				t.Fatalf("output = %q, %v", data, err)
			}
			if len(env.Artifacts) != 1 || env.Artifacts[0].ProviderJobID != fake.jobID {
				t.Errorf("the artifact provenance does not name the job: %+v", env.Artifacts)
			}
			if len(milestones) != 1 || milestones[0].ProviderJobID != fake.jobID || !strings.Contains(milestones[0].Message, "collecting") {
				t.Errorf("a resumed call reports collecting, not submitting: %+v", milestones)
			}
		})
	}
}

// A cancellation (the harness giving up, a lost connection) is reported the
// same way as a timeout.
func TestCancelledPaidVideoNamesItsProviderJob(t *testing.T) {
	fastProviderPolling(t)
	for _, c := range resumeCases {
		t.Run(c.tool, func(t *testing.T) {
			fake := c.fake(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = WithProgress(ctx, func(Progress) {
				// Cut the call off while it waits for the provider.
				time.AfterFunc(50*time.Millisecond, cancel)
			})
			env := RunContext(ctx, c.tool, resumeRequestJSON(t, map[string]any{"output_path": filepath.Join(t.TempDir(), "clip.mp4")}))
			if env.OK || env.Error.Code != "cancelled" || env.Error.Details["provider_job_id"] != fake.jobID {
				t.Fatalf("want a cancelled error naming %s, got %+v", fake.jobID, env.Error)
			}
			if !strings.Contains(env.Error.Message, "resume_job_id") || env.Error.Retryable {
				t.Errorf("the cancelled error does not say how to collect the job: %+v", env.Error)
			}
		})
	}
}

// A job the provider failed, or does not know, is over: the error names it
// but does not suggest collecting it again.
func TestFinishedOrUnknownProviderJobsAreNotOfferedForResume(t *testing.T) {
	fastProviderPolling(t)
	for _, c := range resumeCases {
		t.Run(c.tool+"/unknown", func(t *testing.T) {
			fake := c.fake(t)
			env := RunContext(context.Background(), c.tool, resumeRequestJSON(t, map[string]any{
				"output_path": filepath.Join(t.TempDir(), "clip.mp4"), "resume_job_id": "no-such-job"}))
			if env.OK || env.Error.Details["provider_job_status"] != "not_found" || env.Error.Details["provider_job_id"] != "no-such-job" {
				t.Fatalf("want not_found for an unknown job, got %+v", env.Error)
			}
			if strings.Contains(env.Error.Message, "resume_job_id") {
				t.Errorf("an unknown job must not be offered for resume: %q", env.Error.Message)
			}
			if submits, _ := fake.counts(); submits != 0 {
				t.Errorf("a resume submitted %d jobs", submits)
			}
		})
		t.Run(c.tool+"/failed", func(t *testing.T) {
			fake := c.fake(t)
			fake.fail()
			env := RunContext(context.Background(), c.tool, resumeRequestJSON(t, map[string]any{
				"output_path": filepath.Join(t.TempDir(), "clip.mp4")}))
			if env.OK || env.Error.Details["provider_job_status"] != "failed" || env.Error.Details["provider_job_id"] != fake.jobID {
				t.Fatalf("want a failed job, got %+v", env.Error)
			}
			if strings.Contains(env.Error.Message, "resume_job_id") {
				t.Errorf("a failed job must not be offered for resume: %q", env.Error.Message)
			}
		})
	}
}

func TestResumeJobIDIsValidatedAndEstimatedAsFree(t *testing.T) {
	for _, c := range resumeCases {
		t.Run(c.tool, func(t *testing.T) {
			for _, bad := range []string{"../status", "a/b", "id?x=1", " id", strings.Repeat("a", 129)} {
				env := EstimateContext(context.Background(), c.tool, resumeRequestJSON(t, map[string]any{"resume_job_id": bad}))
				if env.OK || env.Error.Code != "invalid_request" {
					t.Errorf("resume_job_id %q was accepted: %+v", bad, env)
				}
			}
			env := EstimateContext(context.Background(), c.tool, resumeRequestJSON(t, map[string]any{"resume_job_id": "job-1", "mock": true}))
			if env.OK || env.Error.Code != "invalid_request" {
				t.Errorf("resume_job_id with mock was accepted: %+v", env)
			}
			env = EstimateContext(context.Background(), c.tool, resumeRequestJSON(t, map[string]any{"resume_job_id": "job-1"}))
			if !env.OK {
				t.Fatalf("estimate of a resume failed: %+v", env.Error)
			}
			result := env.Result.(map[string]any)
			if result["estimated_cost"] != 0.0 || result["resumes_provider_job"] != "job-1" {
				t.Errorf("collecting a job submits nothing new: %+v", result)
			}
			if env.Execution.EstimatedCost == nil || *env.Execution.EstimatedCost != 0 {
				t.Errorf("execution estimated cost = %v, want 0", env.Execution.EstimatedCost)
			}
		})
	}
}
