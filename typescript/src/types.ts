import type { OpenAPIComponents } from './generated/openapi.js';

export type TaskState = OpenAPIComponents['TaskState'];
export type TaskEventKind = OpenAPIComponents['TaskEventKind'];
export type FailureInfo = OpenAPIComponents['FailureInfo'];
export type ContinueAsNewResult = OpenAPIComponents['ContinueAsNewResult'];
export type TaskResult<T = unknown> = Omit<OpenAPIComponents['TaskResult'], 'value'> & { value?: T };
export type Task<P = unknown, R = unknown> = Omit<OpenAPIComponents['Task'], 'payload' | 'result'> & {
  payload?: P;
  result?: TaskResult<R>;
};
export type TaskEvent = OpenAPIComponents['TaskEvent'];
export type TaskEventInput = OpenAPIComponents['TaskEventInput'];
export type EnqueueTaskRequest<P = unknown> = Omit<OpenAPIComponents['EnqueueTaskRequest'], 'payload'> & {
  payload?: P;
};

export interface ActivityTaskPayload<Args extends unknown[] = unknown[]> {
  activityType: string;
  args?: Args;
  workflowId?: string;
  workflowRunId?: string;
  workflowTaskId?: string;
  attempt?: number;
  cancellationType?: CancellationType;
  retry?: RetryPolicy;
}

export type ScheduleState = OpenAPIComponents['ScheduleState'];
export type ScheduleOverlapPolicy = OpenAPIComponents['ScheduleOverlapPolicy'];
export type ScheduleMissedRunPolicy = OpenAPIComponents['ScheduleMissedRunPolicy'];
export type ScheduleCalendarSpec = OpenAPIComponents['ScheduleCalendarSpec'];
export type ScheduleSpec = OpenAPIComponents['ScheduleSpec'];
export type ScheduleAction<Args extends unknown[] = unknown[]> = Omit<OpenAPIComponents['ScheduleAction'], 'args'> & {
  args?: Args;
};
export type Schedule<Args extends unknown[] = unknown[]> = Omit<OpenAPIComponents['Schedule'], 'action'> & {
  action: ScheduleAction<Args>;
};
export type CreateScheduleRequest<Args extends unknown[] = unknown[]> = Omit<OpenAPIComponents['CreateScheduleRequest'], 'action'> & {
  action: ScheduleAction<Args>;
};
export type UpdateScheduleRequest<Args extends unknown[] = unknown[]> = Omit<OpenAPIComponents['UpdateScheduleRequest'], 'action'> & {
  action?: ScheduleAction<Args>;
};
export type PauseScheduleRequest = OpenAPIComponents['PauseScheduleRequest'];
export type UnpauseScheduleRequest = OpenAPIComponents['UnpauseScheduleRequest'];
export type TriggerScheduleRequest = OpenAPIComponents['TriggerScheduleRequest'];
export type TriggerScheduleResponse<Args extends unknown[] = unknown[], R = unknown> = Omit<OpenAPIComponents['TriggerScheduleResponse'], 'schedule' | 'task'> & {
  schedule: Schedule<Args>;
  task: Task<WorkflowPayload<Args>, R>;
};
export type BackfillScheduleRequest = OpenAPIComponents['BackfillScheduleRequest'];
export type BackfillScheduleResponse<Args extends unknown[] = unknown[], R = unknown> = Omit<OpenAPIComponents['BackfillScheduleResponse'], 'schedule' | 'tasks'> & {
  schedule: Schedule<Args>;
  tasks: Array<Task<WorkflowPayload<Args>, R>>;
};
export type AgentPollDirective = OpenAPIComponents['AgentPollDirective'];
export type PollTaskResponse<P = unknown, R = unknown> = Omit<OpenAPIComponents['PollTaskResponse'], 'task'> & {
  task?: Task<P, R>;
};

export interface AgentUpgradeRequest {
  image?: string;
  expectedVersion?: number;
}

export interface ShellExecPayload {
  command: string;
  args?: string[];
  env?: Record<string, string>;
  working_dir?: string;
  timeout_seconds?: number;
}

/**
 * Payload for the `container.exec` task type — runs a command inside a
 * per-task container the Go agent launches via its docker CLI (proxied
 * through the worker stack's docker socket proxy). Use this for polyglot
 * runtimes (Node, Bun, Python, Go, anything in an image) without baking
 * those runtimes into the agent image.
 *
 * `image` is required. `command`, when set, overrides the image's
 * ENTRYPOINT; `args` becomes the container's CMD. `pull_policy` mirrors
 * `docker run --pull` (`always` | `missing` | `never`); empty defaults to
 * `missing`. Env keys go through the agent's allowlist — DOCKER_*,
 * POSTGRIP_*, and host loader/interpreter prefixes are rejected.
 *
 * The agent runs the container with `--rm --network=none` and never mounts
 * host paths; share state via stdin/args/env.
 */
export interface ContainerExecPayload {
  image: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  working_dir?: string;
  pull_policy?: 'always' | 'missing' | 'never';
  timeout_seconds?: number;
}

/**
 * Isolation tiers a workflow.runtime workload can require. A requested tier
 * is a floor: container work may run in a stronger tier, microvm work must
 * never run in a weaker one, and an unrecognized value satisfies nothing.
 */
export type IsolationTier = 'container' | 'microvm';

export interface WorkflowRuntimePayload {
  runtime_id?: string;
  image?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  working_dir?: string;
  namespace?: string;
  queue?: string;
  pull_policy?: 'always' | 'missing' | 'never';
  timeout_seconds?: number;
  /**
   * Isolation floor this workload requires; empty means the container
   * default. Requires `image` — a command-only runtime executes directly on
   * the agent host, which honors no isolation floor, and the orchestrator
   * rejects that combination at enqueue.
   */
  isolation?: IsolationTier;
}

export type WorkflowFunction<Args extends unknown[] = unknown[], R = unknown> = (...args: Args) => Promise<R> | R;
export type ActivityFunction<Args extends unknown[] = unknown[], R = unknown> = (...args: Args) => Promise<R> | R;
export type WorkflowRegistry = Record<string, WorkflowFunction>;
export type ActivityRegistry = Record<string, ActivityFunction>;
export type CancellationType = 'try_cancel' | 'wait_cancellation_completed' | 'abandon';
export type CancellationScopeType = 'cancellable' | 'non_cancellable';

export interface WorkflowStartOptions<Args extends unknown[] = unknown[]> {
  namespace?: string;
  workflowId?: string;
  workflowIdReusePolicy?: WorkflowIdReusePolicy;
  taskQueue?: string;
  args?: Args;
  leaseTimeoutSeconds?: number;
  workflowRunTimeoutMs?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  searchAttributes?: Record<string, unknown>;
  ui?: WorkflowUIMetadata;
}

export type WorkflowIdReusePolicy = OpenAPIComponents['WorkflowIdReusePolicy'];

export interface WorkflowUIMetadata {
  displayName?: string;
  description?: string;
  details?: Record<string, string | number | boolean>;
  tags?: string[];
}

export interface ContinueAsNewOptions<Args extends unknown[] = unknown[]> {
  workflowId?: string;
  workflowType?: string;
  taskQueue?: string;
  args?: Args;
  leaseTimeoutSeconds?: number;
  workflowRunTimeoutMs?: number;
  retry?: RetryPolicy;
}

export interface ChildWorkflowOptions<Args extends unknown[] = unknown[]> {
  workflowId?: string;
  taskQueue?: string;
  args?: Args;
  leaseTimeoutSeconds?: number;
  workflowRunTimeoutMs?: number;
  cancellationType?: CancellationType;
  cancellationScope?: CancellationScopeType;
  retry?: RetryPolicy;
}

export interface WorkflowExecutionDescription<R = unknown> {
  workflowId: string;
  runId?: string;
  taskId: string;
  namespace: string;
  taskQueue: string;
  workflowType: string;
  status: TaskState | WorkflowExecution['state'];
  attempt?: number;
  leaseTimeoutSeconds?: number;
  workflowRunTimeoutMs?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  searchAttributes?: Record<string, unknown>;
  result?: R;
  error?: string;
  startedAt: string;
  updatedAt: string;
}

export type WorkflowExecution<R = unknown> = Omit<OpenAPIComponents['WorkflowExecution'], 'result'> & {
  result?: TaskResult<R>;
};
export type WorkflowHistoryEvent = OpenAPIComponents['WorkflowHistoryEvent'];
export type WorkflowCountResponse = OpenAPIComponents['WorkflowCountResponse'];
export type Namespace = OpenAPIComponents['Namespace'];
export type CompactResponse = OpenAPIComponents['CompactResponse'];

export interface WorkflowSignalDefinition<Args extends unknown[] = []> {
  name: string;
  type: 'signal';
  args: Args;
}

export interface WorkflowQueryDefinition<R = unknown, Args extends unknown[] = []> {
  name: string;
  type: 'query';
  args: Args;
  result: R;
}

export interface WorkflowUpdateDefinition<R = unknown, Args extends unknown[] = []> {
  name: string;
  type: 'update';
  args: Args;
  result: R;
}

export type SignalWorkflowRequest<Args extends unknown[] = unknown[]> = Omit<OpenAPIComponents['SignalWorkflowRequest'], 'args'> & {
  args?: Args;
};
export type SignalWithStartWorkflowRequest<WorkflowArgs extends unknown[] = unknown[], SignalArgs extends unknown[] = unknown[]> = Omit<OpenAPIComponents['SignalWithStartWorkflowRequest'], 'args' | 'signal'> & {
  args?: WorkflowArgs;
  signal: SignalWorkflowRequest<SignalArgs>;
};
export type SignalWithStartWorkflowResponse<WorkflowArgs extends unknown[] = unknown[], R = unknown> = Omit<OpenAPIComponents['SignalWithStartWorkflowResponse'], 'workflow' | 'task'> & {
  workflow: WorkflowExecution<R>;
  task: Task<WorkflowPayload<WorkflowArgs>, R>;
};
export type CancelWorkflowRequest = OpenAPIComponents['CancelWorkflowRequest'];
export type TerminateWorkflowRequest = OpenAPIComponents['TerminateWorkflowRequest'];

export interface WorkflowQueryPayload<Args extends unknown[] = unknown[]> {
  workflowId: string;
  workflowRunId?: string;
  workflowType: string;
  queryName: string;
  args: Args;
}

export interface WorkflowUpdatePayload<Args extends unknown[] = unknown[]> {
  workflowId: string;
  workflowRunId?: string;
  workflowType: string;
  updateName: string;
  args: Args;
}

export interface WorkflowPayload<Args extends unknown[] = unknown[]> {
  namespace?: string;
  workflowType: string;
  workflowId: string;
  runId?: string;
  workflowIdReusePolicy?: WorkflowIdReusePolicy;
  parentWorkflowId?: string;
  parentWorkflowRunId?: string;
  parentWorkflowTaskId?: string;
  parentCancellationType?: CancellationType;
  continuedFromWorkflowId?: string;
  runTimeoutMs?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  searchAttributes?: Record<string, unknown>;
  args: Args;
}

export interface ActivityInvocationPayload<Args extends unknown[] = unknown[]> {
  activityType: string;
  workflowId?: string;
  workflowRunId?: string;
  workflowTaskId?: string;
  attempt?: number;
  cancellationType?: CancellationType;
  retry?: RetryPolicy;
  args: Args;
}

export interface TimerPayload {
  workflowId?: string;
  workflowRunId?: string;
  workflowTaskId?: string;
  timerId: string;
  durationMs: number;
  fireAt: string;
}

export type RetryPolicy = OpenAPIComponents['RetryPolicy'];

export interface ActivityOptions {
  startToCloseTimeoutMs?: number;
  cancellationType?: CancellationType;
  cancellationScope?: CancellationScopeType;
  retry?: RetryPolicy;
}

// --- sandbox platform ------------------------------------------------------
//
// Mirrors agent-sdk-protocol/sandbox.go. Timestamps are RFC3339 strings, as
// everywhere else in this file.

export type SandboxBackend = OpenAPIComponents['SandboxBackend'];

export type SandboxDesiredState = OpenAPIComponents['SandboxDesiredState'];

/**
 * Only `running`, `stopped`, `deleted` and `failed` are reported by agents
 * today, and `scheduling` is written by the control plane at placement. The
 * rest exist for forward compatibility — treat an unexpected value as
 * in-flight rather than as an error.
 */
export type SandboxObservedState = OpenAPIComponents['SandboxObservedState'];
export type SandboxSessionKind = OpenAPIComponents['SandboxSessionKind'];
export type SandboxResourceLimits = OpenAPIComponents['SandboxResourceLimits'];
export type SandboxPortMapping = OpenAPIComponents['SandboxPortMapping'];
export type SandboxNetworkPolicy = OpenAPIComponents['SandboxNetworkPolicy'];
export type Sandbox = OpenAPIComponents['Sandbox'];

/**
 * `name` must match ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$ and is unique per tenant
 * among live sandboxes, so a duplicate is a 409. `credentialRefs` is reserved —
 * any non-empty value is rejected with 400 today.
 *
 * `image` is required here because it is required on the server. It used to be
 * optional, which let `{ name: 'x' }` type-check even though that create is
 * answered with a 400 unconditionally — giving up the one thing a mirrored
 * request type can do, which is reject an invalid request before the round
 * trip.
 */
export type SandboxCreateRequest = OpenAPIComponents['SandboxCreateRequest'];

/** The list endpoint returns this envelope, not a bare array. */
export type SandboxListResponse = OpenAPIComponents['SandboxListResponse'];

/**
 * `id` is not the digest: uploading identical bytes returns the pre-existing
 * record, so don't assume a fresh id per upload.
 */
export type SandboxWorkspace = OpenAPIComponents['SandboxWorkspace'];
export type SandboxWorkspaceListResponse = OpenAPIComponents['SandboxWorkspaceListResponse'];

/**
 * `rows`/`columns` default to 24x80 and are fixed for the session's life —
 * there is no resize channel. `kind` defaults to `pty`; `exec` requires
 * `command`. The sandbox must be observed running and assigned to an agent,
 * else the server returns a retryable 400.
 */
export type CreateSandboxSessionRequest = OpenAPIComponents['CreateSandboxSessionRequest'];

/** The ticket is returned exactly once and is short-lived. */
export type CreateSandboxSessionResponse = OpenAPIComponents['CreateSandboxSessionResponse'];
