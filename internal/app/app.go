package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"unified-server/internal/account"
	"unified-server/internal/config"
	"unified-server/internal/device"
	"unified-server/internal/httpapi"
	"unified-server/internal/persistence"
	rt "unified-server/internal/runtime"
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
	db     *persistence.SQLite
}

func New(cfg config.Config) (*Server, error) {
	db, err := persistence.Open(cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite failed: %w", err)
	}
	hub := ws.NewHub()
	tpl := template.NewStore(db)
	ups := upstream.NewStore(db)
	runtimeStore := rt.NewStore(db)
	accountStore := account.NewStore(db)
	visionEngine := vision.NewEngine(cfg, tpl)
	devices := device.NewService(hub, cfg.ADBPath)
	tasks := task.NewService(cfg, hub, tpl, visionEngine, devices, ups, accountStore, runtimeStore)
	router := httpapi.NewRouter(cfg, hub, tasks, tpl, ups, visionEngine, devices, runtimeStore, accountStore)

	return &Server{
		cfg:    cfg,
		hub:    hub,
		tpl:    tpl,
		ups:    ups,
		tasks:  tasks,
		vision: visionEngine,
		db:     db,
		http: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           router,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
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
		err := s.http.Shutdown(shutdownCtx)
		_ = s.db.Close()
		return err
	case err := <-serverErr:
		_ = s.db.Close()
		if err == nil || err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("unified-server exited: %w", err)
	}
}
