package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type musicLibraryRequest struct {
	LibraryDir string `json:"library_dir,omitempty"`
}

var audioExtensions = map[string]bool{
	".mp3":  true,
	".wav":  true,
	".m4a":  true,
	".aac":  true,
	".flac": true,
	".ogg":  true,
	".opus": true,
	".aiff": true,
	".aif":  true,
}

func doMusicLibrary(op string, data []byte) (any, []string, error) {
	return doMusicLibraryContext(context.Background(), op, data)
}

func doMusicLibraryContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r musicLibraryRequest
	if len(data) > 0 {
		_ = decode(data, &r)
	}

	libDir := r.LibraryDir
	if libDir == "" {
		libDir = os.Getenv("MUSIC_LIBRARY_DIR")
	}
	if libDir == "" {
		libDir = "music_library"
	}

	if op == "estimate" {
		return estimateResult([]string{"scan_library_directory", "probe_track_durations"}), nil, nil
	}

	info, err := os.Stat(libDir)
	exists := err == nil && info.IsDir()
	if !exists {
		return map[string]any{
			"library_dir":            libDir,
			"exists":                 false,
			"track_count":            0,
			"total_duration_seconds": 0.0,
			"tracks":                 []any{},
		}, nil, nil
	}

	tracks := []map[string]any{}
	totalDuration := 0.0
	_ = filepath.Walk(libDir, func(path string, fileInfo os.FileInfo, walkErr error) error {
		if walkErr != nil || fileInfo.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !audioExtensions[ext] {
			return nil
		}
		dur, probeErr := probeDurationContext(ctx, path, 5*time.Second)
		if probeErr == nil && dur > 0 {
			totalDuration += dur
		}
		tracks = append(tracks, map[string]any{
			"name":             fileInfo.Name(),
			"path":             path,
			"size_bytes":       fileInfo.Size(),
			"duration_seconds": dur,
		})
		return nil
	})

	return map[string]any{
		"library_dir":            libDir,
		"exists":                 true,
		"track_count":            len(tracks),
		"total_duration_seconds": roundFloat(totalDuration, 2),
		"tracks":                 tracks,
	}, nil, nil
}
