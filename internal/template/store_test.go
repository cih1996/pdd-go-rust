package template

import "testing"

func TestRepairLoadedItems_DisablesBrokenOpenCVTemplate(t *testing.T) {
	store := &Store{imageDir: t.TempDir()}

	fixed := store.repairLoadedItems([]Record{
		{
			ID:                "tpl-opencv-demo",
			Label:             "OpenCV Demo",
			RecognitionEngine: "opencv",
			Enabled:           true,
			Method:            "ccoeff_normed",
			Threshold:         0.8,
		},
	})

	if len(fixed) != 1 {
		t.Fatalf("expected 1 template, got %d", len(fixed))
	}
	if fixed[0].Enabled {
		t.Fatalf("expected broken opencv template to be auto-disabled")
	}
}
