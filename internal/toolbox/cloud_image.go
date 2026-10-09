package toolbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type openAIImageRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	Size           string `json:"size,omitempty"`
	AspectRatio    string `json:"aspect_ratio,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	OutputPath     string `json:"output_path,omitempty"`
	Mock           bool   `json:"mock,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type fluxImageRequest struct {
	Prompt         string   `json:"prompt"`
	Model          string   `json:"model,omitempty"`
	AspectRatio    string   `json:"aspect_ratio,omitempty"`
	ImageSize      string   `json:"image_size,omitempty"`
	NumImages      int      `json:"num_images,omitempty"`
	GuidanceScale  *float64 `json:"guidance_scale,omitempty"`
	Seed           *int64   `json:"seed,omitempty"`
	OutputPath     string   `json:"output_path,omitempty"`
	Mock           bool     `json:"mock,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

func doOpenAIImage(op string, data []byte) (any, []string, error) {
	return doOpenAIImageContext(context.Background(), op, data)
}

func doOpenAIImageContext(parent context.Context, op string, data []byte) (any, []string, error) {
	var r openAIImageRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 120)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains([]string{"", "1:1", "16:9", "9:16", "4:3", "3:4"}, r.AspectRatio) ||
		!slices.Contains([]string{"", "standard", "hd"}, r.Quality) ||
		!slices.Contains([]string{"", "vivid", "natural"}, r.Style) {
		return nil, nil, failure("invalid_request", "invalid OpenAI image aspect_ratio, quality, or style", nil)
	}

	model := r.Model
	if model == "" {
		model = "dall-e-3"
	}

	quality := r.Quality
	if quality == "" {
		quality = "standard"
	}

	size := r.Size
	if size == "" {
		switch r.AspectRatio {
		case "16:9":
			size = "1792x1024"
		case "9:16":
			size = "1024x1792"
		case "1:1":
			size = "1024x1024"
		case "4:3":
			size = "1024x768"
		case "3:4":
			size = "768x1024"
		default:
			size = "1024x1024"
		}
	}

	cost := 0.040
	if model == "dall-e-3" {
		if quality == "hd" {
			if size == "1024x1024" {
				cost = 0.080
			} else {
				cost = 0.120
			}
		} else {
			if size != "1024x1024" {
				cost = 0.080
			}
		}
	} else if model == "dall-e-2" {
		cost = 0.020
	}

	if op == "estimate" {
		res := estimateResult([]string{"openai_image_generate"})
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
		outPath = defaultOutput(parent, "openai_image.png")
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	if r.Mock {
		if err := createMockPNG(outPath, 1024, 1024, "OpenAI Image: "+r.Prompt); err != nil {
			return nil, nil, failure("command_failed", "failed to create mock image: "+err.Error(), nil)
		}
		return map[string]any{
			"provider": "openai",
			"model":    model,
			"prompt":   r.Prompt,
			"size":     size,
			"quality":  quality,
			"output":   outPath,
			"mock":     true,
			"url":      "mock://openai/image/" + filepath.Base(outPath),
		}, nil, nil
	}

	payload := map[string]any{
		"model":           model,
		"prompt":          r.Prompt,
		"size":            size,
		"quality":         quality,
		"n":               1,
		"response_format": "b64_json",
	}
	if r.Style != "" {
		payload["style"] = r.Style
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, failure("command_failed", "failed to serialize OpenAI image request: "+err.Error(), nil)
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	endpoint := baseURL + "/v1/images/generations"

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, nil, failure("command_failed", "failed to create OpenAI request: "+err.Error(), nil)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, failure("command_failed", "OpenAI image request failed: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, failure("command_failed", "failed to read OpenAI image response: "+err.Error(), nil)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, failure("command_failed", fmt.Sprintf("OpenAI API error (HTTP %d): %s", resp.StatusCode, bounded(string(body))), nil)
	}

	var openAIResp struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &openAIResp); err != nil || len(openAIResp.Data) == 0 {
		return nil, nil, failure("command_failed", "invalid OpenAI image response format", nil)
	}

	imageURL := openAIResp.Data[0].URL
	if openAIResp.Data[0].B64JSON != "" {
		decoded, decErr := base64.StdEncoding.DecodeString(openAIResp.Data[0].B64JSON)
		if decErr != nil {
			return nil, nil, failure("command_failed", "failed to decode OpenAI b64_json: "+decErr.Error(), nil)
		}
		if err := os.WriteFile(outPath, decoded, 0644); err != nil {
			return nil, nil, failure("command_failed", "failed to save image to output_path: "+err.Error(), nil)
		}
	} else if imageURL != "" {
		if err := downloadHTTPFile(ctx, imageURL, outPath); err != nil {
			return nil, nil, failure("command_failed", "failed to download OpenAI image from URL: "+err.Error(), nil)
		}
	} else {
		return nil, nil, failure("command_failed", "no image data received from OpenAI", nil)
	}

	return map[string]any{
		"provider": "openai",
		"model":    model,
		"prompt":   r.Prompt,
		"size":     size,
		"quality":  quality,
		"output":   outPath,
		"mock":     false,
		"url":      imageURL,
	}, nil, nil
}

func doFluxImage(op string, data []byte) (any, []string, error) {
	return doFluxImageContext(context.Background(), op, data)
}

func doFluxImageContext(parent context.Context, op string, data []byte) (any, []string, error) {
	var r fluxImageRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, nil, failure("invalid_request", "prompt is required", nil)
	}
	timeout, err := cloudTimeout(r.TimeoutSeconds, 120)
	if err != nil {
		return nil, nil, err
	}
	if !slices.Contains([]string{"", "1:1", "16:9", "9:16", "4:3", "3:4", "21:9"}, r.AspectRatio) ||
		r.NumImages < 0 || (r.GuidanceScale != nil && (!finite(*r.GuidanceScale) || *r.GuidanceScale < 0)) {
		return nil, nil, failure("invalid_request", "invalid FLUX aspect_ratio, num_images, or guidance_scale", nil)
	}

	model := r.Model
	if model == "" {
		model = "fal-ai/flux-pro"
	}

	aspectRatio := r.AspectRatio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}

	cost := 0.040
	if strings.Contains(model, "dev") {
		cost = 0.030
	} else if strings.Contains(model, "pro/v1.1") {
		cost = 0.050
	}

	if op == "estimate" {
		res := estimateResult([]string{"flux_image_generate"})
		res["estimated_cost"] = cost
		res["network"] = true
		return res, nil, nil
	}

	apiKey := strings.TrimSpace(os.Getenv("FAL_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("FLUX_API_KEY"))
	}
	if apiKey == "" && !r.Mock {
		return nil, nil, failure("credentials_missing", "FAL_KEY or FLUX_API_KEY is required unless mock=true", nil)
	}
	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(parent, "flux_image.png")
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}

	if r.Mock {
		if err := createMockPNG(outPath, 1280, 720, "FLUX Image: "+r.Prompt); err != nil {
			return nil, nil, failure("command_failed", "failed to create mock image: "+err.Error(), nil)
		}
		var seed int64 = 42
		if r.Seed != nil {
			seed = *r.Seed
		}
		return map[string]any{
			"provider":     "flux",
			"model":        model,
			"prompt":       r.Prompt,
			"aspect_ratio": aspectRatio,
			"output":       outPath,
			"mock":         true,
			"url":          "mock://fal/flux/" + filepath.Base(outPath),
			"seed":         seed,
		}, nil, nil
	}

	payload := map[string]any{
		"prompt":     r.Prompt,
		"image_size": aspectRatioToFalSize(aspectRatio),
		"num_images": 1,
		"sync_mode":  true,
	}
	if r.GuidanceScale != nil {
		payload["guidance_scale"] = *r.GuidanceScale
	}
	if r.Seed != nil {
		payload["seed"] = *r.Seed
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, failure("command_failed", "failed to serialize fal request: "+err.Error(), nil)
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	baseURL := os.Getenv("FAL_BASE_URL")
	if baseURL == "" {
		baseURL = "https://fal.run"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + strings.TrimPrefix(model, "/")
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, nil, failure("command_failed", "failed to create fal request: "+err.Error(), nil)
	}
	httpReq.Header.Set("Authorization", "Key "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, nil, failure("command_failed", "FLUX fal.ai request failed: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, failure("command_failed", "failed to read fal.ai response: "+err.Error(), nil)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, failure("command_failed", fmt.Sprintf("fal.ai API error (HTTP %d): %s", resp.StatusCode, bounded(string(body))), nil)
	}

	var falResp struct {
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
		Seed int64 `json:"seed"`
	}
	if err := json.Unmarshal(body, &falResp); err != nil || len(falResp.Images) == 0 {
		return nil, nil, failure("command_failed", "invalid fal.ai response format", nil)
	}

	imgURL := falResp.Images[0].URL
	if err := downloadHTTPFile(ctx, imgURL, outPath); err != nil {
		return nil, nil, failure("command_failed", "failed to download generated image: "+err.Error(), nil)
	}

	return map[string]any{
		"provider":     "flux",
		"model":        model,
		"prompt":       r.Prompt,
		"aspect_ratio": aspectRatio,
		"output":       outPath,
		"mock":         false,
		"url":          imgURL,
		"seed":         falResp.Seed,
	}, nil, nil
}

func aspectRatioToFalSize(ar string) string {
	switch ar {
	case "16:9":
		return "landscape_16_9"
	case "9:16":
		return "portrait_16_9"
	case "4:3":
		return "landscape_4_3"
	case "3:4":
		return "portrait_4_3"
	case "1:1":
		return "square_hd"
	case "21:9":
		return "landscape_21_9"
	default:
		return "landscape_16_9"
	}
}

func createMockPNG(path string, width, height int, text string) error {
	if width <= 0 {
		width = 512
	}
	if height <= 0 {
		height = 512
	}
	parent := filepath.Dir(path)
	if parent != "." {
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{R: 30, G: 45, B: 75, A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	// Draw a simple border
	borderColor := color.RGBA{R: 80, G: 140, B: 220, A: 255}
	for x := 0; x < width; x++ {
		img.Set(x, 0, borderColor)
		img.Set(x, height-1, borderColor)
	}
	for y := 0; y < height; y++ {
		img.Set(0, y, borderColor)
		img.Set(width-1, y, borderColor)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func downloadHTTPFile(ctx context.Context, url, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error %d downloading file", resp.StatusCode)
	}

	parent := filepath.Dir(targetPath)
	if parent != "." {
		_ = os.MkdirAll(parent, 0755)
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
