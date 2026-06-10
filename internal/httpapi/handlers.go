package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"unified-server/internal/runtime"
	"unified-server/internal/upstream"
)

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
	summary, events, details, pending, adapterLogs, systemConfig := d.Runtime.Snapshot()
	if len(events) == 0 {
		d.Runtime.AddEvent(runtime.EventRecord{
			Level:   "info",
			Message: "Go unified-server runtime is ready",
			Payload: map[string]any{"log_kind": "system"},
		})
		summary, events, details, pending, adapterLogs, systemConfig = d.Runtime.Snapshot()
	}
	adapterState, adapterErr := d.fetchAdapterState(ctx)
	adapterMessage := "adapter state ready"
	if adapterErr != nil {
		adapterMessage = adapterErr.Error()
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
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":             d.Devices.List(),
		"templates":           d.Tpl.List(),
		"details":             details,
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
