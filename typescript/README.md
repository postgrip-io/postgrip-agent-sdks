# PostGrip Agent TypeScript SDK

[![Docs](https://img.shields.io/badge/docs-site-2563EB?logo=readthedocs&logoColor=white)](https://postgrip-io.github.io/postgrip-agent-sdks/typescript/)
[![npm](https://img.shields.io/npm/v/%40postgrip%2Fagent.svg)](https://www.npmjs.com/package/@postgrip/agent)
[![TypeScript](https://img.shields.io/badge/types-TypeScript-3178C6?logo=typescript&logoColor=white)](https://www.npmjs.com/package/@postgrip/agent)
[![CI](https://github.com/postgrip-io/postgrip-agent-sdks/actions/workflows/ci.yml/badge.svg)](https://github.com/postgrip-io/postgrip-agent-sdks/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/postgrip-io/postgrip-agent-sdks.svg)](LICENSE)

This package provides a Temporal-style TypeScript API for defining, submitting, and executing PostGrip workflows. In production, SDK workflow runtimes are supervised by an existing PostGrip agent: the host agent launches the runtime, injects delegated credentials, and keeps generic operational tasks separate from workflow/activity task polling. Client-side SDK code submits `workflow.runtime` tasks to an existing agent pool; it does not enroll or spawn standalone PostGrip agents. The Go, Python, and shared protocol packages live alongside it in this monorepo.

**Docs:** [postgrip-io.github.io/postgrip-agent-sdks/typescript](https://postgrip-io.github.io/postgrip-agent-sdks/typescript/) — quick start, workflow runtime, API guide.

**Current release:** `0.12.3`

## Installation

```sh
npm install @postgrip/agent
```

The package supports Node.js 20 or newer and Bun 1.0 or newer. It is published
as ESM and exports its public API from `@postgrip/agent`.

## Layout

```text
src/                  # TypeScript sources — Connection / Client / workflow runtime
test/                 # Vitest unit, transport, OpenAPI, and packaging regression tests
docs/                 # customer documentation site sources
example/              # runnable workflow examples
../.github/workflows/ # monorepo CI and package release automation
```


It mirrors the common Temporal TypeScript shape documented in Temporal's TypeScript developer guide: a `Connection`, `Client`, `Agent`, registered Workflows, registered Activities, activity helpers such as `heartbeat` and `activityMilestone`, and workflow helpers such as `milestone`, `proxyActivities`, `executeChild`, `continueAsNew`, `sleep`, `condition`, `cancellationRequested`, and `workflowInfo`.

This is not a full Temporal replacement yet. The current PostGrip Agent runtime service supports durable JSON state, namespaces, workflow history, workflow ID reuse policies, memo/search attribute visibility metadata, activity tasks, activity heartbeats, activity cancellation on workflow cancellation, child workflows, continue-as-new, workflow run timeouts, timer tasks, durable schedules, durable signals, replayed queries, durable updates, durable cancellation requests, cancellation scopes, termination, and history replay for activity, child workflow, `sleep()`, retry, signal, query, update, and cancellation commands. Calendars, advanced search queries, and stronger deterministic sandboxing are still future work.

## Example

```ts
// workflow-runtime.ts
// This file is launched by a PostGrip host agent from a workflow.runtime task.
import { Agent, Client, Connection, proxyActivities } from '@postgrip/agent';

const activities = {
  async greet(name: string): Promise<string> {
    return `hello ${name}`;
  },
};

const { greet } = proxyActivities<typeof activities>({
  startToCloseTimeoutMs: 10_000,
  retry: { maximumAttempts: 3 },
});

export async function greetingWorkflow(name: string): Promise<string> {
  return greet(name);
}

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
  workflowIdReusePolicy: 'allow_duplicate_failed_only',
  args: ['PostGrip'],
  workflowRunTimeoutMs: 60_000,
  retry: { maximumAttempts: 3, initialIntervalMs: 1_000 },
  memo: { displayName: 'Greeting' },
  ui: {
    displayName: 'Greeting for PostGrip',
    description: 'Shown on the PostGrip Agents activity tab.',
    details: { customerId: 'cust-1', sdk: 'typescript' },
    tags: ['demo'],
  },
  searchAttributes: { customerId: 'cust-1' },
});

await agent.runUntil(resultPromise);
console.log(await resultPromise);
```

`ui` is SDK-owned console metadata. It is persisted inside workflow memo as `postgrip.ui`, so the Agents activity tab can show a friendly label, description, details, and tags while `memo` remains available for your own data.

Submit that runtime to an existing agent pool from your client process:

```ts
// submit-runtime.ts
import { Client, Connection } from '@postgrip/agent';

const connection = await Connection.connect({
  // Agent token from Settings > Organization > Agent tokens.
  headers: { Authorization: `Bearer ${process.env.POSTGRIP_AGENT_TOKEN}` },
});
const client = new Client({ connection });

await client.task.workflowRuntime({
  queue: 'default',
  command: 'node',
  args: ['dist/workflow-runtime.js'],
  runtimeQueue: 'default',
  env: {
    NODE_ENV: 'production',
  },
});
```

To run the runtime from an image instead of a host command, pass `image` — and
optionally an `isolation` floor of `'container'` (the default) or `'microvm'`:

```ts
await client.task.workflowRuntime({
  queue: 'default',
  image: 'ghcr.io/example/workflow-runtime:1.4.0',
  isolation: 'microvm',
  runtimeQueue: 'default',
});
```

`isolation` is a floor, not an exact match: `'container'` work may be scheduled
onto a stronger tier, but `'microvm'` work is never downgraded — it only leases
to agents advertising that tier. It requires `image`; a command-only runtime
executes directly on the agent host, which honors no isolation floor, and the
orchestrator rejects that combination at enqueue.

## Sandboxes

Sandboxes are persistent development environments assigned to one of your
agents. `client.sandbox` covers the lifecycle plus interactive and
non-interactive execution.

Sandbox endpoints use a **management token**, not an agent token — pass
`authToken` on the connection. Treat the value as opaque; the console issues a
bare hex string with no prefix.

```ts
const connection = await Connection.connect({
  baseUrl: 'https://agents.example.com',
  authToken: process.env.POSTGRIP_TOKEN,
});
const client = new Client({ connection });

const workspace = await client.sandbox.uploadWorkspace(archive, {
  repositoryName: 'my-repo',
  revision,
});
let box = await client.sandbox.create({
  name: 'task-1',
  image: 'postgrip/sandbox:1',
  workspaceId: workspace.id,
  setupCommand: ['/bin/sh', 'setup.sh'],
});

// create() returns as soon as the record exists; the sandbox is not up yet.
box = await client.sandbox.waitUntilRunning(box.id!);

const { exitCode, output } = await client.sandbox.exec(box.id!, ['wc', '-c'], {
  stdin: 'hello',
});

await client.sandbox.delete(box.id!);

// Uploaded workspaces are manageable once no live sandbox uses them.
const workspaces = await client.sandbox.listWorkspaces();
await client.sandbox.getWorkspace(workspace.id);
await client.sandbox.deleteWorkspace(workspace.id);
```

Deleting a workspace still referenced by a live sandbox rejects with `409 Conflict`.

`waitUntilRunning` treats readiness as `observedState === 'running'` **and**
`observedGeneration >= generation`. A "running" reading can predate a start or
stop the assigned agent hasn't observed yet, so state alone can hand back a
sandbox that is about to stop. It returns immediately if the sandbox reaches
`failed`, carrying the sandbox's own failure message.

### Interactive sessions

```ts
const session = await client.sandbox.openSession(box.id!, 'pty', { rows: 40, columns: 120 });
session.onData((chunk) => process.stdout.write(chunk));
session.send('ls -la\n');
const code = await session.exitCode();
```

Four relay properties that are not obvious:

- **stdout and stderr are interleaved.** The relay carries one byte stream, so
  they cannot be separated client-side.
- **There is no resize channel.** `rows`/`columns` are fixed at session
  creation; resizing your terminal mid-session cannot reach the sandbox.
- **Exec stdin can be half-closed.** `exec` sends EOF after its optional
  `stdin`. For a manually opened exec session, call `session.closeInput()`;
  output and exit-status delivery remain open.
- **Exit codes arrive as the WebSocket close status** (`4000 + code`), not in
  the stream. `exitCode()` resolves `undefined` when the close carried no exit
  code — that means the transport ended, not that the process succeeded.

Writes must stay at or below `SANDBOX_RELAY_MAX_FRAME_BYTES` (1 MiB); larger
frames throw locally rather than having the relay close the session.

On Node and Bun, the SDK uses a header-capable WebSocket transport so the relay
receives the connection's management token and custom headers. A custom
transport can be supplied with `webSocketFactory`; it receives those headers
explicitly. Browsers cannot attach headers to the native WebSocket handshake,
so browser sessions must use same-origin cookie authentication.

Inside a managed runtime, the workflow client can inspect and interact with workflows:

```ts
import { Client, Connection } from '@postgrip/agent';

const client = new Client({
  connection: await Connection.connect(),
});

const handle = client.workflow.getHandle<string>('greeting-workflow-id');
console.log(await handle.describe());

await client.workflow.signalWithStart(greetingWorkflow, {
  workflowId: 'greeting-workflow-id',
  taskQueue: 'default',
  args: ['PostGrip'],
  signal: 'poke',
  signalArgs: ['wake up'],
});

const workflows = await client.workflow.list({
  namespace: 'default',
  workflowType: 'greetingWorkflow',
  searchAttributes: { customerId: 'cust-1' },
  limit: 10,
  offset: 0,
});
const workflowCount = await client.workflow.count({
  namespace: 'default',
  workflowType: 'greetingWorkflow',
  searchAttributes: { customerId: 'cust-1' },
});

const tasks = await connection.listTasks({
  namespace: 'default',
  queue: 'default',
  type: 'workflow:greetingWorkflow',
  limit: 10,
});

const schedule = await client.schedule.createWorkflowSchedule({
  scheduleId: 'greeting-every-minute',
  namespace: 'default',
  taskQueue: 'default',
  workflow: greetingWorkflow,
  intervalSeconds: 60,
  overlapPolicy: 'skip',
  args: ['Scheduled PostGrip'],
});

console.log(await client.schedule.get(schedule.id));
await client.schedule.update(schedule.id, {
  spec: { interval_seconds: 300 },
  action: {
    queue: 'default',
    workflowType: 'greetingWorkflow',
    args: ['Updated schedule'],
  },
});
await client.schedule.backfill(schedule.id, {
  start_at: new Date(Date.now() - 900_000).toISOString(),
  end_at: new Date().toISOString(),
});
await client.schedule.pause(schedule.id, { reason: 'maintenance' });
const triggered = await client.schedule.trigger(schedule.id, { reason: 'manual run' });
await client.schedule.unpause(schedule.id);
```

## Schedules

Schedules are durable interval records. When a schedule becomes due, the agent runtime service creates an ordinary `workflow:<name>` task, records `WorkflowScheduled`, and advances `next_run_at`.
Paused schedules do not auto-create workflow tasks, but they can still be manually triggered. By default, schedules use `skip` overlap policy, so an automatic tick is skipped while an earlier workflow from the same schedule is still running. Use `overlapPolicy: 'allow_all'` when concurrent runs are intended.

```ts
await client.schedule.create({
  id: 'hourly-import',
  namespace: 'default',
  overlap_policy: 'allow_all',
  spec: { interval_seconds: 3600 },
  action: {
    queue: 'default',
    workflowType: 'greetingWorkflow',
    args: ['from schedule'],
  },
});
```

## Signals

```ts
const approveSignal = defineSignal<[string]>('approve');
const approverQuery = defineQuery<string>('approver');
const renameUpdate = defineUpdate<string, [string]>('rename');

export async function approvalWorkflow(): Promise<string> {
  let approvedBy = '';
  setHandler(approveSignal, (name) => {
    approvedBy = name;
  });
  setHandler(renameUpdate, (name) => {
    approvedBy = name;
    return approvedBy;
  });
  setHandler(approverQuery, () => approvedBy);
  await condition(() => approvedBy !== '');
  return approvedBy;
}

const handle = await client.workflow.start(approvalWorkflow, { taskQueue: 'default' });
console.log(await handle.query(approverQuery));
await handle.signal(approveSignal, 'alice');
console.log(await handle.query(approverQuery));
const updateHandle = await handle.startUpdate(renameUpdate, 'bob');
console.log(await updateHandle.result());
console.log(await handle.query(approverQuery));
await handle.cancel('no longer needed');
await handle.terminate('force stop');
```

`startUpdate` enqueues an `update:<workflowType>` task and returns a handle that can be awaited later. `executeUpdate` is the convenience form that starts the update and waits for the result. The agent replays workflow history, invokes the registered update handler, records the completed update in workflow history, and wakes the workflow task. Later workflow and query replays apply completed update events before continuing.

## Child Workflows

```ts
export async function childWorkflow(name: string): Promise<string> {
  return `hello ${name}`;
}

export async function parentWorkflow(name: string): Promise<string> {
  return await executeChild(childWorkflow, { args: [name] });
}
```

## Continue As New

```ts
export async function pagedWorkflow(page = 0): Promise<string> {
  if (page < 10) {
    continueAsNew(pagedWorkflow, { args: [page + 1] });
  }
  return 'done';
}
```

## Activity Heartbeats

```ts
const activities = {
  async importRows(rows: string[]): Promise<number> {
    let imported = 0;
    for (const [index, row] of rows.entries()) {
      imported += row.length;
      await heartbeat({ imported });
      await activityMilestone('import row', { index: index + 1, total: rows.length });
      await activityStdout(`imported row ${index + 1}\n`, { stage: 'importRows' });
    }
    return imported;
  },
};
```

Use milestones for ordered steps. A 10-step activity should emit one milestone per completed step; clients can call `handle.watchEvents()` or `client.task.watchEvents(taskId)` and render `kind === 'milestone'` events as a checklist. Use `activityStdout` and `activityStderr` to attach output to the current activity task for the console Activity detail view.

`startToCloseTimeoutMs` is durable. It is encoded as the activity task lease timeout and recorded in `ActivityTaskScheduled` with retry policy metadata. The agent renews activity leases while execution is in progress. If a leased activity misses its deadline, the agent runtime service records `ActivityTaskTimedOut`, fails the activity task, and wakes the blocked workflow task for replay.

The agent also performs basic workflow sandbox checks before execution and rejects common nondeterministic APIs such as `Date.now()`, `new Date()`, `Math.random()`, `crypto.randomUUID()`, `setTimeout()`, and `setInterval()` inside workflow functions. Generate random values and wall-clock timestamps in activities or pass them as explicit workflow inputs.

Workflow `retry` is durable on starts, child workflows, continue-as-new, and workflow schedules. A failed attempt records `WorkflowExecutionAttemptFailed`, the retry delay records `WorkflowExecutionRetryScheduled`, and `WorkflowHandle.result()` follows the active retry task until the workflow reaches a terminal state.

`Client.workflow.signalWithStart()` mirrors the common Temporal client shape: if the workflow does not exist, the agent runtime service creates the workflow execution and records the signal in the same durable history; if it is already running, the agent runtime service only appends the signal and unblocks the workflow task.

`WorkflowHandle.describe()` returns the workflow id, current task id, namespace, task queue, workflow type, status, attempt, run timeout, retry policy, memo, search attributes, timestamps, and terminal result/error when present.

For SDK applications, the documented client-side submission path is
`client.task.workflowRuntime(...)`; workflow and activity tasks are then
coordinated by the managed runtime launched on the host agent.
