package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"unified-server/internal/account"
	"unified-server/internal/config"
	"unified-server/internal/device"
	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
	"unified-server/internal/upstream"
	"unified-server/internal/ws"
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

func TestSubmitExternalTask_RecordsURLTemplateSuccess(t *testing.T) {
	service, cleanup := newExternalSubmitTestService(t)
	defer cleanup()

	taskItem := reserveTestExternalClaim(t, service)
	_, err := service.SubmitExternalTask(context.Background(), ExternalSubmitRequest{
		TaskID:     taskItem.TaskID,
		WorkerID:   "worker-1",
		TemplateID: "tpl-1",
		Result:     "success",
		TaskItems: []ExternalSubmitTaskItem{
			{
				GoodsID:     "123456",
				SKUID:       "6812323",
				Recognition: "success_image",
				Message:     "命中成功图",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected submit success, got %v", err)
	}

	cfg := service.runtime.SystemConfig()
	if len(cfg.URLTemplates) != 1 {
		t.Fatalf("expected 1 url template, got %d", len(cfg.URLTemplates))
	}
	if cfg.URLTemplates[0].TriggerCount != 1 || cfg.URLTemplates[0].SuccessCount != 1 || cfg.URLTemplates[0].RiskCount != 0 {
		t.Fatalf("unexpected url template stats: %+v", cfg.URLTemplates[0])
	}

	_, _, details, _, _, _ := service.runtime.Snapshot()
	if len(details) != 1 {
		t.Fatalf("expected 1 detail record, got %d", len(details))
	}
	if details[0].TemplateID != "tpl-1" || details[0].TemplateLabel != "模板一" {
		t.Fatalf("expected detail to include template info, got %+v", details[0])
	}
	if !strings.Contains(details[0].URL, "goods_id=123456") || !strings.Contains(details[0].URL, "sku_id=6812323") {
		t.Fatalf("expected detail url to use rewritten template url, got %s", details[0].URL)
	}
}

func TestSubmitExternalTask_RecordsURLTemplateRisk(t *testing.T) {
	service, cleanup := newExternalSubmitTestService(t)
	defer cleanup()

	taskItem := reserveTestExternalClaim(t, service)
	_, err := service.SubmitExternalTask(context.Background(), ExternalSubmitRequest{
		TaskID:     taskItem.TaskID,
		WorkerID:   "worker-1",
		TemplateID: "tpl-1",
		Result:     "failure",
		TaskItems: []ExternalSubmitTaskItem{
			{
				GoodsID:     "123456",
				SKUID:       "6812323",
				Recognition: "account_risk",
				Message:     "账号风控",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected submit success, got %v", err)
	}

	cfg := service.runtime.SystemConfig()
	if len(cfg.URLTemplates) != 1 {
		t.Fatalf("expected 1 url template, got %d", len(cfg.URLTemplates))
	}
	if cfg.URLTemplates[0].TriggerCount != 1 || cfg.URLTemplates[0].SuccessCount != 0 || cfg.URLTemplates[0].RiskCount != 1 {
		t.Fatalf("unexpected url template stats: %+v", cfg.URLTemplates[0])
	}
}

func TestSubmitExternalTask_SubmitFailureMarksDetailFailureAndDoesNotCountTemplateSuccess(t *testing.T) {
	service, cleanup := newExternalSubmitTestServiceWithHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/submit-task" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"上传老钱截图失败: 无当前题目作业权限，请重新领题","success":false}`))
	}))
	defer cleanup()

	taskItem := reserveTestExternalClaim(t, service)
	_, err := service.SubmitExternalTask(context.Background(), ExternalSubmitRequest{
		TaskID:     taskItem.TaskID,
		WorkerID:   "worker-1",
		TemplateID: "tpl-1",
		Result:     "success",
		TaskItems: []ExternalSubmitTaskItem{
			{
				GoodsID:     "123456",
				SKUID:       "6812323",
				Recognition: "success_image",
				Message:     "命中成功图",
			},
		},
	})
	if err == nil {
		t.Fatal("expected submit failure")
	}

	cfg := service.runtime.SystemConfig()
	if len(cfg.URLTemplates) != 1 {
		t.Fatalf("expected 1 url template, got %d", len(cfg.URLTemplates))
	}
	if cfg.URLTemplates[0].TriggerCount != 1 || cfg.URLTemplates[0].SuccessCount != 0 || cfg.URLTemplates[0].RiskCount != 0 {
		t.Fatalf("unexpected url template stats after submit failure: %+v", cfg.URLTemplates[0])
	}

	_, _, details, _, _, _ := service.runtime.Snapshot()
	if len(details) != 1 {
		t.Fatalf("expected 1 detail record, got %d", len(details))
	}
	if details[0].Status != "failure" {
		t.Fatalf("expected detail status failure after adapter submit error, got %+v", details[0])
	}
	if details[0].SubmitStatusCode != http.StatusBadRequest {
		t.Fatalf("expected submit status code 400, got %+v", details[0])
	}
	if !strings.Contains(details[0].SubmitError, "无当前题目作业权限，请重新领题") {
		t.Fatalf("expected raw submit error to be preserved, got %+v", details[0])
	}
}

func TestReleaseExpiredGroupedTasks_ReleasesTimedOutGroup(t *testing.T) {
	var submitRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/submit-task" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode submit payload: %v", err)
		}
		submitRequests = append(submitRequests, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	hub := ws.NewHub()
	service := NewService(
		config.Config{AdapterBaseURL: server.URL},
		hub,
		template.NewStore(nil),
		nil,
		device.NewService(hub, ""),
		upstream.NewStore(nil),
		account.NewStore(nil),
		rt.NewStore(nil),
	)
	service.client = server.Client()

	taskItem := clientTask{
		TaskID:          "task-timeout-1",
		UpstreamTaskRef: "ref-timeout-1",
		SourceCode:      "source-timeout",
		SourceName:      "超时上游",
		TaskItems: []clientTaskItem{
			{GoodsID: "g1", SKUID: "s1", StepIndex: 0},
			{GoodsID: "g2", SKUID: "s2", StepIndex: 1},
		},
	}
	candidate := sourceCandidate{Upstream: upstream.Record{Code: "source-timeout"}}
	service.enqueuePendingGroup(taskItem, candidate, buildBusinessKey(taskItem))

	parentKey := buildBusinessKey(taskItem)
	service.mu.Lock()
	service.groups[parentKey].PrefetchedAt = time.Now().Add(-groupTaskTimeout - time.Second).UTC().Format(time.RFC3339)
	service.mu.Unlock()

	service.releaseExpiredGroupedTasks(context.Background())

	if len(submitRequests) != 1 {
		t.Fatalf("expected 1 cancelled submit request, got %d", len(submitRequests))
	}
	if submitRequests[0]["type"] != "cancelled" {
		t.Fatalf("expected cancelled submit type, got %+v", submitRequests[0])
	}
	if service.pendingCount() != 0 {
		t.Fatalf("expected pending queue to be empty after release, got %d", service.pendingCount())
	}
	service.mu.Lock()
	_, groupExists := service.groups[parentKey]
	service.mu.Unlock()
	if groupExists {
		t.Fatal("expected timed out grouped task to be removed")
	}
	_, _, _, pending, _, _ := service.runtime.Snapshot()
	if len(pending) != 0 {
		t.Fatalf("expected runtime pending tasks to be empty, got %d", len(pending))
	}
}

func TestReleaseExpiredGroupedTasks_DoesNotReleaseStartedGroup(t *testing.T) {
	var submitRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/submit-task" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode submit payload: %v", err)
		}
		submitRequests = append(submitRequests, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	hub := ws.NewHub()
	service := NewService(
		config.Config{AdapterBaseURL: server.URL},
		hub,
		template.NewStore(nil),
		nil,
		device.NewService(hub, ""),
		upstream.NewStore(nil),
		account.NewStore(nil),
		rt.NewStore(nil),
	)
	service.client = server.Client()

	taskItem := clientTask{
		TaskID:          "task-started-1",
		UpstreamTaskRef: "ref-started-1",
		SourceCode:      "source-started",
		SourceName:      "开始执行上游",
		TaskItems: []clientTaskItem{
			{GoodsID: "g1", SKUID: "s1", StepIndex: 0},
			{GoodsID: "g2", SKUID: "s2", StepIndex: 1},
		},
	}
	candidate := sourceCandidate{Upstream: upstream.Record{Code: "source-started"}}
	service.enqueuePendingGroup(taskItem, candidate, buildBusinessKey(taskItem))

	parentKey := buildBusinessKey(taskItem)
	service.mu.Lock()
	service.groups[parentKey].PrefetchedAt = time.Now().Add(-groupTaskTimeout - time.Second).UTC().Format(time.RFC3339)
	service.mu.Unlock()

	if _, _, ok := service.takePendingTask(); !ok {
		t.Fatal("expected a child task to move into active state")
	}

	service.releaseExpiredGroupedTasks(context.Background())

	if len(submitRequests) != 0 {
		t.Fatalf("expected no cancel submit for started group, got %d", len(submitRequests))
	}
	service.mu.Lock()
	group := service.groups[parentKey]
	activeCount := len(group.Active)
	pendingCount := len(group.Pending)
	service.mu.Unlock()
	if group == nil {
		t.Fatal("expected started grouped task to remain in queue state")
	}
	if activeCount != 1 || pendingCount != 1 {
		t.Fatalf("expected started group to remain intact, got active=%d pending=%d", activeCount, pendingCount)
	}
}

func newExternalSubmitTestService(t *testing.T) (*Service, func()) {
	return newExternalSubmitTestServiceWithHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/client/submit-task" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
}

func newExternalSubmitTestServiceWithHandler(t *testing.T, handler http.Handler) (*Service, func()) {
	t.Helper()

	server := httptest.NewServer(handler)

	hub := ws.NewHub()
	runtimeStore := rt.NewStore(nil)
	runtimeStore.UpdateSystemConfig(rt.SystemConfig{
		UseURLTemplates: true,
		URLTemplates: []rt.URLTemplateRecord{
			{
				ID:       "tpl-1",
				Name:     "模板一",
				Template: "https://mobile.yangkeduo.com/order_checkout.html?goods_id=1&sku_id=2",
			},
		},
	})
	service := NewService(
		config.Config{AdapterBaseURL: server.URL},
		hub,
		template.NewStore(nil),
		nil,
		device.NewService(hub, ""),
		upstream.NewStore(nil),
		account.NewStore(nil),
		runtimeStore,
	)
	service.client = server.Client()

	return service, server.Close
}

func reserveTestExternalClaim(t *testing.T, service *Service) clientTask {
	t.Helper()

	taskItem := clientTask{
		TaskID:          "task-1",
		UpstreamTaskRef: "ref-1",
		SourceCode:      "source-1",
		SourceName:      "测试上游",
		AccountID:       "acct-1",
		AccountName:     "测试账号",
		TaskItems: []clientTaskItem{
			{
				GoodsID:   "123456",
				SKUID:     "6812323",
				SourceURL: "https://origin.example.com/item",
				StepIndex: 0,
			},
		},
	}
	ok := service.reserveExternalClaim(taskItem, sourceCandidate{
		Upstream: upstream.Record{Code: "source-1"},
		Account:  &account.Record{ID: "acct-1", Name: "测试账号"},
		Key:      "acct:acct-1",
	}, "worker-1", "Worker 1")
	if !ok {
		t.Fatal("expected external claim to be reserved")
	}
	return taskItem
}

func clientSystemConfig() rt.SystemConfig {
	return rt.SystemConfig{}
}
