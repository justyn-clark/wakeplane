# Executors

An executor performs the actual work for a run. Wakeplane dispatches each run to the appropriate executor based on the schedule's `target.kind`. Three executors ship with the current public beta line.

## HTTP executor

Makes an HTTP request to a URL. Useful for webhooks, health checks, and any service that accepts HTTP calls.

```yaml
target:
  kind: http
  method: GET
  url: https://api.example.com/healthz
```

With a POST body:

```yaml
target:
  kind: http
  method: POST
  url: https://api.example.com/jobs/run
  headers:
    Content-Type: application/json
    X-Worker-ID: wakeplane
  body:
    job: export
    format: csv
```

**Receipt:** The executor writes an HTTP receipt containing the response status line and content type.

**Timeout:** The request is made with a context derived from `policy.timeout_seconds`. If the deadline fires, the underlying HTTP client cancels the request.

**Cancellation:** If the run is cancelled (shutdown or `replace` policy), the HTTP request is aborted via context cancellation. Most HTTP clients abort promptly.

## Shell executor

Runs a command with arguments. Useful for scripts, backups, and any process that can be invoked from a shell.

```yaml
target:
  kind: shell
  command: /usr/local/bin/backup.sh
  args:
    - "--compress"
    - "--destination=/mnt/backups"
```

**Receipt:** The executor writes a shell receipt containing `stdout`, `stderr`, and the exit code. A non-zero exit code causes the run to fail.

**Timeout:** The command is started with `exec.CommandContext`. When the timeout fires, the process receives `SIGKILL`.

**Cancellation:** On shutdown or `replace` cancellation, the process receives `SIGKILL` via context. This is a hard stop, not a graceful one.

**Environment:** Wakeplane does not currently support per-target environment injection for shell jobs. Shell commands inherit the daemon process environment.

## Workflow executor

Calls an in-process Go function registered by ID. Useful when Wakeplane is embedded in a Go application and the work is application code rather than an external HTTP or shell call.

```yaml
target:
  kind: workflow
  workflow_id: sync.customers
  input:
    source: crm
    dry_run: false
```

**Explicit registration required.** Wakeplane does not load or discover workflow handlers automatically. Every handler must be registered before the service starts:

```go
service, err := app.NewWithOptions(ctx, cfg,
    app.WithWorkflowHandler("sync.customers", syncCustomersHandler),
    app.WithWorkflowHandler("generate.report", generateReportHandler),
)
```

A handler has this signature:

```go
type WorkflowHandler func(ctx context.Context, input map[string]any) (map[string]any, error)
```

- The `ctx` carries a deadline from `policy.timeout_seconds`.
- `input` is the `target.input` map from the schedule definition.
- Return `(result, nil)` on success — `result` is stored as the `workflow_result` receipt.
- Return `(nil, err)` on failure — retry policy applies.
- If `ctx.Err() != nil` at return time, the run is marked `cancelled` regardless of the returned error.

**Missing handler behavior:** If a schedule targets `workflow_id: X` and no handler is registered for `X`, the run fails with `workflow "X" is not registered`. Retry policy applies. After all retries are exhausted, the run is dead-lettered.

**Cooperative cancellation:** Workflow handlers should check `ctx.Done()` and return promptly. If a handler ignores cancellation, the dispatcher waits until the `CloseContext` deadline, then returns `DeadlineExceeded`. The handler goroutine continues in the background until it returns or the process exits.

**Receipt:** The executor writes a workflow receipt containing the result map returned by the handler.

## Receipt access

Receipts for any run are available at:

```
GET /v1/runs/{id}/receipts
```

The response is an array of receipt objects. Each receipt has a `receipt_kind` field (`shell_output`, `http_response`, `workflow_result`) and kind-specific payload.

## Executor comparison

| Aspect              | HTTP                                          | Shell                    | Workflow                            |
| ------------------- | --------------------------------------------- | ------------------------ | ----------------------------------- |
| Target              | URL + method                                  | Command + args           | Registered handler by ID            |
| Cancellation        | Context → HTTP abort                          | Context → SIGKILL        | Context → ctx.Done() (cooperative)  |
| Timeout enforcement | Via context                                   | Via exec.CommandContext  | Via context                         |
| Receipt kind        | HTTP response summary                         | stdout/stderr/exit code  | Handler return value                |
| Registration        | None needed                                   | None needed              | Must register explicitly            |
| Alpha limits        | Static headers/body only; no secret injection | Inherits daemon user/env | In-process only, no dynamic loading |

## Not shipped yet

- Per-target credential injection (API keys, bearer tokens)
- Dynamic workflow handler loading (plugins, out-of-process execution)
- gRPC executor
- Executor timeout handling for non-cooperative HTTP servers
