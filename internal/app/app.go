package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	if err := migrateLegacyRuntimeData(cfg); err != nil {
		return nil, fmt.Errorf("migrate runtime data failed: %w", err)
	}
	db, err := persistence.Open(cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite failed: %w", err)
	}
	hub := ws.NewHub()
	tpl := template.NewStoreWithRoot(filepath.Join(cfg.RuntimeDir, "templates"), db)
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

func migrateLegacyRuntimeData(cfg config.Config) error {
	targetDir := cfg.RuntimeDir
	if targetDir == "" {
		return nil
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}

	for _, source := range legacyRuntimeDirs() {
		if source == "" || samePath(source, targetDir) {
			continue
		}
		if err := copyFileIfMissing(filepath.Join(source, "unified-server.db"), cfg.SQLitePath); err != nil {
			return err
		}
		for _, name := range []string{"templates", "debug"} {
			if err := copyDirIfMissing(filepath.Join(source, name), filepath.Join(targetDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func legacyRuntimeDirs() []string {
	dirs := make([]string, 0, 2)
	if currentDir, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(currentDir, ".runtime"))
	}
	if exePath, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exePath), ".runtime"))
	}
	return dirs
}

func copyFileIfMissing(source string, destination string) error {
	if !fileExists(source) || fileExists(destination) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func copyDirIfMissing(source string, destination string) error {
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return nil
	}
	if existing, err := os.Stat(destination); err == nil && existing.IsDir() {
		return nil
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		return copyFileIfMissing(path, targetPath)
	})
}

func samePath(left string, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
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
