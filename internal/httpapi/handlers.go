package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"unified-server/internal/upstream"
)

func (d RouterDeps) handleHealth(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{
        "success": true,
        "service": "unified-server",
        "mode": "scaffold",
    })
}

func (d RouterDeps) handleSummary(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{
        "adapter_base_url": d.Config.AdapterBaseURL,
        "template_count": d.Tpl.Count(),
		"upstream_count": d.Upstream.Count(),
        "ocr_templates": d.Tpl.CountByEngine("ocr"),
        "opencv_templates": d.Tpl.CountByEngine("opencv"),
        "vision_mode": d.Vision.Mode(),
        "connected_clients": d.Hub.ClientCount(),
        "devices": d.Devices.List(),
        "runtime_plan": d.Tasks.RuntimePlan(),
    })
}

func (d RouterDeps) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"devices": d.Devices.List(),
		"templates": d.Tpl.List(),
		"details": []any{},
		"summary": map[string]any{
			"total": 0,
			"success": 0,
			"failure": 0,
		},
		"event_log": []any{
			map[string]any{
				"id": "boot-event",
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"device_id": nil,
				"level": "info",
				"message": "Go unified-server scaffold is running",
				"payload": map[string]any{
					"log_kind": "system",
				},
			},
		},
		"pending_tasks": []any{},
		"adapter_submit_logs": []any{},
		"system_config": map[string]any{
			"open_url_delay_seconds": 2,
			"click_image_delay_seconds": 1.2,
			"max_task_sku_count": 0,
			"use_url_templates": false,
			"url_templates": []any{},
		},
		"upstream_configs": d.Upstream.List(),
		"platform_accounts": []any{},
		"upstream_options": buildUpstreamOptions(d.Upstream.List()),
		"service_links": []any{
			map[string]any{
				"key": "unified",
				"name": "Go业务端",
				"url": "http://127.0.0.1:8080",
				"healthy": true,
				"message": "scaffold running",
			},
			map[string]any{
				"key": "adapter",
				"name": "Rust适配器",
				"url": d.Config.AdapterBaseURL,
				"healthy": false,
				"message": "waiting adapter-rs build environment",
			},
		},
	})
}

func (d RouterDeps) handleTemplates(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        writeJSON(w, http.StatusOK, map[string]any{"items": d.Tpl.List()})
    default:
        writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
    }
}

func (d RouterDeps) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": d.Upstream.List()})
	case http.MethodPost:
		var payload upstream.UpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid upstream payload"})
			return
		}
		writeJSON(w, http.StatusCreated, d.Upstream.Create(payload))
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
	writeJSON(w, http.StatusOK, item)
}

func (d RouterDeps) handleVisionDebug(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{
        "success": true,
        "message": "vision debug endpoint scaffolded",
        "planned": []string{
            "opencv region debug",
            "ocr region debug",
            "single loop shared ocr result cache",
            "template matching in process",
        },
    })
}

func (d RouterDeps) handleStartTask(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusAccepted, map[string]any{
        "success": true,
        "message": "task execution scaffolded",
    })
}

func (d RouterDeps) handleDevices(w http.ResponseWriter, _ *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{"items": d.Devices.List()})
}

func buildUpstreamOptions(items []upstream.Record) []map[string]any {
	options := make([]map[string]any, 0, len(items))
	for _, item := range items {
		options = append(options, map[string]any{
			"code": item.Code,
			"name": item.Name,
			"upstream_type": item.UpstreamType,
			"enabled": item.Enabled,
		})
	}
	return options
}
