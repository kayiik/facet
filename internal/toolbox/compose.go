package toolbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type composeCut struct {
	ID              string  `json:"id,omitempty"`
	Source          string  `json:"source,omitempty"`
	InSeconds       float64 `json:"in_seconds"`
	OutSeconds      float64 `json:"out_seconds"`
	Speed           float64 `json:"speed,omitempty"`
	Type            string  `json:"type,omitempty"`
	Text            string  `json:"text,omitempty"`
	Title           string  `json:"title,omitempty"`
	Subtitle        string  `json:"subtitle,omitempty"`
	Stat            string  `json:"stat,omitempty"`
	Label           string  `json:"label,omitempty"`
	MediaKind       string  `json:"media_kind,omitempty"`
	Fit             string  `json:"fit,omitempty"`
	Muted           bool    `json:"muted,omitempty"`
	FontSize        float64 `json:"fontSize,omitempty"`
	BackgroundColor string  `json:"backgroundColor,omitempty"`
	Color           string  `json:"color,omitempty"`
}

type composeEditDecisions struct {
	RendererFamily string       `json:"renderer_family,omitempty"`
	RenderRuntime  string       `json:"render_runtime,omitempty"`
	Cuts           []composeCut `json:"cuts,omitempty"`
	Subtitles      struct {
		Enabled bool   `json:"enabled,omitempty"`
		Source  string `json:"source,omitempty"`
	} `json:"subtitles,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Bespoke  map[string]any `json:"bespoke,omitempty"`
}

type composeOverlay struct {
	AssetPath    string  `json:"asset_path"`
	X            float64 `json:"x,omitempty"`
	Y            float64 `json:"y,omitempty"`
	Width        float64 `json:"width,omitempty"`
	Height       float64 `json:"height,omitempty"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Opacity      float64 `json:"opacity,omitempty"`
}

type composeRequest struct {
	Operation         string                `json:"operation,omitempty"`
	InputPath         string                `json:"input_path,omitempty"`
	OutputPath        string                `json:"output_path,omitempty"`
	Output            string                `json:"output,omitempty"`
	CompositionID     string                `json:"composition_id,omitempty"`
	Composition       string                `json:"composition,omitempty"`
	Cuts              []map[string]any      `json:"cuts,omitempty"`
	Overlays          []composeOverlay      `json:"overlays,omitempty"`
	Captions          any                   `json:"captions,omitempty"`
	Audio             any                   `json:"audio,omitempty"`
	Scenes            []map[string]any      `json:"scenes,omitempty"`
	EditDecisions     *composeEditDecisions `json:"edit_decisions,omitempty"`
	AssetManifest     map[string]any        `json:"asset_manifest,omitempty"`
	AudioPath         string                `json:"audio_path,omitempty"`
	SubtitlePath      string                `json:"subtitle_path,omitempty"`
	SubtitleStyle     map[string]any        `json:"subtitle_style,omitempty"`
	Codec             string                `json:"codec,omitempty"`
	CRF               int                   `json:"crf,omitempty"`
	Preset            string                `json:"preset,omitempty"`
	Profile           string                `json:"profile,omitempty"`
	RemotionTimeoutMS int                   `json:"remotion_timeout_ms,omitempty"`
	TimeoutSeconds    int                   `json:"timeout_seconds,omitempty"`
	RawProps          map[string]any        `json:"-"`
	// LoudnessTarget is the LUFS the rendered audio is normalised to; zero
	// leaves it as rendered.
	LoudnessTarget float64 `json:"-"`
}

func doVideoCompose(op string, data []byte) (any, []string, error) {
	return doVideoComposeContext(context.Background(), op, data)
}

func doVideoComposeContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	// A saved composition is the same operation as its inline JSON. Resolve it
	// here so native, CLI and module callers preserve one canonical contract.
	var ref map[string]json.RawMessage
	if json.Unmarshal(data, &ref) == nil && ref["cuts"] == nil && ref["scenes"] == nil && ref["edit_decisions"] == nil {
		var input string
		if json.Unmarshal(ref["input_path"], &input) == nil && strings.EqualFold(filepath.Ext(input), ".json") {
			body, err := os.ReadFile(input)
			if err != nil {
				return nil, nil, err
			}
			var props map[string]any
			if err := json.Unmarshal(body, &props); err != nil {
				return nil, nil, err
			}
			if len(props) == 1 && props["input_path"] != nil {
				return nil, nil, failure("invalid_request", "composition file must contain props, not another file reference", nil)
			}
			for key, value := range ref {
				if key == "input_path" {
					continue
				}
				if key != "output" && key != "output_path" && key != "timeout_seconds" {
					return nil, nil, failure("invalid_request", "saved composition accepts only output or timeout overrides", nil)
				}
				var decoded any
				_ = json.Unmarshal(value, &decoded)
				props[key] = decoded
				if key == "output_path" {
					delete(props, "output")
				}
			}
			root := filepath.Dir(input)
			if filepath.Base(root) == "artifacts" {
				root = filepath.Dir(root)
			}
			props = ProjectArguments(props, root)
			encoded, _ := json.Marshal(props)
			return doVideoComposeContext(ctx, op, encoded)
		}
	}
	// First check if payload is direct Remotion props or Scene Plan JSON
	var rawMap map[string]any
	if err := json.Unmarshal(data, &rawMap); err == nil && rawMap != nil {
		// Edit decisions for the Remotion runtime render as composer props.
		if edit, ok := rawMap["edit_decisions"].(map[string]any); ok && remotionDecisions(edit) {
			rawMap = editDecisionsProps(rawMap, edit)
		}
		// 1. Composer props: top-level cuts, or a composition chosen by name
		// or renderer family.
		if _, hasCuts := rawMap["cuts"]; hasCuts || composerRequest(rawMap) {
			return doComposerRender(ctx, op, rawMap)
		}
		// 2. A scene plan (top-level "scenes" with start_seconds/end_seconds):
		// each scene becomes a cut by renaming its timings and carrying every
		// other field through, then renders like direct cuts.
		if rawScenes, hasScenes := rawMap["scenes"]; hasScenes {
			scenesRaw, ok := rawScenes.([]any)
			if !ok || len(scenesRaw) == 0 {
				return nil, nil, failure("invalid_request", "scenes must be a nonempty array", nil)
			}
			scenes, err := explainerCutsFromAny(scenesRaw, "scene")
			if err != nil {
				return nil, nil, err
			}
			cuts := make([]any, 0, len(scenes))
			for _, scene := range scenes {
				cuts = append(cuts, mapSceneToCut(scene))
			}
			delete(rawMap, "scenes")
			rawMap["cuts"] = cuts
			return doComposerRender(ctx, op, rawMap)
		}
	}

	var r composeRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	operation := r.Operation
	if operation == "" {
		operation = "compose"
	}
	tmo, err := positiveTimeout(r.TimeoutSeconds, 600)
	if err != nil {
		return nil, nil, err
	}
	// HyperFrames renders an HTML workspace of its own and reaches the
	// network for the scripts that workspace loads. video_compose declares
	// neither effect, and the edit decisions here would not reach that
	// workspace anyway, so the runtime is offered only through
	// hyperframes_compose, where both are declared.
	if r.EditDecisions != nil && strings.EqualFold(strings.TrimSpace(r.EditDecisions.RenderRuntime), "hyperframes") {
		return nil, nil, failure("invalid_request", "render_runtime hyperframes is served by the hyperframes_compose tool; call it directly", nil)
	}

	if op == "estimate" {
		return estimateResult([]string{"video_compose_" + operation}), nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmo)
	defer cancel()

	switch operation {
	case "compose", "render":
		if r.EditDecisions == nil || len(r.EditDecisions.Cuts) == 0 {
			return nil, nil, failure("invalid_request", "edit_decisions with cuts required", nil)
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = r.Output
		}
		if outPath == "" {
			outPath = defaultOutput(ctx, "composed_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}

		runtime := strings.ToLower(r.EditDecisions.RenderRuntime)
		if runtime == "remotion" {
			return doRemotionRenderContext(ctx, r, outPath, tmo)
		}

		// FFmpeg compose implementation
		return doFFmpegComposeContext(ctx, r, outPath, tmo)

	case "remotion_render":
		outPath := r.OutputPath
		if outPath == "" {
			outPath = r.Output
		}
		if outPath == "" {
			outPath = defaultOutput(ctx, "remotion_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		return doRemotionRenderContext(ctx, r, outPath, tmo)

	case "burn_subtitles":
		if err := inputPath(r.InputPath); err != nil {
			return nil, nil, err
		}
		if err := inputPath(r.SubtitlePath); err != nil {
			return nil, nil, err
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "subtitled_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		assStyle := buildSubtitleStyle(r.SubtitleStyle)
		subEscaped := strings.ReplaceAll(filepath.ToSlash(r.SubtitlePath), ":", `\:`)
		vf := fmt.Sprintf("subtitles='%s':force_style='%s'", subEscaped, assStyle)
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath, "-vf", vf, "-c:v", "libx264", "-c:a", "copy", outPath}
		if _, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"operation": "burn_subtitles",
			"input":     r.InputPath,
			"subtitles": r.SubtitlePath,
			"output":    outPath,
		}, nil, nil

	case "overlay":
		if err := inputPath(r.InputPath); err != nil {
			return nil, nil, err
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "overlay_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		if len(r.Overlays) == 0 {
			return nil, nil, failure("invalid_request", "overlays array is required", nil)
		}
		cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath}
		filters := []string{}
		for i, ov := range r.Overlays {
			if err := inputPath(ov.AssetPath); err != nil {
				return nil, nil, err
			}
			cmdArgs = append(cmdArgs, "-i", ov.AssetPath)
			vIn := "[0:v]"
			if i > 0 {
				vIn = fmt.Sprintf("[ov%d]", i-1)
			}
			vOut := fmt.Sprintf("[ov%d]", i)
			if i == len(r.Overlays)-1 {
				vOut = "[outv]"
			}
			filters = append(filters, fmt.Sprintf("%s[%d:v]overlay=x=%s:y=%s:enable='between(t,%s,%s)'%s", vIn, i+1, formatFloat(ov.X), formatFloat(ov.Y), formatFloat(ov.StartSeconds), formatFloat(ov.EndSeconds), vOut))
		}
		cmdArgs = append(cmdArgs, "-filter_complex", strings.Join(filters, ";"), "-map", "[outv]", "-map", "0:a?", "-c:v", "libx264", "-c:a", "copy", outPath)
		if _, err := runCommandContext(ctx, "ffmpeg", cmdArgs...); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"operation":     "overlay",
			"input":         r.InputPath,
			"overlay_count": len(r.Overlays),
			"output":        outPath,
		}, nil, nil

	case "encode":
		if err := inputPath(r.InputPath); err != nil {
			return nil, nil, err
		}
		outPath := r.OutputPath
		if outPath == "" {
			outPath = defaultOutput(ctx, "encoded_output.mp4")
		}
		if err := outputPath(outPath, true, false); err != nil {
			return nil, nil, err
		}
		codec := r.Codec
		if codec == "" {
			codec = "libx264"
		}
		crf := r.CRF
		if crf == 0 {
			crf = 23
		}
		preset := r.Preset
		if preset == "" {
			preset = "medium"
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", r.InputPath, "-c:v", codec, "-crf", strconv.Itoa(crf), "-preset", preset, "-c:a", "aac", outPath}
		if _, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"operation": "encode",
			"input":     r.InputPath,
			"output":    outPath,
			"codec":     codec,
		}, nil, nil

	default:
		return nil, nil, failure("invalid_request", "unknown operation: "+operation, nil)
	}
}

func doFFmpegCompose(r composeRequest, outPath string, tmo time.Duration) (any, []string, error) {
	return doFFmpegComposeContext(context.Background(), r, outPath, tmo)
}

func doFFmpegComposeContext(ctx context.Context, r composeRequest, outPath string, tmo time.Duration) (any, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmo)
	defer cancel()
	tempDir, err := os.MkdirTemp(filepath.Dir(outPath), ".compose_tmp-*")
	if err != nil {
		return nil, nil, failure("command_failed", "unable to create temporary directory", nil)
	}
	defer os.RemoveAll(tempDir)

	cuts := r.EditDecisions.Cuts
	tempSegments := make([]string, len(cuts))
	targetW, targetH := 1920, 1080
	targetFPS := 30.0

	for i, cut := range cuts {
		src := cut.Source
		if err := inputPath(src); err != nil {
			return nil, nil, err
		}
		inS := cut.InSeconds
		outS := cut.OutSeconds
		dur := outS - inS
		if dur <= 0 {
			dur = 1.0
		}
		segFile := filepath.Join(tempDir, fmt.Sprintf("seg_%04d.mp4", i))
		vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%s,format=yuv420p", targetW, targetH, targetW, targetH, formatFloat(targetFPS))

		p, _, err := probeContext(ctx, src)
		if err != nil {
			return nil, nil, err
		}
		var args []string
		if hasAudio(p) {
			args = []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(inS), "-t", formatFloat(dur), "-i", src, "-vf", vf, "-c:v", "libx264", "-crf", "23", "-preset", "medium", "-c:a", "aac", "-ar", "48000", "-ac", "2", segFile}
		} else {
			args = []string{"-hide_banner", "-loglevel", "error", "-y", "-ss", formatFloat(inS), "-t", formatFloat(dur), "-i", src, "-f", "lavfi", "-t", formatFloat(dur), "-i", "anullsrc=r=48000:cl=stereo", "-vf", vf, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-crf", "23", "-preset", "medium", "-c:a", "aac", "-ar", "48000", "-ac", "2", segFile}
		}
		if _, err := runCommandContext(ctx, "ffmpeg", args...); err != nil {
			return nil, nil, err
		}
		tempSegments[i] = segFile
	}

	concatList := filepath.Join(tempDir, "concat.txt")
	b := strings.Builder{}
	for _, ts := range tempSegments {
		abs, _ := filepath.Abs(ts)
		fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(abs, `\`, `/`))
	}
	if err := os.WriteFile(concatList, []byte(b.String()), 0644); err != nil {
		return nil, nil, failure("command_failed", "unable to write concat list", nil)
	}

	concatOut := filepath.Join(tempDir, "concat.mp4")
	if _, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", concatList, "-c", "copy", concatOut); err != nil {
		return nil, nil, err
	}

	finalIn := concatOut
	cmdArgs := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", finalIn}
	subPath := r.SubtitlePath
	if subPath == "" && r.EditDecisions.Subtitles.Enabled && r.EditDecisions.Subtitles.Source != "" {
		subPath = r.EditDecisions.Subtitles.Source
	}

	hasSubs := subPath != "" && fileExists(subPath)
	if hasSubs {
		assStyle := buildSubtitleStyle(r.SubtitleStyle)
		subEscaped := strings.ReplaceAll(filepath.ToSlash(subPath), ":", `\:`)
		cmdArgs = append(cmdArgs, "-vf", fmt.Sprintf("subtitles='%s':force_style='%s'", subEscaped, assStyle))
	}

	if r.AudioPath != "" && fileExists(r.AudioPath) {
		cmdArgs = append(cmdArgs, "-i", r.AudioPath, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-c:a", "aac", "-shortest", outPath)
	} else if hasSubs {
		cmdArgs = append(cmdArgs, "-c:v", "libx264", "-c:a", "copy", outPath)
	} else {
		cmdArgs = append(cmdArgs, "-c", "copy", outPath)
	}

	if _, err := runCommandContext(ctx, "ffmpeg", cmdArgs...); err != nil {
		return nil, nil, err
	}

	return map[string]any{
		"operation":       "compose",
		"cut_count":       len(cuts),
		"has_subtitles":   hasSubs,
		"has_mixed_audio": r.AudioPath != "",
		"output":          outPath,
	}, nil, nil
}

// ComposerDirEnv names the one explicit override of the Remotion composer's
// location, for development checkouts and tests.
const ComposerDirEnv = "FACET_REMOTION_COMPOSER"

// findComposerDir locates the Remotion composer: FACET_REMOTION_COMPOSER when
// it is set, else the runtime's own copy beside the executable
// (<runtime>/dependencies/remotion-composer), like every other dependency.
//
// Nothing is searched relative to the working directory or the user's home,
// and no configuration file is read: which composer renders must not depend
// on where a call happens to run, and a second Toolkit (the Facet App's) must
// never pick up the user-wide one's.
func findComposerDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(ComposerDirEnv)); dir != "" {
		if !fileExists(filepath.Join(dir, "package.json")) {
			return "", failure("dependency_missing", ComposerDirEnv+" names no Remotion composer (package.json not found)", map[string]any{"path": dir})
		}
		return filepath.Abs(dir)
	}
	if dir := runtimeDependency("remotion-composer"); dir != "" && fileExists(filepath.Join(dir, "package.json")) {
		return dir, nil
	}
	return "", failure("dependency_missing", "Remotion composer not found beside this facet; install Facet with the remotion component, or set "+ComposerDirEnv+" to a composer checkout", nil)
}

// truncatedAudioWarning reports narration that will not fit the timeline.
//
// The renderer trims audio to the composition length, so a script longer than
// the video is cut mid-sentence. That is a silent loss of content the caller
// wrote, and the only way to notice it is to listen to the finished file.
//
// Warning rather than refusing: trimming is legitimate when the audio is a
// music bed meant to fade out, and refusing a render for it would block a
// reasonable request. The number is what the caller needs — how much was lost.
func truncatedAudioWarning(props map[string]any, tmo time.Duration) string {
	return truncatedAudioWarningContext(context.Background(), props, tmo)
}

func truncatedAudioWarningContext(ctx context.Context, props map[string]any, tmo time.Duration) string {
	audio, ok := props["audio"].(map[string]any)
	if !ok {
		return ""
	}
	narration, ok := audio["narration"].(map[string]any)
	if !ok {
		return ""
	}
	path, _ := narration["src"].(string)
	if strings.HasPrefix(strings.ToLower(path), "file:") {
		if u, err := url.Parse(path); err == nil && u.Host == "" && u.RawQuery == "" && u.Fragment == "" {
			path = u.Path
			if os.PathSeparator == '\\' && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
		}
	}
	if strings.TrimSpace(path) == "" || !fileExists(path) {
		return ""
	}
	duration, ok := props["duration_seconds"].(float64)
	if !ok || duration <= 0 {
		return ""
	}
	facts, _, err := probeWithContext(ctx, path, tmo)
	if err != nil {
		return ""
	}
	format, ok := facts["format"].(map[string]any)
	if !ok {
		return ""
	}
	audioSeconds, ok := format["duration"].(float64)
	if !ok || audioSeconds <= duration+0.05 {
		return ""
	}
	return fmt.Sprintf(
		"audio is %.2fs but the timeline is %.2fs, so %.2fs will be cut off; "+
			"raise duration_seconds or extend the last cut to keep it",
		audioSeconds, duration, audioSeconds-duration)
}

// renderShape reads the frame count and dimensions a render will use, so an
// estimate can say how long it is likely to take.
//
// Falls back to the composition's own defaults, because that is what the
// render itself will use when the caller names none.
func renderShape(rawMap map[string]any, lastEnd float64) (frames, width, height int) {
	num := func(key string, fallback float64) float64 {
		if v, ok := rawMap[key].(float64); ok && v > 0 {
			return v
		}
		return fallback
	}
	fps := num("fps", 30)
	seconds := num("duration_seconds", lastEnd)
	return int(seconds * fps), int(num("width", 1920)), int(num("height", 1080))
}

// frameAlignedDuration rounds a duration UP to a whole frame boundary.
//
// The composition requires duration_seconds * fps to be a whole frame count
// and refuses anything else outright. Cuts matched to narration land on
// arbitrary boundaries — 8.16 seconds of speech at 30fps is 244.8 frames — so
// sending the plan's raw end failed the render entirely:
//
//	Explainer duration_seconds * fps must be a positive safe integer frame count
//
// Found by running the narrated walkthrough end to end. Every earlier test
// used whole-second timings, where the problem cannot appear.
//
// Rounds UP so the last cut is never clipped: a frame short would cut the
// final moment of the video the caller asked for.
func frameAlignedDuration(seconds, fps float64) float64 {
	if seconds <= 0 || fps <= 0 {
		return seconds
	}
	frames := math.Ceil(seconds*fps - 1e-9)
	return frames / fps
}

// lastCutEnd reports when the final cut ends, which is when the video should.
//
// Returns 0 when no cut names a usable end, so the caller leaves
// duration_seconds unset and the composition keeps its own default rather than
// receiving a fabricated zero.
func lastCutEnd(cuts []map[string]any) float64 {
	last := 0.0
	for _, cut := range cuts {
		if v, ok := cut["out_seconds"].(float64); ok && v > last {
			last = v
		}
	}
	return last
}

// mapSceneToCut turns one scene-plan scene into a Remotion cut.
//
// Timings are RENAMED and everything else is carried through. The previous
// version copied five fields and dropped the rest, so a scene describing its
// content through any of the 63 cut fields the composition reads rendered as
// an EMPTY cut — a valid mp4 with nothing in it, reported as a success.
//
// Verified before the fix: four renders with different text and backgrounds
// produced byte-identical output and the content QA gate reported every frame
// blank.
//
// The scenes schema names only start_seconds and end_seconds and accepts any
// other property, so a caller cannot learn which fields survive. Carrying them
// through is the only behaviour consistent with what the schema accepts.
func mapSceneToCut(scene map[string]any) map[string]any {
	cut := make(map[string]any, len(scene)+2)
	for k, v := range scene {
		switch k {
		case "start_seconds", "end_seconds":
			// Renamed below; the composition reads the in_/out_ pair.
			continue
		case "description":
			// The scene-plan name for a cut's text. An explicit text wins, so
			// a caller supplying both is not overridden by the prose field.
			if _, present := scene["text"]; !present {
				if d, ok := v.(string); ok && d != "" {
					cut["text"] = d
				}
			}
		default:
			cut[k] = v
		}
	}
	cut["in_seconds"] = scene["start_seconds"]
	cut["out_seconds"] = scene["end_seconds"]
	return cut
}

func explainerCutsFromAny(items []any, label string) ([]map[string]any, error) {
	cuts := make([]map[string]any, 0, len(items))
	for i, item := range items {
		cut, ok := item.(map[string]any)
		if !ok {
			return nil, failure("invalid_request", fmt.Sprintf("%s %d must be an object", label, i), nil)
		}
		cuts = append(cuts, cut)
	}
	return cuts, nil
}

func doRemotionRender(r composeRequest, outPath string, tmo time.Duration) (any, []string, error) {
	return doRemotionRenderContext(context.Background(), r, outPath, tmo)
}

func doRemotionRenderContext(ctx context.Context, r composeRequest, outPath string, tmo time.Duration) (any, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmo)
	defer cancel()
	absComposer, err := findComposerDir()
	if err != nil {
		return nil, nil, err
	}
	cliPath := filepath.Join(absComposer, "node_modules", "@remotion", "cli", "remotion-cli.js")
	if !fileExists(cliPath) {
		return nil, nil, failure("dependency_missing", "Remotion render CLI not found; install the composer dependencies with npm ci", map[string]any{"path": cliPath})
	}
	entryFile := filepath.Join(absComposer, "src", "index.tsx")
	if !fileExists(entryFile) {
		return nil, nil, failure("dependency_missing", "Remotion composition entry point not found", map[string]any{"path": entryFile})
	}
	if r.AudioPath != "" {
		if err := inputPath(r.AudioPath); err != nil {
			return nil, nil, err
		}
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}
	renderPath, cleanup, err := temporaryOutput(outPath)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	compositionID := "Explainer"
	if r.CompositionID != "" {
		if !composerCompositions[r.CompositionID] {
			return nil, nil, failure("invalid_request", "composition must be one of "+strings.Join(sortedBoolKeys(composerCompositions), ", "), nil)
		}
		compositionID = r.CompositionID
	}
	if r.RawProps == nil && r.EditDecisions != nil {
		// Typed edit decisions render through the composer path, which keeps
		// every cut field; reaching here means they were not for Remotion.
		return nil, nil, failure("invalid_request", "edit decisions for the Remotion runtime need render_runtime \"remotion\" or a renderer_family", nil)
	}

	var composerWarnings []string
	if r.RawProps != nil {
		if msg := truncatedAudioWarningContext(ctx, r.RawProps, tmo); msg != "" {
			composerWarnings = append(composerWarnings, msg)
		}
	}

	propsJSON, err := buildRemotionProps(r)
	if err != nil {
		return nil, nil, failure("invalid_request", "unable to encode remotion props", nil)
	}
	renderDir, err := os.MkdirTemp("", "facet-remotion-*")
	if err != nil {
		return nil, nil, failure("command_failed", "unable to create remotion staging directory", nil)
	}
	defer os.RemoveAll(renderDir)
	publicDir := filepath.Join(renderDir, "public")
	if err := os.Mkdir(publicDir, 0700); err != nil {
		return nil, nil, err
	}
	propsJSON, err = stageRemotionMedia(propsJSON, absComposer, publicDir, workDirOf(ctx))
	if err != nil {
		return nil, nil, err
	}
	propsPath := filepath.Join(renderDir, "props.json")
	if err := os.WriteFile(propsPath, propsJSON, 0600); err != nil {
		return nil, nil, failure("command_failed", "unable to write remotion props: "+err.Error(), nil)
	}

	absOut, _ := filepath.Abs(renderPath)
	absProps, _ := filepath.Abs(propsPath)

	args := []string{
		cliPath, "render", entryFile, compositionID, absOut,
		"--props=" + absProps,
		"--public-dir=" + publicDir,
		"--pixel-format=yuv420p",
		"--color-space=bt709",
	}
	// Pure graphics with no audio declaration should not acquire an encoder's
	// default audio track. Video cuts retain their source audio unless directed
	// otherwise; an explicit narration/music track is never muted here.
	if silentGraphicProps(propsJSON) && r.AudioPath == "" {
		args = append(args, "--muted")
	}

	// Honour an explicitly requested output profile.
	//
	// Direct Remotion props carry width/height at the top level, but they were
	// only ever written into the props file — Remotion takes the output
	// dimensions as CLI overrides, not props — so a caller asking for 1280x720
	// silently received the composition's 1920x1080 default. The render
	// succeeded and quietly ignored the request, which is worse than failing.
	//
	// The earlier version of this only read RawProps, so a request built from
	// `scenes` — the documented shape — still rendered at the composition
	// default. Verified: the same scene requested at 640x360 and at the
	// default produced BYTE-IDENTICAL 1080p files, because the fields were
	// parsed into r.Width/r.Height and then never used. Fixing it for one
	// input shape left the field declared-and-ignored for the other.
	var w, h int
	if r.RawProps != nil {
		dim := func(key string) int {
			v, ok := r.RawProps[key].(float64)
			if !ok || v <= 0 {
				return 0
			}
			return int(v)
		}
		if v := dim("width"); v > 0 {
			w = v
		}
		if v := dim("height"); v > 0 {
			h = v
		}
	}
	if w > 0 && h > 0 {
		args = append(args, "--width="+strconv.Itoa(w), "--height="+strconv.Itoa(h))
	}

	if browser := findBrowserExecutable(); browser != "" {
		args = append(args, "--browser-executable="+browser)
	}
	args = append(args, remotionLoadFlags()...)
	if stdout, err := runCommandDirContext(ctx, tmo, absComposer, "node", args...); err != nil {
		var failed *toolFailure
		if errors.As(err, &failed) {
			if failed.err.Code == "command_timeout" {
				failed.err.Details["output"] = remotionTimeoutDiagnostic(string(stdout))
				stderr, _ := failed.err.Details["stderr"].(string)
				failed.err.Details["stderr"] = remotionTimeoutDiagnostic(stderr)
			} else {
				// "node failed" names the binary, not the cause, and an agent
				// that cannot tell whether the module is broken or its request
				// was wrong abandons the module and works around it. Lift the
				// renderer's own first error line into the message so the
				// failure is diagnosable without digging through details.
				stderr, _ := failed.err.Details["stderr"].(string)
				if reason := remotionFailureReason(stderr, string(stdout)); reason != "" {
					failed.err.Message = "remotion render failed: " + reason
				}
				failed.err.Details["composer_dir"] = absComposer
			}
		}
		return nil, nil, err
	}

	if r.AudioPath != "" {
		tempMux, cleanupMux, err := temporaryOutput(outPath)
		if err != nil {
			return nil, nil, err
		}
		defer cleanupMux()
		if _, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", renderPath, "-i", r.AudioPath, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-shortest", tempMux); err != nil {
			return nil, nil, err
		}
		renderPath = tempMux
	}

	// Bring the audio to the delivery loudness (social and web: -14 LUFS).
	if err := normalizeLoudness(ctx, renderPath, r.LoudnessTarget); err != nil {
		return nil, nil, err
	}

	// Validate the completed artifact before replacing an existing delivery.
	if _, _, err := probeContext(ctx, renderPath); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(renderPath, outPath); err != nil {
		return nil, nil, failure("command_failed", "remotion output could not be published", map[string]any{"error": bounded(err.Error())})
	}
	// Bind evidence to the actual published bytes, including any post-render mux.
	facts, warnings, err := probeContext(ctx, outPath)
	if err != nil {
		return nil, nil, err
	}

	warnings = append(composerWarnings, warnings...)

	return map[string]any{
		"operation":      "remotion_render",
		"composition_id": compositionID,
		"output":         outPath,
		"output_facts":   facts,
	}, warnings, nil
}

func silentGraphicProps(data []byte) bool {
	var props map[string]any
	if json.Unmarshal(data, &props) != nil {
		return false
	}
	if audio, ok := props["audio"].(map[string]any); ok && len(audio) > 0 {
		return false
	}
	cuts, ok := props["cuts"].([]any)
	if !ok || len(cuts) == 0 {
		return false
	}
	for _, item := range cuts {
		cut, ok := item.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range []string{"source", "src", "video", "video_path", "audio_path"} {
			if value, ok := cut[key].(string); ok && value != "" {
				return false
			}
		}
	}
	return true
}

// Stage only explicit component media fields, never a project/public tree or arbitrary
// strings in metadata. JSON decoding gives us a private copy of the caller's props.
func stageRemotionMedia(data []byte, composer, publicDir, base string) ([]byte, error) {
	var props map[string]any
	if err := json.Unmarshal(data, &props); err != nil {
		return nil, failure("invalid_request", "invalid remotion props", nil)
	}
	const maxFileSize int64 = 2 << 30
	const maxTotalSize int64 = 8 << 30
	var total int64
	staged := map[string]string{}
	stage := func(src string) (string, error) {
		if src == "" || strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "data:") {
			return src, nil // Match resolveAsset's existing URL semantics without fetching.
		}
		if name, ok := staged[src]; ok {
			return name, nil
		}
		invalid := func(message string) (string, error) {
			return "", failure("invalid_request", "remotion media: "+message, nil)
		}
		path := src
		if strings.HasPrefix(strings.ToLower(path), "file:") {
			u, err := url.Parse(path)
			if err != nil || u.Opaque != "" || u.User != nil || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
				return invalid("file URL must identify a local file without query or fragment")
			}
			path = u.Path
			if os.PathSeparator == '\\' && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
			if !filepath.IsAbs(path) {
				return invalid("file URL must be absolute")
			}
		}
		path = filepath.FromSlash(path)
		if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
			return invalid("unsupported scheme, network path or alternate stream")
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".wav", ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".opus", ".mp4", ".mov", ".webm", ".avi", ".mkv", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		default:
			return invalid("unsupported media extension (active documents and non-media files are not public assets)")
		}
		var input *os.File
		var err error
		if filepath.IsAbs(path) {
			// Explicit absolute paths may name media outside the project, but not devices.
			info, statErr := os.Stat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				return invalid("absolute source must be an accessible regular file")
			}
			input, err = os.Open(path)
		} else {
			if !filepath.IsLocal(path) {
				return invalid("relative source must stay inside the project")
			}
			// Root.Open also confines symlinks, including during path resolution.
			// A relative path means the project first (the call's working
			// folder over MCP), then the process's directory, then the
			// composer's own public assets.
			roots := []string{".", filepath.Join(composer, "public")}
			if base != "" {
				roots = append([]string{base}, roots...)
			}
			for _, rootPath := range roots {
				root, openErr := os.OpenRoot(rootPath)
				if openErr != nil {
					err = openErr
				} else {
					info, statErr := root.Stat(path)
					if statErr == nil && !info.Mode().IsRegular() {
						root.Close()
						return invalid("source must be a regular file")
					}
					if statErr != nil {
						err = statErr
					} else {
						input, err = root.Open(path)
					}
					root.Close()
				}
				if !errors.Is(err, os.ErrNotExist) {
					break
				}
			}
		}
		if err != nil {
			return invalid("source is missing, inaccessible or escapes its root")
		}
		defer input.Close()
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxFileSize || total+info.Size() > maxTotalSize || len(staged) >= 256 {
			return invalid("source must be nonempty regular media within staging limits (256 files, 2 GiB each, 8 GiB total)")
		}
		var header [512]byte
		n, err := input.ReadAt(header[:], 0)
		if err != nil && err != io.EOF {
			return invalid("cannot inspect source media")
		}
		mime := http.DetectContentType(header[:n])
		media := strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "image/") || mime == "application/ogg"
		// Containers not recognized by net/http's bounded signature table.
		//
		// The MPEG audio check matches an 11-bit frame sync (0xFF followed by
		// three set bits), which is what the spec actually defines. A tighter
		// mask of 0xf6==0xf0 rejected the most common real headers — 0xf3 and
		// 0xfb, MPEG-1 Layer III — so Facet refused MP3s that its own edge_tts
		// tool had just produced. Verified against a generated narration file
		// whose header is ff f3.
		media = media || (n >= 12 && (string(header[4:8]) == "ftyp" || string(header[4:8]) == "moov" || string(header[4:8]) == "mdat")) ||
			(n >= 4 && (string(header[:4]) == "fLaC" || string(header[:4]) == "\x1a\x45\xdf\xa3" || string(header[:4]) == "II*\x00" || string(header[:4]) == "MM\x00*")) ||
			(n >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0)
		if !media {
			return invalid("source does not have a supported media signature")
		}
		name := fmt.Sprintf("asset-%03d%s", len(staged), ext)
		output, err := os.OpenFile(filepath.Join(publicDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, info.Size()+1))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != info.Size() {
			return invalid("source changed or could not be staged")
		}
		total += written
		staged[src] = name
		return name, nil
	}
	rewrite := func(object map[string]any, keys []string) error {
		for _, key := range keys {
			if src, ok := object[key].(string); ok {
				value, err := stage(src)
				if err != nil {
					return err
				}
				object[key] = value
			}
		}
		return nil
	}
	_ = rewrite
	// Every media field anywhere in the props is staged: cut sources, card
	// backgrounds, screenshots, anime images, cinematic scenes, the talking
	// head's video, product images, audio tracks and sound-effect cues.
	var walk func(value any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if composerNonMediaKeys[key] {
					continue
				}
				if composerMediaKeys[key] {
					switch m := item.(type) {
					case string:
						staged, err := stage(m)
						if err != nil {
							return err
						}
						v[key] = staged
						continue
					case []any:
						for i, entry := range m {
							if s, ok := entry.(string); ok {
								staged, err := stage(s)
								if err != nil {
									return err
								}
								m[i] = staged
							}
						}
						continue
					}
				}
				if err := walk(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range v {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(props); err != nil {
		return nil, err
	}
	return json.Marshal(props)
}

// composerMediaKeys are the prop fields that hold media files.
var composerMediaKeys = map[string]bool{
	"source": true, "src": true, "backgroundImage": true, "backgroundVideo": true, "image": true,
	"images": true, "logo": true, "screenshot": true, "poster": true, "video": true, "videoSrc": true,
	"backgroundSrc": true, "productImage": true, "avatarSrc": true,
}

// composerNonMediaKeys hold no media, so nothing under them is staged.
var composerNonMediaKeys = map[string]bool{
	"metadata": true, "themeConfig": true, "captions": true, "words": true, "delivery_promise": true,
}

// Only retain known progress lines: browser logs can echo props, source code,
// credentials and signed media URLs. Free-form timeout output is not safe to expose.
var remotionProgressLine = regexp.MustCompile(`^(Bundling [0-9]+%|Getting composition|Concurrency +[0-9]+x|Rendered [0-9]+/[0-9]+(, time remaining: [0-9hms .]+)?|Encoded [0-9]+/[0-9]+)$`)

func remotionTimeoutDiagnostic(output string) string {
	var progress strings.Builder
	omitted := false
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if remotionProgressLine.MatchString(line) {
			progress.WriteString(line + "\n")
		} else if line != "" {
			omitted = true
		}
	}
	text := progress.String()
	if omitted {
		text += "[REDACTED non-progress renderer output]\n"
	}
	return bounded(text)
}

func buildSubtitleStyle(style map[string]any) string {
	if style == nil {
		style = map[string]any{}
	}
	font := "Segoe UI"
	if f, ok := style["font"].(string); ok && f != "" {
		font = f
	}
	fontSize := 24
	if fs, ok := style["font_size"].(float64); ok && fs > 0 {
		fontSize = int(fs)
	}
	marginV := 40
	if mv, ok := style["margin_v"].(float64); ok && mv > 0 {
		marginV = int(mv)
	}
	alignment := 2
	if al, ok := style["alignment"].(float64); ok && al > 0 {
		alignment = int(al)
	}
	return fmt.Sprintf("FontName=%s,FontSize=%d,Bold=1,PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,Outline=2,Shadow=1,Alignment=%d,MarginV=%d", font, fontSize, alignment, marginV)
}

func findBrowserExecutable() string {
	if env := os.Getenv("REMOTION_BROWSER_EXECUTABLE"); env != "" && fileExists(env) {
		return env
	}
	if env := os.Getenv("PUPPETEER_EXECUTABLE_PATH"); env != "" && fileExists(env) {
		return env
	}
	if env := os.Getenv("CHROME_PATH"); env != "" && fileExists(env) {
		return env
	}
	// Prefer the renderer-managed headless browser over a desktop browser with
	// user policies/profiles. Explicit overrides above retain precedence.
	if composer, err := findComposerDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(composer, "node_modules", ".remotion", "chrome-headless-shell", "*", "*", "chrome-headless-shell*"))
		for _, candidate := range matches {
			if fileExists(candidate) && (strings.HasSuffix(candidate, ".exe") || filepath.Base(candidate) == "chrome-headless-shell") {
				return candidate
			}
		}
	}
	candidates := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`,
		`/usr/bin/google-chrome`,
		`/usr/bin/chromium-browser`,
		`/usr/bin/chromium`,
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ansiEscape strips terminal colouring so a renderer's own error text is
// readable in a JSON envelope and in a cockpit.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// remotionLoadFlags keep a render alive on a busy machine. Remotion opens half
// the CPU threads as browser tabs and gives each 30 s to load the bundle and
// its fonts; next to other work a late tab misses that and the whole render
// fails part-way. Fewer tabs and a longer allowance cost little speed.
func remotionLoadFlags() []string {
	tabs := runtime.NumCPU() / 4
	if tabs < 2 {
		tabs = 2
	}
	if tabs > 4 {
		tabs = 4
	}
	return []string{"--concurrency=" + strconv.Itoa(tabs), "--timeout=120000"}
}

// remotionFailureReason extracts the renderer's own first error line.
//
// A message naming only the binary — "node failed" — leaves a caller unable to
// tell whether the module is broken or its request was wrong, and an agent that
// cannot tell abandons the module and works around it. That was observed: a
// render failure led an agent to fall back to raw ffmpeg and report a blank
// video as a success.
//
// Only lines the renderer itself emitted are lifted, bounded, and stripped of
// colour codes. Everything else stays in details rather than being guessed at.
func remotionFailureReason(stderr, stdout string) string {
	for _, source := range []string{stderr, stdout} {
		for _, line := range strings.Split(strings.ReplaceAll(source, "\r", "\n"), "\n") {
			line = strings.TrimSpace(ansiEscape.ReplaceAllString(line, ""))
			// Puppeteer teardown noise is a symptom of the real failure, not
			// the failure, and naming it would send a caller after the wrong
			// thing.
			if line == "" || strings.Contains(line, "Was not able to close puppeteer page") {
				continue
			}
			// Remotion labels its errors and then repeats the type, so a real
			// line reads "Error  Error: Could not find composition with ID X".
			// Match on the embedded type rather than a prefix, which is what
			// the actual output looks like once colour codes are stripped. The
			// earliest marker wins, so "TypeError:" is not cut to "Error:".
			best := -1
			for _, marker := range []string{
				"Error:", "TypeError:", "ReferenceError:", "SyntaxError:",
				"Cannot find module", "ENOENT",
			} {
				if i := strings.Index(line, marker); i >= 0 && (best < 0 || i < best) {
					best = i
				}
			}
			if best >= 0 {
				return boundedReason(strings.TrimSpace(line[best:]))
			}
			// Its own errors carry the label alone: "Error  A delayRender()
			// "Loading bundled fonts" was called but not cleared after 28000ms".
			if rest, ok := strings.CutPrefix(line, "Error "); ok && strings.TrimSpace(rest) != "" {
				return boundedReason(strings.TrimSpace(rest))
			}
		}
	}
	return ""
}
func boundedReason(s string) string {
	const limit = 300
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

// buildRemotionProps produces the props JSON the Remotion composition receives.
//
// EXTRACTED SO A TEST CAN CALL THE REAL LOGIC. The first guard for the audio
// defect rebuilt this merge inside the test, which meant it passed no matter
// what this function did -- a circular oracle: both sides computed the same
// answer from the same idea, so removing the fix left the test green.
//
// The envelope path marshals edit_decisions into props, and
// composeEditDecisions has NO audio field, so a request declaring `audio` had
// its narration silently discarded and rendered a SILENT video that reported
// success. composeRequest.Audio was accepted, decoded, and read by nothing --
// the declared-and-ignored class already fixed here once for width/height.
//
// The composition reads `audio` at the TOP LEVEL of props, so it is merged in
// beside the edit decisions rather than nested under them.
func buildRemotionProps(r composeRequest) ([]byte, error) {
	if r.RawProps != nil {
		return json.Marshal(r.RawProps)
	}
	if r.EditDecisions == nil {
		return []byte("{}"), nil
	}

	merged := map[string]any{"cuts": r.EditDecisions.Cuts}
	if r.Audio != nil {
		merged["audio"] = r.Audio
	}
	return json.Marshal(merged)
}
