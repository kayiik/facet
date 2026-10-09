package toolbox

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// composerRequest reports whether a request names a composer composition (by
// id or renderer family) without top-level cuts, as CinematicRenderer and
// TalkingHead props do.
func composerRequest(raw map[string]any) bool {
	for _, key := range []string{"composition", "composition_id", "renderer_family", "bespoke"} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

// remotionDecisions reports whether edit decisions are for the Remotion
// runtime: they say so, or they carry composer cuts without naming another.
func remotionDecisions(edit map[string]any) bool {
	runtime, _ := edit["render_runtime"].(string)
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "remotion":
		return true
	case "":
		_, family := edit["renderer_family"]
		return family
	}
	return false
}

// editDecisionsProps lifts Remotion edit decisions to the top level, where the
// composer path reads them, keeping the request's own output and timeout.
func editDecisionsProps(raw, edit map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range edit {
		out[k] = v
	}
	for _, k := range []string{"output", "output_path", "timeout_seconds", "loudness_target"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}
	if mode, _ := edit["composition_mode"].(string); mode != "atelier" {
		delete(out, "bespoke")
	}
	return out
}

func requestOutput(raw map[string]any) string {
	for _, key := range []string{"output", "output_path"} {
		if o, ok := raw[key].(string); ok && strings.TrimSpace(o) != "" {
			return strings.TrimSpace(o)
		}
	}
	return "renders/final.mp4"
}

func requestTimeout(raw map[string]any) time.Duration {
	if t, ok := raw["timeout_seconds"].(float64); ok && t > 0 {
		return time.Duration(t) * time.Second
	}
	return 600 * time.Second
}

// doComposerRender renders composer props: templated through the bundled
// compositions, or atelier through a project's own Remotion entry.
func doComposerRender(ctx context.Context, op string, raw map[string]any) (any, []string, error) {
	if bespoke, ok := raw["bespoke"].(map[string]any); ok {
		return doAtelierRender(ctx, op, raw, bespoke)
	}
	plan, err := prepareComposerProps(raw)
	if err != nil {
		return nil, nil, err
	}
	outPath := requestOutput(raw)
	tmo := requestTimeout(raw)
	if op == "estimate" {
		last := 0.0
		if v, ok := plan.props["duration_seconds"].(float64); ok {
			last = v
		} else if cuts, ok := plan.props["cuts"].([]any); ok {
			for _, c := range cuts {
				if m, ok := c.(map[string]any); ok {
					if v, ok := m["out_seconds"].(float64); ok && v > last {
						last = v
					}
				}
			}
		}
		f, w, h := renderShape(plan.props, last)
		res := estimateRender([]string{"video_compose_remotion_render"}, f, w, h)
		res["composition_id"] = plan.composition
		return res, nil, nil
	}
	r := composeRequest{
		Operation:      "remotion_render",
		OutputPath:     outPath,
		RawProps:       plan.props,
		CompositionID:  plan.composition,
		LoudnessTarget: plan.loudness,
	}
	if ap, ok := raw["audio_path"].(string); ok && ap != "" {
		r.AudioPath = ap
	}
	return doRemotionRenderContext(ctx, r, outPath, tmo)
}

// doAtelierRender renders a composition written for this production: the
// project's Remotion entry (a .tsx registering compositions with
// registerRoot) is copied beside the composer so it resolves the composer's
// packages and bundled fonts, then rendered like any composition.
func doAtelierRender(ctx context.Context, op string, raw, bespoke map[string]any) (any, []string, error) {
	entry, _ := bespoke["entry"].(string)
	compositionID, _ := bespoke["composition_id"].(string)
	if strings.TrimSpace(entry) == "" || strings.TrimSpace(compositionID) == "" {
		return nil, nil, failure("invalid_request", "bespoke needs entry (the project's Remotion entry .tsx) and composition_id (the id it registers)", nil)
	}
	entry = defaultOutput(ctx, entry)
	if err := inputPath(entry); err != nil {
		return nil, nil, err
	}
	props := map[string]any{}
	if p, ok := bespoke["props_path"].(string); ok && strings.TrimSpace(p) != "" {
		data, err := os.ReadFile(defaultOutput(ctx, p))
		if err != nil {
			return nil, nil, failure("input_not_found", "bespoke.props_path cannot be read: "+err.Error(), nil)
		}
		props["__props_file"] = string(data)
	}
	outPath := requestOutput(raw)
	tmo := requestTimeout(raw)
	if op == "estimate" {
		return map[string]any{"operation": "atelier_render", "entry": entry, "composition_id": compositionID, "output": outPath}, nil, nil
	}
	composer, err := findComposerDir()
	if err != nil {
		return nil, nil, err
	}
	work, err := os.MkdirTemp(composer, "atelier-")
	if err != nil {
		return nil, nil, failure("command_failed", "cannot prepare the atelier workspace beside the composer: "+err.Error(), nil)
	}
	defer os.RemoveAll(work)
	srcDir := filepath.Dir(entry)
	if err := copyTree(srcDir, work); err != nil {
		return nil, nil, failure("command_failed", "cannot copy the atelier composition: "+err.Error(), nil)
	}
	stagedEntry := filepath.Join(work, filepath.Base(entry))
	propsPath := filepath.Join(work, ".facet-props.json")
	propsJSON := "{}"
	if s, ok := props["__props_file"].(string); ok {
		propsJSON = s
	}
	if err := os.WriteFile(propsPath, []byte(propsJSON), 0o600); err != nil {
		return nil, nil, failure("command_failed", "cannot write atelier props: "+err.Error(), nil)
	}
	if err := outputPath(outPath, true, false); err != nil {
		return nil, nil, err
	}
	renderPath, cleanup, err := temporaryOutput(outPath)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	cli := filepath.Join(composer, "node_modules", "@remotion", "cli", "remotion-cli.js")
	args := []string{cli, "render", stagedEntry, compositionID, renderPath, "--props=" + propsPath, "--pixel-format=yuv420p", "--color-space=bt709"}
	if public := filepath.Join(srcDir, "public"); fileExists(public) {
		args = append(args, "--public-dir="+public)
	}
	if browser := findBrowserExecutable(); browser != "" {
		args = append(args, "--browser-executable="+browser)
	}
	args = append(args, remotionLoadFlags()...)
	if out, err := runCommandDirContext(ctx, tmo, composer, "node", args...); err != nil {
		if reason := remotionFailureReason("", string(out)); reason != "" {
			return nil, nil, failure("command_failed", "atelier render failed: "+reason, nil)
		}
		return nil, nil, err
	}
	target := -14.0
	if v, ok := raw["loudness_target"].(float64); ok {
		target = v
	}
	if err := normalizeLoudness(ctx, renderPath, target); err != nil {
		return nil, nil, err
	}
	if err := finalizeOutput(renderPath, outPath, true); err != nil {
		return nil, nil, err
	}
	facts, warnings, err := probeContext(ctx, outPath)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"operation": "atelier_render", "composition_id": compositionID, "output": outPath, "output_facts": facts}, warnings, nil
}

// copyTree copies a directory, skipping node_modules and dot-folders.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			if rel != "." && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
}

// normalizeLoudness brings a rendered video's audio to target LUFS (true peak
// -1.5 dB), copying the picture untouched. A video without audio is left as
// it is.
func normalizeLoudness(ctx context.Context, path string, target float64) error {
	if target == 0 || !hasAudioStream(ctx, path) {
		return nil
	}
	temp, cleanup, err := temporaryOutput(path)
	if err != nil {
		return err
	}
	defer cleanup()
	filter := "loudnorm=I=" + strconv.FormatFloat(target, 'f', 1, 64) + ":TP=-1.5:LRA=11"
	if out, err := runCommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", path,
		"-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-af", filter, "-ar", "48000", "-c:a", "aac", "-b:a", "192k", temp); err != nil {
		return failure("command_failed", "loudness normalisation failed: "+bounded(string(out)+err.Error()), nil)
	}
	return os.Rename(temp, path)
}

func hasAudioStream(ctx context.Context, path string) bool {
	out, err := runCommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "stream=index", "-of", "csv=p=0", path)
	return err == nil && strings.TrimSpace(string(out)) != ""
}
