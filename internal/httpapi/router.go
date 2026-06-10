package httpapi

import (
    "encoding/json"
    "net/http"
    "os"
    "path/filepath"
    "strings"

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
    mux.HandleFunc("/api/state", deps.handleState)
    mux.HandleFunc("/api/runtime/summary", deps.handleSummary)
	mux.HandleFunc("/api/upstreams", deps.handleUpstreams)
    mux.HandleFunc("/api/upstreams/{upstreamId}", deps.handleUpstreamByID)
    mux.HandleFunc("/api/upstreams/{upstreamId}/toggle", deps.handleToggleUpstream)
    mux.HandleFunc("/api/templates", deps.handleTemplates)
    mux.HandleFunc("/api/debug/vision", deps.handleVisionDebug)
    mux.HandleFunc("/api/tasks/start", deps.handleStartTask)
    mux.HandleFunc("/api/devices", deps.handleDevices)
    mux.Handle("/ws/events", deps.Hub)
    registerFrontendRoutes(mux, cfg.FrontendDistDir)

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

func registerFrontendRoutes(mux *http.ServeMux, distDir string) {
    indexPath := filepath.Join(distDir, "index.html")
    if !fileExists(indexPath) {
        return
    }

    fileServer := http.FileServer(http.Dir(distDir))
    mux.Handle("/assets/", fileServer)
    mux.Handle("/favicon.svg", fileServer)
    mux.Handle("/icons.svg", fileServer)
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
            http.NotFound(w, r)
            return
        }
        cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
        requestPath := filepath.Join(distDir, cleanPath)
        if r.URL.Path != "/" && fileExists(requestPath) {
            fileServer.ServeHTTP(w, r)
            return
        }
        http.ServeFile(w, r, indexPath)
    })
}

func fileExists(path string) bool {
    info, err := os.Stat(path)
    if err != nil {
        return false
    }
    return !info.IsDir()
}
