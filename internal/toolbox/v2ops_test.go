package toolbox

import (
	"reflect"
	"testing"
)

func TestWikimediaDeclaresImageAndVideoOutputVariants(t *testing.T) {
	var produces []string
	for _, operation := range V2Operations() {
		if operation.ID == "wikimedia" {
			produces = operation.Produces
			break
		}
	}
	want := []string{"image", "render_video"}
	if !reflect.DeepEqual(produces, want) {
		t.Fatalf("wikimedia produces = %v, want %v", produces, want)
	}

	for _, kind := range []string{"image", "video"} {
		if err := ValidateRequestShape(
			"wikimedia",
			[]byte(`{"query":"ocean","kind":"`+kind+`","output_path":"stock.bin"}`),
		); err != nil {
			t.Errorf("wikimedia %s request shape: %v", kind, err)
		}
	}
}
