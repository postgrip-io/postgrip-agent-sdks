import type {
  BackfillScheduleRequest,
  BackfillScheduleResponse,
  CompactResponse,
  CreateSandboxSessionRequest,
  CreateSandboxSessionResponse,
  CreateScheduleRequest,
  Sandbox,
  SandboxCreateRequest,
  SandboxListResponse,
  SandboxWorkspace,
  Namespace,
  EnqueueTaskRequest,
  PauseScheduleRequest,
  PollTaskResponse,
  Schedule,
  ScheduleState,
  TriggerScheduleRequest,
  TriggerScheduleResponse,
  UnpauseScheduleRequest,
  UpdateScheduleRequest,
  CancelWorkflowRequest,
  TerminateWorkflowRequest,
  Task,
  TaskEvent,
  TaskEventInput,
  TaskResult,
  TaskState,
  WorkflowExecution,
  WorkflowCountResponse,
  WorkflowHistoryEvent,
  SignalWorkflowRequest,
  SignalWithStartWorkflowRequest,
  SignalWithStartWorkflowResponse,
} from './types.js';
import {
  HEADER_AGENT_SIGNATURE,
  HEADER_AGENT_SIGNATURE_KEY_ID,
  HEADER_AGENT_SIGNATURE_TIMESTAMP,
  importSigningKeyFromBase64,
  signRequest,
  type AgentSigningKey,
} from './_signing.js';

export interface ConnectionOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
  headers?: HeadersInit;
  /**
   * Management token, sent as `Authorization: Bearer <token>` on every
   * management-lane request — which is all of them except the agent runtime
   * endpoints. Required for the sandbox APIs, which reject agent tokens.
   *
   * Treat the value as opaque: the console issues a bare hex string with no
   * prefix, so there is nothing to validate.
   */
  authToken?: string;
  agentAuth?: AgentAuthOptions;
}

export interface AgentAuthOptions {
  agentId?: string;
  workerId?: string;
  name?: string;
  host?: string;
  namespace?: string;
  queue?: string;
  accessToken?: string;
  refreshToken?: string;
  accessExpiresAt?: string;
  signingPrivateKey?: string;
}

export const DEFAULT_BASE_URL = 'https://agentorchestrator.postgrip.app';

interface PollTaskOptions {
  namespace?: string;
  queue: string;
  agentId?: string;
  workerId?: string;
  waitSeconds?: number;
  taskTypes?: string[];
  signal?: AbortSignal;
}

interface AgentSessionResponse {
  agentId?: string;
  tenantId: string;
  tokenFamilyId: string;
  accessToken: string;
  refreshToken: string;
  accessExpiresAt: string;
  refreshExpiresAt: string;
  status: string;
  trustState: string;
}

export class Connection {
  readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;
  private readonly headers: HeadersInit | undefined;
  private readonly authToken: string | undefined;
  private agentAuth: AgentAuthOptions = {};
  private agentSessionRefresh: Promise<void> | undefined;
  // Ed25519 keypair injected by the host agent for managed workflow runtimes.
  private agentSigningKey: AgentSigningKey | undefined;

  private constructor(options: Required<Pick<ConnectionOptions, 'baseUrl'>> & Omit<ConnectionOptions, 'baseUrl'>) {
    this.baseUrl = options.baseUrl.replace(/\/+$/, '');
    this.fetchImpl = options.fetch ?? fetch;
    this.headers = options.headers;
    this.authToken = options.authToken?.trim() || undefined;
    this.configureAgentAuth(options.agentAuth ?? {});
  }

  static async connect(options: ConnectionOptions = {}): Promise<Connection> {
    const connection = new Connection({
      ...options,
      baseUrl: options.baseUrl ?? process.env.POSTGRIP_AGENTORCHESTRATOR_URL ?? DEFAULT_BASE_URL,
    });
    await connection.health();
    return connection;
  }

  async health(): Promise<{ status: string }> {
    return this.request('GET', '/healthz');
  }

  configureAgentAuth(options: AgentAuthOptions): void {
    const normalized = normalizeAgentAuthOptions(options);
    this.agentAuth = {
      ...this.agentAuth,
      ...Object.fromEntries(Object.entries(normalized).filter(([, value]) => value != null && value !== '')),
    };
    if (normalized.signingPrivateKey) {
      this.agentSigningKey = importSigningKeyFromBase64(normalized.signingPrivateKey);
    }
  }

  async ensureAgentSession(options: AgentAuthOptions = {}): Promise<boolean> {
    this.configureAgentAuth(options);
    if (this.agentAuth.accessToken && accessTokenIsFresh(this.agentAuth.accessExpiresAt)) {
      return true;
    }
    if (!this.agentAuth.refreshToken) {
      throw new Error('postgrip-agent: managed runtime credentials are required; submit workflow.runtime work to a host agent instead of enrolling SDK agents');
    }
    if (!this.agentSessionRefresh) {
      this.agentSessionRefresh = this.refreshAgentSession().finally(() => {
        this.agentSessionRefresh = undefined;
      });
    }
    await this.agentSessionRefresh;
    return Boolean(this.agentAuth.accessToken);
  }

  async ready(): Promise<{ status: string; stats: Record<string, number> }> {
    return this.request('GET', '/readyz');
  }

  async listNamespaces(): Promise<Namespace[]> {
    return this.request('GET', '/api/v1/namespaces');
  }

  async createNamespace(name: string): Promise<Namespace> {
    return this.request('POST', '/api/v1/namespaces', { name });
  }

  async compact(options: { retentionSeconds?: number } = {}): Promise<CompactResponse> {
    return this.request('POST', '/api/v1/admin/compact', { retention_seconds: options.retentionSeconds ?? 0 });
  }

  async enqueueTask<P = unknown, R = unknown>(request: EnqueueTaskRequest<P>): Promise<Task<P, R>> {
    if (runtimeOnlyTaskType(request.type) && !this.hasAgentRuntimeCredentials()) {
      throw new Error('postgrip-agent: workflow tasks can only be enqueued from a managed runtime; submit workflow.runtime to an agent pool');
    }
    return this.request('POST', '/api/v1/tasks', request);
  }

  async listTasks<P = unknown, R = unknown>(options: {
    namespace?: string;
    queue?: string;
    type?: string;
    state?: TaskState;
    limit?: number;
    offset?: number;
  } = {}): Promise<Task<P, R>[]> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.queue) query.set('queue', options.queue);
    if (options.type) query.set('type', options.type);
    if (options.state) query.set('state', options.state);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    return this.request('GET', `/api/v1/tasks${query.size ? `?${query.toString()}` : ''}`);
  }

  async getTask<P = unknown, R = unknown>(taskId: string): Promise<Task<P, R>> {
    return this.request('GET', `/api/v1/tasks/${encodeURIComponent(taskId)}`);
  }

  async getTaskEvents(taskId: string): Promise<TaskEvent[]> {
    return this.request('GET', `/api/v1/tasks/${encodeURIComponent(taskId)}/events`);
  }

  async createSchedule<Args extends unknown[] = unknown[]>(request: CreateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.request('POST', '/api/v1/schedules', request);
  }

  async listSchedules<Args extends unknown[] = unknown[]>(options: {
    namespace?: string;
    state?: ScheduleState;
    limit?: number;
    offset?: number;
  } = {}): Promise<Array<Schedule<Args>>> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.state) query.set('state', options.state);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    return this.request('GET', `/api/v1/schedules${query.size ? `?${query.toString()}` : ''}`);
  }

  async getSchedule<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.request('GET', `/api/v1/schedules/${encodeURIComponent(scheduleId)}`);
  }

  async updateSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: UpdateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.request('PATCH', `/api/v1/schedules/${encodeURIComponent(scheduleId)}`, request);
  }

  async deleteSchedule<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.request('DELETE', `/api/v1/schedules/${encodeURIComponent(scheduleId)}`);
  }

  async pauseSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: PauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.request('POST', `/api/v1/schedules/${encodeURIComponent(scheduleId)}/pause`, request);
  }

  async unpauseSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: UnpauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.request('POST', `/api/v1/schedules/${encodeURIComponent(scheduleId)}/unpause`, request);
  }

  async triggerSchedule<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: TriggerScheduleRequest = {},
  ): Promise<TriggerScheduleResponse<Args, R>> {
    return this.request('POST', `/api/v1/schedules/${encodeURIComponent(scheduleId)}/trigger`, request);
  }

  async backfillSchedule<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: BackfillScheduleRequest,
  ): Promise<BackfillScheduleResponse<Args, R>> {
    return this.request('POST', `/api/v1/schedules/${encodeURIComponent(scheduleId)}/backfill`, request);
  }

  async listWorkflows<R = unknown>(options: {
    namespace?: string;
    id?: string;
    runId?: string;
    type?: string;
    queue?: string;
    state?: WorkflowExecution['state'];
    query?: string;
    orderBy?: string;
    pageToken?: string;
    searchAttributes?: Record<string, string | number | boolean>;
    limit?: number;
    offset?: number;
  } = {}): Promise<WorkflowExecution<R>[]> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.id) query.set('id', options.id);
    if (options.runId) query.set('run_id', options.runId);
    if (options.type) query.set('type', options.type);
    if (options.queue) query.set('queue', options.queue);
    if (options.state) query.set('state', options.state);
    if (options.query) query.set('query', options.query);
    if (options.orderBy) query.set('order_by', options.orderBy);
    if (options.pageToken) query.set('page_token', options.pageToken);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    for (const [key, value] of Object.entries(options.searchAttributes ?? {})) {
      query.set(`search.${key}`, String(value));
    }
    return this.request('GET', `/api/v1/workflows${query.size ? `?${query.toString()}` : ''}`);
  }

  async countWorkflows(options: {
    namespace?: string;
    id?: string;
    runId?: string;
    type?: string;
    queue?: string;
    state?: WorkflowExecution['state'];
    query?: string;
    searchAttributes?: Record<string, string | number | boolean>;
  } = {}): Promise<WorkflowCountResponse> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.id) query.set('id', options.id);
    if (options.runId) query.set('run_id', options.runId);
    if (options.type) query.set('type', options.type);
    if (options.queue) query.set('queue', options.queue);
    if (options.state) query.set('state', options.state);
    if (options.query) query.set('query', options.query);
    for (const [key, value] of Object.entries(options.searchAttributes ?? {})) {
      query.set(`search.${key}`, String(value));
    }
    return this.request('GET', `/api/v1/workflows/count${query.size ? `?${query.toString()}` : ''}`);
  }

  async getWorkflow<R = unknown>(workflowId: string): Promise<WorkflowExecution<R>> {
    return this.request('GET', `/api/v1/workflows/${encodeURIComponent(workflowId)}`);
  }

  async getWorkflowHistory(workflowId: string): Promise<WorkflowHistoryEvent[]> {
    return this.request('GET', `/api/v1/workflows/${encodeURIComponent(workflowId)}/history`);
  }

  async signalWorkflow<Args extends unknown[] = unknown[]>(workflowId: string, request: SignalWorkflowRequest<Args>): Promise<WorkflowHistoryEvent> {
    return this.request('POST', `/api/v1/workflows/${encodeURIComponent(workflowId)}/signal`, request);
  }

  async signalWithStartWorkflow<WorkflowArgs extends unknown[] = unknown[], SignalArgs extends unknown[] = unknown[], R = unknown>(
    workflowId: string,
    request: SignalWithStartWorkflowRequest<WorkflowArgs, SignalArgs>,
  ): Promise<SignalWithStartWorkflowResponse<WorkflowArgs, R>> {
    if (!this.hasAgentRuntimeCredentials()) {
      throw new Error('postgrip-agent: signal-with-start can only run from a managed runtime; submit workflow.runtime to an agent pool');
    }
    return this.request('POST', `/api/v1/workflows/${encodeURIComponent(workflowId)}/signal-with-start`, request);
  }

  async cancelWorkflow(workflowId: string, request: CancelWorkflowRequest = {}): Promise<WorkflowHistoryEvent> {
    return this.request('POST', `/api/v1/workflows/${encodeURIComponent(workflowId)}/cancel`, request);
  }

  async terminateWorkflow(workflowId: string, request: TerminateWorkflowRequest = {}): Promise<WorkflowHistoryEvent> {
    return this.request('POST', `/api/v1/workflows/${encodeURIComponent(workflowId)}/terminate`, request);
  }

  async pollTask<P = unknown, R = unknown>(options: PollTaskOptions): Promise<Task<P, R> | undefined> {
    const agentId = options.agentId ?? options.workerId;
    if (!agentId) {
      throw new Error('agentId is required');
    }
    const query = new URLSearchParams({
      namespace: options.namespace ?? 'default',
      queue: options.queue,
      agent_id: agentId,
      wait_seconds: String(options.waitSeconds ?? 20),
    });
    if (options.taskTypes?.length) {
      query.set('task_types', options.taskTypes.join(','));
    }
    await this.ensureAgentSession({ namespace: options.namespace ?? 'default', queue: options.queue, agentId });
    const response = await this.request<PollTaskResponse<P, R>>('GET', `/api/v1/agent/poll?${query.toString()}`, undefined, options.signal, { agentAuth: true });
    return response.task;
  }

  async heartbeatTask(taskId: string, agentId: string, event?: TaskEventInput): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.request('POST', this.agentTaskPath(taskId, 'heartbeat', agentId), { event }, undefined, { agentAuth: true });
  }

  async appendTaskEvent(taskId: string, agentId: string, event: TaskEventInput): Promise<TaskEvent> {
    await this.ensureAgentSession({ agentId });
    return this.request('POST', this.agentTaskPath(taskId, 'events', agentId), { event }, undefined, { agentAuth: true });
  }

  async completeTask<R = unknown>(taskId: string, agentId: string, result: TaskResult<R>): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.request('POST', this.agentTaskPath(taskId, 'complete', agentId), { result }, undefined, { agentAuth: true });
  }

  async blockTask(taskId: string, agentId: string, reason?: string): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.request('POST', this.agentTaskPath(taskId, 'block', agentId), { reason }, undefined, { agentAuth: true });
  }

  async failTask<R = unknown>(taskId: string, agentId: string, error: string, result?: TaskResult<R>): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.request('POST', this.agentTaskPath(taskId, 'fail', agentId), { error, result }, undefined, { agentAuth: true });
  }

  private agentTaskPath(taskId: string, action: string, agentId: string): string {
    return `/api/v1/agent/tasks/${encodeURIComponent(taskId)}/${action}?agent_id=${encodeURIComponent(agentId)}`;
  }

  private async refreshAgentSession(): Promise<void> {
    if (this.agentAuth.refreshToken) {
      const session = await this.request<AgentSessionResponse>('POST', '/api/v1/agent/session/refresh', {
        refreshToken: this.agentAuth.refreshToken,
      });
      this.applyAgentSession(session);
      return;
    }
    throw new Error('postgrip-agent: managed runtime refresh token is required');
  }

  private applyAgentSession(session: AgentSessionResponse): void {
    this.configureAgentAuth({
      agentId: session.agentId,
      accessToken: session.accessToken,
      refreshToken: session.refreshToken,
      accessExpiresAt: session.accessExpiresAt,
    });
  }


  // --- sandbox platform ---------------------------------------------------
  //
  // These are management-lane endpoints: an agent access token is rejected on
  // all of them, so the connection needs `authToken`.

  async createSandbox(request: SandboxCreateRequest): Promise<Sandbox> {
    return this.request('POST', '/api/v1/sandboxes', request);
  }

  async listSandboxes(): Promise<Sandbox[]> {
    // The endpoint returns an envelope, not a bare array.
    const response = await this.request<SandboxListResponse>('GET', '/api/v1/sandboxes');
    return response?.sandboxes ?? [];
  }

  async getSandbox(sandboxId: string): Promise<Sandbox> {
    return this.request('GET', `/api/v1/sandboxes/${encodeURIComponent(sandboxId)}`);
  }

  async startSandbox(sandboxId: string): Promise<Sandbox> {
    return this.request('POST', `/api/v1/sandboxes/${encodeURIComponent(sandboxId)}/start`);
  }

  async stopSandbox(sandboxId: string): Promise<Sandbox> {
    return this.request('POST', `/api/v1/sandboxes/${encodeURIComponent(sandboxId)}/stop`);
  }

  async deleteSandbox(sandboxId: string): Promise<Sandbox> {
    return this.request('DELETE', `/api/v1/sandboxes/${encodeURIComponent(sandboxId)}`);
  }

  async createSandboxSession(
    sandboxId: string,
    request: CreateSandboxSessionRequest = {},
  ): Promise<CreateSandboxSessionResponse> {
    return this.request('POST', `/api/v1/sandboxes/${encodeURIComponent(sandboxId)}/sessions`, request);
  }

  /**
   * Uploads a gzipped tar archive and returns the workspace record whose `id`
   * goes in `SandboxCreateRequest.workspaceId`.
   *
   * The body is the raw archive — not multipart. Uploading identical bytes
   * twice returns the pre-existing record rather than creating a second one.
   */
  async uploadWorkspace(
    archive: BodyInit,
    metadata: { repositoryName?: string; revision?: string } = {},
  ): Promise<SandboxWorkspace> {
    const headers = new Headers(this.headers);
    headers.set('Content-Type', 'application/gzip');
    if (this.authToken && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${this.authToken}`);
    }
    if (metadata.repositoryName) headers.set('X-PostGrip-Repository', metadata.repositoryName);
    if (metadata.revision) headers.set('X-PostGrip-Revision', metadata.revision);

    const response = await this.fetchImpl(`${this.baseUrl}/api/v1/workspaces`, {
      method: 'POST',
      headers,
      body: archive,
      // Node's fetch requires this for a streaming request body.
      ...(typeof archive === 'object' && archive !== null && Symbol.asyncIterator in (archive as object)
        ? { duplex: 'half' }
        : {}),
    } as RequestInit);
    if (!response.ok) {
      const payload = await response.json().catch(() => ({ error: response.statusText }));
      throw new Error(typeof payload.error === 'string' ? payload.error : response.statusText);
    }
    return (await response.json()) as SandboxWorkspace;
  }

  /** Management Authorization header value, for the relay dial. */
  authorizationHeader(): string | undefined {
    return this.authToken ? `Bearer ${this.authToken}` : undefined;
  }

  private async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal, options: { agentAuth?: boolean } = {}): Promise<T> {
    const useAgentAuth = options.agentAuth === true || this.shouldUseAgentRuntimeAuth(path);
    if (useAgentAuth) {
      await this.ensureAgentSession();
    }
    const headers = new Headers(this.headers);
    if (useAgentAuth && this.agentAuth.accessToken) {
      headers.set('Authorization', `Bearer ${this.agentAuth.accessToken}`);
    } else if (!useAgentAuth && this.authToken && !headers.has('Authorization')) {
      // An explicit header wins, so callers already passing one keep working.
      headers.set('Authorization', `Bearer ${this.authToken}`);
    }
    if (body != null) {
      headers.set('Content-Type', 'application/json');
    }
    const bodyString = body == null ? '' : JSON.stringify(body);
    if (useAgentAuth && this.agentSigningKey) {
      const queryStart = path.indexOf('?');
      const reqPath = queryStart === -1 ? path : path.slice(0, queryStart);
      const reqQuery = queryStart === -1 ? '' : path.slice(queryStart + 1);
      const ts = Math.floor(Date.now() / 1000);
      headers.set(HEADER_AGENT_SIGNATURE_TIMESTAMP, String(ts));
      headers.set(HEADER_AGENT_SIGNATURE_KEY_ID, this.agentSigningKey.keyId);
      headers.set(HEADER_AGENT_SIGNATURE, signRequest(
        this.agentSigningKey.privateKey, method, reqPath, reqQuery, ts, Buffer.from(bodyString),
      ));
    }
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      signal,
      headers,
      body: body == null ? undefined : bodyString,
    });
    if (!response.ok) {
      const payload = await response.json().catch(() => ({ error: response.statusText }));
      throw new Error(typeof payload.error === 'string' ? payload.error : response.statusText);
    }
    const text = await response.text();
    if (text === '') {
      return undefined as T;
    }
    try {
      return JSON.parse(text) as T;
    } catch (err) {
      throw new Error(`postgrip-agent: ${method} ${path} -> ${response.status} (parse failed): ${text.slice(0, 200)}`);
    }
  }

  private shouldUseAgentRuntimeAuth(path: string): boolean {
    if (!this.agentAuth.accessToken && !this.agentAuth.refreshToken) {
      return false;
    }
    const queryStart = path.indexOf('?');
    const reqPath = queryStart === -1 ? path : path.slice(0, queryStart);
    return reqPath === '/api/v1/tasks'
      || reqPath.startsWith('/api/v1/tasks/')
      || reqPath === '/api/v1/workflows'
      || reqPath.startsWith('/api/v1/workflows/')
      || reqPath === '/api/v1/namespaces';
  }

  private hasAgentRuntimeCredentials(): boolean {
    return Boolean(this.agentAuth.accessToken || this.agentAuth.refreshToken);
  }
}

function accessTokenIsFresh(expiresAt: string | undefined): boolean {
  if (!expiresAt) return false;
  const expiresAtMs = Date.parse(expiresAt);
  return Number.isFinite(expiresAtMs) && expiresAtMs > Date.now() + 30_000;
}

function normalizeAgentAuthOptions(options: AgentAuthOptions): AgentAuthOptions {
  const { workerId, ...canonical } = options;
  return {
    ...canonical,
    agentId: options.agentId ?? workerId,
  };
}

function runtimeOnlyTaskType(taskType: string): boolean {
  const normalized = taskType.trim();
  return normalized === 'timer'
    || normalized.startsWith('workflow:')
    || normalized.startsWith('activity:')
    || normalized.startsWith('query:')
    || normalized.startsWith('update:');
}
