// End-to-end runnable example for the PostGrip Agent TypeScript SDK.
//
// Running it locally submits a workflow.runtime task to an existing agent
// pool. When the host agent launches the runtime, it registers one activity
// (`greet`) and one workflow function (`greetingWorkflow`) with delegated
// agent credentials.
//
// Run:
//
//   cp example/.env.example .env
//   # edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
//   bun run example/greeting.ts
//
// The SDK does not enroll standalone agents; host agents inject delegated
// managed-runtime credentials.

import {
  Agent,
  Client,
  Connection,
  proxyActivities,
  type ActivityRegistry,
  type WorkflowRegistry,
} from '@postgrip/agent';
import { loadExampleEnv } from './env';

loadExampleEnv(import.meta.url);

const DEFAULT_RUNTIME_IMAGE = 'oven/bun:1';
const DEFAULT_RUNTIME_COMMAND = 'sh';
const DEFAULT_RUNTIME_ARGS = [
  '-lc',
  'apt-get update >/dev/null && apt-get install -y git >/dev/null && git clone --depth 1 https://github.com/postgrip-io/agent-sdk-typescript /tmp/agent-sdk-typescript && cd /tmp/agent-sdk-typescript && bun install --frozen-lockfile && bun run build && bun run example/greeting.ts',
];

const activities = {
  async greet(name: string): Promise<string> {
    return `Hello, ${name}`;
  },
};

// `proxyActivities<typeof activities>` with the narrow type fails strict-mode
// variance against `Record<string, ActivityFunction>`. Drop the generic and
// cast the result to keep the narrow types at workflow call sites.
const { greet } = proxyActivities({
  startToCloseTimeoutMs: 10_000,
  retry: { maximumAttempts: 3 },
}) as typeof activities;

export async function greetingWorkflow(name: string): Promise<string> {
  return greet(name);
}

// The activity / workflow types use `unknown[]` for args; narrow signatures
// like `(name: string)` are correct at the call site (proxyActivities preserves
// `typeof activities`) but need a widening cast at the registry boundary.
const workflows = { greetingWorkflow } as unknown as WorkflowRegistry;
const activityRegistry = activities as unknown as ActivityRegistry;

async function main(): Promise<void> {
  if (process.env.POSTGRIP_AGENT_MANAGED_RUNTIME !== 'true') {
    await submitManagedRuntime();
    return;
  }

  const baseUrl = process.env.POSTGRIP_AGENTORCHESTRATOR_URL ?? process.env.POSTGRIP_AGENT_LIVE_SERVER_URL ?? 'https://agentorchestrator.postgrip.app';
  const taskQueue = process.env.POSTGRIP_AGENT_TASK_QUEUE ?? 'typescript-example';
  const agentId = process.env.POSTGRIP_AGENT_ID ?? 'typescript-example-agent';

  const connection = await Connection.connect({
    baseUrl,
    headers: agentTokenHeaders(),
  });

  const agent = await Agent.create({
    connection,
    identity: agentId,
    name: agentId,
    taskQueue,
    workflows,
    activities: activityRegistry,
    maxConcurrentTaskExecutions: 4,
  });

  const client = new Client({ connection });
  const workflowId = `typescript-example-${crypto.randomUUID()}`;

  const result = await agent.runUntil(
    client.workflow.execute('greetingWorkflow', {
      workflowId,
      taskQueue,
      args: [process.env.SDK_EXAMPLE_GREETING_NAME ?? 'PostGrip'],
      ui: {
        displayName: 'TypeScript greeting example',
        description: 'Started from the TypeScript SDK greeting example.',
        details: { sdk: 'typescript' },
        tags: ['sdk-ui-demo', 'typescript'],
      },
      timeoutMs: 60_000,
    }),
  );

  console.log(`workflow ${workflowId} -> ${JSON.stringify(result)}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

async function submitManagedRuntime(): Promise<void> {
  const baseUrl = process.env.POSTGRIP_AGENTORCHESTRATOR_URL ?? process.env.POSTGRIP_AGENT_LIVE_SERVER_URL ?? 'https://agentorchestrator.postgrip.app';
  const connection = await Connection.connect({ baseUrl, headers: agentTokenHeaders() });
  const client = new Client({ connection });
  const args = readStringArrayJSON('SDK_EXAMPLE_RUNTIME_ARGS_JSON')
    ?? readStringArrayJSON('POSTGRIP_EXAMPLE_RUNTIME_ARGS_JSON')
    ?? DEFAULT_RUNTIME_ARGS;
  const queue = process.env.SDK_EXAMPLE_RUNTIME_QUEUE ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_QUEUE ?? 'default';
  const runtimeQueue = process.env.SDK_EXAMPLE_RUNTIME_CHILD_QUEUE ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_CHILD_QUEUE ?? `postgrip-greeting-${crypto.randomUUID().slice(0, 8)}`;
  const pullPolicy = (process.env.SDK_EXAMPLE_RUNTIME_PULL_POLICY ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_PULL_POLICY) as 'always' | 'missing' | 'never' | undefined;
  const task = await client.task.workflowRuntime({
    queue,
    runtimeQueue,
    image: process.env.SDK_EXAMPLE_RUNTIME_IMAGE ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_IMAGE ?? DEFAULT_RUNTIME_IMAGE,
    command: process.env.SDK_EXAMPLE_RUNTIME_COMMAND ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_COMMAND ?? DEFAULT_RUNTIME_COMMAND,
    args,
    working_dir: process.env.SDK_EXAMPLE_RUNTIME_WORKING_DIR ?? process.env.POSTGRIP_EXAMPLE_RUNTIME_WORKING_DIR,
    pull_policy: pullPolicy,
    timeout_seconds: 300,
    leaseTimeoutSeconds: 30,
    env: {
      SDK_EXAMPLE_GREETING_NAME: process.env.SDK_EXAMPLE_GREETING_NAME ?? 'PostGrip',
    },
  });
  console.log(`submitted managed workflow runtime task=${task.id} queue=${queue} runtimeQueue=${runtimeQueue}`);
}

function readStringArrayJSON(name: string): string[] | undefined {
  const value = process.env[name];
  if (!value) return undefined;
  const parsed = JSON.parse(value) as unknown;
  if (!Array.isArray(parsed) || parsed.some((item) => typeof item !== 'string')) {
    throw new Error(`${name} must be a JSON array of strings`);
  }
  return parsed;
}

function agentTokenHeaders(): Record<string, string> {
  const authToken = process.env.POSTGRIP_AGENT_TOKEN ?? '';
  return authToken ? { Authorization: `Bearer ${authToken}` } : {};
}
