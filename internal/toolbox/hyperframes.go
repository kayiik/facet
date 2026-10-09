package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type hyperframesRequest struct {
	Operation      string         `json:"operation"`
	WorkspacePath  string         `json:"workspace_path,omitempty"`
	OutputPath     string         `json:"output_path,omitempty"`
	BlockName      string         `json:"block_name,omitempty"`
	EditDecisions  map[string]any `json:"edit_decisions,omitempty"`
	AssetManifest  map[string]any `json:"asset_manifest,omitempty"`
	Playbook       map[string]any `json:"playbook,omitempty"`
	Profile        string         `json:"profile,omitempty"`
	Quality        string         `json:"quality,omitempty"`
	FPS            int            `json:"fps,omitempty"`
	Strict         bool           `json:"strict,omitempty"`
	SkipContrast   bool           `json:"skip_contrast,omitempty"`
	StrictCheck    bool           `json:"strict_check,omitempty"`
	Snapshots      bool           `json:"snapshots,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
}

// hyperframesEnv names an explicit HyperFrames CLI entry point
// (node_modules/hyperframes/bin/hyperframes.mjs) to use instead of the one the
// installer pins under the runtime's dependencies.
const hyperframesEnv = "FACET_HYPERFRAMES"

// hyperframesPinned is where the installer places the pinned HyperFrames CLI.
func hyperframesPinned() string {
	return runtimeDependency("hyperframes", "node_modules", "hyperframes", "bin", "hyperframes.mjs")
}

// hyperframesEntry resolves the HyperFrames CLI entry point to run with node.
//
// There is deliberately no `npx hyperframes` fallback: npx resolves whatever
// version the registry serves at that moment, so the renderer — and what it
// renders — would change underneath an unchanged request. An explicitly
// configured entry that does not exist is reported rather than silently
// replaced by another install.
func hyperframesEntry() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(hyperframesEnv)); configured != "" {
		if !fileExists(configured) {
			return "", failure("dependency_missing", hyperframesEnv+" does not name an existing HyperFrames entry point", map[string]any{"path": configured})
		}
		return filepath.Abs(configured)
	}
	pinned := hyperframesPinned()
	if pinned != "" && fileExists(pinned) {
		return pinned, nil
	}
	return "", failure("dependency_missing", "HyperFrames is not installed; install Facet's hyperframes component, or set "+hyperframesEnv+" to its bin/hyperframes.mjs", map[string]any{"expected": pinned})
}

// hyperframesDependency reports the pinned CLI for the tool listing.
func hyperframesDependency() map[string]any {
	path, err := hyperframesEntry()
	return map[string]any{
		"name": "hyperframes", "available": err == nil, "path": path, "type": "runtime",
		"resolution": resolutionOf(err == nil, "runtime"),
	}
}

func doHyperFramesCompose(op string, data []byte) (any, []string, error) {
	return doHyperFramesComposeContext(context.Background(), op, data)
}

func doHyperFramesComposeContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r hyperframesRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	operation := r.Operation
	if operation == "" {
		operation = "render"
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 1800)
	if err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		return estimateResult([]string{"hyperframes_" + operation}), nil, nil
	}

	workspace := r.WorkspacePath
	if workspace == "" {
		workspace = defaultOutput(ctx, "hyperframes_workspace")
	}

	switch operation {
	case "doctor":
		nodePath, nodeErr := lookPath("node")
		entry, entryErr := hyperframesEntry()
		ffmpegPath, ffmpegErr := lookPath("ffmpeg")
		return map[string]any{
			"operation": "doctor",
			"runtime_check": map[string]any{
				"runtime_available":     nodeErr == nil && entryErr == nil && ffmpegErr == nil,
				"node_available":        nodeErr == nil,
				"node_path":             nodePath,
				"hyperframes_available": entryErr == nil,
				"hyperframes_path":      entry,
				"ffmpeg_available":      ffmpegErr == nil,
				"ffmpeg_path":           ffmpegPath,
			},
		}, nil, nil

	case "scaffold_workspace":
		// Writes files only; it needs no runtime.
		return scaffoldHyperFrames(workspace, r)

	case "lint", "validate", "inspect", "check":
		args := []string{operation, "--json"}
		if r.SkipContrast {
			args = append(args, "--no-contrast")
		}
		stdout, stderr, err := runHyperFrames(ctx, tmo, workspace, false, args...)
		if err != nil {
			return nil, nil, hyperframesFailure(operation, stdout, err)
		}
		return hyperframesReport(operation, stdout, stderr), nil, nil

	case "add_block":
		if r.BlockName == "" {
			return nil, nil, failure("invalid_request", "block_name is required for add_block", nil)
		}
		stdout, stderr, err := runHyperFrames(ctx, tmo, workspace, false, "add", r.BlockName, "--json", "--no-clipboard")
		if err != nil {
			return nil, nil, hyperframesFailure("add", stdout, err)
		}
		result := hyperframesReport("add_block", stdout, stderr)
		result["block_name"] = r.BlockName
		return result, nil, nil

	case "render", "render_existing":
		outPath := r.OutputPath
		if outPath == "" {
			outPath = filepath.Join(workspace, "renders", "final.mp4")
		}
		absOut, err := filepath.Abs(outPath)
		if err != nil {
			return nil, nil, failure("invalid_request", "output path cannot be resolved", map[string]any{"path": outPath})
		}
		fps := r.FPS
		if fps <= 0 {
			fps = 30
		}
		quality := r.Quality
		if quality == "" {
			quality = "standard"
		}
		if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
			return nil, nil, failure("command_failed", "output directory could not be created", map[string]any{"error": bounded(err.Error())})
		}
		stdout, _, err := runHyperFrames(ctx, tmo, workspace, true, "render", "--output", absOut, "--fps", strconv.Itoa(fps), "--quality", quality)
		if err != nil {
			return nil, nil, hyperframesFailure("render", stdout, err)
		}
		// A renderer that exits cleanly without writing its output must not
		// be reported as a successful render.
		if !fileExists(absOut) {
			return nil, nil, failure("output_validation_failed", "hyperframes render finished but wrote no output", map[string]any{"path": outPath})
		}
		return map[string]any{
			"operation": operation,
			"output":    outPath,
			"workspace": workspace,
		}, nil, nil

	default:
		return nil, nil, failure("invalid_request", "unknown operation: "+operation, nil)
	}
}

// runHyperFrames runs the pinned HyperFrames CLI with node in the workspace.
// Rendering also needs ffmpeg; the checks name what is missing before
// anything starts.
func runHyperFrames(ctx context.Context, tmo time.Duration, workspace string, render bool, args ...string) ([]byte, []byte, error) {
	entry, err := hyperframesEntry()
	if err != nil {
		return nil, nil, err
	}
	if _, err := lookPath("node"); err != nil {
		return nil, nil, failure("dependency_missing", "node is required to run HyperFrames", nil)
	}
	if render {
		if _, err := lookPath("ffmpeg"); err != nil {
			return nil, nil, failure("dependency_missing", "ffmpeg is required to render with HyperFrames", nil)
		}
	}
	return runCommandDirOutput(ctx, tmo, workspace, "node", append([]string{entry}, args...)...)
}

// hyperframesFailure prefixes a CLI failure with the operation while keeping
// its code: a timeout must stay a (retryable) timeout.
func hyperframesFailure(operation string, stdout []byte, err error) error {
	var tf *toolFailure
	if errors.As(err, &tf) {
		tf.err.Message = "hyperframes " + operation + " failed: " + tf.err.Message
		if len(stdout) > 0 {
			tf.err.Details["output"] = bounded(string(stdout))
		}
		return tf
	}
	return failure("command_failed", "hyperframes "+operation+" failed: "+err.Error(), map[string]any{"output": bounded(string(stdout))})
}

// hyperframesReport wraps the CLI's own JSON report. A report is the CLI's
// data, not Facet's, so it is carried under one field rather than merged
// into the result, where its keys could collide with the result contract.
func hyperframesReport(operation string, stdout, stderr []byte) map[string]any {
	result := map[string]any{"operation": operation}
	var report any
	if json.Unmarshal(stdout, &report) == nil {
		result["report"] = report
		return result
	}
	result["raw"] = bounded(strings.TrimSpace(string(stdout) + "\n" + string(stderr)))
	return result
}

func scaffoldHyperFrames(workspace string, r hyperframesRequest) (any, []string, error) {
	for _, dir := range []string{workspace, filepath.Join(workspace, "assets"), filepath.Join(workspace, "compositions")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, failure("command_failed", "unable to create workspace directory", map[string]any{"path": dir, "error": bounded(err.Error())})
		}
	}
	config := map[string]any{
		"registry": "https://raw.githubusercontent.com/heygen-com/hyperframes/main/registry",
		"paths": map[string]string{
			"blocks":     "compositions",
			"components": "compositions/components",
			"assets":     "assets",
		},
	}
	configData, _ := json.MarshalIndent(config, "", "  ")
	// The composition loads GSAP from a CDN when it renders, which is one
	// reason this tool declares network access.
	const htmlSkeleton = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>HyperFrames Composition</title>
  <script src="https://cdn.jsdelivr.net/npm/gsap@3.14.2/dist/gsap.min.js"></script>
</head>
<body>
  <div data-composition-id="root" data-start="0" data-duration="10" data-width="1920" data-height="1080">
  </div>
</body>
</html>`
	files := []string{filepath.Join(workspace, "hyperframes.json"), filepath.Join(workspace, "index.html")}
	for i, content := range [][]byte{configData, []byte(htmlSkeleton)} {
		if err := os.WriteFile(files[i], content, 0o644); err != nil {
			return nil, nil, failure("command_failed", "unable to write workspace file", map[string]any{"path": files[i], "error": bounded(err.Error())})
		}
	}
	cutCount := 0
	if cuts, ok := r.EditDecisions["cuts"].([]any); ok {
		cutCount = len(cuts)
	}
	return map[string]any{
		"operation": "scaffold_workspace",
		"workspace": workspace,
		"cut_count": cutCount,
		"files":     files,
	}, nil, nil
}
