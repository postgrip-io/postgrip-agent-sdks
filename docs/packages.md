---
title: Packages
layout: default
nav_order: 4
---

# Packages

The SDK ships as five focused public sub-packages plus two internal ones. Each public package owns a clear slice of the surface; the directory tree itself documents the shape.

```
go.postgrip.io/sdk/
├─ client/             Connection + Client + Task/Workflow/Schedule sub-clients
├─ worker/             Worker polling loop + workflow.Context implementation
├─ workflow/           Context interface, options, Func, SignalChannel
├─ activity/           Func, Info, GetInfo, Heartbeat, Milestone
├─ failure/            Application/Cancelled/Timeout/TaskFailed
└─ internal/
   ├─ replay/          Workflow replay engine (cursor over durable history)
   └─ jsonenv/         JSON helpers shared by client + worker
```

## Dependency graph

```
failure ──┐
workflow ─┼─→ client ──→ worker
activity ─┘             ↑
internal/replay ────────┘
internal/jsonenv ─→ client, worker
```

`failure`, `workflow`, `activity`, and the `internal/*` packages are leaves (depend only on `agent-sdk-protocol`). `client` adds `failure` + `workflow`. `worker` depends on all of them. The graph is acyclic and intentionally so.

---

## client

The customer-facing entrypoint for talking to the runtime service.

**Construction:**

```go
conn, _ := client.NewConnection(client.ConnectionOptions{Address: ..., AuthToken: ...})
c := client.New(conn)
```

**Sub-clients:**

| Path                        | Purpose                                                        |
|:----------------------------|:---------------------------------------------------------------|
| `c.Task.WorkflowRuntime`  | Submit a managed SDK runtime to an existing agent pool. |
| `c.Task.Get / List / Events / Result / WatchEvents`  | Inspect tasks; wait for results. |
| `c.Workflow.Start / SignalWithStart / GetHandle`     | Start workflows; reach existing ones. |
| `c.Schedule.Create / List / Get / Update / Pause / Unpause / Trigger / Backfill / Delete` | Manage scheduled workflows. |

Client-side SDK applications should submit workflow runtimes with
`c.Task.WorkflowRuntime`. Lower-level task helpers are runtime integration
primitives, not the documented SDK workflow submission path.

`WorkflowHandle` (returned from `Workflow.Start` and `Workflow.GetHandle`) is the durable reference to a workflow run: `Result`, `Describe`, `Signal`, `Cancel`, `Terminate`, `History`.

The package re-exports the wire types from `agent-sdk-protocol` (`Task`, `WorkflowExecution`, `Schedule`, etc.) so customer code stays within the `client` namespace.

## worker

The managed runtime worker. Use this only inside a process launched by a
`workflow.runtime` task; pure submitters don't need it.

```go
w, _ := worker.New(worker.Options{
    Connection: conn,
    Queue:      "default",
    Workflows:  workflow.Registry{"Greet": GreetWorkflow},
    Activities: activity.Registry{"GreetActivity": GreetActivity},
})
w.Run(ctx)
```

`Run` polls forever; `Shutdown(ctx, drainTimeout)` stops the poll loop and waits for in-flight tasks to drain. Concurrency is bounded by `MaxConcurrentTasks` (default 4).

The `workflow.Context` implementation lives in this package — it ties together a `*client.Connection`, the `internal/replay` engine, and the customer's workflow body.

## workflow

The interface every customer workflow body receives, plus the option types and signal channel.

```go
func GreetWorkflow(ctx workflow.Context, args []any) (any, error) {
    var greeting string
    if err := ctx.ExecuteActivity("GreetActivity", args, &greeting, nil); err != nil {
        return nil, err
    }
    return greeting, nil
}
```

**Key types:**

- `Context` — the workflow-scoped context. `Sleep`, `ExecuteActivity`, `ExecuteChildWorkflow`, `GetSignalChannel`, `SetQueryHandler`, `SetUpdateHandler`, `Milestone`, `ContinueAsNew`.
- `ActivityOptions`, `ChildWorkflowOptions`, `ContinueAsNewOptions` — per-call configuration. Zero values mean "use runtime defaults".
- `SignalChannel` — pre-seeded from durable history on each replay; `Receive` drains then suspends.
- `Func`, `Registry` — the function shape and registration map workers consume.
- `IsSuspended`, `IsContinueAsNew` — predicates for advanced control flow.

`Context` cannot import `client`, so the implementation lives in `worker`. The interface contract is checked at compile time in worker via `var _ workflow.Context = (*workflowContext)(nil)`.

## activity

The function shape activities satisfy and the helpers customer activity code calls inside its body.

```go
func GreetActivity(ctx context.Context, args []any) (any, error) {
    info, _ := activity.GetInfo(ctx)
    activity.Heartbeat(ctx, map[string]any{"step": "greeting"})
    activity.Stdout(ctx, "generated greeting\n", activity.OutputOptions{Stage: "greeting"})
    return "hello, " + args[0].(string), nil
}
```

**Key types:**

- `Func`, `Registry` — the activity equivalent of `workflow.Func`.
- `Info` — runtime metadata about the in-flight activity (task ID, agent ID, attempt, args).
- `GetInfo`, `Heartbeat`, `Milestone`, `Stdout`, `Stderr` — read or emit events from inside the activity body. `Stdout` and `Stderr` attach task output to the current activity task for the console Activity detail view. Each errors if called outside an activity invocation.

The Worker constructs an `activity.Runtime` per task and attaches it to the context via `WithRuntime` before invoking your `Func`. The helpers read it back out of the context.

## failure

Structured failure types that flow through workflows, activities, and task results.

```go
return nil, failure.NewNonRetryable("not allowed", "Forbidden", userID)
```

**Types:**

- `Application` — the structured workflow/activity failure. Carries `Type`, `NonRetryable`, `Details`.
- `Cancelled` — the runtime cancelled the task.
- `Timeout` — operation exceeded its deadline.
- `TaskFailed` — terminal task failure with an underlying `*Application`.
- `SDKError` — wraps SDK-internal failures (transport, encode/decode).

**Predicates and constructors:**

- `IsApplication`, `IsCancelled`, `IsTimeout`.
- `NewApplication` (retryable), `NewNonRetryable`.
- `FromInfo`, `ToInfo` — wire-format conversions; rarely called by customers.

The runtime service uses `Application.NonRetryable` to decide whether to retry an activity. Set it intentionally.

## internal/replay

The workflow replay engine. Walks durable history; serves persisted command results to the workflow body; raises determinism violations. Customers don't use this directly.

The engine deliberately returns *its own* sentinel errors (`ErrCancellationRequested`, `*DeterminismError`); the `worker` package translates those to `failure.Cancelled` and `failure.Application` at the SDK boundary. This keeps replay free of SDK error types and avoids a dependency cycle.

## internal/jsonenv

Tiny JSON helpers — `Marshal` (panics on failure, used for wire payloads) and `DecodeResult` (decodes `TaskResult.Value` into a target). Both `client` and `worker` need them; living in `internal/` keeps them off the public API surface.
