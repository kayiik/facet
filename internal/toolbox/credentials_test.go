package toolbox

import "testing"

// flux_image and kling_video accept FLUX_API_KEY and KLING_API_KEY as well as
// FAL_KEY. Their dependency report must say so, or a person with a working
// key is told the tool is unconfigured.
func TestAlternativeCredentialNamesConfigureTheTool(t *testing.T) {
	for tool, alternative := range map[string]string{"flux_image": "FLUX_API_KEY", "kling_video": "KLING_API_KEY"} {
		for _, key := range []string{"FAL_KEY", "FLUX_API_KEY", "KLING_API_KEY"} {
			t.Setenv(key, "")
		}
		entry := summary(tool)
		if configured, _ := entry["configured"].(bool); configured {
			t.Fatalf("%s is configured with no key at all", tool)
		}
		t.Setenv(alternative, "test-key")
		entry = summary(tool)
		if configured, _ := entry["configured"].(bool); !configured {
			t.Errorf("%s is not configured with %s set: %v", tool, alternative, entry["dependencies"])
		}
		deps, _ := entry["dependencies"].([]any)
		found := false
		for _, d := range deps {
			if m, ok := d.(map[string]any); ok && m["name"] == alternative {
				found = true
				if names, _ := m["alternatives"].([]string); len(names) != 2 || names[0] != "FAL_KEY" {
					t.Errorf("%s alternatives = %v", tool, m["alternatives"])
				}
			}
		}
		if !found {
			t.Errorf("%s does not report the key it would use: %v", tool, deps)
		}
	}
}
