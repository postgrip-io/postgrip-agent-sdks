// Long-running sequential example.
//
// Running it locally submits a workflow.runtime task to an existing agent
// pool. When the host agent launches the runtime, it runs five workflows
// back-to-back, each chaining five activity calls separated by durable
// timers.
//
// Run:
//
//   cp example/.env.example .env
//   # edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
//   bun run example/longrun.ts
//
// The SDK does not enroll standalone agents; host agents inject delegated
// managed-runtime credentials.

import {
  Agent,
  activityStdout,
  Client,
  Connection,
  proxyActivities,
  sleep,
  type ActivityRegistry,
  type WorkflowRegistry,
} from '@postgrip/agent';
import { loadExampleEnv } from './env';

loadExampleEnv(import.meta.url);

const STEPS_PER_WORKFLOW = readPositiveIntegerAny(['POSTGRIP_EXAMPLE_STEPS', 'SDK_EXAMPLE_STEPS'], 5);
const WORKFLOW_RUNS = readPositiveIntegerAny(['POSTGRIP_EXAMPLE_WORKFLOW_RUNS', 'SDK_EXAMPLE_WORKFLOW_RUNS'], 5);
const STEP_SLEEP_MS = readPositiveIntegerAny(['POSTGRIP_EXAMPLE_STEP_SLEEP_SECONDS', 'SDK_EXAMPLE_STEP_SLEEP_SECONDS'], 13) * 1000;
const WORKFLOW_TIMEOUT_MS = readPositiveIntegerAny(['POSTGRIP_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS', 'SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS'], 5 * 60) * 1000;
const RUN_LABEL = envAny(['POSTGRIP_EXAMPLE_RUN_LABEL', 'SDK_EXAMPLE_RUN_LABEL'], 'PostGrip');
const DEFAULT_RUNTIME_IMAGE = 'oven/bun:1';
const DEFAULT_RUNTIME_COMMAND = 'sh';
const DEFAULT_RUNTIME_ARGS = [
  '-lc',
  'apt-get update >/dev/null && apt-get install -y git >/dev/null && git clone --depth 1 https://github.com/postgrip-io/agent-sdk-typescript /tmp/agent-sdk-typescript && cd /tmp/agent-sdk-typescript && bun install --frozen-lockfile && bun run build && bun run example/longrun.ts',
];

const activities = {
  async processStep(name: string, step: number): Promise<string> {
    const result = `processed step ${step} for ${name}`;
    await activityStdout(`${result}\n`, {
      stage: 'processStep',
      details: { step, name },
    });
    return result;
  },
};

const { processStep } = proxyActivities({
  startToCloseTimeoutMs: 30_000,
  retry: { maximumAttempts: 3 },
}) as typeof activities;

export async function LongRunningWorkflow(name: string, steps: number): Promise<string> {
  for (let i = 1; i <= steps; i++) {
    await processStep(name, i);
    await sleep(STEP_SLEEP_MS);
  }
  return `completed ${steps} steps for ${name}`;
}

const workflows = { LongRunningWorkflow } as unknown as WorkflowRegistry;
const activityRegistry = activities as unknown as ActivityRegistry;

async function main(): Promise<void> {
  if (process.env.POSTGRIP_AGENT_MANAGED_RUNTIME !== 'true') {
    await submitManagedRuntime();
    return;
  }

  const baseUrl = process.env.POSTGRIP_AGENTORCHESTRATOR_URL ?? process.env.POSTGRIP_AGENT_LIVE_SERVER_URL ?? 'https://agentorchestrator.postgrip.app';
  const taskQueue = process.env.POSTGRIP_AGENT_TASK_QUEUE ?? 'typescript-longrun';
  const agentId = process.env.POSTGRIP_AGENT_ID ?? 'typescript-longrun-agent';

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

  await agent.runUntil((async () => {
    const overallStart = Date.now();
    for (let i = 1; i <= WORKFLOW_RUNS; i++) {
      const runStart = Date.now();
      const workflowId = `ts-longrun-${slug(RUN_LABEL)}-${crypto.randomUUID()}-${i}`;
      console.log(`[${i}/${WORKFLOW_RUNS}] starting ${workflowId}`);
      const result = await client.workflow.execute('LongRunningWorkflow', {
        workflowId,
        taskQueue,
        args: [`${RUN_LABEL}-${i}`, STEPS_PER_WORKFLOW],
        ui: {
          displayName: `${RUN_LABEL} long run #${i}`,
          description: `Runs ${STEPS_PER_WORKFLOW} steps with ${STEP_SLEEP_MS / 1000}s sleeps between steps.`,
          details: {
            sdk: 'typescript',
            steps: STEPS_PER_WORKFLOW,
            sleepSeconds: STEP_SLEEP_MS / 1000,
          },
          tags: ['sdk-ui-demo', 'typescript'],
        },
        timeoutMs: WORKFLOW_TIMEOUT_MS,
      });
      console.log(`[${i}/${WORKFLOW_RUNS}] ${workflowId} -> ${JSON.stringify(result)} (${Math.round((Date.now() - runStart) / 1000)}s)`);
    }
    console.log(`done — ${WORKFLOW_RUNS} workflows in ${Math.round((Date.now() - overallStart) / 1000)}s`);
  })());
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

function readPositiveInteger(name: string, fallback: number): number {
  const value = process.env[name];
  if (!value) return fallback;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    console.warn(`invalid ${name}=${JSON.stringify(value)}; using ${fallback}`);
    return fallback;
  }
  return parsed;
}

function readPositiveIntegerAny(names: string[], fallback: number): number {
  for (const name of names) {
    if (process.env[name]) return readPositiveInteger(name, fallback);
  }
  return fallback;
}

function envAny(names: string[], fallback: string): string {
  for (const name of names) {
    const value = process.env[name];
    if (value) return value;
  }
  return fallback;
}

function envOptional(names: string[]): string | undefined {
  for (const name of names) {
    const value = process.env[name];
    if (value) return value;
  }
  return undefined;
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

async function submitManagedRuntime(): Promise<void> {
  const baseUrl = process.env.POSTGRIP_AGENTORCHESTRATOR_URL ?? process.env.POSTGRIP_AGENT_LIVE_SERVER_URL ?? 'https://agentorchestrator.postgrip.app';
  const connection = await Connection.connect({ baseUrl, headers: agentTokenHeaders() });
  const client = new Client({ connection });
  const queue = envAny(['POSTGRIP_EXAMPLE_RUNTIME_QUEUE', 'SDK_EXAMPLE_RUNTIME_QUEUE'], 'default');
  const runtimeQueue = envAny(['POSTGRIP_EXAMPLE_RUNTIME_CHILD_QUEUE', 'SDK_EXAMPLE_RUNTIME_CHILD_QUEUE'], `sdk-runtime-${slug(RUN_LABEL)}-${crypto.randomUUID().slice(0, 8)}`);
  const args = readStringArrayJSON('SDK_EXAMPLE_RUNTIME_ARGS_JSON')
    ?? readStringArrayJSON('POSTGRIP_EXAMPLE_RUNTIME_ARGS_JSON')
    ?? DEFAULT_RUNTIME_ARGS;
  const task = await client.task.workflowRuntime({
    queue,
    runtimeQueue,
    image: envAny(['POSTGRIP_EXAMPLE_RUNTIME_IMAGE', 'SDK_EXAMPLE_RUNTIME_IMAGE'], DEFAULT_RUNTIME_IMAGE),
    command: envAny(['POSTGRIP_EXAMPLE_RUNTIME_COMMAND', 'SDK_EXAMPLE_RUNTIME_COMMAND'], DEFAULT_RUNTIME_COMMAND),
    args,
    working_dir: envOptional(['POSTGRIP_EXAMPLE_RUNTIME_WORKING_DIR', 'SDK_EXAMPLE_RUNTIME_WORKING_DIR']),
    pull_policy: envOptional(['POSTGRIP_EXAMPLE_RUNTIME_PULL_POLICY', 'SDK_EXAMPLE_RUNTIME_PULL_POLICY']) as 'always' | 'missing' | 'never' | undefined,
    timeout_seconds: readPositiveIntegerAny(['POSTGRIP_EXAMPLE_RUNTIME_TIMEOUT_SECONDS', 'SDK_EXAMPLE_RUNTIME_TIMEOUT_SECONDS'], 900),
    leaseTimeoutSeconds: readPositiveIntegerAny(['POSTGRIP_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS', 'SDK_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS'], 30),
    env: {
      SDK_EXAMPLE_RUN_LABEL: RUN_LABEL,
      SDK_EXAMPLE_WORKFLOW_RUNS: String(WORKFLOW_RUNS),
      SDK_EXAMPLE_STEPS: String(STEPS_PER_WORKFLOW),
      SDK_EXAMPLE_STEP_SLEEP_SECONDS: String(STEP_SLEEP_MS / 1000),
      SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS: String(WORKFLOW_TIMEOUT_MS / 1000),
    },
  });
  console.log(`submitted managed workflow runtime task=${task.id} queue=${queue} runtimeQueue=${runtimeQueue}`);
}

function slug(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'run';
}

function agentTokenHeaders(): Record<string, string> {
  const authToken = process.env.POSTGRIP_AGENT_TOKEN ?? '';
  return authToken ? { Authorization: `Bearer ${authToken}` } : {};
}
