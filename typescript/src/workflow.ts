import { AsyncLocalStorage } from 'node:async_hooks';

import { ApplicationFailure, CancelledFailure } from './errors.js';
import type {
  ActivityFunction,
  ActivityOptions,
  ChildWorkflowOptions,
  ContinueAsNewOptions,
  CancellationScopeType,
  WorkflowFunction,
  WorkflowQueryDefinition,
  WorkflowSignalDefinition,
  WorkflowUpdateDefinition,
} from './types.js';

interface WorkflowRuntime {
  workflowId: string;
  workflowRunId: string;
  taskQueue: string;
  workflowType: string;
  cancellationScopes: CancellationScopeType[];
  executeActivity: (name: string, args: unknown[], options: ActivityOptions) => Promise<unknown>;
  executeChild: (name: string, args: unknown[], options: ChildWorkflowOptions) => Promise<unknown>;
  continueAsNew: (workflowType: string, args: unknown[], options: ContinueAsNewOptions) => never;
  sleep: (ms: number, cancellationScope?: CancellationScopeType) => Promise<void>;
  condition: (predicate: () => boolean, timeoutMs?: number, cancellationScope?: CancellationScopeType) => Promise<boolean>;
  cancellationRequested: () => boolean;
  setSignalHandler: (name: string, handler: (...args: unknown[]) => unknown) => void;
  setQueryHandler: (name: string, handler: (...args: unknown[]) => unknown) => void;
  setUpdateHandler: (name: string, handler: (...args: unknown[]) => unknown) => void;
  emit: (event: { kind?: 'milestone' | 'progress'; stage?: string; message?: string; details?: Record<string, unknown> }) => Promise<void>;
}

const workflowRuntimeStorage = new AsyncLocalStorage<WorkflowRuntime>();
const fallbackCancellationScopeStack: CancellationScopeType[] = [];

export class CancellationScope {
  static async cancellable<R>(fn: () => Promise<R> | R): Promise<R> {
    return runInCancellationScope('cancellable', fn);
  }

  static async nonCancellable<R>(fn: () => Promise<R> | R): Promise<R> {
    return runInCancellationScope('non_cancellable', fn);
  }

  static current(): CancellationScopeType {
    return currentCancellationScope();
  }
}

export function workflowInfo(): Omit<WorkflowRuntime, 'cancellationScopes' | 'executeActivity' | 'executeChild' | 'continueAsNew' | 'sleep' | 'condition' | 'cancellationRequested' | 'setSignalHandler' | 'setQueryHandler' | 'setUpdateHandler' | 'emit'> {
  const runtime = currentRuntime();
  return {
    workflowId: runtime.workflowId,
    workflowRunId: runtime.workflowRunId,
    taskQueue: runtime.taskQueue,
    workflowType: runtime.workflowType,
  };
}

export interface WorkflowMilestoneOptions {
  index?: number;
  total?: number;
  status?: 'started' | 'completed' | 'failed' | 'skipped';
  details?: Record<string, unknown>;
}

export async function milestone(name: string, options: WorkflowMilestoneOptions = {}): Promise<void> {
  const runtime = currentRuntime();
  await runtime.emit({
    kind: 'milestone',
    stage: 'milestone',
    message: milestoneMessage(name, options),
    details: {
      ...(options.details ?? {}),
      milestone: true,
      name,
      status: options.status ?? 'completed',
      ...(options.index == null ? {} : { index: options.index }),
      ...(options.total == null ? {} : { total: options.total }),
    },
  });
}

export function sleep(ms: number): Promise<void> {
  const runtime = workflowRuntimeStorage.getStore();
  if (runtime) {
    throwIfScopedCancellationRequested(runtime);
    return runtime.sleep(ms, currentCancellationScope());
  }
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function condition(predicate: () => boolean, timeoutMs?: number): Promise<boolean> {
  const runtime = currentRuntime();
  if (predicate()) return Promise.resolve(true);
  throwIfScopedCancellationRequested(runtime);
  return runtime.condition(predicate, timeoutMs, currentCancellationScope());
}

export function cancellationRequested(): boolean {
  const runtime = currentRuntime();
  return currentCancellationScope() !== 'non_cancellable' && runtime.cancellationRequested();
}

export function proxyActivities<T extends Record<string, ActivityFunction>>(options: ActivityOptions = {}): T {
  return new Proxy({}, {
    get(_target, property) {
      if (typeof property !== 'string') return undefined;
      return async (...args: unknown[]) => executeActivity(property, args, options);
    },
  }) as T;
}

export async function executeChild<WF extends WorkflowFunction>(
  workflow: WF | string,
  options: ChildWorkflowOptions<Parameters<WF>> = {},
): Promise<Awaited<ReturnType<WF>>> {
  const workflowType = typeof workflow === 'string' ? workflow : workflow.name;
  if (!workflowType) {
    throw new Error('child workflow type is required');
  }
  const runtime = currentRuntime();
  const args = options.args ?? ([] as unknown as Parameters<WF>);
  throwIfScopedCancellationRequested(runtime);
  return await runtime.executeChild(workflowType, args, { ...options, cancellationScope: currentCancellationScope() }) as Awaited<ReturnType<WF>>;
}

export function continueAsNew<WF extends WorkflowFunction>(
  workflow?: WF | string,
  options: ContinueAsNewOptions<Parameters<WF>> = {},
): never {
  const runtime = currentRuntime();
  const workflowType = options.workflowType ?? (typeof workflow === 'string' ? workflow : workflow?.name) ?? runtime.workflowType;
  if (!workflowType) {
    throw new Error('workflow type is required');
  }
  const args = options.args ?? ([] as unknown as Parameters<WF>);
  return runtime.continueAsNew(workflowType, args, options);
}

export function defineSignal<Args extends unknown[] = []>(name: string): WorkflowSignalDefinition<Args> {
  return { name, type: 'signal', args: [] as unknown as Args };
}

export function defineQuery<R = unknown, Args extends unknown[] = []>(name: string): WorkflowQueryDefinition<R, Args> {
  return { name, type: 'query', args: [] as unknown as Args, result: undefined as R };
}

export function defineUpdate<R = unknown, Args extends unknown[] = []>(name: string): WorkflowUpdateDefinition<R, Args> {
  return { name, type: 'update', args: [] as unknown as Args, result: undefined as R };
}

export function setHandler(definition: WorkflowSignalDefinition | WorkflowQueryDefinition | WorkflowUpdateDefinition, handler: (...args: unknown[]) => unknown): void {
  const runtime = currentRuntime();
  if (definition.type === 'signal') {
    runtime.setSignalHandler(definition.name, handler);
    return;
  }
  if (definition.type === 'update') {
    runtime.setUpdateHandler(definition.name, handler);
    return;
  }
  runtime.setQueryHandler(definition.name, handler);
}

export function validateWorkflowSandbox(workflow: WorkflowFunction): void {
  if ((workflow as { postgripWorkflowSandboxed?: boolean }).postgripWorkflowSandboxed === false) {
    return;
  }
  const source = Function.prototype.toString.call(workflow);
  const bannedCalls: Array<[RegExp, string, string]> = [
    [/\bDate\.now\s*\(/, 'Date.now()', 'use workflowInfo(), workflow time carried in history, or an activity'],
    [/\bnew\s+Date\s*\(/, 'new Date()', 'use workflowInfo(), workflow time carried in history, or an activity'],
    [/\bMath\.random\s*\(/, 'Math.random()', 'move randomness into an activity'],
    [/\bcrypto\.randomUUID\s*\(/, 'crypto.randomUUID()', 'generate IDs in the client, options, or an activity'],
    [/\bsetTimeout\s*\(/, 'setTimeout()', 'use sleep() for durable timers'],
    [/\bsetInterval\s*\(/, 'setInterval()', 'use sleep() for durable timers'],
  ];
  const violations = bannedCalls
    .filter(([pattern]) => pattern.test(source))
    .map(([, call, reason]) => `${call} is not allowed, ${reason}`);
  if (violations.length > 0) {
    throw ApplicationFailure.nonRetryable(
      `workflow sandbox rejected nondeterministic API use: ${violations.join('; ')}`,
      'DeterminismViolation',
    );
  }
}

export async function runInWorkflowRuntime<R>(runtime: WorkflowRuntime, fn: () => Promise<R> | R): Promise<R> {
  runtime.cancellationScopes = [];
  return workflowRuntimeStorage.run(runtime, async () => await fn());
}

export class ContinueAsNewCommand extends Error {
  readonly workflowType: string;
  readonly args: unknown[];
  readonly options: ContinueAsNewOptions;

  constructor(workflowType: string, args: unknown[], options: ContinueAsNewOptions) {
    super(`continue workflow as new ${workflowType}`);
    this.name = 'ContinueAsNewCommand';
    this.workflowType = workflowType;
    this.args = args;
    this.options = options;
  }
}

async function executeActivity(name: string, args: unknown[], options: ActivityOptions): Promise<unknown> {
  const runtime = currentRuntime();
  await runtime.emit({
    stage: 'activity',
    message: `scheduling activity ${name}`,
    details: { activity: name },
  });
  throwIfScopedCancellationRequested(runtime);
  return runtime.executeActivity(name, args, { ...options, cancellationScope: currentCancellationScope() });
}

function milestoneMessage(name: string, options: WorkflowMilestoneOptions): string {
  const status = options.status ?? 'completed';
  const prefix = options.index == null || options.total == null
    ? ''
    : `step ${options.index}/${options.total} `;
  return `${prefix}${status} ${name}`.trim();
}

async function runInCancellationScope<R>(scope: CancellationScopeType, fn: () => Promise<R> | R): Promise<R> {
  const scopes = currentCancellationScopeStack();
  scopes.push(scope);
  try {
    return await fn();
  } finally {
    scopes.pop();
  }
}

function currentCancellationScope(): CancellationScopeType {
  const scopes = currentCancellationScopeStack();
  return scopes[scopes.length - 1] ?? 'cancellable';
}

function currentCancellationScopeStack(): CancellationScopeType[] {
  const runtime = workflowRuntimeStorage.getStore();
  return runtime?.cancellationScopes ?? fallbackCancellationScopeStack;
}

function throwIfScopedCancellationRequested(runtime: WorkflowRuntime): void {
  if (currentCancellationScope() !== 'non_cancellable' && runtime.cancellationRequested()) {
    throw new CancelledFailure('workflow cancellation requested');
  }
}

function currentRuntime(): WorkflowRuntime {
  const runtime = workflowRuntimeStorage.getStore();
  if (!runtime) {
    throw new Error('workflow API called outside of a PostGrip workflow runtime');
  }
  return runtime;
}

export function withTimeout<T>(promise: Promise<T>, timeoutMs: number | undefined, message: string): Promise<T> {
  if (!timeoutMs || timeoutMs <= 0) return promise;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(ApplicationFailure.create({ message, type: 'TimeoutFailure' })), timeoutMs);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (err) => {
        clearTimeout(timer);
        reject(err);
      },
    );
  });
}

export function nextRetryDelayMs(attempt: number, options: ActivityOptions): number {
  const initial = options.retry?.initialIntervalMs ?? 1000;
  const coefficient = options.retry?.backoffCoefficient ?? 2;
  const max = options.retry?.maximumIntervalMs ?? 30_000;
  return Math.min(max, Math.round(initial * coefficient ** (attempt - 1)));
}
