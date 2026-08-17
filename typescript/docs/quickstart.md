# Quick start

Two pieces make up the normal SDK flow: a client submits a managed
`workflow.runtime` task to an existing PostGrip agent pool, and that managed
runtime registers workflow and activity functions.

## Submit a workflow runtime

A client process uses an Agent token from Settings > Organization > Agent tokens
and submits a `workflow.runtime` task. The host PostGrip agent launches the
runtime process and injects delegated credentials.

```ts
import { Client, Connection } from '@postgrip/agent';

const connection = await Connection.connect({
  // Agent token from Settings > Organization > Agent tokens.
  headers: { Authorization: `Bearer ${process.env.POSTGRIP_AGENT_TOKEN}` },
});

const client = new Client({ connection });

const task = await client.task.workflowRuntime({
  queue: 'default',
  command: 'node',
  args: ['dist/workflow-runtime.js'],
  runtimeQueue: 'default',
  env: {
    NODE_ENV: 'production',
  },
});
console.log('submitted workflow runtime', task.id);
```

!!! note
    The SDK does not enroll standalone PostGrip agents. It submits workflow runtimes to agent pools that are already enrolled in PostGrip.

## Run a managed workflow runtime worker

The runtime process is launched by a host agent from the `workflow.runtime`
task. Inside that process, an SDK `Agent` registers workflow and activity
functions, then polls for workflow/activity tasks using delegated credentials.

```ts
import {
  Agent,
  Client,
  Connection,
  proxyActivities,
} from '@postgrip/agent';

// Activities are plain async functions. Inside the body, activity helpers
// (heartbeat, activityMilestone, activityStdout, activityInfo) work via
// the per-task runtime the agent attaches.
const activities = {
  async greet(name: string): Promise<string> {
    return `Hello, ${name}`;
  },
};

// proxyActivities returns a typed proxy. Calling greet(...) inside a
// workflow schedules the activity via the runtime.
const { greet } = proxyActivities<typeof activities>({
  startToCloseTimeoutMs: 10_000,
  retry: { maximumAttempts: 3 },
});

// Workflows are regular async functions.
export async function greetingWorkflow(name: string): Promise<string> {
  return greet(name);
}

// The host agent injects delegated runtime credentials.
const connection = await Connection.connect();

const agent = await Agent.create({
  connection,
  namespace: 'default',
  taskQueue: 'default',
  workflows: { greetingWorkflow },
  activities,
  maxConcurrentTaskExecutions: 8,
});

const client = new Client({ connection });
const resultPromise = client.workflow.execute(greetingWorkflow, {
  namespace: 'default',
  taskQueue: 'default',
  workflowId: 'greeting-workflow-id',
  args: ['PostGrip'],
});

// runUntil starts the runtime worker and resolves when the supplied promise
// does. For long-lived runtimes, use agent.run() and wire your own shutdown
// signaling.
await agent.runUntil(resultPromise);
console.log(await resultPromise);
```

The SDK `Agent` loops inside the managed runtime, leasing tasks from the configured queue, heartbeating each leased task, and dispatching to your registered functions. Concurrency is bounded by `maxConcurrentTaskExecutions` (default 4).

## Start a workflow from elsewhere

From the client side, start the workflow you registered above and read its result via a handle:

```ts
const handle = await client.workflow.start(greetingWorkflow, {
  workflowId: 'greeting-2',
  taskQueue: 'default',
  args: ['world'],
});

const result: string = await handle.result();
console.log(result); // Hello, world
```

`start` returns a `WorkflowHandle` — use it to wait, signal, query, update, cancel, terminate, or read history.

## Streaming events

Tasks emit ordered events (started / heartbeat / milestone / progress / stdout / stderr / completed / failed). To stream them as they land:

```ts
for await (const event of handle.watchEvents()) {
  console.log(event.kind, event.message);
}
```

The async iterator closes when the task reaches a terminal state.

## Where to next

- [Workflow runtime](workflow-runtime.md) — the durable replay model: how `sleep` / proxy-activity calls work under the hood, what determinism means, the sandbox, signals and queries, ContinueAsNew.
- [API](api.md) — the public surface re-exported from `@postgrip/agent`.
