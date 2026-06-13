package httpapi

import (
	"testing"

	"unified-server/internal/template"
)

func TestFilterDebugTemplates_SkipsMatchOnceAfterMatch(t *testing.T) {
	templates := []template.Record{
		{ID: "click-a", TemplateType: "click_image", MatchOncePerTask: true},
		{ID: "click-b", TemplateType: "click_image"},
	}

	filtered := filterDebugTemplates(templates, "click_image", false, map[string]struct{}{"click-a": {}})
	if len(filtered) != 1 || filtered[0].ID != "click-b" {
		t.Fatalf("expected matched once template to be skipped, got %+v", filtered)
	}
}

func TestFilterDebugTemplates_SkipsRequiresClickFailReleaseBeforeClick(t *testing.T) {
	templates := []template.Record{
		{ID: "fail-a", TemplateType: "fail_release", RequiresClick: true},
		{ID: "fail-b", TemplateType: "fail_release"},
	}

	filtered := filterDebugTemplates(templates, "fail_release", false, map[string]struct{}{})
	if len(filtered) != 1 || filtered[0].ID != "fail-b" {
		t.Fatalf("expected requires-click fail_release template to be skipped before click, got %+v", filtered)
	}
}
