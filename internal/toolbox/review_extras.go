package toolbox

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// contactSheet tiles count evenly spaced frames into one JPEG in dir, so a
// reviewer sees the whole video's look and pacing in one image.
func contactSheet(ctx context.Context, input, dir string, duration float64, count, columns int) (map[string]any, error) {
	if count <= 0 {
		count = 12
	}
	if count > 24 {
		count = 24
	}
	if columns <= 0 {
		columns = 4
	}
	if duration <= 0 {
		return nil, fmt.Errorf("the video has no duration")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rows := (count + columns - 1) / columns
	out := filepath.Join(dir, "contact-sheet.jpg")
	// One frame every duration/count seconds, scaled to 480 px wide tiles.
	rate := float64(count) / duration
	filter := fmt.Sprintf("fps=%s,scale=480:-2,tile=%dx%d:padding=6:margin=6", strconv.FormatFloat(rate, 'f', 6, 64), columns, rows)
	if outText, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", input, "-vf", filter, "-frames:v", "1", "-q:v", "3", out); err != nil {
		return nil, fmt.Errorf("%s", bounded(string(outText)+err.Error()))
	}
	return map[string]any{"path": out, "frames": count, "columns": columns}, nil
}

var integratedLoudness = regexp.MustCompile(`I:\s+(-?[0-9.]+) LUFS`)

// reviewCoverage compares the narration and captions with the video: where
// narration is missing for long stretches, captions outside the video, and
// the integrated loudness.
func reviewCoverage(ctx context.Context, input string, duration float64, timingPath, captionsPath string) (map[string]any, []string) {
	cov := map[string]any{"video_seconds": duration}
	var findings []string
	if strings.TrimSpace(timingPath) != "" {
		if t, err := loadTiming(timingPath); err != nil {
			findings = append(findings, "coverage: the timing record could not be read")
		} else {
			cov["narration_seconds"] = t.Duration
			gaps := []string{}
			last := 0.0
			for _, l := range t.Lines {
				if l.Start-last > 1.5 {
					gaps = append(gaps, fmt.Sprintf("%.1f–%.1f s", last, l.Start))
				}
				last = math.Max(last, l.End)
			}
			cov["narration_gaps"] = gaps
			if t.Duration > duration+0.05 {
				findings = append(findings, fmt.Sprintf("coverage: the narration (%.1f s) is longer than the video (%.1f s) and is cut off", t.Duration, duration))
			}
		}
	}
	if strings.TrimSpace(captionsPath) != "" {
		if data, err := os.ReadFile(captionsPath); err != nil {
			findings = append(findings, "coverage: the captions file could not be read")
		} else {
			ends := srtTimes.FindAllStringSubmatch(string(data), -1)
			late := 0
			for _, m := range ends {
				if srtSeconds(m[2]) > duration+0.05 {
					late++
				}
			}
			cov["caption_cues"] = len(ends)
			if late > 0 {
				findings = append(findings, fmt.Sprintf("coverage: %d caption cues end after the video", late))
			}
		}
	}
	if out, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-nostats", "-i", input, "-map", "0:a:0", "-af", "ebur128", "-f", "null", "-"); err == nil {
		if m := integratedLoudness.FindAllStringSubmatch(string(out), -1); len(m) > 0 {
			if v, err := strconv.ParseFloat(m[len(m)-1][1], 64); err == nil {
				cov["integrated_lufs"] = v
				if v > -11 || v < -20 {
					findings = append(findings, fmt.Sprintf("coverage: loudness is %.1f LUFS; social and web delivery aims for about -14", v))
				}
			}
		}
	}
	return cov, findings
}

var srtTimes = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}[,.]\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}[,.]\d{3})`)

func srtSeconds(stamp string) float64 {
	stamp = strings.ReplaceAll(stamp, ",", ".")
	parts := strings.Split(stamp, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(parts[0], 64)
	m, _ := strconv.ParseFloat(parts[1], 64)
	s, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + s
}
