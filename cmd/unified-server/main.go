package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"

    "unified-server/internal/app"
    "unified-server/internal/config"
)

func main() {
    cfg := config.Load()
    server := app.New(cfg)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := server.Run(ctx); err != nil {
        log.Fatal(err)
    }
}