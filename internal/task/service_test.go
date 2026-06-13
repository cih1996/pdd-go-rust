package task

import (
	"strings"
	"testing"

	"unified-server/internal/device"
	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
)

func TestRewriteTemplateURL_ReplacesNestedFromURL(t *testing.T) {
	raw := "https://mobile.yangkeduo.com/login.html?from=https%3A%2F%2Fmobile.yangkeduo.com%2Forder_checkout.html%3Fgoods_id%3D654097226224%26sku_id%3D1888467061279&refer_page_name=order_checkout"
	got := rewriteTemplateURL(raw, "111222333444", "999888777666")
	if !strings.Contains(got, "goods_id%3D111222333444") {
		t.Fatalf("expected rewritten goods_id in nested url, got %s", got)
	}
	if !strings.Contains(got, "sku_id%3D999888777666") {
		t.Fatalf("expected rewritten sku_id in nested url, got %s", got)
	}
}

func TestRewriteTemplateURL_ReplacesEncodedGoodsListJSON(t *testing.T) {
	raw := "https://mobile.yangkeduo.com/login.html?from=https%3A%2F%2Fmobile.yangkeduo.com%2Ftransac_batch_checkout.html%3Fgoods_list%3D%255B%257B%2522goods_id%2522%253A784768292368%252C%2522sku_id%2522%253A1763658263238%252C%2522goods_number%2522%253A1%257D%255D%26back_page%3Dtransac_batch_checkout&refer_page_name=transac_batch_checkout"
	got := rewriteTemplateURL(raw, "222333444555", "666777888999")
	if !strings.Contains(got, "goods_id%2522%253A222333444555") {
		t.Fatalf("expected rewritten goods_id in goods_list, got %s", got)
	}
	if !strings.Contains(got, "sku_id%2522%253A666777888999") {
		t.Fatalf("expected rewritten sku_id in goods_list, got %s", got)
	}
}

func TestBuildTaskURL_UsesDefaultCheckoutURL(t *testing.T) {
	got := buildTaskURL(clientSystemConfig(), clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	want := "https://mobile.yangkeduo.com/order_checkout.html?goods_id=123456&sku_id=6812323"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestSelectTaskURL_UsesTemplateAndReturnsTemplateID(t *testing.T) {
	selection := selectTaskURL(rt.SystemConfig{
		UseURLTemplates: true,
		URLTemplates: []rt.URLTemplateRecord{
			{
				ID:       "urltpl_demo",
				Template: "https://mobile.yangkeduo.com/order_checkout.html?goods_id=100&sku_id=200",
			},
		},
	}, clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	if selection.TemplateID != "urltpl_demo" {
		t.Fatalf("expected template id urltpl_demo, got %s", selection.TemplateID)
	}
	if !strings.Contains(selection.URL, "goods_id=123456") || !strings.Contains(selection.URL, "sku_id=6812323") {
		t.Fatalf("expected rewritten url, got %s", selection.URL)
	}
}

func TestSelectTaskURLForDevice_StartsFromFirstTemplate(t *testing.T) {
	service := &Service{
		urlTemplateState: map[string]deviceURLTemplateState{
			"dev-1": newDeviceURLTemplateState(),
		},
	}
	selection := service.selectTaskURLForDevice("dev-1", rt.SystemConfig{
		UseURLTemplates: true,
		URLTemplates: []rt.URLTemplateRecord{
			{ID: "tpl-1", Template: "https://a.example.com/?goods_id=1&sku_id=1"},
			{ID: "tpl-2", Template: "https://b.example.com/?goods_id=2&sku_id=2"},
		},
	}, clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	if selection.TemplateID != "tpl-1" || selection.TemplateIndex != 1 || selection.TemplateTotal != 2 {
		t.Fatalf("unexpected selection: %+v", selection)
	}
}

func TestAdvanceDeviceURLTemplateAfterRisk_SwitchesAndStopsAfterExhausted(t *testing.T) {
	service := &Service{
		urlTemplateState: map[string]deviceURLTemplateState{
			"dev-1": newDeviceURLTemplateState(),
		},
	}
	cfg := rt.SystemConfig{
		UseURLTemplates: true,
		URLTemplates: []rt.URLTemplateRecord{
			{ID: "tpl-1", Template: "https://a.example.com/?goods_id=1&sku_id=1"},
			{ID: "tpl-2", Template: "https://b.example.com/?goods_id=2&sku_id=2"},
		},
	}
	first := service.selectTaskURLForDevice("dev-1", cfg, clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	if advanced, exhausted := service.advanceDeviceURLTemplateAfterRisk("dev-1", cfg, first.TemplateID); !advanced || exhausted {
		t.Fatalf("expected advance to next template, got advanced=%v exhausted=%v", advanced, exhausted)
	}
	second := service.selectTaskURLForDevice("dev-1", cfg, clientTaskItem{GoodsID: "123456", SKUID: "6812323"})
	if second.TemplateID != "tpl-2" || second.TemplateIndex != 2 {
		t.Fatalf("expected second template after risk, got %+v", second)
	}
	if advanced, exhausted := service.advanceDeviceURLTemplateAfterRisk("dev-1", cfg, second.TemplateID); !advanced || !exhausted {
		t.Fatalf("expected exhaustion after last template risk, got advanced=%v exhausted=%v", advanced, exhausted)
	}
}

func TestActiveURLTemplatesForDevice_UsesSelectedSubset(t *testing.T) {
	devices := device.NewService(nil, "")
	devices.UpdateURLTemplateSelection("dev-1", []string{"tpl-2"})
	service := &Service{
		devices: devices,
	}
	templates := service.activeURLTemplatesForDevice("dev-1", rt.SystemConfig{
		UseURLTemplates: true,
		URLTemplates: []rt.URLTemplateRecord{
			{ID: "tpl-1", Template: "https://a.example.com/?goods_id=1&sku_id=1"},
			{ID: "tpl-2", Template: "https://b.example.com/?goods_id=2&sku_id=2"},
		},
	})
	if len(templates) != 1 || templates[0].ID != "tpl-2" {
		t.Fatalf("expected only tpl-2, got %+v", templates)
	}
}

func TestDetailMessageWithTemplate_AppendsTemplateContext(t *testing.T) {
	got := detailMessageWithTemplate("命中失败释放", matchedTemplateMeta{
		TemplateID:    "tpl-fail-1",
		TemplateLabel: "释放按钮",
	})
	if !strings.Contains(got, "释放按钮") || !strings.Contains(got, "tpl-fail-1") {
		t.Fatalf("expected message to include template context, got %s", got)
	}
}

func TestCurrentTaskTemplateMessage_IncludesTemplateContext(t *testing.T) {
	got := currentTaskTemplateMessage("命中模板，执行点击 (123,456)", template.Record{
		Label:             "店铺优惠按钮",
		TemplateType:      "click_image",
		RecognitionEngine: "ocr",
	})
	if !strings.Contains(got, "店铺优惠按钮") || !strings.Contains(got, "点击图") || !strings.Contains(got, "OCR") {
		t.Fatalf("expected current task message to include template context, got %s", got)
	}
}

func TestFilterStageTemplates_SkipsRequiresClickFailReleaseBeforeClick(t *testing.T) {
	service := &Service{
		tpl: &template.Store{},
	}
	service.tpl = &template.Store{}
	templates := []template.Record{
		{ID: "fail-a", TemplateType: "fail_release", Enabled: true, RecognitionEngine: "opencv"},
		{ID: "fail-b", TemplateType: "fail_release", Enabled: true, RecognitionEngine: "opencv", RequiresClick: true},
	}

	store := template.NewStore(nil)
	store.ImportRecords(templates, true)
	service.tpl = store

	beforeClick := service.filterStageTemplates("fail_release", false, map[string]struct{}{})
	if len(beforeClick) != 1 || beforeClick[0].ID != "fail-a" {
		t.Fatalf("expected only non-requires-click fail_release template before click, got %+v", beforeClick)
	}

	afterClick := service.filterStageTemplates("fail_release", true, map[string]struct{}{})
	if len(afterClick) != 2 {
		t.Fatalf("expected both fail_release templates after click, got %+v", afterClick)
	}
}

func TestFilterStageTemplates_SkipsMatchOnceTemplateAfterItWasMatched(t *testing.T) {
	store := template.NewStore(nil)
	store.ImportRecords([]template.Record{
		{ID: "click-a", TemplateType: "click_image", Enabled: true, RecognitionEngine: "opencv"},
		{ID: "click-b", TemplateType: "click_image", Enabled: true, RecognitionEngine: "opencv", MatchOncePerTask: true},
	}, true)
	service := &Service{tpl: store}

	filtered := service.filterStageTemplates("click_image", false, map[string]struct{}{"click-b": {}})
	if len(filtered) != 1 || filtered[0].ID != "click-a" {
		t.Fatalf("expected match-once template to be skipped after match, got %+v", filtered)
	}
}

func TestCloneBytes_ReturnsIndependentCopy(t *testing.T) {
	source := []byte("before-click")
	cloned := cloneBytes(source)
	source[0] = 'X'
	if string(cloned) != "before-click" {
		t.Fatalf("expected cloned bytes to remain unchanged, got %q", string(cloned))
	}
}

func TestRememberFirstCapture_KeepsFirstCapture(t *testing.T) {
	first := rememberFirstCapture(nil, []byte("first-click"))
	second := rememberFirstCapture(first, []byte("second-click"))
	if string(second) != "first-click" {
		t.Fatalf("expected first click capture to be preserved, got %q", string(second))
	}
}

func clientSystemConfig() rt.SystemConfig {
	return rt.SystemConfig{}
}
