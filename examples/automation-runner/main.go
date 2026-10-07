// The automation runner is an example execution adapter. Scheduling, policy,
// retries of occurrences, and the run ledger stay in Wakeplane.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	address := envOr("AUTOMATION_RUNNER_ADDR", "127.0.0.1:8091")
	runner, err := newRunner(ctx, runnerConfig{
		StateDir:          envOr("AUTOMATION_RUNNER_STATE_DIR", "examples/automation-runner/state"),
		PublicURL:         envOr("AUTOMATION_RUNNER_PUBLIC_URL", "http://"+address),
		Token:             os.Getenv("AUTOMATION_RUNNER_TOKEN"),
		GitHubURL:         envOr("AUTOMATION_RUNNER_GITHUB_URL", "https://api.github.com"),
		GitHubToken:       os.Getenv("AUTOMATION_RUNNER_GITHUB_TOKEN"),
		NotifyToken:       os.Getenv("AUTOMATION_RUNNER_NOTIFY_TOKEN"),
		NotifyOrigin:      os.Getenv("AUTOMATION_RUNNER_NOTIFY_ORIGIN"),
		DiscordWebhookURL: os.Getenv("AUTOMATION_RUNNER_DISCORD_WEBHOOK_URL"),
		GmailFrom:         os.Getenv("AUTOMATION_RUNNER_GMAIL_FROM"),
		GmailTo:           os.Getenv("AUTOMATION_RUNNER_GMAIL_TO"),
		GmailClientID:     os.Getenv("AUTOMATION_RUNNER_GMAIL_CLIENT_ID"),
		GmailClientSecret: os.Getenv("AUTOMATION_RUNNER_GMAIL_CLIENT_SECRET"),
		GmailRefreshToken: os.Getenv("AUTOMATION_RUNNER_GMAIL_REFRESH_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()
	server := &http.Server{
		Addr: address, Handler: runner.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()
	log.Printf("automation runner listening on %s; durable state in %s", address, runner.config.StateDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
