package toolbox

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenAIImageEstimateAndMock(t *testing.T) {
	dir := t.TempDir()
	outImg := filepath.Join(dir, "gen.png")

	// Estimate
	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "A futuristic cityscape at dusk",
		"aspect_ratio": "16:9",
		"quality":      "hd",
	})
	est, _, err := doOpenAIImage("estimate", reqData)
	if err != nil {
		t.Fatalf("openai_image estimate failed: %v", err)
	}
	estMap := est.(map[string]any)
	if estMap["estimated_cost"].(float64) <= 0 || !estMap["network"].(bool) {
		t.Fatalf("unexpected estimate result: %#v", estMap)
	}

	// Mock output must be explicitly requested, regardless of credentials.
	origKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("OPENAI_API_KEY", origKey)
		}
	}()

	runReq, _ := json.Marshal(map[string]any{
		"prompt":      "A futuristic cityscape at dusk",
		"size":        "1024x1024",
		"output_path": outImg,
		"mock":        true,
	})
	res, _, err := doOpenAIImage("run", runReq)
	if err != nil {
		t.Fatalf("openai_image run failed in mock mode: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != true || resMap["provider"] != "openai" {
		t.Fatalf("unexpected run result: %#v", resMap)
	}

	// Verify generated PNG file is a valid image
	f, err := os.Open(outImg)
	if err != nil {
		t.Fatalf("output image file was not created: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("output file is not a valid PNG: %v", err)
	}
	if img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		t.Fatalf("invalid image bounds: %v", img.Bounds())
	}
}

func TestFluxImageEstimateAndMock(t *testing.T) {
	dir := t.TempDir()
	outImg := filepath.Join(dir, "flux.png")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "Cinematic portrait of an astronaut",
		"aspect_ratio": "16:9",
		"model":        "fal-ai/flux-pro",
	})
	est, _, err := doFluxImage("estimate", reqData)
	if err != nil {
		t.Fatalf("flux_image estimate failed: %v", err)
	}
	estMap := est.(map[string]any)
	if estMap["estimated_cost"].(float64) <= 0 {
		t.Fatalf("unexpected flux estimate: %#v", estMap)
	}

	origKey := os.Getenv("FAL_KEY")
	os.Unsetenv("FAL_KEY")
	os.Unsetenv("FLUX_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("FAL_KEY", origKey)
		}
	}()

	runReq, _ := json.Marshal(map[string]any{
		"prompt":       "Cinematic portrait of an astronaut",
		"aspect_ratio": "16:9",
		"output_path":  outImg,
		"mock":         true,
	})
	res, _, err := doFluxImage("run", runReq)
	if err != nil {
		t.Fatalf("flux_image run failed in mock mode: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != true || resMap["provider"] != "flux" {
		t.Fatalf("unexpected flux run result: %#v", resMap)
	}

	if _, err := os.Stat(outImg); err != nil {
		t.Fatalf("output image file missing: %v", err)
	}
}

func TestKlingVideoEstimateAndMock(t *testing.T) {
	dir := t.TempDir()
	outVid := filepath.Join(dir, "kling.mp4")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "Ocean waves in slow motion",
		"duration":     10.0,
		"mode":         "pro",
		"aspect_ratio": "16:9",
	})
	est, _, err := doKlingVideo("estimate", reqData)
	if err != nil {
		t.Fatalf("kling_video estimate failed: %v", err)
	}
	estMap := est.(map[string]any)
	if estMap["estimated_cost"].(float64) < 0.20 {
		t.Fatalf("unexpected kling estimate: %#v", estMap)
	}

	origKey := os.Getenv("FAL_KEY")
	os.Unsetenv("FAL_KEY")
	os.Unsetenv("KLING_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("FAL_KEY", origKey)
		}
	}()

	runReq, _ := json.Marshal(map[string]any{
		"prompt":      "Ocean waves in slow motion",
		"duration":    5.0,
		"output_path": outVid,
		"mock":        true,
	})
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Without FFmpeg there is no honest placeholder video: mock mode fails.
		if _, _, err := doKlingVideo("run", runReq); err == nil {
			t.Fatal("mock mode succeeded without ffmpeg")
		}
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Without FFmpeg there is no honest placeholder video: mock mode fails.
		if _, _, err := doKlingVideo("run", runReq); err == nil {
			t.Fatal("mock mode succeeded without ffmpeg")
		}
		t.Skip("ffmpeg not installed")
	}
	res, _, err := doKlingVideo("run", runReq)
	if err != nil {
		t.Fatalf("kling_video run failed in mock mode: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != true || resMap["provider"] != "kling" {
		t.Fatalf("unexpected kling run result: %#v", resMap)
	}

	if _, err := os.Stat(outVid); err != nil {
		t.Fatalf("output video file missing: %v", err)
	}
}

func TestSoraVideoEstimateAndMock(t *testing.T) {
	dir := t.TempDir()
	outVid := filepath.Join(dir, "sora.mp4")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "Drone flight over autumn forest",
		"duration":     5.0,
		"resolution":   "1080p",
		"aspect_ratio": "16:9",
	})
	est, _, err := doSoraVideo("estimate", reqData)
	if err != nil {
		t.Fatalf("sora_video estimate failed: %v", err)
	}
	estMap := est.(map[string]any)
	if estMap["estimated_cost"].(float64) < 0.20 {
		t.Fatalf("unexpected sora estimate: %#v", estMap)
	}

	origKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer func() {
		if origKey != "" {
			os.Setenv("OPENAI_API_KEY", origKey)
		}
	}()

	runReq, _ := json.Marshal(map[string]any{
		"prompt":      "Drone flight over autumn forest",
		"duration":    5.0,
		"output_path": outVid,
		"mock":        true,
	})
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Without FFmpeg there is no honest placeholder video: mock mode fails.
		if _, _, err := doSoraVideo("run", runReq); err == nil {
			t.Fatal("mock mode succeeded without ffmpeg")
		}
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Without FFmpeg there is no honest placeholder video: mock mode fails.
		if _, _, err := doSoraVideo("run", runReq); err == nil {
			t.Fatal("mock mode succeeded without ffmpeg")
		}
		t.Skip("ffmpeg not installed")
	}
	res, _, err := doSoraVideo("run", runReq)
	if err != nil {
		t.Fatalf("sora_video run failed in mock mode: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != true || resMap["provider"] != "sora" {
		t.Fatalf("unexpected sora run result: %#v", resMap)
	}

	if _, err := os.Stat(outVid); err != nil {
		t.Fatalf("output video file missing: %v", err)
	}
}

func TestColorGradeFFmpegExecution(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	srcVid := filepath.Join(dir, "input.mp4")
	ffmpeg(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", srcVid)

	// 1. Estimate
	estReq, _ := json.Marshal(map[string]any{
		"input_path":  srcVid,
		"output_path": filepath.Join(dir, "out_est.mp4"),
		"profile":     "cinematic_warm",
	})
	est, _, err := doColorGrade("estimate", estReq)
	if err != nil {
		t.Fatalf("color_grade estimate failed: %v", err)
	}
	if est.(map[string]any)["estimated_cost"] != 0.0 {
		t.Fatalf("color_grade estimate cost should be 0: %#v", est)
	}

	// 2. Cinematic Warm Grade with intensity 0.8
	gradedWarm := filepath.Join(dir, "graded_warm.mp4")
	warmReq, _ := json.Marshal(map[string]any{
		"input_path":  srcVid,
		"output_path": gradedWarm,
		"profile":     "cinematic_warm",
		"intensity":   0.8,
	})
	res, _, err := doColorGrade("run", warmReq)
	if err != nil {
		t.Fatalf("color_grade warm failed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["profile"] != "cinematic_warm" || resMap["output"] != gradedWarm {
		t.Fatalf("unexpected color grade result: %#v", resMap)
	}

	// Verify graded output with probe
	p, _, err := probe(gradedWarm, 30_000_000_000)
	if err != nil {
		t.Fatalf("probe of graded output failed: %v", err)
	}
	v := firstVideo(p)
	if v["width"] != 320 || v["height"] != 240 {
		t.Fatalf("unexpected dimensions: %#v", v)
	}
	if !hasAudio(p) {
		t.Fatalf("expected audio track to be preserved in color grade: %#v", p)
	}

	// 3. High Contrast with extra adjustments
	gradedHC := filepath.Join(dir, "graded_hc.mp4")
	contrastVal := 1.1
	satVal := 1.2
	hcReq, _ := json.Marshal(map[string]any{
		"input_path":  srcVid,
		"output_path": gradedHC,
		"profile":     "high_contrast",
		"contrast":    &contrastVal,
		"saturation":  &satVal,
		"intensity":   1.0,
	})
	if _, _, err := doColorGrade("run", hcReq); err != nil {
		t.Fatalf("color_grade high_contrast failed: %v", err)
	}
	if _, err := os.Stat(gradedHC); err != nil {
		t.Fatalf("high contrast output missing: %v", err)
	}
}

func TestImageSelectorRankingAndFacts(t *testing.T) {
	// 1. General discovery
	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "A complex technical system diagram with labeled components and annotations",
		"aspect_ratio": "16:9",
	})
	res, _, err := doImageSelector("run", reqData)
	if err != nil {
		t.Fatalf("image_selector run failed: %v", err)
	}
	resMap := res.(map[string]any)
	candidates := resMap["candidates"].([]imageCandidateFact)
	// openai_image, flux_image and wikimedia: only tools Facet has.
	if len(candidates) < 3 {
		t.Fatalf("expected at least 3 image candidates, got %d", len(candidates))
	}
	if resMap["selected_recommendation"] == "" {
		t.Fatalf("missing selected_recommendation: %#v", resMap)
	}

	// Verify transparent facts exist on all candidates
	for _, c := range candidates {
		if c.Name == "" || c.Provider == "" || len(c.SupportedAspectRatios) == 0 || len(c.Strengths) == 0 {
			t.Fatalf("candidate missing required facts: %#v", c)
		}
	}

	// 2. Free budget constraint
	freeReq, _ := json.Marshal(map[string]any{
		"prompt":      "City skyline at sunrise",
		"budget_tier": "free",
	})
	freeRes, _, err := doImageSelector("run", freeReq)
	if err != nil {
		t.Fatalf("free image selector failed: %v", err)
	}
	freeCandidates := freeRes.(map[string]any)["candidates"].([]imageCandidateFact)
	// Top candidate under free budget tier must have estimated_cost == 0.0
	if freeCandidates[0].EstimatedCost != 0.0 {
		t.Fatalf("expected free candidate at top rank for free budget tier, got: %#v", freeCandidates[0])
	}

	// 3. Preferred provider
	prefReq, _ := json.Marshal(map[string]any{
		"prompt":             "Artistic concept illustration",
		"preferred_provider": "flux",
	})
	prefRes, _, err := doImageSelector("run", prefReq)
	if err != nil {
		t.Fatalf("preferred provider selector failed: %v", err)
	}
	prefRec := prefRes.(map[string]any)["selected_recommendation"].(string)
	if prefRec != "flux_image" {
		t.Fatalf("expected flux_image recommendation, got %s", prefRec)
	}
}

func TestVideoSelectorRankingAndFacts(t *testing.T) {
	// 1. Cinematic intent
	dur := 5.0
	reqData, _ := json.Marshal(map[string]any{
		"prompt":       "Epic cinematic drone shot flying over a frozen glacier at sunset",
		"duration":     &dur,
		"aspect_ratio": "16:9",
		"intent":       "cinematic",
	})
	res, _, err := doVideoSelector("run", reqData)
	if err != nil {
		t.Fatalf("video_selector run failed: %v", err)
	}
	resMap := res.(map[string]any)
	candidates := resMap["candidates"].([]videoCandidateFact)
	if len(candidates) < 5 {
		t.Fatalf("expected at least 5 video candidates, got %d", len(candidates))
	}
	if resMap["selected_recommendation"] == "" {
		t.Fatalf("missing selected_recommendation: %#v", resMap)
	}

	// Check candidate facts: min/max duration, supported aspect ratios, native audio
	for _, c := range candidates {
		if c.MinDuration <= 0 || c.MaxDuration <= 0 || len(c.SupportedAspectRatios) == 0 {
			t.Fatalf("video candidate missing facts: %#v", c)
		}
	}

	// 2. Audio required constraint
	audioReq, _ := json.Marshal(map[string]any{
		"prompt":        "Character talking in a coffee shop with background music and ambient sounds",
		"require_audio": true,
	})
	audioRes, _, err := doVideoSelector("run", audioReq)
	if err != nil {
		t.Fatalf("audio required video selector failed: %v", err)
	}
	audioCandidates := audioRes.(map[string]any)["candidates"].([]videoCandidateFact)
	// Candidates supporting native audio should be boosted
	foundNativeAudio := false
	for _, c := range audioCandidates[:2] {
		if c.SupportsNativeAudio {
			foundNativeAudio = true
			break
		}
	}
	if !foundNativeAudio {
		t.Fatalf("expected native audio candidates near top when require_audio=true: %#v", audioCandidates)
	}

	// 3. Stock mode filter
	stockReq, _ := json.Marshal(map[string]any{
		"prompt":      "Traffic on highway",
		"source_type": "stock",
	})
	stockRes, _, err := doVideoSelector("run", stockReq)
	if err != nil {
		t.Fatalf("stock video selector failed: %v", err)
	}
	stockCandidates := stockRes.(map[string]any)["candidates"].([]videoCandidateFact)
	if stockCandidates[0].Type != "stock" {
		t.Fatalf("expected stock candidate on top for source_type='stock': %#v", stockCandidates[0])
	}
}

func TestDownloadHTTPFileHelper(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock binary media content"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "downloaded.bin")
	if err := downloadHTTPFile(t.Context(), ts.URL, dest); err != nil {
		t.Fatalf("downloadHTTPFile failed: %v", err)
	}
	content, err := os.ReadFile(dest)
	if err != nil || string(content) != "mock binary media content" {
		t.Fatalf("download content mismatch: %s, err: %v", string(content), err)
	}
}

func TestOpenAIImageLiveHTTP(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Return 1x1 PNG encoded in base64: iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==
		resp := map[string]any{
			"data": []map[string]any{
				{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_BASE_URL", mockServer.URL)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "openai_live.png")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":      "A majestic eagle in flight",
		"output_path": outPath,
	})
	res, _, err := doOpenAIImage("run", reqData)
	if err != nil {
		t.Fatalf("live HTTP call failed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != false || resMap["provider"] != "openai" {
		t.Fatalf("expected live result, got: %#v", resMap)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file not created from live b64 response: %v", err)
	}
}

func TestFluxImageLiveHTTP(t *testing.T) {
	var mediaServer *httptest.Server
	mediaServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image.jpg" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("mock-jpeg-bytes"))
			return
		}
		if r.Header.Get("Authorization") != "Key test-fal-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"images": []map[string]any{
				{"url": mediaServer.URL + "/image.jpg"},
			},
			"seed": 12345,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mediaServer.Close()

	t.Setenv("FAL_KEY", "test-fal-key")
	t.Setenv("FAL_BASE_URL", mediaServer.URL)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "flux_live.png")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":      "Neon cyberpunk street",
		"output_path": outPath,
	})
	res, _, err := doFluxImage("run", reqData)
	if err != nil {
		t.Fatalf("live FLUX HTTP call failed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != false || resMap["provider"] != "flux" {
		t.Fatalf("expected live FLUX result, got: %#v", resMap)
	}
	data, err := os.ReadFile(outPath)
	if err != nil || string(data) != "mock-jpeg-bytes" {
		t.Fatalf("output file content mismatch: %s, err: %v", string(data), err)
	}
}

func TestKlingVideoLiveHTTP(t *testing.T) {
	var mediaServer *httptest.Server
	mediaServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/video.mp4" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("mock-kling-mp4-data"))
			return
		}
		if r.Header.Get("Authorization") != "Key test-kling-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"request_id": "req-12345",
			"video": map[string]any{
				"url": mediaServer.URL + "/video.mp4",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mediaServer.Close()

	t.Setenv("FAL_KEY", "test-kling-key")
	t.Setenv("FAL_QUEUE_BASE_URL", mediaServer.URL)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "kling_live.mp4")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":      "Waterfall in rainforest",
		"duration":    5.0,
		"output_path": outPath,
	})
	res, _, err := doKlingVideo("run", reqData)
	if err != nil {
		t.Fatalf("live Kling HTTP call failed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != false || resMap["provider"] != "kling" {
		t.Fatalf("expected live Kling result, got: %#v", resMap)
	}
	data, err := os.ReadFile(outPath)
	if err != nil || string(data) != "mock-kling-mp4-data" {
		t.Fatalf("output video content mismatch: %s, err: %v", string(data), err)
	}
}

func TestSoraVideoLiveHTTP(t *testing.T) {
	var mediaServer *httptest.Server
	mediaServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sora.mp4" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("mock-sora-mp4-data"))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-sora-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"id":        "sora-job-99",
			"video_url": mediaServer.URL + "/sora.mp4",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mediaServer.Close()

	t.Setenv("OPENAI_API_KEY", "test-sora-key")
	t.Setenv("OPENAI_BASE_URL", mediaServer.URL)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "sora_live.mp4")

	reqData, _ := json.Marshal(map[string]any{
		"prompt":      "Snow covered mountain summit",
		"duration":    5.0,
		"output_path": outPath,
	})
	res, _, err := doSoraVideo("run", reqData)
	if err != nil {
		t.Fatalf("live Sora HTTP call failed: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["mock"] != false || resMap["provider"] != "sora" {
		t.Fatalf("expected live Sora result, got: %#v", resMap)
	}
	data, err := os.ReadFile(outPath)
	if err != nil || string(data) != "mock-sora-mp4-data" {
		t.Fatalf("output video content mismatch: %s, err: %v", string(data), err)
	}
}
