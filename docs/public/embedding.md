# Embedding

Wakeplane is designed to be embedded as a library in Go applications. This lets you run the full scheduling control plane inside your process without deploying a separate daemon.

## When to embed

Embed Wakeplane when:
- Your application already manages a long-running process (HTTP server, daemon)
- You want in-process workflow handlers that call your application code directly
- You do not want to manage a separate daemon deployment

Use the standalone daemon when:
- You want to schedule work that is independent of any particular application
- You are calling HTTP or shell targets that do not need application code

## Installation

```bash
go get github.com/justyn-clark/wakeplane
```

## Construction

```go
import (
    "github.com/justyn-clark/wakeplane/internal/app"
    "github.com/justyn-clark/wakeplane/internal/config"
)

cfg := config.FromEnv("my-service")

service, err := app.NewWithOptions(ctx, cfg,
    app.WithWorkflowHandler("sync.customers", syncCustomersHandler),
    app.WithWorkflowHandler("generate.report", generateReportHandler),
)
if err != nil {
    log.Fatal(err)
}
```

`NewWithOptions` opens the SQLite database, runs migrations, and wires the planner, dispatcher, and executor registry. It does not start any background loops.

**Registration options:**

- `WithWorkflowHandler(id, handler)` — register a single workflow handler by ID
- `WithWorkflowRegistry(registry)` — pass a pre-built `*executors.WorkflowRegistry` for bulk registration

If no handlers are registered, the service starts normally. Schedules targeting `workflow` targets will fail at dispatch time with `"workflow X is not registered"`.

## Lifecycle

### Starting

```go
go func() {
    if err := service.Run(ctx); err != nil && err != context.Canceled {
        log.Printf("service run: %v", err)
    }
}()
```

`Run` starts the planner and dispatcher loops. It blocks until the context is cancelled or an unrecoverable error occurs. Call it exactly once — a second call returns `"service already running"`.

### Stopping

```go
// With default 5-second timeout:
err := service.Close()

// With explicit timeout:
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
err := service.CloseContext(ctx)
```

**Shutdown sequence:**

1. Cancel the run context — planner and dispatcher loops stop
2. Wait for the run loop goroutine to exit
3. Call `dispatcher.Shutdown` — cancel all active execution contexts, wait for in-flight work to drain
4. Close the SQLite store

Each phase emits structured log lines so you can trace where shutdown stalled.

**If `CloseContext` exceeds its deadline:**
- Returns `context.DeadlineExceeded`
- The store is **not** closed (it was not reached in the sequence)
- Active runs retain `running` status
- On next startup, expired leases trigger recovery

## HTTP server coordination

Wakeplane does not manage its own HTTP listener. You wire it:

```go
server := &http.Server{
    Addr:    cfg.HTTPAddress,
    Handler: api.NewMux(service),
}

// Shut down HTTP when context cancels
go func() {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = server.Shutdown(shutdownCtx)
}()

// Run the scheduling service
go func() {
    if err := service.Run(ctx); err != nil && err != context.Canceled {
        log.Printf("service run: %v", err)
        stop() // cancel the root context
    }
}()

// Block on HTTP
if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
    log.Fatal(err)
}
```

See [examples/embedded/main.go](../../examples/embedded/main.go) for a complete working example.

## Workflow handler contract

```go
type WorkflowHandler func(ctx context.Context, input map[string]any) (map[string]any, error)
```

| Aspect | Behavior |
|---|---|
| `ctx` | Carries a deadline from `policy.timeout_seconds`. Closed on shutdown or `replace` cancellation. |
| `input` | The `target.input` map from the schedule definition. Nil if not set. |
| `(result, nil)` | Run succeeds. `result` is stored as a `workflow_result` receipt. |
| `(nil, err)` | Run fails. `err.Error()` stored as `error_text`. Retry policy applies. |
| `ctx.Err() != nil` at return | Run marked `cancelled` regardless of returned error. |

**Cooperative cancellation:** Handlers should check `ctx.Done()` and return promptly. If a handler ignores cancellation, the dispatcher waits until the `CloseContext` deadline, then returns `DeadlineExceeded`. The handler goroutine continues in the background until it returns or the process exits.

## Recovery guarantees

| Crash point | DB state | Recovery action |
|---|---|---|
| After claim, before mark-running | `claimed`, lease exists | Lease expires → reset to `pending` |
| After mark-running, before finish | `running`, lease exists | Lease expires → mark `failed`, retry scheduled |
| After finish, before retry insert | `failed`, no retry | **No automatic recovery** — retry is lost |
| Retry scheduled, before dispatch | `retry_scheduled` | Picked up by next dispatcher tick |

The "after finish, before retry insert" gap is a known limitation of the alpha. `FinishRun` and retry `InsertRun` are not in a single transaction.

## Configuration

The embedded service reads the same environment variables as the daemon:

```go
cfg := config.FromEnv("my-service-version")
```

Override programmatically by modifying `cfg` fields before passing to `NewWithOptions`.
