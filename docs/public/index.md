# Wakeplane Docs

Wakeplane is a durable scheduling control plane for long-running systems. These docs cover the current public alpha (`0.1.x`).

> **Alpha:** Pre-stable. No authentication. Bind to a trusted network only. See [Security](security.md).

## Getting started

- [Quickstart](quickstart.md) — start the daemon, create a schedule, inspect runs in under five minutes

## Understanding Wakeplane

- [Concepts](concepts.md) — planner, dispatcher, occurrence keys, leases, receipts, dead letters
- [Schedules](schedules.md) — cron/interval/once, YAML manifest shape, timezone behavior, pause/resume
- [Policies](policies.md) — overlap (allow/forbid/queue_latest/replace), misfire, timeout, retry
- [Executors](executors.md) — HTTP, shell, and workflow targets; receipt behavior; registration
- [Run States](run-states.md) — the full state machine, transition rules, crash recovery semantics

## Reference

- [API](api.md) — endpoint list, error envelope, pagination, filtering, content types
- [Embedding](embedding.md) — using Wakeplane as a Go library in your application
- [Storage](storage.md) — SQLite-first rationale, constraints, portability seam
- [Runbook](runbook.md) — startup, health checks, shutdown, metrics, common failures
- [Releasing](releasing.md) — versioning, release checklist, breaking change definition
- [Security](security.md) — no-auth posture, trusted-network requirements, planned work

## Current scope

Wakeplane `0.1.x` ships as:
- Single-process Go daemon and CLI
- SQLite-first storage with embedded migrations
- HTTP, shell, and in-process workflow executors
- HTTP JSON API and Cobra CLI
- Planner and dispatcher loops with durable run ledger
- Metrics, health, readiness, and status endpoints
- Structured shutdown and drain logging

Not yet in alpha:
- Authentication, RBAC, or multi-tenancy
- Postgres backend
- UI
- Distributed coordination
- Dynamic plugin loading
