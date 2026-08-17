import { SandboxClient } from './sandbox.js';
import { Connection } from './connection.js';
import { TaskFailedError, TimeoutFailure } from './errors.js';
import type {
  BackfillScheduleRequest,
  BackfillScheduleResponse,
  ContainerExecPayload,
  CreateScheduleRequest,
  PauseScheduleRequest,
  RetryPolicy,
  Schedule,
  ScheduleOverlapPolicy,
  ScheduleState,
  ShellExecPayload,
  Task,
  TaskEvent,
  TriggerScheduleRequest,
  TriggerScheduleResponse,
  UnpauseScheduleRequest,
  UpdateScheduleRequest,
  WorkflowExecution,
  WorkflowExecutionDescription,
  WorkflowFunction,
  WorkflowHistoryEvent,
  WorkflowRuntimePayload,
  WorkflowIdReusePolicy,
  WorkflowQueryDefinition,
  WorkflowQueryPayload,
  WorkflowSignalDefinition,
  WorkflowUpdateDefinition,
  WorkflowUpdatePayload,
  WorkflowPayload,
  WorkflowStartOptions,
  WorkflowUIMetadata,
} from './types.js';

export interface ClientOptions {
  connection: Connection;
}

export interface WatchEventsOptions {
  pollIntervalMs?: number;
  signal?: AbortSignal;
}

const POSTGRIP_UI_MEMO_KEY = 'postgrip.ui';

function memoWithWorkflowUI(
  memo?: Record<string, unknown>,
  ui?: WorkflowUIMetadata,
): Record<string, unknown> | undefined {
  if (!ui) return memo;
  const cleanUI: WorkflowUIMetadata = {};
  if (ui.displayName?.trim()) cleanUI.displayName = ui.displayName.trim();
  if (ui.description?.trim()) cleanUI.description = ui.description.trim();
  if (ui.tags?.length) {
    const tags = ui.tags.map((tag) => tag.trim()).filter(Boolean);
    if (tags.length) cleanUI.tags = tags;
  }
  if (ui.details) {
    const details = Object.fromEntries(
      Object.entries(ui.details)
        .map(([key, value]) => [key.trim(), value] as const)
        .filter(([key]) => key !== ''),
    );
    if (Object.keys(details).length > 0) cleanUI.details = details;
  }
  if (Object.keys(cleanUI).length === 0) return memo;
  return { ...(memo ?? {}), [POSTGRIP_UI_MEMO_KEY]: cleanUI };
}

export class Client {
  readonly workflow: WorkflowClient;
  readonly task: TaskClient;
  readonly schedule: ScheduleClient;
  readonly sandbox: SandboxClient;

  constructor(options: ClientOptions) {
    this.workflow = new WorkflowClient(options.connection);
    this.task = new TaskClient(options.connection);
    this.schedule = new ScheduleClient(options.connection);
    this.sandbox = new SandboxClient(options.connection);
  }
}

export class WorkflowClient {
  constructor(private readonly connection: Connection) {}

  async start<WF extends WorkflowFunction>(
    workflow: WF | string,
    options: WorkflowStartOptions<Parameters<WF>> = {},
  ): Promise<WorkflowHandle<Awaited<ReturnType<WF>>>> {
    const workflowType = typeof workflow === 'string' ? workflow : workflow.name;
    if (!workflowType) {
      throw new Error('workflow type is required');
    }
    const workflowId = options.workflowId ?? crypto.randomUUID();
    const namespace = options.namespace ?? 'default';
    const task = await this.connection.enqueueTask<WorkflowPayload<Parameters<WF>>, Awaited<ReturnType<WF>>>({
      namespace,
      queue: options.taskQueue,
      type: `workflow:${workflowType}`,
      payload: {
        namespace,
        workflowType,
        workflowId,
        workflowIdReusePolicy: options.workflowIdReusePolicy,
        runTimeoutMs: options.workflowRunTimeoutMs,
        retry: options.retry,
        memo: memoWithWorkflowUI(options.memo, options.ui),
        searchAttributes: options.searchAttributes,
        args: options.args ?? ([] as unknown as Parameters<WF>),
      },
      lease_timeout_seconds: options.leaseTimeoutSeconds,
    });
    return new WorkflowHandle(this.connection, task.id, workflowId, workflowType, task.payload?.runId);
  }

  async execute<WF extends WorkflowFunction>(
    workflow: WF | string,
    options: WorkflowStartOptions<Parameters<WF>> & { pollIntervalMs?: number; timeoutMs?: number } = {},
  ): Promise<Awaited<ReturnType<WF>>> {
    const handle = await this.start(workflow, options);
    return handle.result({ pollIntervalMs: options.pollIntervalMs, timeoutMs: options.timeoutMs });
  }

  async signalWithStart<WF extends WorkflowFunction, SignalArgs extends unknown[] = unknown[]>(
    workflow: WF | string,
    options: WorkflowStartOptions<Parameters<WF>> & {
      signal: WorkflowSignalDefinition<SignalArgs> | string;
      signalArgs?: SignalArgs;
    },
  ): Promise<WorkflowHandle<Awaited<ReturnType<WF>>>> {
    const workflowType = typeof workflow === 'string' ? workflow : workflow.name;
    if (!workflowType) {
      throw new Error('workflow type is required');
    }
    const workflowId = options.workflowId ?? crypto.randomUUID();
    const namespace = options.namespace ?? 'default';
    const signalName = typeof options.signal === 'string' ? options.signal : options.signal.name;
    const response = await this.connection.signalWithStartWorkflow<Parameters<WF>, SignalArgs, Awaited<ReturnType<WF>>>(workflowId, {
      namespace,
      queue: options.taskQueue,
      workflowType,
      workflowId,
      workflowIdReusePolicy: options.workflowIdReusePolicy,
      lease_timeout_seconds: options.leaseTimeoutSeconds,
      runTimeoutMs: options.workflowRunTimeoutMs,
      retry: options.retry,
      memo: memoWithWorkflowUI(options.memo, options.ui),
      searchAttributes: options.searchAttributes,
      args: options.args ?? ([] as unknown as Parameters<WF>),
      signal: {
        name: signalName,
        args: options.signalArgs ?? ([] as unknown as SignalArgs),
      },
    });
    return new WorkflowHandle(this.connection, response.task.id, response.workflow.id, response.workflow.type, response.workflow.run_id);
  }

  getHandle<R = unknown>(id: string, options: { workflowId?: string; runId?: string; taskId?: string; workflowType?: string } = {}): WorkflowHandle<R> {
    return new WorkflowHandle(
      this.connection,
      options.taskId ?? (options.workflowId ? id : undefined),
      options.workflowId ?? id,
      options.workflowType ?? 'unknown',
      options.runId,
    );
  }

  async list(options: {
    namespace?: string;
    workflowId?: string;
    runId?: string;
    taskQueue?: string;
    workflowType?: string;
    state?: WorkflowExecution['state'];
    query?: string;
    orderBy?: string;
    pageToken?: string;
    searchAttributes?: Record<string, string | number | boolean>;
    limit?: number;
    offset?: number;
  } = {}): Promise<Array<WorkflowExecutionDescription>> {
    const workflows = await this.connection.listWorkflows({
      namespace: options.namespace,
      id: options.workflowId,
      runId: options.runId,
      queue: options.taskQueue,
      type: options.workflowType,
      state: options.state,
      query: options.query,
      orderBy: options.orderBy,
      pageToken: options.pageToken,
      searchAttributes: options.searchAttributes,
      limit: options.limit,
      offset: options.offset,
    });
    return workflows
      .map((workflow) => describeWorkflowExecution(workflow));
  }

  async count(options: {
    namespace?: string;
    workflowId?: string;
    runId?: string;
    taskQueue?: string;
    workflowType?: string;
    state?: WorkflowExecution['state'];
    query?: string;
    searchAttributes?: Record<string, string | number | boolean>;
  } = {}): Promise<number> {
    const response = await this.connection.countWorkflows({
      namespace: options.namespace,
      id: options.workflowId,
      runId: options.runId,
      queue: options.taskQueue,
      type: options.workflowType,
      state: options.state,
      query: options.query,
      searchAttributes: options.searchAttributes,
    });
    return response.count;
  }
}

export class WorkflowHandle<R = unknown> {
  private resolvedTaskId: string | undefined;
  private resolvedWorkflowId: string;
  private resolvedWorkflowType: string;
  private resolvedRunId: string | undefined;

  constructor(
    private readonly connection: Connection,
    taskId: string | undefined,
    readonly workflowId: string,
    readonly workflowType: string,
    readonly runId?: string,
  ) {
    this.resolvedTaskId = taskId;
    this.resolvedWorkflowId = workflowId;
    this.resolvedWorkflowType = workflowType;
    this.resolvedRunId = runId;
  }

  get taskId(): string {
    return this.resolvedTaskId ?? this.resolvedWorkflowId;
  }

  async describe(): Promise<WorkflowExecutionDescription<R>> {
    const workflow = await this.resolveWorkflow().catch(async () => undefined);
    if (workflow) {
      return describeWorkflowExecution(workflow);
    }
    return describeWorkflowTask(await this.connection.getTask<WorkflowPayload, R>(this.taskId));
  }

  async result(options: { pollIntervalMs?: number; timeoutMs?: number } = {}): Promise<R> {
    const started = Date.now();
    const pollIntervalMs = options.pollIntervalMs ?? 1000;
    let taskId = await this.resolveTaskId();
    for (;;) {
      const task = await this.connection.getTask<WorkflowPayload, R>(taskId);
      if (task.state === 'succeeded') {
        if (task.result?.continue_as_new?.task_id) {
          taskId = task.result.continue_as_new.task_id;
          this.resolvedTaskId = taskId;
          continue;
        }
        return task.result?.value as R;
      }
      if (task.state === 'failed') {
        const workflow = await this.resolveWorkflow().catch(async () => undefined);
        if (workflow && workflow.state === 'running' && workflow.task_id !== taskId) {
          taskId = workflow.task_id;
          this.resolvedTaskId = taskId;
          continue;
        }
        if (workflow && workflow.state === 'succeeded') {
          return workflow.result?.value as R;
        }
        throw new TaskFailedError(task.id, task.error ?? 'workflow failed');
      }
      if (options.timeoutMs != null && Date.now() - started > options.timeoutMs) {
        throw new TimeoutFailure(`workflow ${this.resolvedWorkflowId} timed out`);
      }
      await delay(pollIntervalMs);
    }
  }

  async events(): Promise<TaskEvent[]> {
    return this.connection.getTaskEvents(await this.resolveTaskId());
  }

  async *watchEvents(options: WatchEventsOptions = {}): AsyncGenerator<TaskEvent, void, void> {
    const taskId = await this.resolveTaskId();
    yield* watchTaskEvents(this.connection, taskId, options);
  }

  async history(): Promise<WorkflowHistoryEvent[]> {
    return this.connection.getWorkflowHistory(await this.resolveWorkflowRunOrId());
  }

  async signal<Args extends unknown[]>(definition: WorkflowSignalDefinition<Args> | string, ...args: Args): Promise<void> {
    const name = typeof definition === 'string' ? definition : definition.name;
    await this.connection.signalWorkflow(await this.resolveWorkflowRunOrId(), { name, args });
  }

  async cancel(reason?: string): Promise<void> {
    await this.connection.cancelWorkflow(await this.resolveWorkflowRunOrId(), { reason });
  }

  async terminate(reason?: string): Promise<void> {
    await this.connection.terminateWorkflow(await this.resolveWorkflowRunOrId(), { reason });
  }

  async query<R = unknown, Args extends unknown[] = unknown[]>(definition: WorkflowQueryDefinition<R, Args> | string, ...args: Args): Promise<R> {
    const name = typeof definition === 'string' ? definition : definition.name;
    const description = await this.describe();
    const workflowType = await this.resolveWorkflowType();
    const task = await this.connection.enqueueTask<WorkflowQueryPayload<Args>, R>({
      queue: description.taskQueue,
      namespace: description.namespace,
      type: `query:${workflowType}`,
      payload: {
        workflowId: await this.resolveWorkflowId(),
        workflowRunId: description.runId,
        workflowType,
        queryName: name,
        args,
      },
    });
    for (;;) {
      const current = await this.connection.getTask<WorkflowQueryPayload<Args>, R>(task.id);
      if (current.state === 'succeeded') return current.result?.value as R;
      if (current.state === 'failed') {
        throw new TaskFailedError(current.id, current.error ?? 'workflow query failed');
      }
      await delay(50);
    }
  }

  async executeUpdate<R = unknown, Args extends unknown[] = unknown[]>(definition: WorkflowUpdateDefinition<R, Args> | string, ...args: Args): Promise<R> {
    const handle = await this.startUpdate(definition, ...args);
    return handle.result();
  }

  async startUpdate<R = unknown, Args extends unknown[] = unknown[]>(
    definition: WorkflowUpdateDefinition<R, Args> | string,
    ...args: Args
  ): Promise<WorkflowUpdateHandle<R>> {
    const name = typeof definition === 'string' ? definition : definition.name;
    const description = await this.describe();
    const workflowType = await this.resolveWorkflowType();
    const task = await this.connection.enqueueTask<WorkflowUpdatePayload<Args>, R>({
      queue: description.taskQueue,
      namespace: description.namespace,
      type: `update:${workflowType}`,
      payload: {
        workflowId: await this.resolveWorkflowId(),
        workflowRunId: description.runId,
        workflowType,
        updateName: name,
        args,
      },
    });
    return new WorkflowUpdateHandle(this.connection, task.id);
  }

  private async resolveTaskId(): Promise<string> {
    if (this.resolvedTaskId) {
      return this.resolvedTaskId;
    }
    const workflow = await this.resolveWorkflow().catch(async () => undefined);
    if (workflow) {
      return workflow.task_id;
    }
    const task = await this.connection.getTask<WorkflowPayload, R>(this.taskId);
    this.resolvedTaskId = task.id;
    this.resolvedWorkflowId = task.payload?.workflowId ?? task.id;
    this.resolvedRunId = task.payload?.runId ?? this.resolvedRunId;
    this.resolvedWorkflowType = task.payload?.workflowType ?? task.type.replace(/^workflow:/, '');
    return task.id;
  }

  private async resolveWorkflow(): Promise<WorkflowExecution<R>> {
    const workflow = await this.connection.getWorkflow<R>(this.resolvedRunId ?? this.resolvedWorkflowId);
    this.resolvedTaskId = workflow.task_id;
    this.resolvedWorkflowId = workflow.id;
    this.resolvedRunId = workflow.run_id;
    this.resolvedWorkflowType = workflow.type;
    return workflow;
  }

  private async resolveWorkflowType(): Promise<string> {
    if (this.resolvedWorkflowType !== 'unknown') {
      return this.resolvedWorkflowType;
    }
    const workflow = await this.resolveWorkflow();
    return workflow.type;
  }

  private async resolveWorkflowId(): Promise<string> {
    if (this.resolvedWorkflowId !== this.taskId) {
      return this.resolvedWorkflowId;
    }
    await this.resolveTaskId();
    return this.resolvedWorkflowId;
  }

  private async resolveWorkflowRunOrId(): Promise<string> {
    if (this.resolvedRunId) {
      return this.resolvedRunId;
    }
    await this.resolveWorkflow();
    return this.resolvedRunId ?? this.resolvedWorkflowId;
  }
}

export class WorkflowUpdateHandle<R = unknown> {
  constructor(
    private readonly connection: Connection,
    readonly updateId: string,
  ) {}

  async result(options: { pollIntervalMs?: number; timeoutMs?: number } = {}): Promise<R> {
    const started = Date.now();
    const pollIntervalMs = options.pollIntervalMs ?? 50;
    for (;;) {
      const current = await this.connection.getTask<WorkflowUpdatePayload, R>(this.updateId);
      if (current.state === 'succeeded') return current.result?.value as R;
      if (current.state === 'failed') {
        throw new TaskFailedError(current.id, current.error ?? 'workflow update failed');
      }
      if (options.timeoutMs != null && Date.now() - started > options.timeoutMs) {
        throw new TimeoutFailure(`workflow update ${this.updateId} timed out`);
      }
      await delay(pollIntervalMs);
    }
  }

  async events(): Promise<TaskEvent[]> {
    return this.connection.getTaskEvents(this.updateId);
  }

  async *watchEvents(options: WatchEventsOptions = {}): AsyncGenerator<TaskEvent, void, void> {
    yield* watchTaskEvents(this.connection, this.updateId, options);
  }
}

export class TaskClient {
  constructor(private readonly connection: Connection) {}

  async enqueue<P = unknown, R = unknown>(input: {
    type: string;
    namespace?: string;
    queue?: string;
    payload?: P;
    leaseTimeoutSeconds?: number;
  }): Promise<Task<P, R>> {
    return this.connection.enqueueTask<P, R>({
      type: input.type,
      namespace: input.namespace,
      queue: input.queue,
      payload: input.payload,
      lease_timeout_seconds: input.leaseTimeoutSeconds,
    });
  }

  async shellExec(input: { queue?: string } & ShellExecPayload): Promise<Task<ShellExecPayload>> {
    const { queue, ...payload } = input;
    return this.enqueue({ type: 'shell.exec', queue, payload });
  }

  /**
   * Enqueue a `container.exec` task. The Go agent will launch a per-task
   * container from `input.image` via its docker CLI and stream stdout /
   * stderr back as task events. Requires the agent to be on the docker
   * socket proxy network (DOCKER_HOST set on the agent process).
   *
   * Mirrors {@link shellExec}: pass the payload fields plus an optional
   * `queue`, get back the enqueued task.
   */
  async containerExec(input: { queue?: string } & ContainerExecPayload): Promise<Task<ContainerExecPayload>> {
    const { queue, ...payload } = input;
    return this.enqueue({ type: 'container.exec', queue, payload });
  }

  async workflowRuntime(input: {
    namespace?: string;
    queue?: string;
    runtimeQueue?: string;
    runtimeNamespace?: string;
    leaseTimeoutSeconds?: number;
  } & Omit<WorkflowRuntimePayload, 'queue' | 'namespace'>): Promise<Task<WorkflowRuntimePayload>> {
    const {
      namespace,
      queue,
      runtimeQueue,
      runtimeNamespace,
      leaseTimeoutSeconds,
      ...payload
    } = input;
    const isolatedRuntimeQueue = runtimeQueue ?? `postgrip-runtime-${crypto.randomUUID()}`;
    return this.enqueue({
      type: 'workflow.runtime',
      namespace,
      queue,
      leaseTimeoutSeconds,
      payload: {
        ...payload,
        namespace: runtimeNamespace,
        queue: isolatedRuntimeQueue,
      },
    });
  }

  async noop(queue?: string): Promise<Task> {
    return this.enqueue({ type: 'noop', queue });
  }

  async events(taskId: string): Promise<TaskEvent[]> {
    return this.connection.getTaskEvents(taskId);
  }

  async *watchEvents(taskId: string, options: WatchEventsOptions = {}): AsyncGenerator<TaskEvent, void, void> {
    yield* watchTaskEvents(this.connection, taskId, options);
  }
}

export class ScheduleClient {
  constructor(private readonly connection: Connection) {}

  async create<Args extends unknown[] = unknown[]>(input: CreateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.connection.createSchedule(input);
  }

  async createWorkflowSchedule<WF extends WorkflowFunction>(input: {
    scheduleId?: string;
    namespace?: string;
    workflow: WF | string;
    taskQueue?: string;
    args?: Parameters<WF>;
    intervalSeconds?: number;
    cron?: string;
    calendar?: {
      minute?: number[];
      hour?: number[];
      day_of_month?: number[];
      month?: number[];
      day_of_week?: number[];
    };
    timezone?: string;
    jitterSeconds?: number;
    catchUpWindowSeconds?: number;
    missedRunPolicy?: 'catch_up' | 'skip';
    startAt?: Date | string;
    workflowId?: string;
    workflowIdReusePolicy?: WorkflowIdReusePolicy;
    overlapPolicy?: ScheduleOverlapPolicy;
    workflowRunTimeoutMs?: number;
    retry?: RetryPolicy;
    memo?: Record<string, unknown>;
    searchAttributes?: Record<string, unknown>;
    ui?: WorkflowUIMetadata;
  }): Promise<Schedule<Parameters<WF>>> {
    const workflowType = typeof input.workflow === 'string' ? input.workflow : input.workflow.name;
    if (!workflowType) {
      throw new Error('workflow type is required');
    }
    return this.create({
      id: input.scheduleId,
      namespace: input.namespace,
      overlap_policy: input.overlapPolicy,
      spec: {
        interval_seconds: input.intervalSeconds,
        cron: input.cron,
        calendar: input.calendar,
        timezone: input.timezone,
        jitter_seconds: input.jitterSeconds,
        catch_up_window_seconds: input.catchUpWindowSeconds,
        missed_run_policy: input.missedRunPolicy,
        start_at: input.startAt instanceof Date ? input.startAt.toISOString() : input.startAt,
      },
      action: {
        namespace: input.namespace,
        queue: input.taskQueue,
        workflowType,
        workflowId: input.workflowId,
        workflowIdReusePolicy: input.workflowIdReusePolicy,
        runTimeoutMs: input.workflowRunTimeoutMs,
        retry: input.retry,
        memo: memoWithWorkflowUI(input.memo, input.ui),
        searchAttributes: input.searchAttributes,
        args: input.args,
      },
    });
  }

  async list<Args extends unknown[] = unknown[]>(options: {
    namespace?: string;
    state?: ScheduleState;
    limit?: number;
    offset?: number;
  } = {}): Promise<Array<Schedule<Args>>> {
    return this.connection.listSchedules(options);
  }

  async get<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.connection.getSchedule(scheduleId);
  }

  async update<Args extends unknown[] = unknown[]>(scheduleId: string, input: UpdateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.connection.updateSchedule(scheduleId, input);
  }

  async delete<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.connection.deleteSchedule(scheduleId);
  }

  async pause<Args extends unknown[] = unknown[]>(scheduleId: string, request: PauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.connection.pauseSchedule(scheduleId, request);
  }

  async unpause<Args extends unknown[] = unknown[]>(scheduleId: string, request: UnpauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.connection.unpauseSchedule(scheduleId, request);
  }

  async trigger<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: TriggerScheduleRequest = {},
  ): Promise<TriggerScheduleResponse<Args, R>> {
    return this.connection.triggerSchedule(scheduleId, request);
  }

  async backfill<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: BackfillScheduleRequest,
  ): Promise<BackfillScheduleResponse<Args, R>> {
    return this.connection.backfillSchedule(scheduleId, request);
  }
}

function describeWorkflowTask<R>(task: Task<WorkflowPayload, R>): WorkflowExecutionDescription<R> {
  return {
    workflowId: task.payload?.workflowId ?? task.id,
    runId: task.payload?.runId,
    taskId: task.id,
    namespace: task.namespace,
    taskQueue: task.queue,
    workflowType: task.payload?.workflowType ?? task.type.replace(/^workflow:/, ''),
    status: task.state,
    attempt: task.attempt,
    leaseTimeoutSeconds: task.lease_timeout_seconds,
    workflowRunTimeoutMs: task.payload?.runTimeoutMs,
    retry: task.payload?.retry,
    memo: task.payload?.memo,
    searchAttributes: task.payload?.searchAttributes,
    result: task.result?.value,
    error: task.error,
    startedAt: task.created_at,
    updatedAt: task.updated_at,
  };
}

function describeWorkflowExecution<R>(workflow: WorkflowExecution<R>): WorkflowExecutionDescription<R> {
  return {
    workflowId: workflow.id,
    runId: workflow.run_id,
    taskId: workflow.task_id,
    namespace: workflow.namespace,
    taskQueue: workflow.queue,
    workflowType: workflow.type,
    status: workflow.state === 'running' ? 'leased' : workflow.state,
    attempt: workflow.attempt,
    workflowRunTimeoutMs: workflow.run_timeout_ms,
    retry: workflow.retry,
    memo: workflow.memo,
    searchAttributes: workflow.search_attributes,
    result: workflow.result?.value as R,
    error: workflow.error,
    startedAt: workflow.created_at,
    updatedAt: workflow.updated_at,
  };
}

async function* watchTaskEvents(
  connection: Connection,
  taskId: string,
  options: WatchEventsOptions,
): AsyncGenerator<TaskEvent, void, void> {
  const pollIntervalMs = options.pollIntervalMs ?? 1000;
  const seen = new Set<string>();
  while (!options.signal?.aborted) {
    const events = await connection.getTaskEvents(taskId);
    for (const event of events) {
      if (!seen.has(event.id)) {
        seen.add(event.id);
        yield event;
      }
    }
    const task = await connection.getTask(taskId);
    if (task.state === 'succeeded' || task.state === 'failed') {
      return;
    }
    await delay(pollIntervalMs);
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
