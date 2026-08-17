export type TaskState = 'queued' | 'leased' | 'blocked' | 'succeeded' | 'failed';

export type TaskEventKind =
  | 'leased'
  | 'started'
  | 'heartbeat'
  | 'milestone'
  | 'progress'
  | 'stdout'
  | 'stderr'
  | 'completed'
  | 'failed';

export interface TaskResult<T = unknown> {
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  message?: string;
  value?: T;
  failure?: FailureInfo;
  continue_as_new?: ContinueAsNewResult;
  started_at?: string;
  finished_at?: string;
}

export interface FailureInfo {
  message?: string;
  type?: string;
  non_retryable?: boolean;
  details?: unknown[];
}

export interface ContinueAsNewResult {
  workflow_id: string;
  workflow_type: string;
  task_queue: string;
  task_id: string;
}

export interface Task<P = unknown, R = unknown> {
  id: string;
  tenantId?: string;
  namespace: string;
  queue: string;
  type: string;
  payload?: P;
  state: TaskState;
  attempt: number;
  agent_id?: string;
  lease_timeout_seconds: number;
  not_before?: string;
  leased_until?: string;
  created_at: string;
  updated_at: string;
  result?: TaskResult<R>;
  error?: string;
}

export interface TaskEvent {
  id: string;
  tenantId?: string;
  task_id: string;
  agent_id?: string;
  kind: TaskEventKind;
  stage?: string;
  message?: string;
  stream?: string;
  data?: string;
  details?: Record<string, unknown>;
  created_at: string;
}

export interface TaskEventInput {
  kind: TaskEventKind;
  stage?: string;
  message?: string;
  stream?: string;
  data?: string;
  details?: Record<string, unknown>;
}

export interface EnqueueTaskRequest<P = unknown> {
  tenantId?: string;
  namespace?: string;
  queue?: string;
  type: string;
  payload?: P;
  lease_timeout_seconds?: number;
}

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

export type ScheduleState = 'active' | 'paused' | 'deleted';
export type ScheduleOverlapPolicy = 'skip' | 'allow_all';
export type ScheduleMissedRunPolicy = 'catch_up' | 'skip';

export interface ScheduleCalendarSpec {
  minute?: number[];
  hour?: number[];
  day_of_month?: number[];
  month?: number[];
  day_of_week?: number[];
}

export interface ScheduleSpec {
  interval_seconds?: number;
  cron?: string;
  calendar?: ScheduleCalendarSpec;
  timezone?: string;
  jitter_seconds?: number;
  catch_up_window_seconds?: number;
  missed_run_policy?: ScheduleMissedRunPolicy;
  start_at?: string;
}

export interface ScheduleAction<Args extends unknown[] = unknown[]> {
  namespace?: string;
  queue?: string;
  workflowType: string;
  workflowId?: string;
  workflowIdReusePolicy?: WorkflowIdReusePolicy;
  runTimeoutMs?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  searchAttributes?: Record<string, unknown>;
  args?: Args;
}

export interface Schedule<Args extends unknown[] = unknown[]> {
  id: string;
  tenantId?: string;
  namespace: string;
  state: ScheduleState;
  overlap_policy?: ScheduleOverlapPolicy;
  spec: ScheduleSpec;
  action: ScheduleAction<Args>;
  last_run_at?: string;
  next_run_at: string;
  created_at: string;
  updated_at: string;
}

export interface CreateScheduleRequest<Args extends unknown[] = unknown[]> {
  id?: string;
  namespace?: string;
  overlap_policy?: ScheduleOverlapPolicy;
  spec: ScheduleSpec;
  action: ScheduleAction<Args>;
}

export interface UpdateScheduleRequest<Args extends unknown[] = unknown[]> {
  overlap_policy?: ScheduleOverlapPolicy;
  spec?: ScheduleSpec;
  action?: ScheduleAction<Args>;
}

export interface PauseScheduleRequest {
  reason?: string;
}

export interface UnpauseScheduleRequest {
  reason?: string;
}

export interface TriggerScheduleRequest {
  reason?: string;
}

export interface TriggerScheduleResponse<Args extends unknown[] = unknown[], R = unknown> {
  schedule: Schedule<Args>;
  task: Task<WorkflowPayload<Args>, R>;
}

export interface BackfillScheduleRequest {
  start_at: string;
  end_at: string;
}

export interface BackfillScheduleResponse<Args extends unknown[] = unknown[], R = unknown> {
  schedule: Schedule<Args>;
  tasks: Array<Task<WorkflowPayload<Args>, R>>;
}

export interface PollTaskResponse<P = unknown, R = unknown> {
  task?: Task<P, R>;
  directive?: AgentPollDirective;
}

export interface AgentPollDirective {
  type: 'upgrade' | 'shutdown' | 'log_level' | 'poll_now' | 'attest';
  image?: string;
  expectedVersion?: number;
  force?: boolean;
  logLevel?: string;
  subject?: 'agent' | 'agent_helper';
}

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

export type WorkflowIdReusePolicy = 'allow_duplicate' | 'allow_duplicate_failed_only' | 'reject_duplicate';

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

export interface WorkflowExecution<R = unknown> {
  id: string;
  tenantId?: string;
  run_id: string;
  namespace: string;
  type: string;
  queue: string;
  task_id: string;
  agent_id?: string;
  state: 'running' | 'succeeded' | 'failed' | 'continued_as_new';
  attempt?: number;
  run_timeout_ms?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  search_attributes?: Record<string, unknown>;
  result?: TaskResult<R>;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface WorkflowHistoryEvent {
  id: string;
  workflow_id: string;
  tenantId?: string;
  task_id?: string;
  type: string;
  attributes?: Record<string, unknown>;
  created_at: string;
}

export interface WorkflowCountResponse {
  count: number;
}

export interface Namespace {
  name: string;
  created_at: string;
  updated_at: string;
}

export interface CompactResponse {
  removed_tasks: number;
  removed_workflows: number;
}

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

export interface SignalWorkflowRequest<Args extends unknown[] = unknown[]> {
  name: string;
  args?: Args;
}

export interface SignalWithStartWorkflowRequest<WorkflowArgs extends unknown[] = unknown[], SignalArgs extends unknown[] = unknown[]> {
  namespace?: string;
  queue?: string;
  workflowType: string;
  workflowId?: string;
  workflowIdReusePolicy?: WorkflowIdReusePolicy;
  lease_timeout_seconds?: number;
  runTimeoutMs?: number;
  retry?: RetryPolicy;
  memo?: Record<string, unknown>;
  searchAttributes?: Record<string, unknown>;
  args?: WorkflowArgs;
  signal: SignalWorkflowRequest<SignalArgs>;
}

export interface SignalWithStartWorkflowResponse<WorkflowArgs extends unknown[] = unknown[], R = unknown> {
  workflow: WorkflowExecution<R>;
  task: Task<WorkflowPayload<WorkflowArgs>, R>;
  signal: WorkflowHistoryEvent;
}

export interface CancelWorkflowRequest {
  reason?: string;
}

export interface TerminateWorkflowRequest {
  reason?: string;
}

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

export interface RetryPolicy {
  maximumAttempts?: number;
  initialIntervalMs?: number;
  backoffCoefficient?: number;
  maximumIntervalMs?: number;
  expirationIntervalMs?: number;
  nonRetryableErrorTypes?: string[];
}

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

export type SandboxBackend = 'auto' | 'docker' | 'podman' | 'firecracker';

export type SandboxDesiredState = 'running' | 'stopped' | 'deleted';

/**
 * Only `running`, `stopped`, `deleted` and `failed` are reported by agents
 * today, and `scheduling` is written by the control plane at placement. The
 * rest exist for forward compatibility — treat an unexpected value as
 * in-flight rather than as an error.
 */
export type SandboxObservedState =
  | 'requested'
  | 'scheduling'
  | 'provisioning'
  | 'setting_up'
  | 'running'
  | 'stopping'
  | 'stopped'
  | 'starting'
  | 'deleting'
  | 'deleted'
  | 'failed';

export type SandboxSessionKind = 'pty' | 'exec';

export interface SandboxResourceLimits {
  cpus?: number;
  memoryBytes?: number;
  diskBytes?: number;
}

export interface SandboxPortMapping {
  hostPort: number;
  guestPort: number;
  protocol?: string;
}

/** `internetEgress` combined with `denyHostAccess` or `allowCidrs` is a 400. */
export interface SandboxNetworkPolicy {
  internetEgress: boolean;
  allowCidrs?: string[];
  denyHostAccess: boolean;
  ports?: SandboxPortMapping[];
}

export interface Sandbox {
  tenantId: string;
  id: string;
  name: string;
  createdBy: string;
  agentId?: string;
  desiredState: SandboxDesiredState;
  observedState: SandboxObservedState;
  generation: number;
  observedGeneration: number;
  backend: SandboxBackend;
  isolationClass: string;
  image: string;
  architecture?: string;
  workspaceId?: string;
  repositoryName?: string;
  setupCommand?: string[];
  credentialRefs?: string[];
  resourceLimits: SandboxResourceLimits;
  networkPolicy: SandboxNetworkPolicy;
  labels?: Record<string, string>;
  runtimeInstanceId?: string;
  failureCode?: string;
  failureMessage?: string;
  expiresAt?: string;
  lastActivityAt?: string;
  createdAt: string;
  updatedAt: string;
  stoppedAt?: string;
  deletedAt?: string;
}

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
export interface SandboxCreateRequest {
  name: string;
  image: string;
  backend?: SandboxBackend;
  architecture?: string;
  workspaceId?: string;
  repositoryName?: string;
  setupCommand?: string[];
  credentialRefs?: string[];
  resourceLimits?: SandboxResourceLimits;
  networkPolicy?: SandboxNetworkPolicy;
  labels?: Record<string, string>;
  expiresAt?: string;
}

/** The list endpoint returns this envelope, not a bare array. */
export interface SandboxListResponse {
  sandboxes: Sandbox[];
}

/**
 * `id` is not the digest: uploading identical bytes returns the pre-existing
 * record, so don't assume a fresh id per upload.
 */
export interface SandboxWorkspace {
  tenantId: string;
  id: string;
  sha256: string;
  sizeBytes: number;
  repositoryName: string;
  revision?: string;
  createdBy: string;
  createdAt: string;
  expiresAt: string;
}

/**
 * `rows`/`columns` default to 24x80 and are fixed for the session's life —
 * there is no resize channel. `kind` defaults to `pty`; `exec` requires
 * `command`. The sandbox must be observed running and assigned to an agent,
 * else the server returns a retryable 400.
 */
export interface CreateSandboxSessionRequest {
  rows?: number;
  columns?: number;
  kind?: SandboxSessionKind;
  command?: string[];
}

/** The ticket is returned exactly once and is short-lived. */
export interface CreateSandboxSessionResponse {
  id: string;
  ticket: string;
  expiresAt: string;
}
