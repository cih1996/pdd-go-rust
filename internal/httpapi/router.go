package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"unified-server/internal/account"
	"unified-server/internal/config"
	"unified-server/internal/device"
	rt "unified-server/internal/runtime"
	"unified-server/internal/task"
	"unified-server/internal/template"
	"unified-server/internal/upstream"
	"unified-server/internal/vision"
	"unified-server/internal/ws"
)

type RouterDeps struct {
	Config   config.Config
	Hub      *ws.Hub
	Tasks    *task.Service
	Tpl      *template.Store
	Upstream *upstream.Store
	Vision   *vision.Engine
	Devices  *device.Service
	Runtime  *rt.Store
	Accounts *account.Store
}

func NewRouter(cfg config.Config, hub *ws.Hub, tasks *task.Service, tpl *template.Store, upstreams *upstream.Store, visionEngine *vision.Engine, devices *device.Service, runtimeStore *rt.Store, accountStore *account.Store) http.Handler {
	deps := RouterDeps{Config: cfg, Hub: hub, Tasks: tasks, Tpl: tpl, Upstream: upstreams, Vision: visionEngine, Devices: devices, Runtime: runtimeStore, Accounts: accountStore}
	debugHandler := newDebugCommandHandler(hub, cfg, devices, runtimeStore, visionEngine, tpl)
	hub.SetMessageHandler(debugHandler.Handle)
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", deps.handleHealth)
	mux.HandleFunc("/api/state", deps.handleState)
	mux.HandleFunc("/api/runtime/summary", deps.handleSummary)
	mux.HandleFunc("/api/upstreams", deps.handleUpstreams)
	mux.HandleFunc("/api/upstreams/{upstreamId}", deps.handleUpstreamByID)
	mux.HandleFunc("/api/upstreams/{upstreamId}/toggle", deps.handleToggleUpstream)
	mux.HandleFunc("/api/templates", deps.handleTemplates)
	mux.HandleFunc("/api/templates/import", deps.handleImportTemplates)
	mux.HandleFunc("/api/templates/export", deps.handleExportTemplates)
	mux.HandleFunc("/api/templates/{templateId}", deps.handleTemplateByID)
	mux.HandleFunc("/api/templates/{templateId}/move", deps.handleMoveTemplate)
	mux.HandleFunc("/api/templates/{templateId}/test", deps.handleTestTemplate)
	mux.HandleFunc("/api/templates/test-unsaved", deps.handleTestUnsavedTemplate)
	mux.HandleFunc("/api/debug/vision", deps.handleVisionDebug)
	mux.HandleFunc("/api/debug/run", deps.handleDebugRun)
	mux.HandleFunc("/api/debug/capture", deps.handleDebugCapture)
	mux.HandleFunc("/api/debug/match-selection", deps.handleDebugMatchSelection)
	mux.HandleFunc("/api/debug/ocr-selection", deps.handleDebugOCRSelection)
	mux.HandleFunc("/api/tasks/start", deps.handleStartTask)
	mux.HandleFunc("/api/tasks/stop", deps.handleStopTask)
	mux.HandleFunc("/api/devices", deps.handleDevices)
	mux.HandleFunc("/api/devices/{deviceId}/url-templates", deps.handleDeviceURLTemplates)
	mux.HandleFunc("/api/devices/connect", deps.handleConnectDevice)
	mux.HandleFunc("/api/system-config", deps.handleSystemConfig)
	mux.HandleFunc("/api/platform-accounts/import", deps.handleImportPlatformAccounts)
	mux.HandleFunc("/api/platform-accounts/{accountId}/toggle", deps.handleTogglePlatformAccount)
	mux.HandleFunc("/api/platform-accounts/{accountId}", deps.handleDeletePlatformAccount)
	mux.HandleFunc("/api/mock-data/import", deps.handleImportMockData)
	mux.Handle("/ws/events", deps.Hub)
	mux.Handle("/api/assets/templates/", http.StripPrefix("/api/assets/templates/", http.FileServer(http.Dir(tpl.ImageDir()))))
	mux.Handle("/api/assets/debug/", http.StripPrefix("/api/assets/debug/", http.FileServer(http.Dir(cfg.DebugAssetDir))))
	registerFrontendRoutes(mux, cfg.FrontendDistDir)

	return withCORS(withJSONHeaders(mux))
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

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
