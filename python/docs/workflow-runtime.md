# Workflow runtime

The PostGrip workflow runtime is *durable*: a workflow body can run for hours or days, survive agent restarts, recover from failed activities with retries, and react to signals delivered while it was paused. This page explains how that works in the Python SDK so you can write workflows that behave correctly under all of those conditions.

## The replay model

Every time the runtime service hands a workflow task to an agent, the agent:

1. Fetches the **full durable history** of the workflow from the runtime service.
2. Builds an in-memory cursor over that history.
3. Constructs a fresh workflow runtime context and **runs your `@workflow.run` coroutine from the top**.
4. Each `await` inside the coroutine that touches workflow APIs (`workflow.execute_activity`, `workflow.sleep`, `workflow.execute_child`, `workflow.condition`, signal channel reads) consults the replay cursor before scheduling anything new.

The cursor advances by one event per call (per command type). What happens at each call:

| Replay state for this command                                    | What happens                                                          |
|:-----------------------------------------------------------------|:----------------------------------------------------------------------|
| History records this exact command, completed                    | The persisted result is decoded and returned from the `await`.        |
| History records this exact command, still in flight              | The `await` suspends; the agent reports the task as blocked and waits for redelivery. |
| History exhausted past this point                                | The agent enqueues a fresh command and the `await` suspends.          |
| History records a *different* command at this position           | A non-retryable `ApplicationFailure` tagged `WorkflowDeterminismViolation` is raised. |

When the body suspends, the agent calls `BlockTask` on the runtime service. The workflow task moves to the **blocked** state — *not* failed. The runtime service redelivers the task whenever a dependency resolves (an activity completes, a timer fires, a signal arrives), at which point the agent re-runs your coroutine from the top with fuller history.

!!! warning "Workflow bodies must be deterministic"
    Because the body re-runs on every redelivery, anything that varies between runs — calling `time.time()`, generating wall-clock-driven random IDs, iterating a dict and using insertion order to schedule commands — eventually produces a `WorkflowDeterminismViolation`. Use `workflow.now()` for time, deterministic IDs, and stable iteration order (e.g. sort keys) when looping.

## The sandbox

The Python SDK ships with an AST-walking sandbox that rejects common nondeterministic APIs at workflow definition load time:

- `time.time()`, `time.monotonic()`, `time.sleep()`
- `asyncio.sleep()`
- `random.*()`
- `uuid.uuid4()`
- Direct `datetime.now()` / `datetime.utcnow()`

If your workflow imports any of these at the top of a file decorated with `@workflow.defn`, the decorator raises an `ApplicationFailure` with a helpful message pointing at the offending name and suggesting the safe replacement:

| Don't use         | Use instead                              |
|:------------------|:-----------------------------------------|
| `time.time()`     | `workflow.now()`                         |
| `time.sleep(n)` / `asyncio.sleep(n)` | `await workflow.sleep(n)`     |
| `random.random()` | An activity, or a deterministic seed     |
| `uuid.uuid4()`    | A deterministic ID, or an activity       |

The sandbox is a static check — it can't catch dynamically-imported nondeterministic code (e.g. a helper module that calls `time.time()` internally). Treat it as a guard rail, not a guarantee. If you need randomness or wall-clock time, do it inside an activity where it's allowed.

## Activities

Activities are the right place for non-deterministic work: HTTP calls, database queries, anything that touches the outside world or wall-clock state.

```python
from datetime import timedelta
from postgrip_agent import RetryPolicy, workflow

@workflow.defn
class MyWorkflow:
    @workflow.run
    async def run(self, user_id: str) -> str:
        resp = await workflow.execute_activity(
            "FetchUser",
            user_id,
            schedule_to_close_timeout=timedelta(seconds=30),
            retry_policy=RetryPolicy(maximum_attempts=5),
        )
        return resp["name"]
```

The runtime service handles retries based on `retry_policy`. From the workflow body's perspective, `execute_activity` either eventually returns the activity's result or raises the failure that exhausted retries.

If the activity raised an `ApplicationFailure(non_retryable=True)`, the runtime service skips retries. Use `raise ApplicationFailure(..., non_retryable=True)` from inside an activity for permanent errors (validation, "not found", etc.).

## Timers

`workflow.sleep(d)` is not `asyncio.sleep`. It enqueues a durable timer task with the runtime service:

```python
await workflow.sleep(timedelta(minutes=10))
```

The first time your body reaches that line, the timer is enqueued and the `await` suspends. When the timer fires, the runtime service redelivers, your body re-runs, and on the second pass `workflow.sleep` sees the recorded timer and returns immediately so execution continues past it.

## Child workflows

`workflow.execute_child` schedules a separate workflow execution and waits for its result. Same suspension semantics as `execute_activity`; the child runs its own replay loop.

## Signals, queries, and updates

Signals are inputs sent into a running workflow from outside. Queries are read-only state reads. Updates are synchronous-from-the-caller's-perspective handlers that may trigger commands. All three follow the same pattern: a top-level definition declares the name, then `workflow.set_handler` wires it to a method.

```python
from postgrip_agent import workflow, CancelledFailure

# Declare names at module scope so client and worker can reference them.
on_message = workflow.define_signal("on_message")
status = workflow.define_query("status")
replace_messages = workflow.define_update("replace_messages")


@workflow.defn
class MyWorkflow:
    def __init__(self):
        self._messages: list[str] = []
        # Register handlers from __init__ so each new replay re-registers them.
        workflow.set_handler(on_message, self._handle_message)
        workflow.set_handler(status, self._handle_status)
        workflow.set_handler(replace_messages, self._handle_replace)

    async def _handle_message(self, msg: str) -> None:
        self._messages.append(msg)

    def _handle_status(self) -> dict:
        # Query handlers are read-only — don't schedule commands here.
        return {"received": len(self._messages)}

    async def _handle_replace(self, msgs: list[str]) -> int:
        self._messages = list(msgs)
        return len(self._messages)

    @workflow.run
    async def run(self) -> list[str]:
        await workflow.condition(lambda: len(self._messages) >= 3)
        return self._messages
```

`workflow.condition` is the durable equivalent of polling: it suspends until the predicate is true, with the runtime service redelivering the task on every relevant history event.

From the client side, signals / queries / updates are sent through the workflow handle:

```python
await handle.signal("on_message", "hello")
state = await handle.query("status")
count = await handle.execute_update("replace_messages", ["a", "b"])
```

## Cancellation

When the runtime service receives a cancellation request, the next replay sees the corresponding history event. `workflow.execute_activity`, `workflow.sleep`, `workflow.execute_child`, and `workflow.condition` all check for cancellation before scheduling new commands and raise `CancelledFailure` if requested.

To cancel from the client side: `await handle.cancel("reason")`.

For activities to react to cancellation, periodically `await activity.heartbeat()` — when the runtime service has a cancellation request for an activity that's currently leased, the heartbeat call surfaces it as a `CancelledFailure`:

```python
from postgrip_agent import activity, CancelledFailure

@activity.defn
async def long_running(items: list) -> int:
    count = 0
    for item in items:
        try:
            await activity.heartbeat({"processed": count})
        except CancelledFailure:
            # Clean up partial work, then propagate.
            raise
        count += await process(item)
    return count
```

## ContinueAsNew

Long-running workflows accumulate history. Eventually that history gets big enough to slow down replay. The fix is `workflow.continue_as_new`: end the current run and atomically schedule a new run with a fresh history.

```python
@workflow.run
async def run(self, counter: int = 0) -> int:
    for _ in range(1000):
        # ... do work, schedule activities, etc.
        counter += 1
    if counter < 1_000_000:
        workflow.continue_as_new(counter)  # raises ContinueAsNewCommand
    return counter
```

`workflow.continue_as_new(...)` raises a sentinel exception that the agent translates to a runtime-service ContinueAsNewResult on completion. Don't catch it — let it propagate out.

## What happens on agent crash

If the agent crashes mid-task, the runtime service notices via heartbeat-loss and redelivers the task to another agent. Replay does the rest: the new agent runs your body from the top, sees the same history, and continues from where the previous agent left off.

This is why workflow bodies must be idempotent under re-invocation. If your body has a side effect outside of `execute_activity` (e.g. directly hitting a database from the workflow body), it will run again on every redelivery — and trip the sandbox if it's a forbidden API.
