package httpapi

import (
	"encoding/json"
	"net/http"

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
