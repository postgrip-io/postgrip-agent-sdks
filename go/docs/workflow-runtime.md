---
title: Workflow runtime
layout: default
nav_order: 5
---

# Workflow runtime

The PostGrip workflow runtime is *durable*: a workflow body can run for hours or days, survive worker restarts, recover from failed activities with retries, and react to signals delivered while it was paused. This page explains how that works in the Go SDK so you can write workflows that behave correctly under all of those conditions.

## The replay model

Every time the runtime service hands a workflow task to a worker, the worker:

1. Fetches the **full durable history** of the workflow from the runtime service.
2. Builds an in-memory cursor (`internal/replay.Replay`) over that history.
3. Constructs a fresh `workflow.Context` and **runs your workflow body from the top**, passing it that context.
4. Each call from your body to `ctx.Sleep`, `ctx.ExecuteActivity`, `ctx.ExecuteChildWorkflow`, or `ctx.GetSignalChannel().Receive()` consults the replay cursor before doing anything new.

The cursor advances by one event per call (per command type). What happens at each call:

| Replay state for this command                                    | What happens                                                          |
|:-----------------------------------------------------------------|:----------------------------------------------------------------------|
| History records this exact command, and it completed             | The persisted result is decoded into your `target`.                   |
| History records this exact command, still in flight              | The body returns `*workflow.Suspended`.                                |
| History is exhausted past this point                             | The worker enqueues a fresh command and the body returns `*workflow.Suspended`. |
| History records a *different* command at this position           | The worker raises a non-retryable `WorkflowDeterminismViolation` failure. |

When the body returns `Suspended`, the worker calls `BlockTask` on the runtime service. The workflow task moves to the **blocked** state — *not* failed. The runtime service redelivers the task whenever a dependency resolves (an activity completes, a timer fires, a signal arrives), at which point the worker re-runs your body from the top with fuller history.

{: .important }
> **Workflow bodies must be deterministic.** Because the body re-runs on every redelivery, anything that varies between runs — calling `time.Now()` directly, reading wall-clock-driven random IDs, iterating a map and using order to schedule commands — will eventually produce a `WorkflowDeterminismViolation`. Use `ctx.Now()` for time, deterministic IDs for any per-step identifiers, and stable iteration order (e.g. sort keys) when looping.

## Activities

Activities are the right place for non-deterministic work: HTTP calls, database queries, anything that touches the outside world or wall-clock state.

```go
func MyWorkflow(ctx workflow.Context, args []any) (any, error) {
    var resp ApiResponse
    err := ctx.ExecuteActivity("FetchUser", []any{userID}, &resp, &workflow.ActivityOptions{
        LeaseTimeoutSeconds: 30,
        Retry:               &workflow.RetryPolicy{MaximumAttempts: 5},
    })
    if err != nil {
        return nil, err
    }
    return resp.Name, nil
}
```

The runtime service handles retries based on `RetryPolicy`. From the workflow body's perspective, `ExecuteActivity` either eventually returns the activity's result or returns the failure that exhausted retries — it doesn't matter how many attempts happened underneath.

If the activity returned a `failure.Application` with `NonRetryable: true`, the runtime service skips retries and surfaces the failure immediately. Use `failure.NewNonRetryable(...)` from inside an activity for permanent errors (validation failures, "not found", etc.).

## Timers

`ctx.Sleep(d)` is not `time.Sleep`. It enqueues a durable timer task with the runtime service:

```go
if err := ctx.Sleep(10 * time.Minute); err != nil {
    return nil, err
}
```

The first time your body reaches that line, the timer is enqueued and the body returns `Suspended`. When the timer fires, the runtime service redelivers, your body re-runs, and on the second pass `ctx.Sleep` sees the recorded `TimerFired` event and returns nil immediately so execution continues past the sleep.

The `duration` argument is part of the determinism check — varying it between runs raises a violation.

## Child workflows

`ctx.ExecuteChildWorkflow` schedules a separate workflow execution and waits for its result. The semantics mirror `ExecuteActivity` (history-recorded, suspends, returns result on the lease that observes completion) but the child runs its own replay loop.

## Signals

Signals are inputs sent into a running workflow from outside. Workflow code receives them through a channel:

```go
ch := ctx.GetSignalChannel("ready")
args, err := ch.Receive(ctx)
if err != nil {
    return nil, err
}
```

`Receive` is **non-blocking** in the traditional sense. On replay, the channel is pre-seeded with every signal recorded in history; `Receive` drains the buffer one entry per call. When the buffer empties, `Receive` returns `Suspended`. The worker blocks the task; the runtime service redelivers when a new signal arrives; the next replay re-seeds the buffer with the additional signal and the body proceeds.

This means: **`Receive` doesn't actually wait for new signals during a single workflow execution.** It returns whatever's already buffered, then suspends until the runtime service has more.

## ContinueAsNew

Long-running workflows accumulate history. Eventually that history gets big enough to make replay slow and history-fetch round-trips expensive. The fix is `ContinueAsNew`: end the current run and atomically schedule a new run with a fresh history.

```go
func LongRunner(ctx workflow.Context, args []any) (any, error) {
    counter, _ := args[0].(int)
    for i := 0; i < 1000; i++ {
        // ... do work, schedule activities, etc.
        counter++
    }
    if counter < 1_000_000 {
        return ctx.ContinueAsNew(workflow.ContinueAsNewOptions{
            Args: []any{counter},
        })
    }
    return counter, nil
}
```

`ContinueAsNew` is modeled as an error sentinel internally so workflow code can `return` it directly. The worker recognizes the sentinel and tells the runtime service to schedule the new run instead of marking the current one failed. Customer code typically just writes `return ctx.ContinueAsNew(...)`.

## Cancellation

When the runtime service receives `WorkflowCancellationRequested`, the next replay will see that history event. `ctx.Sleep`, `ctx.ExecuteActivity`, and `ctx.ExecuteChildWorkflow` all check for cancellation before scheduling new commands and return `*failure.Cancelled` if requested. Customer code can check with `failure.IsCancelled(err)`.

To cancel from the client side: `handle.Cancel(ctx, "reason")`.

For activities to know about cancellation, the activity body should respect `ctx.Done()` on its `context.Context` — that's the standard Go cancellation channel, propagated through.

## What happens on worker crash

If the worker crashes mid-task, the runtime service notices via heartbeat-loss and redelivers the task to another worker. Replay does the rest: the new worker runs your body from the top, sees the same history, and continues from where the previous worker left off.

This is why workflow bodies must be idempotent under re-invocation. If your body has a side effect outside of `ctx.ExecuteActivity` (e.g. directly hitting a database from the workflow body), it will run again on every redelivery.

## Querying state

Workflow bodies can register read-only query handlers:

```go
ctx.SetQueryHandler("status", func(args []any) (any, error) {
    return currentStatus, nil
})
```

Queries don't trigger any commands — they read in-memory state during replay. The runtime service routes a `query:<name>` task to the worker, which replays the workflow up to the registration point and invokes the handler.

{: .warning }
> Query / update task dispatch isn't yet wired through `Worker` in this SDK version. The handlers register fine but executing them requires a workflow runtime that keeps state across queries — that's on the roadmap. For now, query/update task types surface as unsupported in `Worker.dispatch`.

## Updates

Updates are like signals but synchronous from the client's perspective. The client blocks until the update handler returns; the handler may schedule commands. Like queries, registration works in this SDK but dispatch is gated behind the same future Worker capability.
