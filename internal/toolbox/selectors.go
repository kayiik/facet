package toolbox

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type imageSelectorRequest struct {
	Prompt               string   `json:"prompt,omitempty"`
	AspectRatio          string   `json:"aspect_ratio,omitempty"`
	Style                string   `json:"style,omitempty"`
	SceneType            string   `json:"scene_type,omitempty"`
	BudgetTier           string   `json:"budget_tier,omitempty"`
	MaxCost              *float64 `json:"max_cost,omitempty"`
	PreferredProvider    string   `json:"preferred_provider,omitempty"`
	AllowedProviders     []string `json:"allowed_providers,omitempty"`
	GenerationMode       string   `json:"generation_mode,omitempty"`
	RequireTextRendering bool     `json:"require_text_rendering,omitempty"`
	RequireVector        bool     `json:"require_vector,omitempty"`
}

type imageCandidateFact struct {
	Name                  string   `json:"name"`
	Provider              string   `json:"provider"`
	Type                  string   `json:"type"`
	Configured            bool     `json:"configured"`
	EstimatedCost         float64  `json:"estimated_cost"`
	CostDescription       string   `json:"cost_description"`
	SupportedAspectRatios []string `json:"supported_aspect_ratios"`
	SupportsTextRendering bool     `json:"supports_text_rendering"`
	SupportsEditing       bool     `json:"supports_editing"`
	SuitabilityScore      float64  `json:"suitability_score"`
	Strengths             []string `json:"strengths"`
	Limitations           []string `json:"limitations"`
	RankingReasons        []string `json:"ranking_reasons"`
}

type videoSelectorRequest struct {
	Prompt            string   `json:"prompt,omitempty"`
	AspectRatio       string   `json:"aspect_ratio,omitempty"`
	Duration          *float64 `json:"duration,omitempty"`
	Style             string   `json:"style,omitempty"`
	Intent            string   `json:"intent,omitempty"`
	BudgetTier        string   `json:"budget_tier,omitempty"`
	MaxCost           *float64 `json:"max_cost,omitempty"`
	PreferredProvider string   `json:"preferred_provider,omitempty"`
	AllowedProviders  []string `json:"allowed_providers,omitempty"`
	SourceType        string   `json:"source_type,omitempty"`
	RequireAudio      bool     `json:"require_audio,omitempty"`
}

type videoCandidateFact struct {
	Name                  string   `json:"name"`
	Provider              string   `json:"provider"`
	Type                  string   `json:"type"`
	Configured            bool     `json:"configured"`
	EstimatedCost         float64  `json:"estimated_cost"`
	CostDescription       string   `json:"cost_description"`
	MinDuration           float64  `json:"min_duration"`
	MaxDuration           float64  `json:"max_duration"`
	SupportedAspectRatios []string `json:"supported_aspect_ratios"`
	SupportsNativeAudio   bool     `json:"supports_native_audio"`
	SuitabilityScore      float64  `json:"suitability_score"`
	Strengths             []string `json:"strengths"`
	Limitations           []string `json:"limitations"`
	RankingReasons        []string `json:"ranking_reasons"`
}

func doImageSelector(op string, data []byte) (any, []string, error) {
	var r imageSelectorRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		res := estimateResult([]string{"image_selector_rank"})
		res["estimated_cost"] = 0.0
		res["network"] = false
		return res, nil, nil
	}

	rawCandidates := []imageCandidateFact{
		{
			Name:                  "openai_image",
			Provider:              "openai",
			Type:                  "generation",
			Configured:            os.Getenv("OPENAI_API_KEY") != "",
			EstimatedCost:         0.04,
			CostDescription:       "$0.040 (standard 1024x1024) - $0.080 (HD / wide)",
			SupportedAspectRatios: []string{"1:1", "16:9", "9:16", "4:3", "3:4"},
			SupportsTextRendering: true,
			SupportsEditing:       true,
			Strengths:             []string{"complex instruction following", "accurate on-image typography and labels", "multi-element compositions"},
			Limitations:           []string{"higher cost per image", "requires OPENAI_API_KEY"},
		},
		{
			Name:                  "flux_image",
			Provider:              "flux",
			Type:                  "generation",
			Configured:            os.Getenv("FAL_KEY") != "" || os.Getenv("FLUX_API_KEY") != "",
			EstimatedCost:         0.04,
			CostDescription:       "$0.030 - $0.050 per image (FLUX Pro / Dev)",
			SupportedAspectRatios: []string{"1:1", "16:9", "9:16", "4:3", "3:4", "21:9"},
			SupportsTextRendering: false,
			SupportsEditing:       false,
			Strengths:             []string{"exceptional photorealism", "cinematic lighting", "high artistic aesthetic", "broad aspect ratio support"},
			Limitations:           []string{"text rendering is variable", "requires FAL_KEY"},
		},
		{
			Name:                  "wikimedia",
			Provider:              "wikimedia",
			Type:                  "stock",
			Configured:            true,
			EstimatedCost:         0.00,
			CostDescription:       "Free ($0.00 / public domain)",
			SupportedAspectRatios: []string{"16:9", "4:3", "1:1"},
			SupportsTextRendering: false,
			SupportsEditing:       false,
			Strengths:             []string{"historical, scientific, and educational media", "public domain licenses", "no API key required"},
			Limitations:           []string{"variable resolution and vintage artifacts"},
		},
	}

	allowedSet := map[string]bool{}
	for _, a := range r.AllowedProviders {
		allowedSet[strings.ToLower(strings.TrimSpace(a))] = true
	}

	pref := strings.ToLower(strings.TrimSpace(r.PreferredProvider))
	requestedAR := strings.TrimSpace(r.AspectRatio)
	style := strings.ToLower(strings.TrimSpace(r.Style))
	if style == "" {
		style = strings.ToLower(strings.TrimSpace(r.SceneType))
	}
	promptLower := strings.ToLower(r.Prompt)

	var evaluated []imageCandidateFact
	numConfigured := 0

	for _, c := range rawCandidates {
		if len(allowedSet) > 0 {
			if !allowedSet[strings.ToLower(c.Name)] && !allowedSet[strings.ToLower(c.Provider)] {
				continue
			}
		}

		if c.Configured {
			numConfigured++
		}

		score := 0.50
		reasons := []string{}

		// Configured signal
		if c.Configured {
			score += 0.20
			reasons = append(reasons, "credentials configured and ready")
		} else {
			score -= 0.25
			reasons = append(reasons, "credentials unconfigured; requires API key")
		}

		// Preferred provider
		if pref != "" && pref != "auto" {
			if strings.EqualFold(pref, c.Name) || strings.EqualFold(pref, c.Provider) {
				score += 0.45
				reasons = append(reasons, "matches preferred_provider: "+pref)
			} else {
				score -= 0.20
			}
		}

		// Aspect ratio support
		if requestedAR != "" {
			supported := false
			for _, ar := range c.SupportedAspectRatios {
				if ar == requestedAR {
					supported = true
					break
				}
			}
			if supported {
				score += 0.10
				reasons = append(reasons, "natively supports aspect ratio "+requestedAR)
			} else {
				score -= 0.25
				reasons = append(reasons, "does not natively support aspect ratio "+requestedAR)
			}
		}

		// Generation mode filter
		if r.GenerationMode != "" && r.GenerationMode != "any" {
			if r.GenerationMode == "create" {
				if c.Type == "generation" {
					score += 0.15
					reasons = append(reasons, "matches generation mode: create")
				} else {
					score -= 0.20
					reasons = append(reasons, "stock candidate deprioritized for generative create mode")
				}
			} else if r.GenerationMode == "stock" {
				if c.Type == "stock" {
					score += 0.20
					reasons = append(reasons, "matches stock sourcing mode")
				} else {
					score -= 0.25
					reasons = append(reasons, "generative candidate deprioritized for stock mode")
				}
			} else if r.GenerationMode == "edit" {
				if c.SupportsEditing {
					score += 0.25
					reasons = append(reasons, "supports image-to-image editing")
				} else {
					score -= 0.30
					reasons = append(reasons, "does not support image editing")
				}
			}
		}

		// Text rendering constraint
		if r.RequireTextRendering || strings.Contains(promptLower, "text") || strings.Contains(promptLower, "label") || strings.Contains(promptLower, "infographic") {
			if c.SupportsTextRendering {
				score += 0.30
				reasons = append(reasons, "superior typographic accuracy and text rendering")
			} else if c.Type == "generation" {
				score -= 0.15
				reasons = append(reasons, "text rendering is less reliable")
			}
		}

		// Style matching
		if style != "" {
			if strings.Contains(style, "photo") || strings.Contains(style, "real") {
				if c.Name == "flux_image" {
					score += 0.15
					reasons = append(reasons, "well-suited for photorealistic aesthetics")
				}
			} else if strings.Contains(style, "illustrat") || strings.Contains(style, "anime") || strings.Contains(style, "diagram") {
				if c.Name == "flux_image" || c.Name == "openai_image" {
					score += 0.15
					reasons = append(reasons, "well-suited for stylized/illustrative aesthetics")
				}
			}
		}

		// Budget tier & max cost
		if r.BudgetTier == "free" {
			if c.EstimatedCost == 0 {
				score += 0.25
				reasons = append(reasons, "zero cost conforms to free budget tier")
			} else {
				score -= 0.40
				reasons = append(reasons, "paid tool exceeds free budget tier")
			}
		}
		if r.MaxCost != nil {
			if c.EstimatedCost > *r.MaxCost {
				score -= 0.50
				reasons = append(reasons, fmt.Sprintf("estimated cost $%.3f exceeds max_cost $%.3f", c.EstimatedCost, *r.MaxCost))
			}
		}

		c.SuitabilityScore = math.Round(math.Max(0.0, math.Min(1.0, score))*100) / 100
		c.RankingReasons = reasons
		evaluated = append(evaluated, c)
	}

	sort.SliceStable(evaluated, func(i, j int) bool {
		return evaluated[i].SuitabilityScore > evaluated[j].SuitabilityScore
	})

	topRec := ""
	rationale := "No candidates match the specified criteria."
	if len(evaluated) > 0 {
		top := evaluated[0]
		topRec = top.Name
		rationale = fmt.Sprintf("Recommended %s (%s, cost: %s, score: %.2f) because: %s.",
			top.Name, top.Type, top.CostDescription, top.SuitabilityScore, strings.Join(top.RankingReasons, "; "))
	}

	return map[string]any{
		"selected_recommendation": topRec,
		"rationale":               rationale,
		"candidates":              evaluated,
		"total_candidates":        len(evaluated),
		"configured_candidates":   numConfigured,
		"requested_aspect_ratio":  r.AspectRatio,
		"requested_style":         r.Style,
	}, nil, nil
}

func doVideoSelector(op string, data []byte) (any, []string, error) {
	var r videoSelectorRequest
	if err := decode(data, &r); err != nil {
		return nil, nil, err
	}

	if op == "estimate" {
		res := estimateResult([]string{"video_selector_rank"})
		res["estimated_cost"] = 0.0
		res["network"] = false
		return res, nil, nil
	}

	rawCandidates := []videoCandidateFact{
		{
			Name:                  "kling_video",
			Provider:              "kling",
			Type:                  "generation",
			Configured:            os.Getenv("FAL_KEY") != "" || os.Getenv("KLING_API_KEY") != "",
			EstimatedCost:         0.10,
			CostDescription:       "$0.10 (5s std) - $0.20 (10s std) / $0.40 (pro)",
			MinDuration:           5.0,
			MaxDuration:           10.0,
			SupportedAspectRatios: []string{"16:9", "9:16", "1:1"},
			SupportsNativeAudio:   false,
			Strengths:             []string{"strong motion coherence", "cinematic camera dynamics", "stylized and anime realism", "pro mode fidelity"},
			Limitations:           []string{"no native synchronized audio", "discrete 5s/10s duration increments"},
		},
		{
			Name:                  "sora_video",
			Provider:              "sora",
			Type:                  "generation",
			Configured:            os.Getenv("OPENAI_API_KEY") != "",
			EstimatedCost:         0.20,
			CostDescription:       "$0.20 (5s 720p) - $0.40 (10s 720p) / $0.60 (1080p)",
			MinDuration:           4.0,
			MaxDuration:           12.0,
			SupportedAspectRatios: []string{"16:9", "9:16", "1:1"},
			SupportsNativeAudio:   false,
			Strengths:             []string{"physical realism and fluid dynamics", "complex multi-subject interactions", "high prompt adherence"},
			Limitations:           []string{"higher cost per generation", "no native audio sync"},
		},
		{
			Name:                  "pexels_video",
			Provider:              "pexels",
			Type:                  "stock",
			Configured:            os.Getenv("PEXELS_API_KEY") != "",
			EstimatedCost:         0.00,
			CostDescription:       "Free ($0.00)",
			MinDuration:           1.0,
			MaxDuration:           120.0,
			SupportedAspectRatios: []string{"16:9", "9:16", "1:1"},
			SupportsNativeAudio:   false,
			Strengths:             []string{"real-world 4K/HD video footage", "instant search and download", "zero cost"},
			Limitations:           []string{"stock catalog dependent", "cannot generate bespoke scenarios"},
		},
		{
			Name:                  "pixabay_video",
			Provider:              "pixabay",
			Type:                  "stock",
			Configured:            os.Getenv("PIXABAY_API_KEY") != "",
			EstimatedCost:         0.00,
			CostDescription:       "Free ($0.00)",
			MinDuration:           1.0,
			MaxDuration:           120.0,
			SupportedAspectRatios: []string{"16:9", "9:16", "1:1"},
			SupportsNativeAudio:   false,
			Strengths:             []string{"broad catalog of video clips and motion backgrounds", "zero cost"},
			Limitations:           []string{"variable resolution", "stock catalog dependent"},
		},
		{
			Name:                  "wikimedia",
			Provider:              "wikimedia",
			Type:                  "stock",
			Configured:            true,
			EstimatedCost:         0.00,
			CostDescription:       "Free ($0.00 / public domain)",
			MinDuration:           1.0,
			MaxDuration:           600.0,
			SupportedAspectRatios: []string{"16:9", "4:3"},
			SupportsNativeAudio:   true,
			Strengths:             []string{"historical and educational archival clips", "public domain", "no API key required"},
			Limitations:           []string{"variable quality and vintage codecs"},
		},
	}

	allowedSet := map[string]bool{}
	for _, a := range r.AllowedProviders {
		allowedSet[strings.ToLower(strings.TrimSpace(a))] = true
	}

	pref := strings.ToLower(strings.TrimSpace(r.PreferredProvider))
	requestedAR := strings.TrimSpace(r.AspectRatio)
	intent := strings.ToLower(strings.TrimSpace(r.Intent))
	if intent == "" {
		intent = strings.ToLower(strings.TrimSpace(r.Style))
	}

	var evaluated []videoCandidateFact
	numConfigured := 0

	for _, c := range rawCandidates {
		if len(allowedSet) > 0 {
			if !allowedSet[strings.ToLower(c.Name)] && !allowedSet[strings.ToLower(c.Provider)] {
				continue
			}
		}

		if c.Configured {
			numConfigured++
		}

		score := 0.50
		reasons := []string{}

		// Configured signal
		if c.Configured {
			score += 0.20
			reasons = append(reasons, "credentials configured and ready")
		} else {
			score -= 0.25
			reasons = append(reasons, "credentials unconfigured; requires API key")
		}

		// Preferred provider
		if pref != "" && pref != "auto" {
			if strings.EqualFold(pref, c.Name) || strings.EqualFold(pref, c.Provider) {
				score += 0.45
				reasons = append(reasons, "matches preferred_provider: "+pref)
			} else {
				score -= 0.20
			}
		}

		// Duration check
		if r.Duration != nil && *r.Duration > 0 {
			dur := *r.Duration
			if dur >= c.MinDuration && dur <= c.MaxDuration {
				score += 0.15
				reasons = append(reasons, fmt.Sprintf("duration %.1fs within supported range [%.0fs-%.0fs]", dur, c.MinDuration, c.MaxDuration))
			} else {
				score -= 0.35
				reasons = append(reasons, fmt.Sprintf("requested duration %.1fs outside supported range [%.0fs-%.0fs]", dur, c.MinDuration, c.MaxDuration))
			}
		}

		// Aspect ratio support
		if requestedAR != "" {
			supported := false
			for _, ar := range c.SupportedAspectRatios {
				if ar == requestedAR {
					supported = true
					break
				}
			}
			if supported {
				score += 0.10
				reasons = append(reasons, "natively supports aspect ratio "+requestedAR)
			} else {
				score -= 0.30
				reasons = append(reasons, "does not natively support aspect ratio "+requestedAR)
			}
		}

		// Audio requirement
		if r.RequireAudio {
			if c.SupportsNativeAudio {
				score += 0.30
				reasons = append(reasons, "provides native synchronized audio generation")
			} else {
				score -= 0.25
				reasons = append(reasons, "lacks native synchronized audio generation")
			}
		}

		// Intent / style matching
		if intent != "" {
			if strings.Contains(intent, "cinematic") || strings.Contains(intent, "trailer") {
				if c.Name == "kling_video" || c.Name == "sora_video" {
					score += 0.20
					reasons = append(reasons, "high cinematic motion coherence and aesthetic rating")
				}
			} else if strings.Contains(intent, "stock") || strings.Contains(intent, "real") || strings.Contains(intent, "b-roll") {
				if c.Type == "stock" {
					score += 0.25
					reasons = append(reasons, "well-suited for authentic stock footage")
				}
			} else if strings.Contains(intent, "landscape") {
				if c.Name == "pexels_video" {
					score += 0.20
					reasons = append(reasons, "strong landscape and environmental rendering")
				}
			}
		}

		// Source type filter
		if r.SourceType != "" && r.SourceType != "any" {
			if r.SourceType == "stock" {
				if c.Type == "stock" {
					score += 0.20
					reasons = append(reasons, "matches stock sourcing request")
				} else {
					score -= 0.30
					reasons = append(reasons, "generative candidate deprioritized for stock mode")
				}
			} else if r.SourceType == "text_to_video" || r.SourceType == "image_to_video" {
				if c.Type == "generation" {
					score += 0.15
					reasons = append(reasons, "matches AI generation workflow")
				} else {
					score -= 0.30
					reasons = append(reasons, "stock candidate deprioritized for AI generation workflow")
				}
			}
		}

		// Budget tier & max cost
		if r.BudgetTier == "free" {
			if c.EstimatedCost == 0 {
				score += 0.25
				reasons = append(reasons, "zero cost conforms to free budget tier")
			} else {
				score -= 0.40
				reasons = append(reasons, "paid video tool exceeds free budget tier")
			}
		}
		if r.MaxCost != nil {
			if c.EstimatedCost > *r.MaxCost {
				score -= 0.50
				reasons = append(reasons, fmt.Sprintf("estimated cost $%.3f exceeds max_cost $%.3f", c.EstimatedCost, *r.MaxCost))
			}
		}

		c.SuitabilityScore = math.Round(math.Max(0.0, math.Min(1.0, score))*100) / 100
		c.RankingReasons = reasons
		evaluated = append(evaluated, c)
	}

	sort.SliceStable(evaluated, func(i, j int) bool {
		return evaluated[i].SuitabilityScore > evaluated[j].SuitabilityScore
	})

	topRec := ""
	rationale := "No candidates match the specified criteria."
	if len(evaluated) > 0 {
		top := evaluated[0]
		topRec = top.Name
		rationale = fmt.Sprintf("Recommended %s (%s, cost: %s, score: %.2f) because: %s.",
			top.Name, top.Type, top.CostDescription, top.SuitabilityScore, strings.Join(top.RankingReasons, "; "))
	}

	var durVal any
	if r.Duration != nil {
		durVal = *r.Duration
	}

	return map[string]any{
		"selected_recommendation": topRec,
		"rationale":               rationale,
		"candidates":              evaluated,
		"total_candidates":        len(evaluated),
		"configured_candidates":   numConfigured,
		"requested_duration":      durVal,
		"requested_aspect_ratio":  r.AspectRatio,
	}, nil, nil
}
