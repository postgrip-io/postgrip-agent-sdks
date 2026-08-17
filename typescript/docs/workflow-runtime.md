# Workflow runtime

The PostGrip workflow runtime is *durable*: a workflow body can run for hours or days, survive agent restarts, recover from failed activities with retries, and react to signals delivered while it was paused. This page explains how that works in the TypeScript SDK so you can write workflows that behave correctly under all of those conditions.

## The replay model

Every time the runtime service hands a workflow task to an agent, the agent:

1. Fetches the **full durable history** of the workflow from the runtime service.
2. Builds an in-memory cursor over that history.
3. Constructs a fresh workflow runtime context and **calls your workflow function from the top**, passing it the workflow args.
4. Awaits inside the function that touch workflow APIs (`sleep`, calls on `proxyActivities` proxies, `executeChild`, `condition`, signal handler awaits) consult the replay cursor before scheduling anything new.

The cursor advances by one event per call (per command type). What happens at each call:

| Replay state for this command                                    | What happens                                                          |
|:-----------------------------------------------------------------|:----------------------------------------------------------------------|
| History records this exact command, completed                    | The persisted result is decoded and the awaited promise resolves with it. |
| History records this exact command, still in flight              | The promise stays pending; the agent reports the task as blocked and waits for redelivery. |
| History exhausted past this point                                | The agent enqueues a fresh command and the promise stays pending.     |
| History records a *different* command at this position           | A non-retryable `ApplicationFailure` tagged `WorkflowDeterminismViolation` is thrown. |

When the function's outermost promise stays pending past its scheduled commands, the agent calls `BlockTask` on the runtime service. The workflow task moves to the **blocked** state — *not* failed. The runtime service redelivers the task whenever a dependency resolves (an activity completes, a timer fires, a signal arrives), at which point the agent re-runs your function from the top with fuller history.

!!! warning "Workflow functions must be deterministic"
    Because the function re-runs on every redelivery, anything that varies between runs — calling `Date.now()`, generating wall-clock-driven random IDs, iterating an object with non-stable key order to schedule commands — eventually produces a `WorkflowDeterminismViolation`. Use `workflowInfo()`'s `now` for time, deterministic IDs, and stable iteration order (e.g. sorted keys) when looping.

## The sandbox

`validateWorkflowSandbox(workflowFn)` is exported and walks the workflow body's source for nondeterministic API patterns:

- `Math.random()`
- `Date.now()` / `new Date()` / `Date()`
- `setTimeout` / `setInterval`
- `crypto.randomUUID()`

If your workflow uses any of these directly, the validator throws `ApplicationFailure` with a message pointing at the offending API. Run it once during agent startup or as part of your workflow registration code:

```ts
import { validateWorkflowSandbox } from '@postgrip/agent';

const workflows = { greetingWorkflow };
for (const wf of Object.values(workflows)) {
  validateWorkflowSandbox(wf);
}
```

| Don't use         | Use instead                              |
|:------------------|:-----------------------------------------|
| `Date.now()`      | `workflowInfo().now`                     |
| `setTimeout(_, n)` | `await sleep(n)`                        |
| `Math.random()`   | An activity, or a deterministic seed     |
| `crypto.randomUUID()` | A deterministic ID, or an activity   |

The sandbox is a static check — it can't catch dynamically-imported nondeterministic code (e.g. a helper module that calls `Date.now()` internally). Treat it as a guard rail, not a guarantee. If you need randomness or wall-clock time, do it inside an activity where it's allowed.

## Activities

Activities are the right place for non-deterministic work: HTTP calls, database queries, anything that touches the outside world or wall-clock state.

```ts
import { proxyActivities } from '@postgrip/agent';

const activities = {
  async fetchUser(id: string): Promise<{ name: string }> {
    const resp = await fetch(`https://api.example.com/users/${id}`);
    return resp.json();
  },
};

const { fetchUser } = proxyActivities<typeof activities>({
  startToCloseTimeoutMs: 30_000,
  retry: { maximumAttempts: 5 },
});

export async function myWorkflow(userId: string): Promise<string> {
  const user = await fetchUser(userId);
  return user.name;
}
```

The runtime service handles retries based on the proxy's `retry` option. From the workflow's perspective, the activity call either eventually returns its result or throws the failure that exhausted retries.

If an activity throws an `ApplicationFailure({ nonRetryable: true })`, the runtime skips retries. Use that pattern for permanent errors (validation, "not found", etc.).

## Timers

`sleep(ms)` is **not** `setTimeout`. It enqueues a durable timer task with the runtime service:

```ts
import { sleep } from '@postgrip/agent';

await sleep(60_000); // ten minutes
```

The first time your workflow reaches the line, the timer is enqueued and the awaited promise stays pending. When the timer fires, the runtime service redelivers, your function re-runs, and on the second pass `sleep` sees the recorded timer in history and returns immediately.

## Child workflows

`executeChild` schedules a separate workflow execution and waits for its result:

```ts
import { executeChild } from '@postgrip/agent';

export async function parentWorkflow(): Promise<string> {
  const child = await executeChild(childWorkflow, {
    workflowId: 'child-id',
    args: ['hello'],
  });
  return child;
}
```

Same suspension semantics as activity calls; the child runs its own replay loop.

## Signals, queries, and updates

Signals are inputs sent into a running workflow from outside. Queries are read-only state reads. Updates are synchronous-from-the-caller's-perspective handlers that may trigger commands. All three follow the same pattern: a top-level definition declares the name, then `setHandler` wires it to a function inside the workflow body.

```ts
import { defineSignal, defineQuery, defineUpdate, setHandler, condition } from '@postgrip/agent';

// Declare names at module scope so client and worker can reference them.
export const onMessage = defineSignal<[message: string]>('on_message');
export const status = defineQuery<{ received: number }>('status');
export const replaceMessages = defineUpdate<number, [messages: string[]]>('replace_messages');

export async function chatWorkflow(): Promise<string[]> {
  const messages: string[] = [];

  setHandler(onMessage, (msg: string) => {
    messages.push(msg);
  });

  setHandler(status, () => ({ received: messages.length }));

  setHandler(replaceMessages, (msgs: string[]) => {
    messages.length = 0;
    messages.push(...msgs);
    return messages.length;
  });

  await condition(() => messages.length >= 3);
  return messages;
}
```

`condition(predicate, timeoutMs?)` is the durable equivalent of polling: it suspends until the predicate is true, with the runtime service redelivering the task on every relevant history event.

From the client side, signals / queries / updates are sent through the workflow handle:

```ts
await handle.signal('on_message', 'hello');
const state = await handle.query('status');
const count = await handle.executeUpdate('replace_messages', ['a', 'b']);
```

## Cancellation

When the runtime service receives a cancellation request, the next replay sees the corresponding history event. `sleep`, proxy-activity calls, `executeChild`, and `condition` all check for cancellation before scheduling new commands and throw `CancelledFailure` if requested.

To cancel from the client side: `await handle.cancel('reason')`.

For activities to react to cancellation, periodically `await heartbeat()` — when the runtime service has a cancellation request for an activity that's currently leased, the heartbeat throws `CancelledFailure`:

```ts
import { heartbeat, CancelledFailure } from '@postgrip/agent';

async function longRunning(items: unknown[]): Promise<number> {
  let count = 0;
  for (const item of items) {
    try {
      await heartbeat({ processed: count });
    } catch (e) {
      if (e instanceof CancelledFailure) {
        // Clean up partial work, then propagate.
        throw e;
      }
      throw e;
    }
    count += await process(item);
  }
  return count;
}
```

## ContinueAsNew

Long-running workflows accumulate history. Eventually that history gets big enough to slow down replay. The fix is `continueAsNew`: end the current run and atomically schedule a new run with a fresh history.

```ts
import { continueAsNew } from '@postgrip/agent';

export async function longRunner(counter = 0): Promise<number> {
  for (let i = 0; i < 1000; i++) {
    // ... do work, schedule activities, etc.
    counter++;
  }
  if (counter < 1_000_000) {
    continueAsNew(longRunner, counter); // throws ContinueAsNewCommand
  }
  return counter;
}
```

`continueAsNew(...)` throws a `ContinueAsNewCommand` exception that the agent catches and translates into a runtime-service `ContinueAsNewResult`. Don't `try/catch` it — let it propagate out.

## What happens on agent crash

If the agent crashes mid-task, the runtime service notices via heartbeat-loss and redelivers the task to another agent. Replay does the rest: the new agent calls your workflow function from the top, sees the same history, and continues from where the previous agent left off.

This is why workflow functions must be idempotent under re-invocation. If your function has a side effect outside of activity calls (e.g. directly hitting a database from the workflow body), it will run again on every redelivery — and trip the sandbox if it's a forbidden API.
