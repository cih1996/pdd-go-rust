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
	server, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
