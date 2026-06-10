package httpapi

import (
    "encoding/json"
    "net/http"

    "unified-server/internal/config"
    "unified-server/internal/device"
    "unified-server/internal/task"
    "unified-server/internal/template"
	"unified-server/internal/upstream"
    "unified-server/internal/vision"
    "unified-server/internal/ws"
)

type RouterDeps struct {
    Config  config.Config
    Hub     *ws.Hub
    Tasks   *task.Service
    Tpl     *template.Store
	Upstream *upstream.Store
    Vision  *vision.Engine
    Devices *device.Service
}

func NewRouter(cfg config.Config, hub *ws.Hub, tasks *task.Service, tpl *template.Store, upstreams *upstream.Store, visionEngine *vision.Engine, devices *device.Service) http.Handler {
	deps := RouterDeps{Config: cfg, Hub: hub, Tasks: tasks, Tpl: tpl, Upstream: upstreams, Vision: visionEngine, Devices: devices}
    mux := http.NewServeMux()

    mux.HandleFunc("/api/health", deps.handleHealth)
    mux.HandleFunc("/api/runtime/summary", deps.handleSummary)
	mux.HandleFunc("/api/upstreams", deps.handleUpstreams)
    mux.HandleFunc("/api/templates", deps.handleTemplates)
    mux.HandleFunc("/api/debug/vision", deps.handleVisionDebug)
    mux.HandleFunc("/api/tasks/start", deps.handleStartTask)
    mux.HandleFunc("/api/devices", deps.handleDevices)
    mux.Handle("/ws/events", deps.Hub)

    return withJSONHeaders(mux)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(value)
}

func withJSONHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Powered-By", "unified-server")
        next.ServeHTTP(w, r)
    })
}
