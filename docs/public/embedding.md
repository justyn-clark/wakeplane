# Embedding

Wakeplane can run inside a Go application by integrating this repository's internal packages. This is a source-level integration for this module or forks that retain Go's internal-package import boundary. It is outside the 1.0 public semver contract; use the standalone daemon, REST API, and CLI for a stable integration.

> **Operator warning:** embedding does not change the network boundary. The HTTP API supports single-operator bearer auth for `/v1/...`, but it has no RBAC or multi-tenancy. Bind it to localhost, a trusted subnet, VPN, Tailscale, or a reverse-proxied private network.

## When to embed

Embed Wakeplane when:

- Your application already manages a long-running process (HTTP server, daemon)
- You want in-process workflow handlers that call your application code directly
- You do not want to manage a separate daemon deployment

Use the standalone daemon when:

- You want to schedule work that is independent of any particular application
- You are calling HTTP or shell targets that do not need application code

## 1.0 boundary

The `v1.0.0` embedding example uses `internal/...` packages and does not expose an importable public Go library API. A 1.x release may change internal types, function signatures, and lifecycle wiring. Pin the exact source revision and rerun your integration tests when upgrading.

The [Stable contract](stable-contract.md) defines the standalone boundary. In-process workflow registration is useful for source integrations, but does not expand that public compatibility promise.

## Construction

```go
cfg := config.FromEnv("embed-example")
service, err := app.NewWithOptions(ctx, cfg,
	app.WithWorkflowHandler("sync.customers", func(ctx context.Context, input map[string]any) (map[string]any, error) {
		return map[string]any{
			"status": "completed",
			"source": input["source"],
		}, nil
	}),
)
```

`NewWithOptions` opens the configured SQLite or Postgres backend, runs its migrations, and wires the planner, dispatcher, and executor registry. It does not start any background loops.

**Registration options:**

- `WithWorkflowHandler(id, handler)` - register a single workflow handler by ID
- `WithWorkflowRegistry(registry)` - pass a pre-built `*executors.WorkflowRegistry` for bulk registration

If no handlers are registered, the service starts normally. Schedules targeting `workflow` targets will fail at dispatch time with `"workflow X is not registered"`.

## Lifecycle

### Starting

```go
go func() {
	if err := service.Run(ctx); err != nil && err != context.Canceled {
		log.Printf("service run: %v", err)
		stop()
	}
}()
```

`Run` starts the planner and dispatcher loops. It blocks until the context is cancelled or an unrecoverable error occurs. Call it exactly once - a second call returns `"service already running"`.

### Stopping

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
err := service.CloseContext(ctx)
```

**Shutdown sequence:**

1. Cancel the run context - planner and dispatcher loops stop
2. Wait for the run loop goroutine to exit
3. Call `dispatcher.Shutdown` - cancel all active execution contexts, wait for in-flight work to drain
4. Close the configured store

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

if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
	log.Fatal(err)
}
```

See [examples/embedded/main.go](../../examples/embedded/main.go) for a complete working example.

## Workflow handler contract

```go
type WorkflowHandler func(ctx context.Context, input map[string]any) (map[string]any, error)
```

| Aspect                       | Behavior                                                                                        |
| ---------------------------- | ----------------------------------------------------------------------------------------------- |
| `ctx`                        | Carries a deadline from `policy.timeout_seconds`. Closed on shutdown or `replace` cancellation. |
| `input`                      | The `target.input` map from the schedule definition. Nil if not set.                            |
| `(result, nil)`              | Run succeeds. `result` is stored as a `workflow_result` receipt.                                |
| `(nil, err)`                 | Run fails. `err.Error()` stored as `error_text`. Retry policy applies.                          |
| `ctx.Err() != nil` at return | Run marked `cancelled` regardless of returned error.                                            |

**Cooperative cancellation:** Handlers should check `ctx.Done()` and return promptly. If a handler ignores cancellation, the dispatcher waits until the `CloseContext` deadline, then returns `DeadlineExceeded`. The handler goroutine continues in the background until it returns or the process exits.

## Recovery guarantees

| Crash point                       | DB state                                | Recovery action                                                                 |
| --------------------------------- | --------------------------------------- | ------------------------------------------------------------------------------- |
| After claim, before mark-running  | `claimed`, expired lease                | Reset to `pending` after validating the expired claim                           |
| During ordinary execution         | `running`, expired lease                | Atomically record failure plus policy retry or dead letter and remove the lease |
| During tracked external work      | Unresolved remote checkpoint            | Resume lookup/polling of the same occurrence and remote identity                |
| During failure transaction        | Uncommitted changes                     | Transaction rolls back; later recovery can retry the transition                 |
| After failure transaction commits | Terminal attempt plus retry/dead letter | Follow-up is already durable; retry is eligible at its due time                 |

Failure outcome, follow-up retry/dead letter, and lease removal now share a transaction. A historical failed row without a retry is not automatically rewritten or repaired on startup. Successful outcome and individual diagnostic receipt inserts are separate writes: a crash can leave a successful run with fewer receipts. Treat the durable status, remote checkpoint, provider acceptance and actual delivery as separate evidence.

Recovery does not establish exactly-once side effects. Ordinary HTTP/shell/workflow work can have completed before a crash, and providers must support idempotency where duplicate side effects matter. See [Automation](automation.md) for tracked-job and unconfirmed-delivery behavior.

## Configuration

The embedded service reads the same environment variables as the daemon through `config.FromEnv(version)`. Override fields on `cfg` before passing it to `NewWithOptions`.
