package app

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "unified-server/internal/config"
    "unified-server/internal/device"
    "unified-server/internal/httpapi"
    "unified-server/internal/task"
    "unified-server/internal/template"
	"unified-server/internal/upstream"
    "unified-server/internal/vision"
    "unified-server/internal/ws"
)

type Server struct {
    cfg    config.Config
    http   *http.Server
    hub    *ws.Hub
    tasks  *task.Service
    tpl    *template.Store
	ups    *upstream.Store
    vision *vision.Engine
}

func New(cfg config.Config) *Server {
    hub := ws.NewHub()
    tpl := template.NewStore()
	ups := upstream.NewStore()
    visionEngine := vision.NewEngine(cfg, tpl)
    devices := device.NewService(hub)
    tasks := task.NewService(hub, tpl, visionEngine, devices)
	router := httpapi.NewRouter(cfg, hub, tasks, tpl, ups, visionEngine, devices)

    return &Server{
        cfg: cfg,
        hub: hub,
        tpl: tpl,
		ups: ups,
        tasks: tasks,
        vision: visionEngine,
        http: &http.Server{
            Addr:              cfg.HTTPAddr,
            Handler:           router,
            ReadHeaderTimeout: 10 * time.Second,
        },
    }
}

func (s *Server) Run(ctx context.Context) error {
    go s.hub.Run(ctx)

    serverErr := make(chan error, 1)
    go func() {
        serverErr <- s.http.ListenAndServe()
    }()

    select {
    case <-ctx.Done():
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        return s.http.Shutdown(shutdownCtx)
    case err := <-serverErr:
        if err == nil || err == http.ErrServerClosed {
            return nil
        }
        return fmt.Errorf("unified-server exited: %w", err)
    }
}
