package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// plan_check reviews a scene plan before anything is made: does the timeline
// have gaps or overlaps, does the picture vary, is it a slideshow when motion
// was promised, does it cover the narration, and can every asset be made with
// the providers configured on this machine.

type planCheckRequest struct {
	ScenePlanPath   string          `json:"scene_plan_path,omitempty"`
	ScenePlan       json.RawMessage `json:"scene_plan,omitempty"`
	TimingPath      string          `json:"timing_path,omitempty"`
	DeliveryPromise string          `json:"delivery_promise,omitempty"`
	DurationSeconds float64         `json:"duration_seconds,omitempty"`
}

type planScene struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Start    float64          `json:"start_seconds"`
	End      float64          `json:"end_seconds"`
	Required []map[string]any `json:"required_assets"`
}

// staticSceneTypes show text or a still image without motion of their own.
var staticSceneTypes = map[string]bool{
	"text_card": true, "hero_title": true, "section_title": true, "callout": true, "stat_card": true,
	"image": true, "still": true, "title": true, "text": true,
}

// assetCapabilities maps an asset's source or kind to the capability that
// makes it.
var assetCapabilities = map[string]string{
	"generate": "image_generation", "generated": "image_generation", "image_generation": "image_generation",
	"video_generation": "video_generation", "generated_video": "video_generation",
	"stock": "video_stock", "stock_video": "video_stock", "stock_image": "image_stock",
	"narration": "voice", "voice": "voice", "tts": "voice", "music": "music", "sfx": "sfx",
	"capture": "capture", "screen_recording": "capture", "avatar": "avatar",
}

func doPlanCheckContext(ctx context.Context, op string, data []byte) (any, []string, error) {
	var r planCheckRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}
	var plan struct {
		Scenes []planScene `json:"scenes"`
	}
	if err := loadRecord(r.ScenePlan, r.ScenePlanPath, "scene_plan", &plan); err != nil {
		return nil, nil, err
	}
	if len(plan.Scenes) == 0 {
		return nil, nil, failure("invalid_request", "the scene plan has no scenes", nil)
	}
	if op == "estimate" {
		return estimateResult([]string{"plan_check"}), nil, nil
	}
	var findings []checkFinding
	scenes := append([]planScene(nil), plan.Scenes...)
	sort.SliceStable(scenes, func(i, j int) bool { return scenes[i].Start < scenes[j].Start })
	var gaps, overlaps []string
	for i := 1; i < len(scenes); i++ {
		prev, cur := scenes[i-1], scenes[i]
		switch {
		case cur.Start-prev.End > 0.05:
			gaps = append(gaps, fmt.Sprintf("%s→%s (%.1f s)", prev.ID, cur.ID, cur.Start-prev.End))
		case prev.End-cur.Start > 0.05:
			overlaps = append(overlaps, fmt.Sprintf("%s/%s (%.1f s)", prev.ID, cur.ID, prev.End-cur.Start))
		}
	}
	if len(gaps) > 0 {
		findings = append(findings, checkFinding{"critical", "the timeline has gaps: " + strings.Join(gaps, ", "), "extend a scene or add one so the picture never goes blank"})
	}
	if len(overlaps) > 0 {
		findings = append(findings, checkFinding{"critical", "scenes overlap: " + strings.Join(overlaps, ", "), "fix the start and end times"})
	}
	run := 1
	for i := 1; i < len(scenes); i++ {
		if scenes[i].Type != "" && scenes[i].Type == scenes[i-1].Type {
			run++
			if run == 3 {
				findings = append(findings, checkFinding{"suggestion", fmt.Sprintf("three %s scenes in a row ending at %s", scenes[i].Type, scenes[i].ID), "vary the scene type to keep the eye moving"})
			}
		} else {
			run = 1
		}
	}
	end := scenes[len(scenes)-1].End
	total := 0.0
	static := 0.0
	types := map[string]bool{}
	for _, s := range scenes {
		d := math.Max(0, s.End-s.Start)
		total += d
		types[s.Type] = true
		if staticSceneTypes[s.Type] {
			static += d
		}
	}
	motionShare := 1.0
	if total > 0 {
		motionShare = 1 - static/total
	}
	risk := "low"
	switch {
	case motionShare < 0.4:
		risk = "high"
	case motionShare < 0.7:
		risk = "medium"
	}
	if r.DeliveryPromise == "motion_led" && motionShare < 0.7 {
		findings = append(findings, checkFinding{"critical", fmt.Sprintf("a motion-led video, but only %.0f%% of the time has motion", motionShare*100), "replace static cards with footage, generated motion or animated scenes, or agree a different promise"})
	} else if risk == "high" {
		findings = append(findings, checkFinding{"suggestion", fmt.Sprintf("only %.0f%% of the time has motion: this will read as a slideshow", motionShare*100), "add camera moves, charts, footage or animated scenes"})
	}
	if r.DurationSeconds > 0 && math.Abs(end-r.DurationSeconds) > 0.5 {
		findings = append(findings, checkFinding{"critical", fmt.Sprintf("the plan ends at %.1f s; the video should last %.1f s", end, r.DurationSeconds), "retime the scenes"})
	}
	coverage := map[string]any{}
	if strings.TrimSpace(r.TimingPath) != "" {
		timing, err := loadTiming(r.TimingPath)
		if err != nil {
			return nil, nil, err
		}
		coverage["narration_seconds"] = timing.Duration
		coverage["plan_seconds"] = end
		if end+0.05 < timing.Duration {
			findings = append(findings, checkFinding{"critical", fmt.Sprintf("the narration runs to %.1f s but the scenes end at %.1f s", timing.Duration, end), "extend the last scene or add one"})
		}
	}
	missing := map[string]bool{}
	for _, s := range scenes {
		for _, asset := range s.Required {
			for _, key := range []string{"source", "kind", "type", "capability"} {
				v, _ := asset[key].(string)
				capability, ok := assetCapabilities[strings.ToLower(v)]
				if !ok {
					continue
				}
				if !capabilityReady(capability) {
					missing[capability] = true
				}
				break
			}
		}
	}
	unavailable := []string{}
	for c := range missing {
		unavailable = append(unavailable, c)
	}
	sort.Strings(unavailable)
	if len(unavailable) > 0 {
		findings = append(findings, checkFinding{"critical", "no configured provider for: " + strings.Join(unavailable, ", "), "use the free alternative from capabilities, or ask the operator to set the provider up"})
	}
	if findings == nil {
		findings = []checkFinding{}
	}
	return map[string]any{
		"operation": "plan_check", "scenes": len(scenes), "scene_types": len(types),
		"motion_share": math.Round(motionShare*100) / 100, "slideshow_risk": risk,
		"ends_at": end, "coverage": coverage, "unavailable": unavailable, "findings": findings,
	}, nil, nil
}

// capabilityReady reports whether any tool providing capability is
// configured on this machine.
func capabilityReady(capability string) bool {
	for _, name := range names {
		for _, c := range ToolMeta(name).Capabilities {
			if c == capability {
				if configured, _ := summary(name)["configured"].(bool); configured {
					return true
				}
			}
		}
	}
	return false
}
