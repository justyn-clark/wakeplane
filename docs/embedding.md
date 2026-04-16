# Embedding Contract

Wakeplane is designed to be embedded as a library in Go applications. This document defines the lifecycle contract, registration expectations, and behavioral guarantees that embedding code must understand.

## Construction

Create a service with `app.NewWithOptions`:

```go
service, err := app.NewWithOptions(ctx, cfg,
    app.WithWorkflowHandler("sync.customers", syncCustomersHandler),
    app.WithWorkflowHandler("generate.report", generateReportHandler),
)
```

`NewWithOptions` opens the SQLite database, runs migrations, and wires the planner, dispatcher, and executor registry. It does not start any background loops.

**Options:**

- `WithWorkflowHandler(id, handler)` — register a single workflow handler by ID.
- `WithWorkflowRegistry(registry)` — pass a pre-built `*executors.WorkflowRegistry` for bulk registration.

If no workflow handlers are registered, the service still starts. Schedules targeting `workflow` targets will fail at dispatch time with `"workflow X is not registered"`.

## Lifecycle

### Run

`service.Run(ctx)` starts the scheduler and dispatcher loops. It blocks until the context is cancelled or an unrecoverable error occurs.

```go
go func() {
    if err := service.Run(ctx); err != nil && err != context.Canceled {
        log.Printf("service run: %v", err)
    }
}()
```

`Run` may only be called once. A second call returns `"service already running"`.

### Close

`service.Close()` requests shutdown with a 5-second timeout. For explicit control:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
err := service.CloseContext(ctx)
```

**Shutdown sequence:**

1. Cancel the run context (scheduler and dispatcher ticker loops stop).
2. Wait for the run loop goroutine to exit.
3. Call `dispatcher.Shutdown` which cancels all active execution contexts and waits for in-flight work to drain.
4. Close the SQLite store.

**Shutdown logging:** Each phase emits structured log lines (`shutdown requested`, `draining`, `run loop stopped`, `dispatcher shutdown`, `shutdown complete` or timeout warnings) so operators can trace exactly where shutdown stalled.

### CloseContext timeout behavior

If `CloseContext` exceeds its deadline:

- Returns `context.DeadlineExceeded`.
- The store is **not** closed (it was never reached in the shutdown sequence).
- Active runs retain their `running` status in the ledger.
- On next startup, expired leases trigger recovery: `running` runs with expired leases are marked `failed` and retried according to retry policy.

This means Wakeplane does not force-close the database underneath active work. Process supervision should handle the final termination if graceful drain does not complete.

## HTTP server coordination

Wakeplane does not manage its own HTTP listener. Embedding code must:

1. Create the HTTP mux: `handler := api.NewMux(service)`
2. Create and manage the `http.Server` and listener.
3. Coordinate server shutdown with service shutdown on signal.

```go
server := &http.Server{Addr: cfg.HTTPAddress, Handler: api.NewMux(service)}

go func() {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = server.Shutdown(shutdownCtx)
}()

go func() {
    if err := service.Run(ctx); err != nil && err != context.Canceled {
        log.Printf("service run: %v", err)
        stop() // cancel the root context
    }
}()

if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
    log.Fatal(err)
}
```

See [examples/embedded/main.go](../examples/embedded/main.go) for a complete working example.

## Workflow handler contract

A workflow handler has the signature:

```go
type WorkflowHandler func(ctx context.Context, input map[string]any) (map[string]any, error)
```

**Context:** The `ctx` carries a timeout derived from `policy.timeout_seconds` on the schedule. When the timeout fires or shutdown is requested, `ctx.Done()` is closed.

**Input:** The `input` map is the `target.input` field from the schedule definition. It is `nil` if not set.

**Return values:**

- `(result, nil)` — run succeeds. `result` is stored as a `workflow_result` receipt.
- `(nil, err)` — run fails. `err.Error()` is stored as `error_text`. Retry policy applies.
- If `ctx.Err() != nil` at return time, the run is marked `cancelled` regardless of the returned error.

**Cooperative cancellation:** Handlers should check `ctx.Done()` and return promptly. If a handler ignores cancellation, the dispatcher waits until the `CloseContext` deadline is exceeded, then returns `DeadlineExceeded`. The handler goroutine continues running in the background until it returns or the process exits.

## Missing workflow behavior

If a schedule targets `workflow_id: X` and no handler is registered for `X`:

- The executor returns an error: `workflow "X" is not registered`.
- The run is marked `failed`.
- Retry policy applies (the run will be retried, and will fail again if the handler is still missing).
- After `max_attempts` retries, the run is dead-lettered.

## Recovery guarantees

On startup, the dispatcher recovers stale state from the previous process:

| Crash point                       | DB state after crash             | Recovery action                                                    |
| --------------------------------- | -------------------------------- | ------------------------------------------------------------------ |
| After claim, before mark-running  | Run is `claimed`, lease exists   | Lease expires → run reset to `pending`                             |
| After mark-running, before finish | Run is `running`, lease exists   | Lease expires → run marked `failed`, retry scheduled               |
| After finish, before retry insert | Run is `failed`, no retry exists | **No automatic recovery** — retry is lost                          |
| Retry scheduled, before dispatch  | Run is `retry_scheduled`         | Picked up by next dispatcher tick when `retry_available_at` passes |

The "after finish, before retry insert" gap is a known limitation. `FinishRun` and retry `InsertRun` are not in a single transaction. In practice, the window is extremely small (two sequential SQLite writes), but embedding code should be aware that a process kill at exactly this moment can lose a retry attempt.
