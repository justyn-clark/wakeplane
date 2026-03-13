package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/justyn-clark/wakeplane/internal/api"
	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.FromEnv("embed-example")
	service, err := app.NewWithOptions(ctx, cfg,
		app.WithWorkflowHandler("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
			return map[string]any{
				"status": "completed",
				"source": input["source"],
			}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			log.Printf("service close: %v", err)
		}
	}()

	server := &http.Server{
		Addr:    cfg.HTTPAddress,
		Handler: api.NewMux(service),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := service.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("service run: %v", err)
			stop()
		}
	}()

	log.Printf("embedding example listening on %s", cfg.HTTPAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
