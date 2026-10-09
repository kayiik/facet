package toolbox

import (
	"context"
	"encoding/json"
	"testing"
)

// A selector recommends tools by name. Every name it can recommend must be a
// tool this facet has: pexels_image, pixabay_image, seedance_video and
// veo_video were recommended once, and none of them exists.
func TestSelectorsRecommendOnlyRegisteredTools(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range Names() {
		registered[name] = true
	}
	for _, selector := range []string{"image_selector", "video_selector"} {
		env := RunContext(context.Background(), selector, []byte(`{}`))
		if !env.OK {
			t.Fatalf("%s failed: %+v", selector, env.Error)
		}
		raw, err := json.Marshal(env.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Candidates []struct {
				Name string `json:"name"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Candidates) == 0 {
			t.Fatalf("%s returned no candidates: %s", selector, raw)
		}
		for _, c := range result.Candidates {
			if !registered[c.Name] {
				t.Errorf("%s recommends %q, which is not a Facet tool", selector, c.Name)
			}
		}
	}
}
