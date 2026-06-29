package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"unified-server/internal/runtime"
	"unified-server/internal/task"
	"unified-server/internal/upstream"
)

// Submit count endpoints
func (d RouterDeps) handleSubmitCount(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"submit_count": d.Tasks.Runtime().SubmitCount()})
		return
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func (d RouterDeps) handleResetSubmitCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	d.Tasks.Runtime().ResetSubmitCount()
	writeJSON(w, http.StatusOK, map[string]any{"submit_count": 0})
}

func (d RouterDeps) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"service": "unified-server",
		"mode":    "runtime",
	})
}

func (d RouterDeps) handleSummary(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"adapter_base_url":  d.Config.AdapterBaseURL,
		"template_count":    d.Tpl.Count(),
		"upstream_count":    d.Upstream.Count(),
		"ocr_templates":     d.Tpl.CountByEngine("ocr"),
		"opencv_templates":  d.Tpl.CountByEngine("opencv"),
		"vision_mode":       d.Vision.Mode(),
		"vision_capability": d.Vision.Capability(),
		"connected_clients": d.Hub.ClientCount(),
		"devices":           d.Devices.List(),
		"runtime_plan":      d.Tasks.RuntimePlan(),
		"platform_accounts": len(d.Accounts.List()),
		"system_config":     d.Runtime.SystemConfig(),
	})
}

func (d RouterDeps) handleState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if _, err := d.Devices.Scan(ctx); err != nil {
		d.Runtime.AddEvent(runtime.EventRecord{
			Level:   "warning",
			Message: "状态刷新时 ADB 同步失败",
			Payload: map[string]any{"error": err.Error()},
		})
	}
	if err := d.syncAllUpstreamsToAdapter(ctx); err != nil {
		d.Runtime.AddEvent(runtime.EventRecord{
			Level:   "warning",
			Message: "状态刷新时适配器上游同步失败",
			Payload: map[string]any{"error": err.Error()},
		})
	}
	summary, events, _, pending, adapterLogs, systemConfig := d.Runtime.Snapshot()
	if len(events) == 0 {
		d.Runtime.AddEvent(runtime.EventRecord{
			Level:   "info",
			Message: "Go unified-server runtime is ready",
			Payload: map[string]any{"log_kind": "system"},
		})
		summary, events, _, pending, adapterLogs, systemConfig = d.Runtime.Snapshot()
	}
	adapterState, adapterErr := d.fetchAdapterState(ctx)
	adapterMessage := "adapter state ready"
	if adapterErr != nil {
		adapterMessage = adapterErr.Error()
	}
	opencvMessage := "opencv state ready"
	opencvErr := d.checkServiceHealth(ctx, strings.TrimRight(d.Config.OpenCVBaseURL, "/")+"/health")
	if opencvErr != nil {
		opencvMessage = opencvErr.Error()
	}
	ocrMessage := "ocr state ready"
	ocrErr := d.checkServiceHealth(ctx, strings.TrimRight(d.Config.OCRBaseURL, "/")+"/health")
	if ocrErr != nil {
		ocrMessage = ocrErr.Error()
	}
	serviceLinks := []any{
		map[string]any{
			"key":     "unified",
			"name":    "Go业务端",
			"url":     "http://127.0.0.1:" + strings.TrimPrefix(d.Config.HTTPAddr, ":"),
			"healthy": true,
			"message": "runtime running",
		},
		map[string]any{
			"key":     "adapter",
			"name":    "Rust适配器",
			"url":     d.Config.AdapterBaseURL,
			"healthy": adapterErr == nil,
			"message": adapterMessage,
		},
		map[string]any{
			"key":     "opencv",
			"name":    "OpenCV服务",
			"url":     d.Config.OpenCVBaseURL,
			"healthy": opencvErr == nil,
			"message": opencvMessage,
		},
		map[string]any{
			"key":     "ocr",
			"name":    "OCR服务",
			"url":     d.Config.OCRBaseURL,
			"healthy": ocrErr == nil,
			"message": ocrMessage,
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":             d.Devices.List(),
		"templates":           d.Tpl.List(),
		"summary":             summary,
		"event_log":           events,
		"pending_tasks":       pending,
		"adapter_submit_logs": adapterLogs,
		"system_config":       systemConfig,
		"upstream_configs":    d.Upstream.List(),
		"platform_accounts":   d.Accounts.List(),
		"upstream_options":    buildUpstreamOptions(d.Upstream.List()),
		"service_links":       serviceLinks,
		"adapter_state":       adapterState,
	})
}

func (d RouterDeps) handleDetails(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rangeKey := strings.TrimSpace(r.URL.Query().Get("range_key"))
		offset := parsePositiveIntQuery(r, "offset", 0)
		limit := parsePositiveIntQuery(r, "limit", 30)
		if limit <= 0 {
			limit = 30
		}
		details := filterDetailsByRange(d.Runtime.Details(), rangeKey)
		total := len(details)
		rangeSummary := buildRangeSummary(d.Runtime.Summary(), rangeKey)
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"details":  details[offset:end],
			"total":    total,
			"offset":   offset,
			"limit":    limit,
			"has_more": end < total,
			"summary":  rangeSummary,
		})
	case http.MethodDelete:
		d.Runtime.ClearDetails()
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "执行明细已清空",
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func buildRangeSummary(summary runtime.Summary, rangeKey string) runtime.DailyStats {
	if summary.Daily == nil {
		return runtime.DailyStats{Total: summary.Total, Success: summary.Success, Failure: summary.Failure}
	}
	now := time.Now()
	switch rangeKey {
	case "today":
		return cloneDailyStats(summary.Daily[now.Format("2006-01-02")])
	case "yesterday":
		return cloneDailyStats(summary.Daily[now.AddDate(0, 0, -1).Format("2006-01-02")])
	case "7d":
		result := runtime.DailyStats{}
		for index := 0; index < 7; index++ {
			item := summary.Daily[now.AddDate(0, 0, -index).Format("2006-01-02")]
			if item == nil {
				continue
			}
			result.Total += item.Total
			result.Success += item.Success
			result.Failure += item.Failure
		}
		return result
	default:
		return runtime.DailyStats{Total: summary.Total, Success: summary.Success, Failure: summary.Failure}
	}
}

func cloneDailyStats(item *runtime.DailyStats) runtime.DailyStats {
	if item == nil {
		return runtime.DailyStats{}
	}
	return runtime.DailyStats{
		Total:   item.Total,
		Success: item.Success,
		Failure: item.Failure,
	}
}

func parsePositiveIntQuery(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func filterDetailsByRange(details []runtime.DetailRecord, rangeKey string) []runtime.DetailRecord {
	if rangeKey == "" {
		return details
	}
	now := time.Now()
	location := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	yesterday := today.AddDate(0, 0, -1)
	sevenDaysAgo := today.AddDate(0, 0, -6)
	filtered := make([]runtime.DetailRecord, 0, len(details))
	for _, detail := range details {
		detailTime, err := time.Parse(time.RFC3339, detail.Timestamp)
		if err != nil {
			continue
		}
		localTime := detailTime.In(location)
		keep := false
		switch rangeKey {
		case "today":
			keep = !localTime.Before(today)
		case "yesterday":
			keep = !localTime.Before(yesterday) && localTime.Before(today)
		case "7d":
			keep = !localTime.Before(sevenDaysAgo)
		default:
			keep = true
		}
		if keep {
			filtered = append(filtered, detail)
		}
	}
	return filtered
}

func (d RouterDeps) fetchAdapterState(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.Config.AdapterBaseURL, "/")+"/api/state", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("adapter status %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (d RouterDeps) checkServiceHealth(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("service status %d", resp.StatusCode)
	}
	return nil
}

func (d RouterDeps) handleTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, d.Tpl.List())
	case http.MethodPost:
		input, err := parseTemplateInput(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		item, err := d.Tpl.Create(input)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (d RouterDeps) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, d.Upstream.List())
	case http.MethodPost:
		var payload upstream.UpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid upstream payload"})
			return
		}
		item := d.Upstream.Create(payload)
		if err := d.syncAllUpstreamsToAdapter(r.Context()); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, item)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (d RouterDeps) handleUpstreamByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("upstreamId")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing upstream id"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload upstream.UpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid upstream payload"})
			return
		}
		item, ok := d.Upstream.Update(id, payload)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "upstream not found"})
			return
		}
		if err := d.syncAllUpstreamsToAdapter(r.Context()); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !d.Upstream.Delete(id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "upstream not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": "upstream deleted"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (d RouterDeps) handleToggleUpstream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	id := r.PathValue("upstreamId")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing upstream id"})
		return
	}

	var payload struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid toggle payload"})
		return
	}

	item, ok := d.Upstream.Toggle(id, payload.Enabled)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "upstream not found"})
		return
	}
	if err := d.syncAllUpstreamsToAdapter(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (d RouterDeps) handleVisionDebug(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    "vision capability snapshot",
		"capability": d.Vision.Capability(),
		"planned": []string{
			"opencv region debug",
			"ocr region debug",
			"single loop shared ocr result cache",
			"template matching in process",
		},
	})
}

func (d RouterDeps) handleStartTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		DeviceIDs []string `json:"device_ids"`
		Mode      string   `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid task start payload"})
		return
	}
	if err := d.syncAllUpstreamsToAdapter(r.Context()); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	started, skipped := d.Tasks.Start(payload.DeviceIDs, payload.Mode)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started, "skipped": skipped})
}

func (d RouterDeps) handleStopTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		DeviceIDs []string `json:"device_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid task stop payload"})
		return
	}
	stopped, missing := d.Tasks.Stop(payload.DeviceIDs)
	writeJSON(w, http.StatusOK, map[string]any{"stopped": stopped, "missing": missing})
}

func (d RouterDeps) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := d.Devices.Scan(ctx); err != nil {
		d.Runtime.AddEvent(runtime.EventRecord{Level: "warning", Message: "ADB 扫描失败", Payload: map[string]any{"error": err.Error()}})
	}
	writeJSON(w, http.StatusOK, d.Devices.List())
}

func (d RouterDeps) handleConnectDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid connect payload"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	item, err := d.Devices.Connect(ctx, payload.Endpoint)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "device connected", "device": item})
}

func (d RouterDeps) handleDeviceURLTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	deviceID := strings.TrimSpace(r.PathValue("deviceId"))
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing device id"})
		return
	}
	var payload struct {
		TemplateIDs []string `json:"template_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid device url template payload"})
		return
	}
	normalized := make([]string, 0, len(payload.TemplateIDs))
	seen := map[string]struct{}{}
	for _, item := range payload.TemplateIDs {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	deviceItem := d.Devices.UpdateURLTemplateSelection(deviceID, normalized)
	d.Tasks.ResetDeviceURLTemplateState(deviceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "device url templates updated",
		"device":  deviceItem,
	})
}

func (d RouterDeps) handleSystemConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, d.Runtime.SystemConfig())
	case http.MethodPost:
		var payload runtime.SystemConfig
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid system config payload"})
			return
		}
		writeJSON(w, http.StatusOK, d.Runtime.UpdateSystemConfig(payload))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (d RouterDeps) handleImportPlatformAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		UpstreamCode string `json:"upstream_code"`
		Lines        string `json:"lines"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid platform account payload"})
		return
	}
	var target upstream.Record
	found := false
	for _, item := range d.Upstream.List() {
		if item.Code == payload.UpstreamCode {
			target = item
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "upstream not found"})
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	d.Accounts.Import(target, payload.Lines, enabled)
	writeJSON(w, http.StatusOK, d.Accounts.List())
}

func (d RouterDeps) handleTogglePlatformAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	accountID := r.PathValue("accountId")
	var payload struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid toggle payload"})
		return
	}
	item, ok := d.Accounts.Toggle(accountID, payload.Enabled)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account not found"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (d RouterDeps) handleTestPlatformAccountFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	accountID := strings.TrimSpace(r.PathValue("accountId"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing account id"})
		return
	}
	accountItem, ok := d.Accounts.Get(accountID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account not found"})
		return
	}
	var upstreamItem upstream.Record
	found := false
	for _, item := range d.Upstream.List() {
		if item.Code == accountItem.UpstreamCode {
			upstreamItem = item
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "upstream not found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := d.syncAllUpstreamsToAdapter(ctx); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	result, err := d.Tasks.TestPlatformAccountFetch(ctx, upstreamItem, accountItem)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d RouterDeps) handleExternalFetchTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !d.Runtime.SystemConfig().ExternalAPIEnabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "external api disabled"})
		return
	}
	var payload task.ExternalFetchRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid fetch payload"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := d.syncAllUpstreamsToAdapter(ctx); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	result, err := d.Tasks.FetchExternalTask(ctx, payload)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d RouterDeps) handleExternalURLTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !d.Runtime.SystemConfig().ExternalAPIEnabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "external api disabled"})
		return
	}
	writeJSON(w, http.StatusOK, d.Tasks.ListExternalURLTemplates())
}

func (d RouterDeps) handleExternalSubmitTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !d.Runtime.SystemConfig().ExternalAPIEnabled {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "external api disabled"})
		return
	}
	var payload task.ExternalSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid submit payload"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	result, err := d.Tasks.SubmitExternalTask(ctx, payload)
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "不能为空") || strings.Contains(err.Error(), "不属于该 worker") || strings.Contains(err.Error(), "仅支持") || strings.Contains(err.Error(), "未找到对应的外部任务认领记录") || strings.Contains(err.Error(), "解析第") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{
			"error":        err.Error(),
			"task_id":      result.TaskID,
			"detail_ids":   result.DetailIDs,
			"capture_urls": result.CaptureURLs,
			"device_id":    result.DeviceID,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d RouterDeps) handleDeletePlatformAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	accountID := r.PathValue("accountId")
	if !d.Accounts.Delete(accountID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "account deleted"})
}

func buildUpstreamOptions(items []upstream.Record) []map[string]any {
	options := make([]map[string]any, 0, len(items))
	for _, item := range items {
		options = append(options, map[string]any{
			"code":          item.Code,
			"name":          item.Name,
			"upstream_type": item.UpstreamType,
			"enabled":       item.Enabled,
		})
	}
	return options
}
