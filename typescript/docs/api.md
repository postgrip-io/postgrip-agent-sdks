# API

Public surface re-exported from `@postgrip/agent`. Customer code should import every name from the package root — sub-paths under `dist/` aren't part of the supported API.

## Top-level

| Name                                                                                                                       | Purpose                                                       |
|:---------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------|
| `Client`                                                                                                                   | High-level entrypoint; group of `task` / `workflow` / `schedule` sub-clients. |
| `Connection`                                                                                                               | HTTP transport. `Connection.connect({ baseUrl, headers })`.   |
| `Agent`                                                                                                                    | Polling agent; leases tasks and dispatches workflow / activity functions. |
| `Worker`                                                                                                                   | Re-export of `Agent` for code mirroring Temporal's naming.    |
| `WorkflowHandle`, `WorkflowUpdateHandle`                                                                                   | Durable references to workflow runs / in-flight updates.      |
| `TaskClient`, `WorkflowClient`, `ScheduleClient`                                                                           | Sub-clients accessed via `client.task`, `client.workflow`, `client.schedule`. |

## Workflow runtime helpers (call from inside a workflow function)

| Name                                                                          | Purpose                                                  |
|:------------------------------------------------------------------------------|:---------------------------------------------------------|
| `workflowInfo()`                                                              | Workflow id, run id, task queue, type, deterministic `now`. |
| `await sleep(ms)`                                                             | Durable timer.                                           |
| `proxyActivities<T>(options)`                                                 | Returns a typed proxy whose calls schedule activities.    |
| `await executeChild(workflowFn, options)`                                     | Schedule a child workflow.                                |
| `await condition(predicate, timeoutMs?)`                                      | Suspend until the predicate is true (or timeout).         |
| `cancellationRequested()`                                                     | True if the runtime requested cancellation.               |
| `defineSignal<Args>(name)` / `defineQuery<R, Args>(name)` / `defineUpdate<R, Args>(name)` | Declare a signal / query / update; returns a definition. |
| `setHandler(definition, handler)`                                             | Bind a handler to a definition.                           |
| `await milestone(name, options)`                                              | Emit a milestone event for the workflow task.             |
| `continueAsNew(workflowFn, ...args)`                                          | Restart the current workflow with fresh history (throws).  |
| `validateWorkflowSandbox(workflowFn)`                                         | Static sandbox check; throws if the body uses banned APIs. |
| `CancellationScope`                                                           | Class for scoping cancellation behavior.                  |

## Activity helpers (call from inside an activity body)

| Name                                                                       | Purpose                                                  |
|:---------------------------------------------------------------------------|:---------------------------------------------------------|
| `activityInfo()`                                                           | Task id, activity type.                                  |
| `await heartbeat(details?)`                                                | Heartbeat the activity. Throws `CancelledFailure` if the runtime requested cancellation. |
| `await activityMilestone(name, options)`                                   | Emit a milestone event for the activity task. (Re-export of `activity.ts`'s `milestone`, renamed to avoid clash with the workflow-side `milestone`.) |
| `await activityStdout(data, options?)`                                     | Emit stdout output for the current activity task.        |
| `await activityStderr(data, options?)`                                     | Emit stderr output for the current activity task.        |

## Errors

| Name                                                                       | Purpose                                                  |
|:---------------------------------------------------------------------------|:---------------------------------------------------------|
| `ApplicationFailure`                                                       | Structured failure with type tag, retryability, details. |
| `CancelledFailure`                                                         | Runtime cancelled the task.                              |
| `TimeoutFailure`                                                           | Operation exceeded its deadline.                         |
| `TaskFailedError`                                                          | Terminal task failure with the underlying `ApplicationFailure`. |
| `PostGripAgentError`                                                       | Wraps SDK-internal failures (transport, encode/decode).  |

Constructors: `new ApplicationFailure(message, { type, nonRetryable, details })`. Check with `instanceof ApplicationFailure`.

## Wire types

The package re-exports the wire-format types defined in `src/types.ts`, mirroring the Go [`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol). Common ones (`Task`, `WorkflowExecution`, `WorkflowHistoryEvent`, `RetryPolicy`, `Schedule`, `FailureInfo`) and their request/response counterparts are all `import type { ... }`-able from `@postgrip/agent`.

## TypeScript

The package ships with type declarations (`dist/index.d.ts`). All public functions and classes are fully typed; activity proxies generated by `proxyActivities<typeof activities>(...)` carry the call signatures of the activity functions in the bag.

## Source

When in doubt, the code is the documentation:

- [`src/index.ts`](https://github.com/postgrip-io/agent-sdk-typescript/blob/main/src/index.ts) — full re-export list.
- [`src/types.ts`](https://github.com/postgrip-io/agent-sdk-typescript/blob/main/src/types.ts) — wire-type definitions.
- [`src/workflow.ts`](https://github.com/postgrip-io/agent-sdk-typescript/blob/main/src/workflow.ts), [`src/activity.ts`](https://github.com/postgrip-io/agent-sdk-typescript/blob/main/src/activity.ts) — runtime helpers.
