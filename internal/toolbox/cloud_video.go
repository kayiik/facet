package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type klingVideoRequest struct {
	Prompt         string  `json:"prompt"`
	Model          string  `json:"model,omitempty"`
	Duration       float64 `json:"duration,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	ImageURL       string  `json:"image_url,omitempty"`
	OutputPath     string  `json:"output_path,omitempty"`
	Mock           bool    `json:"mock,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"`
	ResumeJobID    string  `json:"resume_job_id,omitempty"`
}

type soraVideoRequest struct {
	Prompt         string  `json:"prompt"`
	Model          string  `json:"model,omitempty"`
	Duration       float64 `json:"duration,omitempty"`
	AspectRatio    string  `json:"aspect_ratio,omitempty"`
	Resolution     string  `json:"resolution,omitempty"`
	OutputPath     string  `json:"output_path,omitempty"`
	Mock           bool    `json:"mock,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"`
	ResumeJobID    string  `json:"resume_job_id,omitempty"`
}

// providerPollInterval is how often a provider job's status is checked.
// Tests shorten it.
var providerPollInterval = 3 * time.Second

// klingQueueApp is the fal queue application the Kling endpoints belong to;
// a request is addressed under it whichever endpoint submitted it.
const klingQueueApp = "fal-ai/kling-video"

// resumeRequest checks a request's resume_job_id against its mock flag:
// mock mode submits no provider job, so there is none to collect.
func resumeRequest(id string, mock bool) error {
	if err := validResumeJobID(id); err != nil {
		return err
	}
	if id != "" && mock {
		return failure("invalid_request", "resume_job_id collects a provider job, which mock mode never submits", nil)
	}
	return nil
}

// resumeEstimate is the estimate of collecting an existing provider job:
// nothing new is submitted, so nothing new is charged.
func resumeEstimate(operation, id string) map[string]any {
	res := estimateResult([]string{operation})
	res["network"] = true
	res["resumes_provider_job"] = id
	return res
}

func doKlingVideo(op string, data []byte) (any, []string, error) {
	return doKlingVideoContext(context.Background(), op, data)
}

func doKlingVideoContext(parent context.Context, op string, data []byte) (any, []string, error) {
	var r klingVideoRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	if err := resumeRequest(r.ResumeJobID, r.Mock); err != nil {
		return nil, nil, err
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains([]float64{0, 5, 10}, r.Duration) ||
		!slices.Contains([]string{"", "16:9", "9:16", "1:1"}, r.AspectRatio) ||
		!slices.Contains([]string{"", "std", "pro"}, r.Mode) {
		return nil, nil, failure("invalid_request", "Kling requires duration 5/10, aspect_ratio 16:9/9:16/1:1, and mode std/pro", nil)
	}

	model := r.Model
	if model == "" {
		model = "fal-ai/kling-video"
	}

	duration := r.Duration
	if duration <= 0 {
		duration = 5.0
	}

	aspectRatio := r.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}

	mode := r.Mode
	if mode == "" {
		mode = "std"
	}

	cost := 0.10
	if duration > 5 {
		cost = 0.20
	}
	if mode == "pro" {
		cost *= 2.0
	}

	if op == "estimate" {
		if r.ResumeJobID != "" {
			return resumeEstimate("kling_video_collect", r.ResumeJobID), nil, nil
		}
		res := estimateResult([]string{"kling_video_generate"})
		res["estimated_cost"] = cost
		res["network"] = true
		return res, nil, nil
	}

	apiKey := strings.TrimSpace(os.Getenv("FAL_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("KLING_API_KEY"))
	}
	if apiKey == "" && !r.Mock {
		return nil, nil, failure("credentials_missing", "FAL_KEY or KLING_API_KEY is required unless mock=true", nil)
	}
	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(parent, "kling_video.mp4")
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	if r.Mock {
		if err := createMockVideo(outPath, 640, 360, duration); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"provider":     "kling",
			"model":        model,
			"prompt":       r.Prompt,
			"duration":     duration,
			"aspect_ratio": aspectRatio,
			"mode":         mode,
			"output":       outPath,
			"mock":         true,
			"video_url":    "mock://fal/kling/" + filepath.Base(outPath),
		}, nil, nil
	}

	queueBase := strings.TrimRight(os.Getenv("FAL_QUEUE_BASE_URL"), "/")
	if queueBase == "" {
		queueBase = "https://queue.fal.run"
	}
	authorization := "Key " + apiKey
	client := &http.Client{}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var requestID, statusURL, responseURL, videoURL string
	if r.ResumeJobID != "" {
		requestID = r.ResumeJobID
		noteProviderJob(ctx, "kling_video", requestID, true)
	} else {
		endpoint := queueBase + "/fal-ai/kling-video/v1/standard/text-to-video"
		if mode == "pro" {
			endpoint = queueBase + "/fal-ai/kling-video/v1/pro/text-to-video"
		}
		if r.ImageURL != "" {
			endpoint = queueBase + "/fal-ai/kling-video/v1/standard/image-to-video"
			if mode == "pro" {
				endpoint = queueBase + "/fal-ai/kling-video/v1/pro/image-to-video"
			}
		}

		payload := map[string]any{
			"prompt":       r.Prompt,
			"duration":     fmt.Sprintf("%d", int(duration)),
			"aspect_ratio": aspectRatio,
		}
		if r.ImageURL != "" {
			payload["image_url"] = r.ImageURL
		}

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, failure("command_failed", "failed to serialize Kling video request: "+err.Error(), nil)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payloadBytes))
		if err != nil {
			return nil, nil, failure("command_failed", "failed to create Kling request: "+err.Error(), nil)
		}
		httpReq.Header.Set("Authorization", authorization)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, nil, failure("command_failed", "Kling request failed: "+err.Error(), nil)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, failure("command_failed", "failed to read Kling response: "+err.Error(), nil)
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return nil, nil, failure("command_failed", fmt.Sprintf("Kling API error (HTTP %d): %s", resp.StatusCode, bounded(string(body))), nil)
		}

		var queueResp struct {
			RequestID   string `json:"request_id"`
			StatusURL   string `json:"status_url"`
			ResponseURL string `json:"response_url"`
			Video       struct {
				URL string `json:"url"`
			} `json:"video"`
		}
		if err := json.Unmarshal(body, &queueResp); err != nil {
			return nil, nil, failure("command_failed", "failed to parse Kling queue response: "+err.Error(), nil)
		}
		requestID, statusURL, responseURL = queueResp.RequestID, queueResp.StatusURL, queueResp.ResponseURL
		videoURL = queueResp.Video.URL
		if requestID != "" && validResumeJobID(requestID) == nil {
			noteProviderJob(ctx, "kling_video", requestID, false)
		}
	}

	if videoURL == "" {
		// A submission Facet cannot follow may still have been accepted and
		// charged, so these are not reported as retryable.
		if statusURL == "" && responseURL == "" && requestID == "" {
			return nil, nil, failure("command_failed", "Kling returned neither a video nor a queue request", nil)
		}
		if (statusURL == "" || responseURL == "") && validResumeJobID(requestID) != nil {
			return nil, nil, failure("command_failed", "Kling returned a queue request without a usable id", nil)
		}
		if statusURL == "" {
			statusURL = queueBase + "/" + klingQueueApp + "/requests/" + requestID + "/status"
		}
		if responseURL == "" {
			responseURL = queueBase + "/" + klingQueueApp + "/requests/" + requestID
		}
		videoURL, err = collectFalQueueVideo(ctx, client, authorization, requestID, statusURL, responseURL)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := downloadHTTPFile(ctx, videoURL, outPath); err != nil {
		return nil, nil, failure("command_failed", "failed to download Kling video: "+err.Error(), nil)
	}

	res := map[string]any{
		"provider":     "kling",
		"model":        model,
		"prompt":       r.Prompt,
		"duration":     duration,
		"aspect_ratio": aspectRatio,
		"mode":         mode,
		"output":       outPath,
		"mock":         false,
		"video_url":    videoURL,
		"resumed":      r.ResumeJobID != "",
	}
	if requestID != "" {
		res["provider_job_id"] = requestID
	}
	return res, nil, nil
}

// collectFalQueueVideo waits for a fal queue request and returns its video's
// URL. A request the queue does not know, or one that failed, is reported as
// over (details.provider_job_status), so it is not collected again.
func collectFalQueueVideo(ctx context.Context, client *http.Client, authorization, requestID, statusURL, responseURL string) (string, error) {
	err := pollProviderJob(ctx, "Kling video generation timed out while polling", func() (bool, error) {
		code, body, err := providerGet(ctx, client, statusURL, authorization)
		if err != nil {
			return false, nil // a dropped poll is retried at the next interval
		}
		switch {
		case code == http.StatusNotFound:
			return false, failure("command_failed", "the Kling queue has no request "+requestID,
				map[string]any{"provider_job_status": "not_found"})
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			return false, failure("command_failed", fmt.Sprintf("the Kling queue refused the status request (HTTP %d)", code), nil)
		case code >= http.StatusMultipleChoices:
			return false, nil
		}
		var status struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(body, &status)
		switch status.Status {
		case "COMPLETED":
			return true, nil
		case "FAILED", "ERROR":
			return false, failure("command_failed", "Kling video generation failed on the provider",
				map[string]any{"provider_job_status": "failed"})
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	code, body, err := providerGet(ctx, client, responseURL, authorization)
	if err != nil {
		return "", failure("command_failed", "failed to fetch the Kling result: "+err.Error(), nil)
	}
	if code != http.StatusOK {
		details := map[string]any{}
		if code >= http.StatusBadRequest && code < http.StatusInternalServerError &&
			code != http.StatusUnauthorized && code != http.StatusForbidden && code != http.StatusTooManyRequests {
			// The request ended with an error at the provider; collecting it
			// again returns the same error.
			details["provider_job_status"] = "failed"
		}
		return "", failure("command_failed", fmt.Sprintf("Kling result request failed (HTTP %d): %s", code, bounded(string(body))), details)
	}
	var final struct {
		Video struct {
			URL string `json:"url"`
		} `json:"video"`
	}
	if err := json.Unmarshal(body, &final); err != nil || final.Video.URL == "" {
		return "", failure("provider_response_invalid", "the Kling result names no video", nil)
	}
	return final.Video.URL, nil
}

// pollProviderJob calls check now and then every providerPollInterval until
// it reports the job done or fails, or ctx ends.
func pollProviderJob(ctx context.Context, timeoutMessage string, check func() (done bool, err error)) error {
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return failure("command_timeout", timeoutMessage, nil)
		case <-time.After(providerPollInterval):
		}
	}
}

// providerGet fetches a provider status or result document.
func providerGet(ctx context.Context, client *http.Client, url, authorization string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", authorization)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

func doSoraVideo(op string, data []byte) (any, []string, error) {
	return doSoraVideoContext(context.Background(), op, data)
}

func doSoraVideoContext(parent context.Context, op string, data []byte) (any, []string, error) {
	var r soraVideoRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	if err := resumeRequest(r.ResumeJobID, r.Mock); err != nil {
		return nil, nil, err
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 300)
	if err != nil {
		return nil, nil, err
	}
	if !finite(r.Duration) || r.Duration < 0 || r.Duration > 20 || (r.Duration > 0 && r.Duration < 1) ||
		!slices.Contains([]string{"", "16:9", "9:16", "1:1"}, r.AspectRatio) ||
		!slices.Contains([]string{"", "720p", "1080p"}, r.Resolution) {
		return nil, nil, failure("invalid_request", "invalid Sora duration, aspect_ratio, or resolution", nil)
	}

	model := r.Model
	if model == "" {
		model = "sora-2"
	}

	duration := r.Duration
	if duration <= 0 {
		duration = 5.0
	}

	aspectRatio := r.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}

	resolution := r.Resolution
	if resolution == "" {
		resolution = "720p"
	}

	cost := 0.20
	if duration > 5 {
		cost = 0.40
	}
	if resolution == "1080p" {
		cost += 0.20
	}

	if op == "estimate" {
		if r.ResumeJobID != "" {
			return resumeEstimate("sora_video_collect", r.ResumeJobID), nil, nil
		}
		res := estimateResult([]string{"sora_video_generate"})
		res["estimated_cost"] = cost
		res["network"] = true
		return res, nil, nil
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" && !r.Mock {
		return nil, nil, failure("credentials_missing", "OPENAI_API_KEY is required unless mock=true", nil)
	}
	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(parent, "sora_video.mp4")
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	if r.Mock {
		if err := createMockVideo(outPath, 1280, 720, duration); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"provider":     "sora",
			"model":        model,
			"prompt":       r.Prompt,
			"duration":     duration,
			"aspect_ratio": aspectRatio,
			"resolution":   resolution,
			"output":       outPath,
			"mock":         true,
			"video_url":    "mock://openai/sora/" + filepath.Base(outPath),
		}, nil, nil
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	baseURL := strings.TrimRight(os.Getenv("OPENAI_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	authorization := "Bearer " + apiKey
	client := &http.Client{}

	var jobID, videoURL string
	if r.ResumeJobID != "" {
		jobID = r.ResumeJobID
		noteProviderJob(ctx, "sora_video", jobID, true)
	} else {
		payload := map[string]any{
			"model":        model,
			"prompt":       r.Prompt,
			"duration":     duration,
			"aspect_ratio": aspectRatio,
			"resolution":   resolution,
		}

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, failure("command_failed", "failed to serialize Sora request: "+err.Error(), nil)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/videos/generations", bytes.NewReader(payloadBytes))
		if err != nil {
			return nil, nil, failure("command_failed", "failed to create Sora request: "+err.Error(), nil)
		}
		httpReq.Header.Set("Authorization", authorization)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, nil, failure("command_failed", "Sora request failed: "+err.Error(), nil)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, failure("command_failed", "failed to read Sora response: "+err.Error(), nil)
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			return nil, nil, failure("command_failed", fmt.Sprintf("OpenAI Sora API error (HTTP %d): %s", resp.StatusCode, bounded(string(body))), nil)
		}

		var job soraJob
		_ = json.Unmarshal(body, &job)
		jobID, videoURL = job.ID, job.videoURL()
		if jobID != "" && validResumeJobID(jobID) == nil {
			noteProviderJob(ctx, "sora_video", jobID, false)
		}
		if videoURL == "" && job.Status == "failed" {
			return nil, nil, failure("command_failed", "Sora video generation failed on the provider",
				map[string]any{"provider_job_status": "failed"})
		}
	}

	if videoURL == "" {
		if validResumeJobID(jobID) != nil {
			// The submission may still have been accepted and charged, so
			// this is not reported as retryable.
			return nil, nil, failure("command_failed", "Sora returned neither a video nor a usable job id", nil)
		}
		videoURL, err = collectSoraVideo(ctx, client, authorization, baseURL+"/v1/videos/generations/"+jobID, jobID)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := downloadHTTPFile(ctx, videoURL, outPath); err != nil {
		return nil, nil, failure("command_failed", "failed to download Sora video: "+err.Error(), nil)
	}

	res := map[string]any{
		"provider":     "sora",
		"model":        model,
		"prompt":       r.Prompt,
		"duration":     duration,
		"aspect_ratio": aspectRatio,
		"resolution":   resolution,
		"output":       outPath,
		"mock":         false,
		"video_url":    videoURL,
		"resumed":      r.ResumeJobID != "",
	}
	if jobID != "" {
		res["provider_job_id"] = jobID
	}
	return res, nil, nil
}

// soraJob is a Sora video job as the API reports it.
type soraJob struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	VideoURL string `json:"video_url"`
	Data     []struct {
		URL string `json:"url"`
	} `json:"data"`
}

func (j soraJob) videoURL() string {
	if j.VideoURL != "" {
		return j.VideoURL
	}
	if len(j.Data) > 0 {
		return j.Data[0].URL
	}
	return ""
}

// collectSoraVideo waits for a Sora job and returns its video's URL. A job
// the API does not know, or one that failed, is reported as over
// (details.provider_job_status), so it is not collected again.
func collectSoraVideo(ctx context.Context, client *http.Client, authorization, jobURL, jobID string) (string, error) {
	var videoURL string
	err := pollProviderJob(ctx, "Sora video generation timed out while polling", func() (bool, error) {
		code, body, err := providerGet(ctx, client, jobURL, authorization)
		if err != nil {
			return false, nil // a dropped poll is retried at the next interval
		}
		switch {
		case code == http.StatusNotFound:
			return false, failure("command_failed", "OpenAI has no Sora job "+jobID,
				map[string]any{"provider_job_status": "not_found"})
		case code == http.StatusUnauthorized || code == http.StatusForbidden:
			return false, failure("command_failed", fmt.Sprintf("OpenAI refused the Sora status request (HTTP %d)", code), nil)
		case code >= http.StatusMultipleChoices:
			return false, nil
		}
		var job soraJob
		_ = json.Unmarshal(body, &job)
		switch job.Status {
		case "completed", "succeeded":
			if videoURL = job.videoURL(); videoURL == "" {
				return false, failure("provider_response_invalid", "the completed Sora job names no video", nil)
			}
			return true, nil
		case "failed":
			return false, failure("command_failed", "Sora video generation failed on the provider",
				map[string]any{"provider_job_status": "failed"})
		}
		return false, nil
	})
	return videoURL, err
}

// createMockVideo makes a real, playable placeholder video with FFmpeg, so mock
// mode exercises everything downstream of generation. Without FFmpeg there is
// no honest placeholder: the run fails rather than writing bytes that only
// claim to be video.
func createMockVideo(path string, width, height int, duration float64) error {
	if width <= 0 {
		width = 640
	}
	if height <= 0 {
		height = 360
	}
	if duration <= 0 {
		duration = 1.0
	}
	if _, err := lookPath("ffmpeg"); err != nil {
		return failure("dependency_missing", "mock mode makes its placeholder video with ffmpeg, which is not available", nil)
	}
	parent := filepath.Dir(path)
	if parent != "." {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return failure("command_failed", "unable to create the output directory: "+err.Error(), nil)
		}
	}
	durStr := fmt.Sprintf("%.2f", duration)
	sizeStr := fmt.Sprintf("%dx%d", width, height)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=navy:size=%s:rate=24:duration=%s", sizeStr, durStr),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%s", durStr),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest",
		path,
	}
	if _, err := runCommand(30*time.Second, "ffmpeg", args...); err != nil {
		return err
	}
	return nil
}
