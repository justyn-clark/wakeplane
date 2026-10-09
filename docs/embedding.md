# Embedding Contract

Wakeplane can be integrated into Go applications through this repository's `internal/...` packages. This is a source-level integration for the module or compatible forks, not an importable public Go library API. It remains outside the 1.0 public semver contract; pin the exact source revision and test your integration when upgrading. The supported standalone boundary is defined in [Stable contract](public/stable-contract.md).

## Construction

Create a service with `app.NewWithOptions`:

```go
service, err := app.NewWithOptions(ctx, cfg,
    app.WithWorkflowHandler("sync.customers", syncCustomersHandler),
    app.WithWorkflowHandler("generate.report", generateReportHandler),
)
```

`NewWithOptions` opens the configured SQLite or Postgres backend, runs its migrations, and wires the planner, dispatcher, and executor registry. It does not start any background loops.

**Options:**

- `WithWorkflowHandler(id, handler)` - register a single workflow handler by ID.
- `WithWorkflowRegistry(registry)` - pass a pre-built `*executors.WorkflowRegistry` for bulk registration.

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
4. Close the configured store.

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

- `(result, nil)` - run succeeds. `result` is stored as a `workflow_result` receipt.
- `(nil, err)` - run fails. `err.Error()` is stored as `error_text`. Retry policy applies.
- If `ctx.Err() != nil` at return time, the run is marked `cancelled` regardless of the returned error.

**Cooperative cancellation:** Handlers should check `ctx.Done()` and return promptly. If a handler ignores cancellation, the dispatcher waits until the `CloseContext` deadline is exceeded, then returns `DeadlineExceeded`. The handler goroutine continues running in the background until it returns or the process exits.

## Missing workflow behavior

If a schedule targets `workflow_id: X` and no handler is registered for `X`:

- The executor returns an error: `workflow "X" is not registered`.
- The run is `failed` with a new retry attempt when policy permits; otherwise it is `dead_lettered`.
- `max_attempts` counts total attempts including the first. Values `0` and `1`, or strategy `none`, allow no retry.
- Register the handler before retrying: the same missing registration fails again.

## Recovery guarantees

On startup, the dispatcher recovers stale state from the previous process:

| Crash point                       | DB state                                | Recovery action                                                              |
| --------------------------------- | --------------------------------------- | ---------------------------------------------------------------------------- |
| After claim, before mark-running  | `claimed`, expired lease                | Reset to `pending` after validating the expired claim                        |
| During ordinary execution         | `running`, expired lease                | Atomically record failure plus policy retry/dead letter and remove the lease |
| During tracked external work      | Unresolved remote checkpoint            | Resume lookup/polling of the same occurrence and remote identity             |
| During failure transaction        | Uncommitted changes                     | Roll back; later recovery can retry the transition                           |
| After failure transaction commits | Terminal attempt plus retry/dead letter | Follow-up is already durable                                                 |

Failure outcome, follow-up retry/dead letter, and lease removal share a transaction. Historical failed rows without retries are not automatically rewritten on startup. Successful outcome and diagnostic receipt inserts remain separate writes; a crash can leave fewer receipts after a successful outcome. Neither durable state nor recovery establishes exactly-once side effects at a remote provider. See [Automation](public/automation.md).
