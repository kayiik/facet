package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type pexelsRequest struct {
	Query            string `json:"query"`
	Orientation      string `json:"orientation,omitempty"`
	Size             string `json:"size,omitempty"`
	MinDuration      int    `json:"min_duration,omitempty"`
	MaxDuration      int    `json:"max_duration,omitempty"`
	PerPage          int    `json:"per_page,omitempty"`
	Page             int    `json:"page,omitempty"`
	PreferredQuality string `json:"preferred_quality,omitempty"`
	OutputPath       string `json:"output_path,omitempty"`
	TimeoutSeconds   int    `json:"timeout_seconds,omitempty"`
}

type pixabayRequest struct {
	Query            string `json:"query"`
	VideoType        string `json:"video_type,omitempty"`
	Category         string `json:"category,omitempty"`
	MinDuration      int    `json:"min_duration,omitempty"`
	MaxDuration      int    `json:"max_duration,omitempty"`
	PerPage          int    `json:"per_page,omitempty"`
	Page             int    `json:"page,omitempty"`
	PreferredQuality string `json:"preferred_quality,omitempty"`
	OutputPath       string `json:"output_path,omitempty"`
	TimeoutSeconds   int    `json:"timeout_seconds,omitempty"`
}

type wikimediaRequest struct {
	Query          string  `json:"query"`
	Kind           string  `json:"kind,omitempty"`
	MinWidth       int     `json:"min_width,omitempty"`
	MinDuration    float64 `json:"min_duration,omitempty"`
	MaxDuration    float64 `json:"max_duration,omitempty"`
	Orientation    string  `json:"orientation,omitempty"`
	PerPage        int     `json:"per_page,omitempty"`
	Page           int     `json:"page,omitempty"`
	OutputPath     string  `json:"output_path,omitempty"`
	TimeoutSeconds int     `json:"timeout_seconds,omitempty"`
}

type stockQueryItem struct {
	Query  string `json:"query"`
	SlotID string `json:"slot_id,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

type directClipSearchRequest struct {
	OutputDir         string           `json:"output_dir"`
	Queries           []stockQueryItem `json:"queries"`
	Sources           []string         `json:"sources,omitempty"`
	ClipsPerQuery     int              `json:"clips_per_query,omitempty"`
	ExtractThumbnails bool             `json:"extract_thumbnails,omitempty"`
	SkipExisting      bool             `json:"skip_existing,omitempty"`
	TimeoutSeconds    int              `json:"timeout_seconds,omitempty"`
}

type stockCandidate struct {
	ClipID      string  `json:"clip_id"`
	Source      string  `json:"source"`
	SourceID    string  `json:"source_id"`
	SourceURL   string  `json:"source_url"`
	DownloadURL string  `json:"download_url"`
	Kind        string  `json:"kind"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Duration    float64 `json:"duration"`
	Creator     string  `json:"creator,omitempty"`
	License     string  `json:"license,omitempty"`
}

func doPexelsVideo(op string, data []byte) (any, []string, error) {
	return doPexelsVideoContext(context.Background(), op, data)
}

func doPexelsVideoContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r pexelsRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Query) == "" {
		return nil, nil, failure("invalid_request", "query is required", nil)
	}
	if op == "estimate" {
		return estimateResult([]string{"pexels_search", "pexels_download"}), nil, nil
	}

	apiKey := os.Getenv("PEXELS_API_KEY")
	if apiKey == "" {
		return nil, nil, failure("unconfigured", "PEXELS_API_KEY environment variable is not set", nil)
	}

	perPage := r.PerPage
	if perPage <= 0 {
		perPage = 5
	}
	page := r.Page
	if page <= 0 {
		page = 1
	}

	u, _ := url.Parse("https://api.pexels.com/videos/search")
	q := u.Query()
	q.Set("query", r.Query)
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	if r.Orientation != "" {
		q.Set("orientation", r.Orientation)
	}
	if r.Size != "" {
		q.Set("size", r.Size)
	}
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("Authorization", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, failure("command_failed", "Pexels search request failed: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, failure("command_failed", fmt.Sprintf("Pexels API error (HTTP %d): %s", resp.StatusCode, string(body)), nil)
	}

	var pexelsResp struct {
		TotalResults int `json:"total_results"`
		Videos       []struct {
			ID       int    `json:"id"`
			Duration int    `json:"duration"`
			URL      string `json:"url"`
			User     struct {
				Name string `json:"name"`
			} `json:"user"`
			VideoFiles []struct {
				ID      int     `json:"id"`
				Quality string  `json:"quality"`
				Width   int     `json:"width"`
				Height  int     `json:"height"`
				FPS     float64 `json:"fps"`
				Link    string  `json:"link"`
			} `json:"video_files"`
		} `json:"videos"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pexelsResp); err != nil {
		return nil, nil, failure("command_failed", "unable to parse Pexels response", nil)
	}

	if len(pexelsResp.Videos) == 0 {
		return nil, nil, failure("not_found", "no videos found for query: "+r.Query, map[string]any{"total_results": 0})
	}

	chosenVideo := pexelsResp.Videos[0]
	var chosenFile struct {
		ID      int     `json:"id"`
		Quality string  `json:"quality"`
		Width   int     `json:"width"`
		Height  int     `json:"height"`
		FPS     float64 `json:"fps"`
		Link    string  `json:"link"`
	}
	prefQual := r.PreferredQuality
	if prefQual == "" {
		prefQual = "hd"
	}
	for _, vf := range chosenVideo.VideoFiles {
		if vf.Quality == prefQual {
			chosenFile = vf
			break
		}
	}
	if chosenFile.Link == "" && len(chosenVideo.VideoFiles) > 0 {
		chosenFile = chosenVideo.VideoFiles[0]
	}

	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(ctx, fmt.Sprintf("pexels_video_%d.mp4", chosenVideo.ID))
	}
	if err := downloadFileContext(ctx, chosenFile.Link, outPath, 120*time.Second); err != nil {
		return nil, nil, failure("command_failed", "unable to download video from Pexels: "+err.Error(), nil)
	}

	return map[string]any{
		"provider":         "pexels",
		"video_id":         chosenVideo.ID,
		"user":             chosenVideo.User.Name,
		"duration_seconds": chosenVideo.Duration,
		"width":            chosenFile.Width,
		"height":           chosenFile.Height,
		"fps":              chosenFile.FPS,
		"quality":          chosenFile.Quality,
		"query":            r.Query,
		"output":           outPath,
		"total_results":    pexelsResp.TotalResults,
		"results_returned": len(pexelsResp.Videos),
		"license":          "Pexels License (free, no attribution required)",
		"pexels_url":       chosenVideo.URL,
	}, nil, nil
}

func doPixabayVideo(op string, data []byte) (any, []string, error) {
	return doPixabayVideoContext(context.Background(), op, data)
}

func doPixabayVideoContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r pixabayRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Query) == "" {
		return nil, nil, failure("invalid_request", "query is required", nil)
	}
	if op == "estimate" {
		return estimateResult([]string{"pixabay_search", "pixabay_download"}), nil, nil
	}

	apiKey := os.Getenv("PIXABAY_API_KEY")
	if apiKey == "" {
		return nil, nil, failure("unconfigured", "PIXABAY_API_KEY environment variable is not set", nil)
	}

	perPage := r.PerPage
	if perPage < 3 {
		perPage = 5
	}
	if perPage > 200 {
		perPage = 200
	}
	page := r.Page
	if page <= 0 {
		page = 1
	}

	u, _ := url.Parse("https://pixabay.com/api/videos/")
	q := u.Query()
	q.Set("key", apiKey)
	q.Set("q", r.Query)
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	q.Set("safesearch", "true")
	if r.VideoType != "" && r.VideoType != "all" {
		q.Set("video_type", r.VideoType)
	}
	if r.Category != "" {
		q.Set("category", r.Category)
	}
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, failure("command_failed", "Pixabay search request failed: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, failure("command_failed", fmt.Sprintf("Pixabay API error (HTTP %d): %s", resp.StatusCode, string(body)), nil)
	}

	var pixabayResp struct {
		Total int `json:"total"`
		Hits  []struct {
			ID       int    `json:"id"`
			Duration int    `json:"duration"`
			User     string `json:"user"`
			Tags     string `json:"tags"`
			PageURL  string `json:"pageURL"`
			Videos   map[string]struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
				Size   int    `json:"size"`
			} `json:"videos"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pixabayResp); err != nil {
		return nil, nil, failure("command_failed", "unable to parse Pixabay response", nil)
	}

	if len(pixabayResp.Hits) == 0 {
		return nil, nil, failure("not_found", "no videos found for query: "+r.Query, map[string]any{"total_results": 0})
	}

	chosenHit := pixabayResp.Hits[0]
	prefQual := r.PreferredQuality
	if prefQual == "" {
		prefQual = "large"
	}
	vInfo, ok := chosenHit.Videos[prefQual]
	if !ok || vInfo.URL == "" {
		for _, qKey := range []string{"large", "medium", "small", "tiny"} {
			if candidate, exists := chosenHit.Videos[qKey]; exists && candidate.URL != "" {
				vInfo = candidate
				break
			}
		}
	}

	if vInfo.URL == "" {
		return nil, nil, failure("not_found", "no downloadable video file found in hit", nil)
	}

	outPath := r.OutputPath
	if outPath == "" {
		outPath = defaultOutput(ctx, fmt.Sprintf("pixabay_video_%d.mp4", chosenHit.ID))
	}
	if err := downloadFileContext(ctx, vInfo.URL, outPath, 120*time.Second); err != nil {
		return nil, nil, failure("command_failed", "unable to download video from Pixabay: "+err.Error(), nil)
	}

	return map[string]any{
		"provider":         "pixabay",
		"video_id":         chosenHit.ID,
		"user":             chosenHit.User,
		"tags":             chosenHit.Tags,
		"duration_seconds": chosenHit.Duration,
		"width":            vInfo.Width,
		"height":           vInfo.Height,
		"query":            r.Query,
		"output":           outPath,
		"total_results":    pixabayResp.Total,
		"results_returned": len(pixabayResp.Hits),
		"license":          "Pixabay Content License (free, no attribution required)",
		"page_url":         chosenHit.PageURL,
	}, nil, nil
}

func doWikimedia(op string, data []byte) (any, []string, error) {
	return doWikimediaContext(context.Background(), op, data)
}

func doWikimediaContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r wikimediaRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.Query) == "" {
		return nil, nil, failure("invalid_request", "query is required", nil)
	}
	if op == "estimate" {
		return estimateResult([]string{"wikimedia_search", "wikimedia_download"}), nil, nil
	}

	kind := r.Kind
	if kind == "" {
		kind = "video"
	}
	searchPrefix := "filetype:video"
	if kind == "image" {
		searchPrefix = "filetype:image"
	}
	searchText := fmt.Sprintf("%s %s", searchPrefix, r.Query)

	perPage := r.PerPage
	if perPage <= 0 {
		perPage = 5
	}
	if perPage > 50 {
		perPage = 50
	}
	page := r.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * perPage

	u, _ := url.Parse("https://commons.wikimedia.org/w/api.php")
	q := u.Query()
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("generator", "search")
	q.Set("gsrsearch", searchText)
	q.Set("gsrnamespace", "6")
	q.Set("gsrlimit", strconv.Itoa(perPage))
	q.Set("gsroffset", strconv.Itoa(offset))
	q.Set("prop", "imageinfo|info")
	q.Set("iiprop", "url|size|mime|extmetadata|mediatype")
	q.Set("iiurlwidth", "640")
	q.Set("inprop", "url")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", "FacetBot/1.0 (https://github.com/kayiik/facet)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, failure("command_failed", "Wikimedia Commons search request failed: "+err.Error(), nil)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, failure("command_failed", fmt.Sprintf("Wikimedia API error (HTTP %d): %s", resp.StatusCode, string(body)), nil)
	}

	var wikiResp struct {
		Query struct {
			Pages map[string]struct {
				PageID    int    `json:"pageid"`
				Title     string `json:"title"`
				ImageInfo []struct {
					URL         string  `json:"url"`
					ThumbURL    string  `json:"thumburl"`
					Description string  `json:"descriptionurl"`
					Width       int     `json:"width"`
					Height      int     `json:"height"`
					Duration    float64 `json:"duration"`
					MIME        string  `json:"mime"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&wikiResp); err != nil {
		return nil, nil, failure("command_failed", "unable to parse Wikimedia response", nil)
	}

	candidates := []stockCandidate{}
	for _, p := range wikiResp.Query.Pages {
		if len(p.ImageInfo) > 0 {
			info := p.ImageInfo[0]
			candKind := "video"
			if strings.HasPrefix(info.MIME, "image/") {
				candKind = "image"
			}
			candidates = append(candidates, stockCandidate{
				ClipID:      fmt.Sprintf("wiki_%d", p.PageID),
				Source:      "wikimedia",
				SourceID:    strconv.Itoa(p.PageID),
				SourceURL:   info.Description,
				DownloadURL: info.URL,
				Kind:        candKind,
				Width:       info.Width,
				Height:      info.Height,
				Duration:    info.Duration,
				License:     "Wikimedia Commons",
			})
		}
	}

	if len(candidates) == 0 {
		return nil, nil, failure("not_found", "no media found on Wikimedia for query: "+r.Query, nil)
	}

	chosen := candidates[0]
	outPath := r.OutputPath
	if outPath == "" {
		ext := filepath.Ext(chosen.DownloadURL)
		if ext == "" {
			ext = ".mp4"
		}
		outPath = defaultOutput(ctx, fmt.Sprintf("wikimedia_%s%s", chosen.SourceID, ext))
	}
	if err := downloadFileContext(ctx, chosen.DownloadURL, outPath, 120*time.Second); err != nil {
		return nil, nil, failure("command_failed", "unable to download from Wikimedia: "+err.Error(), nil)
	}

	return map[string]any{
		"provider":         "wikimedia",
		"source_id":        chosen.SourceID,
		"duration_seconds": chosen.Duration,
		"width":            chosen.Width,
		"height":           chosen.Height,
		"query":            r.Query,
		"output":           outPath,
		"license":          chosen.License,
		"download_url":     chosen.DownloadURL,
	}, nil, nil
}

func doDirectClipSearch(op string, data []byte) (any, []string, error) {
	return doDirectClipSearchContext(context.Background(), op, data)
}

func doDirectClipSearchContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r directClipSearchRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(r.OutputDir) == "" {
		return nil, nil, failure("invalid_request", "output_dir is required", nil)
	}
	if len(r.Queries) == 0 {
		return nil, nil, failure("invalid_request", "queries array is required", nil)
	}
	if op == "estimate" {
		return estimateResult([]string{"direct_clip_search", "download_clips", "extract_thumbnails"}), nil, nil
	}

	clipsDir := filepath.Join(r.OutputDir, "clips")
	thumbsDir := filepath.Join(r.OutputDir, "thumbnails")
	_ = os.MkdirAll(clipsDir, 0755)
	if r.ExtractThumbnails {
		_ = os.MkdirAll(thumbsDir, 0755)
	}

	clipsPerQuery := r.ClipsPerQuery
	if clipsPerQuery <= 0 {
		clipsPerQuery = 3
	}

	downloaded := []map[string]any{}
	perSourceCounts := map[string]int{}

	for _, qItem := range r.Queries {
		qStr := qItem.Query
		slotID := qItem.SlotID
		collected := 0

		// Search sources: Pexels if available, Pixabay if available, else Wikimedia
		if os.Getenv("PEXELS_API_KEY") != "" && collected < clipsPerQuery {
			pexReq := map[string]any{
				"query":       qStr,
				"per_page":    clipsPerQuery,
				"output_path": filepath.Join(clipsDir, fmt.Sprintf("pexels_%s.mp4", sanitizeFilename(qStr))),
			}
			pexData, _ := json.Marshal(pexReq)
			if res, _, err := doPexelsVideoContext(ctx, "run", pexData); err == nil {
				m := res.(map[string]any)
				outP := m["output"].(string)
				thumbP := ""
				if r.ExtractThumbnails {
					thumbP = filepath.Join(thumbsDir, fmt.Sprintf("%s.jpg", filepath.Base(outP)))
					extractThumbnailContext(ctx, outP, thumbP)
				}
				downloaded = append(downloaded, map[string]any{
					"clip_id":   filepath.Base(outP),
					"source":    "pexels",
					"query":     qStr,
					"slot_id":   slotID,
					"path":      outP,
					"thumbnail": thumbP,
					"duration":  m["duration_seconds"],
				})
				perSourceCounts["pexels"]++
				collected++
			}
		}

		if os.Getenv("PIXABAY_API_KEY") != "" && collected < clipsPerQuery {
			pixReq := map[string]any{
				"query":       qStr,
				"per_page":    clipsPerQuery,
				"output_path": filepath.Join(clipsDir, fmt.Sprintf("pixabay_%s.mp4", sanitizeFilename(qStr))),
			}
			pixData, _ := json.Marshal(pixReq)
			if res, _, err := doPixabayVideoContext(ctx, "run", pixData); err == nil {
				m := res.(map[string]any)
				outP := m["output"].(string)
				thumbP := ""
				if r.ExtractThumbnails {
					thumbP = filepath.Join(thumbsDir, fmt.Sprintf("%s.jpg", filepath.Base(outP)))
					extractThumbnailContext(ctx, outP, thumbP)
				}
				downloaded = append(downloaded, map[string]any{
					"clip_id":   filepath.Base(outP),
					"source":    "pixabay",
					"query":     qStr,
					"slot_id":   slotID,
					"path":      outP,
					"thumbnail": thumbP,
					"duration":  m["duration_seconds"],
				})
				perSourceCounts["pixabay"]++
				collected++
			}
		}

		if collected < clipsPerQuery {
			wikiReq := map[string]any{
				"query":       qStr,
				"per_page":    clipsPerQuery,
				"output_path": filepath.Join(clipsDir, fmt.Sprintf("wikimedia_%s.mp4", sanitizeFilename(qStr))),
			}
			wikiData, _ := json.Marshal(wikiReq)
			if res, _, err := doWikimediaContext(ctx, "run", wikiData); err == nil {
				m := res.(map[string]any)
				outP := m["output"].(string)
				thumbP := ""
				if r.ExtractThumbnails {
					thumbP = filepath.Join(thumbsDir, fmt.Sprintf("%s.jpg", filepath.Base(outP)))
					extractThumbnailContext(ctx, outP, thumbP)
				}
				downloaded = append(downloaded, map[string]any{
					"clip_id":   filepath.Base(outP),
					"source":    "wikimedia",
					"query":     qStr,
					"slot_id":   slotID,
					"path":      outP,
					"thumbnail": thumbP,
					"duration":  m["duration_seconds"],
				})
				perSourceCounts["wikimedia"]++
				collected++
			}
		}
	}

	return map[string]any{
		"output_dir":        r.OutputDir,
		"clips_downloaded":  len(downloaded),
		"total_clips":       len(downloaded),
		"per_source_counts": perSourceCounts,
		"queries_run":       len(r.Queries),
		"clips":             downloaded,
	}, nil, nil
}

func downloadFile(fileURL, targetPath string, timeout time.Duration) error {
	return downloadFileContext(context.Background(), fileURL, targetPath, timeout)
}

func downloadFileContext(ctx context.Context, fileURL, targetPath string, timeout time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "FacetBot/1.0 (https://github.com/kayiik/facet)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func sanitizeFilename(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	b := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	res := b.String()
	if len(res) > 30 {
		res = res[:30]
	}
	return res
}

func extractThumbnail(videoPath, thumbPath string) {
	extractThumbnailContext(context.Background(), videoPath, thumbPath)
}

func extractThumbnailContext(ctx context.Context, videoPath, thumbPath string) {
	dur, err := probeDurationContext(ctx, videoPath, 5*time.Second)
	seekTime := 1.0
	if err == nil && dur > 1.0 {
		seekTime = dur / 2.0
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(seekTime), "-i", videoPath, "-frames:v", "1", "-q:v", "2", thumbPath}
	_, _ = runCommandDirContext(ctx, 10*time.Second, "", "ffmpeg", args...)
}
