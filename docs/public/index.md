# Wakeplane Docs

Wakeplane is a durable scheduling control plane for long-running systems. These docs cover the current public beta release line.

> **Beta:** public release discipline and downloadable artifacts are in place. Security posture is single-operator and trusted-network-oriented: bearer-token auth is available for `/v1/...`, but there is no RBAC or multi-tenancy. SQLite remains the default local mode, with Postgres work in progress behind the store seam. See [Security](security.md) and [Status](status.md).

## Start here

- [Install](install.md) - release downloads, `go install`, source builds, checksum verification, and a smoke test
- [Quickstart](quickstart.md) - start the daemon, create a schedule, inspect runs in under five minutes
- [GitHub](https://github.com/justyn-clark/wakeplane) - canonical public repository

## Use cases

- You need an internal scheduling control plane with durable run recording.
- You want to embed scheduling into a Go service and register workflow handlers explicitly.
- You need an operator-visible replacement for ad hoc cron in a system where retries, overlap policy, and audit history matter.

## Avoid when

- You need a public multi-tenant SaaS scheduler.
- You need an auth-heavy enterprise control plane today.
- You need a distributed workflow engine or DAG orchestrator.

## Getting started

- [Quickstart](quickstart.md) - start the daemon, create a schedule, inspect runs in under five minutes

## Understanding Wakeplane

- [Concepts](concepts.md) - planner, dispatcher, occurrence keys, leases, receipts, dead letters
- [Schedules](schedules.md) - cron/interval/once, YAML manifest shape, timezone behavior, pause/resume
- [Policies](policies.md) - overlap (allow/forbid/queue_latest/replace), misfire, timeout, retry
- [Executors](executors.md) - HTTP, shell, and workflow targets; receipt behavior; registration
- [Run States](run-states.md) - the full state machine, transition rules, crash recovery semantics

## Reference

- [Install](install.md) - release artifacts, checksum verification, `go install`, and source build paths
- [CLI](cli.md) - generated from the real Cobra command tree
- [API](api.md) - endpoint list, error envelope, pagination, filtering, content types
- [Embedding](embedding.md) - using Wakeplane as a Go library in your application
- [Storage](storage.md) - SQLite local mode, Postgres production mode, and migration bridge
- [Runbook](runbook.md) - startup, health checks, shutdown, metrics, common failures
- [Releasing](releasing.md) - versioning, release checklist, breaking change definition
- [Security](security.md) - bearer-token auth, trusted-network requirements, planned work
- [Status](status.md) - beta gate, 1.0 gate, and explicit out-of-scope boundaries

## Current scope

The current public beta line ships as:

- Single-process Go daemon and CLI
- SQLite-first storage with embedded migrations and a Postgres backend seam
- HTTP, shell, and in-process workflow executors
- HTTP JSON API and Cobra CLI
- Planner and dispatcher loops with durable run ledger
- Metrics, health, readiness, and status endpoints
- Structured shutdown and drain logging

## Beta constraints

Wakeplane is beta because the release discipline is now real:

- docs must match code exactly
- release artifacts and checksums must be published from tags
- security posture must remain explicit
- example code must be copied from tested source or validated in CI

Not yet shipped:

- RBAC or multi-tenancy
- UI
- Distributed coordination
- Dynamic plugin loading

## Operational context

Wakeplane has a real local-operator deployment that remains an important proving ground.

That does not change the product boundary. Wakeplane is still intended to run as a standalone scheduler:

- on a personal machine
- inside a local operator environment
- in other small internal systems that need durable scheduling without a larger orchestration stack
