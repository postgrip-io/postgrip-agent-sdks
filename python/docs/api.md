# API

The full public surface re-exported from `postgrip_agent`.

## Top-level

| Name                                                                                                                       | Purpose                                                       |
|:---------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------|
| `Client`                                                                                                                   | High-level entrypoint; group of Task / Workflow / Schedule sub-clients. |
| `Connection`                                                                                                               | HTTP transport. Most code reaches for `Client.connect(...)` instead. |
| `Agent`                                                                                                                    | Polling agent; leases tasks and dispatches workflow / activity bodies. |
| `Worker`                                                                                                                   | Backwards-compat alias to `Agent` for code mirroring the Temporal naming. |
| `WorkflowHandle`, `WorkflowUpdateHandle`                                                                                   | Durable references to workflow runs / in-flight updates.     |
| `TaskClient`, `WorkflowClient`, `ScheduleClient`                                                                           | Sub-clients accessed via `client.task`, `client.workflow`, `client.schedule`. |

## Decorators

| Name                                                                       | Purpose                                                  |
|:---------------------------------------------------------------------------|:---------------------------------------------------------|
| `@workflow.defn`                                                           | Marks a class as a workflow definition. Triggers the AST sandbox. |
| `@workflow.run`                                                            | Marks the entrypoint coroutine on a `@workflow.defn` class.       |
| `workflow.define_signal(name)` / `define_query(name)` / `define_update(name)` | Declare a signal / query / update name; returns a definition.    |
| `workflow.set_handler(definition, handler)`                                | Bind a handler method to a definition. Call from `__init__` so each replay re-registers. |
| `@activity.defn`                                                           | Marks an async function as an activity definition.        |

## Workflow runtime helpers (call from inside `@workflow.run`)

| Name                                                                                | Purpose                                                  |
|:------------------------------------------------------------------------------------|:---------------------------------------------------------|
| `workflow.now()`                                                                    | Workflow-deterministic current time.                     |
| `await workflow.sleep(d)`                                                           | Durable timer. Replaces `asyncio.sleep` in workflow code.|
| `await workflow.execute_activity(name_or_fn, *args, ...)`                           | Schedule an activity, suspend until completion.          |
| `await workflow.execute_child(workflow_class, *args, ...)`                 | Schedule a child workflow.                               |
| `await workflow.condition(lambda: ..., timeout=None)`                          | Suspend until the predicate is true (or timeout).        |
| `workflow.continue_as_new(*args)`                                                   | Restart the current workflow with fresh history.         |
| `workflow.milestone(name, index=..., total=...)`                                    | Emit a milestone event for the workflow task.            |
| `workflow.info()`                                                                   | Workflow id, run id, task queue, type.                   |
| `CancellationScope`                                                                 | Wrap an `async with` block to scope cancellation.        |

## Activity helpers (call from inside an `@activity.defn` body)

| Name                                                                       | Purpose                                                  |
|:---------------------------------------------------------------------------|:---------------------------------------------------------|
| `await activity.heartbeat(details=None)`                                   | Long-running activities ping this on a timer. Raises `CancelledFailure` if the runtime service has requested cancellation. |
| `await activity.milestone(name, index=..., total=...)`                     | Emit a milestone event for the activity task.            |
| `await activity.stdout(data, stage=..., details=...)`                      | Emit stdout output for the current activity task.        |
| `await activity.stderr(data, stage=..., details=...)`                      | Emit stderr output for the current activity task.        |
| `activity.info()`                                                          | Task id, activity type.                                  |

## Errors

| Name                                                                       | Purpose                                                  |
|:---------------------------------------------------------------------------|:---------------------------------------------------------|
| `ApplicationFailure`                                                       | Structured failure with type tag + retryability + details.|
| `CancelledFailure`                                                         | Runtime cancelled the task.                              |
| `TimeoutFailure`                                                           | Operation exceeded its deadline.                         |
| `TaskFailedError`                                                          | Terminal task failure with the underlying `ApplicationFailure`.|
| `PostGripAgentError`                                                       | Wraps SDK-internal failures (transport, encode/decode).  |

Constructors / predicates: `ApplicationFailure(message, type=, non_retryable=, details=)`; check with `isinstance(err, ApplicationFailure)`.

## Wire types

The `postgrip_agent.types` module mirrors the Go [`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) wire shapes as Python dataclasses / TypedDicts. Common ones (`Task`, `WorkflowExecution`, `WorkflowHistoryEvent`, `RetryPolicy`, `Schedule`, `FailureInfo`) are also re-exported at the top level.

When in doubt, the code is the documentation:

- [`src/postgrip_agent/__init__.py`](https://github.com/postgrip-io/agent-sdk-python/blob/main/src/postgrip_agent/__init__.py) — full re-export list.
- [`src/postgrip_agent/types.py`](https://github.com/postgrip-io/agent-sdk-python/blob/main/src/postgrip_agent/types.py) — wire-type definitions.

## Type checking

The package ships with `py.typed` so [mypy](https://mypy-lang.org/) and [pyright](https://github.com/microsoft/pyright) pick up annotations on every public name without extra configuration.
