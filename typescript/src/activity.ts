import { AsyncLocalStorage } from 'node:async_hooks';

import { CancelledFailure } from './errors.js';

interface ActivityRuntime {
  taskId: string;
  activityType: string;
  heartbeat: (details?: Record<string, unknown>) => Promise<void>;
  emit: (event: ActivityEventInput) => Promise<void>;
}

const activityRuntimeStorage = new AsyncLocalStorage<ActivityRuntime>();

export interface ActivityInfo {
  taskId: string;
  activityType: string;
}

export interface MilestoneOptions {
  index?: number;
  total?: number;
  status?: 'started' | 'completed' | 'failed' | 'skipped';
  details?: Record<string, unknown>;
}

export interface ActivityOutputOptions {
  stage?: string;
  message?: string;
  details?: Record<string, unknown>;
}

export interface ActivityEventInput {
  kind: 'milestone' | 'progress' | 'stdout' | 'stderr';
  stage?: string;
  message?: string;
  stream?: 'stdout' | 'stderr';
  data?: string;
  details?: Record<string, unknown>;
}

export function activityInfo(): ActivityInfo {
  const runtime = currentActivityRuntime();
  return {
    taskId: runtime.taskId,
    activityType: runtime.activityType,
  };
}

export async function heartbeat(details?: Record<string, unknown>): Promise<void> {
  try {
    await currentActivityRuntime().heartbeat(details);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (message.includes('terminal') || message.includes('not leased')) {
      throw new CancelledFailure('activity cancellation requested');
    }
    throw err;
  }
}

export async function milestone(name: string, options: MilestoneOptions = {}): Promise<void> {
  const details = {
    ...(options.details ?? {}),
    milestone: true,
    name,
    status: options.status ?? 'completed',
    ...(options.index == null ? {} : { index: options.index }),
    ...(options.total == null ? {} : { total: options.total }),
  };
  await currentActivityRuntime().emit({
    kind: 'milestone',
    stage: 'milestone',
    message: milestoneMessage(name, options),
    details,
  });
}

export async function stdout(data: string, options: ActivityOutputOptions = {}): Promise<void> {
  await emitOutput('stdout', data, options);
}

export async function stderr(data: string, options: ActivityOutputOptions = {}): Promise<void> {
  await emitOutput('stderr', data, options);
}

export async function runInActivityRuntime<R>(runtime: ActivityRuntime, fn: () => Promise<R> | R): Promise<R> {
  return activityRuntimeStorage.run(runtime, async () => fn());
}

async function emitOutput(stream: 'stdout' | 'stderr', data: string, options: ActivityOutputOptions): Promise<void> {
  await currentActivityRuntime().emit({
    kind: stream,
    stage: options.stage ?? 'activity',
    message: options.message,
    stream,
    data,
    details: options.details,
  });
}

function milestoneMessage(name: string, options: MilestoneOptions): string {
  const status = options.status ?? 'completed';
  const prefix = options.index == null || options.total == null
    ? ''
    : `step ${options.index}/${options.total} `;
  return `${prefix}${status} ${name}`.trim();
}

function currentActivityRuntime(): ActivityRuntime {
  const runtime = activityRuntimeStorage.getStore();
  if (!runtime) {
    throw new Error('activity API called outside of a PostGrip activity runtime');
  }
  return runtime;
}
