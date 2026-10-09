package toolbox

import (
	"reflect"
	"testing"
)

func TestCaptionBurnIsHonestlyCanonicalFFmpegOperation(t *testing.T) {
	names := Names()
	if contains(names, "remotion_caption_burn") {
		t.Fatal("remotion_caption_burn is still advertised as canonical")
	}
	if !contains(names, "ffmpeg_caption_burn") {
		t.Fatal("ffmpeg_caption_burn is missing from canonical discovery")
	}
	description := summary("ffmpeg_caption_burn")
	deps, _ := description["dependencies"].([]any)
	var got []string
	for _, dependency := range deps {
		if item, ok := dependency.(map[string]any); ok {
			got = append(got, item["name"].(string))
		}
	}
	if !reflect.DeepEqual(got, []string{"ffmpeg"}) {
		t.Fatalf("ffmpeg_caption_burn dependencies = %v, want [ffmpeg]", got)
	}
	if provider := executionFor("ffmpeg_caption_burn").Provider; provider != "ffmpeg" {
		t.Fatalf("provider = %q, want ffmpeg", provider)
	}
}

func TestCanonicalRequestShapeRejectsMissingRequiredParameters(t *testing.T) {
	if err := ValidateRequestShape("source_edit", []byte(`{"output":"out.mp4"}`)); err == nil {
		t.Fatal("source_edit shape accepted missing segments and target")
	}
	if err := ValidateRequestShape("output_review", []byte(`{"input":"future.mp4"}`)); err != nil {
		t.Fatalf("valid downstream review shape failed: %v", err)
	}
}
