# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Go SDK for the PostGrip Agent runtime service. Mirrors `../typescript` and `../python`; wire shapes come from the local `../protocol` module through the repository's `go.work`. The root package (`package sdk`) holds only `doc.go` — customers import the sub-packages they need (`client`, `worker`, `workflow`, `activity`, `failure`).

The module path remains `go.postgrip.io/sdk`. Its vanity metadata still points at the legacy `agent-sdk-go` distribution repository; see `../MIGRATION.md` before changing or archiving that repository.

## Commands

```sh
go test ./...                                  # all tests across all packages
go test ./worker -run TestSleep -v             # single test or pattern in one package
go vet ./...
gofmt -l .                                     # CI fails if non-empty
```

Root CI runs `gofmt -l`, `go vet ./go/...`, `go test ./go/...`, and cross-language drift validation (see `../.github/workflows/ci.yml`). The workspace uses the local protocol module; keep the version in `go.mod` valid for standalone consumers.

## Architecture

### Package DAG (acyclic — keep it that way)

```
failure ─┐
workflow ┼─→ client ─→ worker
activity ┘            ↑
internal/replay ──────┘
internal/jsonenv ──→ client, worker
```

`failure`, `workflow`, `activity`, `internal/replay`, `internal/jsonenv` are leaves (depend only on `agent-sdk-protocol`). `client` adds `failure` and `workflow`. `worker` depends on all of them. Adding an import that creates a cycle (e.g., `workflow` reaching for `client`) is a sign you've put logic in the wrong package — see "Sentinel translation" below.

### Interface in `/workflow`, implementation in `/worker`

`workflow.Context` is the *interface* customer workflow bodies receive (`workflow/workflow.go`). It cannot import `client` (would cycle). The *implementation* `workflowContext` lives in `worker/workflow_context.go` because it ties together a `*client.Connection`, a `*replay.Replay`, and the customer's `workflow.Func`. The compile-time assertion `var _ workflow.Context = (*workflowContext)(nil)` at the bottom of that file ensures the impl never silently drifts from the interface.

When adding a method to the workflow surface: declare it on the interface in `/workflow`, implement it on `workflowContext` in `/worker`. Don't put implementation logic in `/workflow` itself.

Tests for workflow-context behavior live in `/worker` for the same reason — they exercise the impl. Tests for the *interface contract* (sentinels, options, `IsContinueAsNew` / `IsSuspended` predicates) live in `/workflow`.

### Workflow execution model

Each leased workflow task goes through:

1. Worker fetches the **full durable history** from the runtime service.
2. `replay.New(history)` builds a cursor walker.
3. A fresh `workflowContext` wraps it; the customer's `workflow.Func` is invoked.
4. Body's calls to `Sleep` / `ExecuteActivity` / `ExecuteChildWorkflow` consult the replay first:
   - History records the command → return persisted result, or `*workflow.Suspended` if still in-flight.
   - History exhausted → enqueue a fresh command and return `*workflow.Suspended`.
   - Body asks for a different command than recorded → `*replay.DeterminismError` → translated to a non-retryable `WorkflowDeterminismViolation` `failure.Application`.

**Suspension is not failure.** When the body returns `*workflow.Suspended`, the Worker calls `BlockTask` (not `FailTask`); the runtime service redelivers when dependencies resolve and the body re-runs from the top with fuller history. **This is the central invariant — workflow bodies must be deterministic and idempotent under re-invocation.** Anything that breaks determinism (random IDs, wall-clock time, map iteration order affecting commands) will surface as a `WorkflowDeterminismViolation` on the next replay.

**`ContinueAsNew` is also modeled as a sentinel error**, not a real failure. `ctx.ContinueAsNew(opts)` returns `*workflow.ContinueAsNewSentinel`; the Worker recognizes it via `errors.As` and translates to a runtime-service `ContinueAsNewResult`. Workflow bodies should `return ctx.ContinueAsNew(...)`.

### Sentinel translation at the SDK boundary

`internal/replay` returns its own private sentinel error types (`ErrCancellationRequested`, `*DeterminismError`). It does **not** know about `failure.Cancelled` or `failure.Application` — that's intentional, so replay stays a leaf and never depends on `failure` (otherwise any code reachable from `failure` could pull in replay).

The boundary translation happens in `worker/workflow_context.go` via `checkReplayCancellation` and `translateReplayError`. **Don't bypass them.** If you add a new replay sentinel kind, add the translation in those two helpers and keep replay's API typed in its own error vocabulary.

### Connection auth dual-mode

`client.Connection` handles two auth flows in one type:

- **Management endpoints** (Task / Workflow / Schedule / namespace CRUD): `Authorization: Bearer <ConnectionOptions.AuthToken>`.
- **Agent endpoints** (`/api/v1/agent/*` and `/api/v1/agents/*/poll`): a delegated refreshable access token injected by the host agent. `ensureAgentSession` runs implicitly before every agent-authenticated request (poll, heartbeat, complete/fail/block, emit-event), refreshing via the stored refresh token as needed. The SDK must not enroll standalone agents.

The `do(ctx, method, path, body, out, agentAuth bool)` method picks the auth header based on the boolean. Preserve that contract when changing transport. For tests against a stub server, use `Connection.SeedAgentSession(agentID, accessToken, expiresAt)` to install delegated runtime credentials.

### Activity runtime

Activity helpers (`activity.GetInfo`, `Heartbeat`, `Milestone`) read from a `*activity.Runtime` attached to the activity's `context.Context` via a private context key. The Worker calls `activity.WithRuntime(ctx, runtime)` before invoking the `activity.Func`. The helpers return an error when called outside an activity invocation — the context value isn't there. Don't add globals; new activity-side helpers should follow the same context-key pattern.

### JSON convention: marshal panics on failure

`internal/jsonenv.Marshal` **panics** if `json.Marshal` fails, instead of returning an error. The SDK only marshals types it controls (maps, structs with JSON-safe fields, customer args we treat as opaque), so a marshal failure is a programming error worth surfacing immediately rather than letting a corrupted wire payload propagate. Don't change this to error-returning without auditing every caller.

### Wire-shape re-exports

`client/aliases.go` re-exports the protocol's wire types (`Task`, `WorkflowExecution`, `Schedule`, etc.) so customer code doesn't have to import `agent-sdk-protocol` directly. When the protocol adds a new wire type that customer code needs to reference, add the alias here. Don't shadow the protocol type with an SDK-side struct unless you're deliberately diverging the shape — that's almost always wrong (loses the polyglot contract with the TS and Python SDKs).
