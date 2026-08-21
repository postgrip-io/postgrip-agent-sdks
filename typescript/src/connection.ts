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
import {
  encodeOpenAPIQuery,
  OpenAPIClient,
  resolveOpenAPIOperation,
  UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REPOSITORY,
  UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REVISION,
  type OpenAPIOperationId,
  type OpenAPIComponents,
  type OpenAPIRequestOptions,
  type OpenAPIResponseBody,
} from './generated/openapi.js';

export interface ConnectionOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
  headers?: HeadersInit;
  /**
   * Bearer token sent on management and global-admin requests. Use a tenant
   * management token for normal APIs and a dedicated connection configured
   * with the service's global admin token for compaction.
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

export const DEFAULT_BASE_URL = 'https://agentorchestrator1.postgrip.io';

interface PollTaskOptions {
  namespace?: string;
  queue: string;
  agentId?: string;
  workerId?: string;
  waitSeconds?: number;
  taskTypes?: string[];
  signal?: AbortSignal;
}

type AgentSessionResponse = OpenAPIComponents['AgentSessionResponse'];

export class Connection {
  readonly baseUrl: string;
  readonly openapi: OpenAPIClient;
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
    this.openapi = new OpenAPIClient((id, requestOptions) => this.requestOpenAPI(id, requestOptions));
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
    return this.openapi.health();
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
    return this.openapi.ready();
  }

  async listNamespaces(): Promise<Namespace[]> {
    return this.openapi.listNamespaces();
  }

  async createNamespace(name: string): Promise<Namespace> {
    return this.openapi.createNamespace({ body: { name } });
  }

  async compact(options: { retentionSeconds?: number } = {}): Promise<CompactResponse> {
    // The connection must be configured with the service's global admin token;
    // tenant management tokens are intentionally rejected by this operation.
    return this.openapi.compact({ body: { retention_seconds: options.retentionSeconds ?? 0 } });
  }

  async enqueueTask<P = unknown, R = unknown>(request: EnqueueTaskRequest<P>): Promise<Task<P, R>> {
    if (runtimeOnlyTaskType(request.type) && !this.hasAgentRuntimeCredentials()) {
      throw new Error('postgrip-agent: workflow tasks can only be enqueued from a managed runtime; submit workflow.runtime to an agent pool');
    }
    return this.openapi.enqueueTask({ body: request }) as Promise<Task<P, R>>;
  }

  async listTasks<P = unknown, R = unknown>(options: {
    namespace?: string;
    queue?: string;
    type?: string;
    state?: TaskState;
    orderBy?: string;
    pageToken?: string;
    limit?: number;
    offset?: number;
  } = {}): Promise<Task<P, R>[]> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.queue) query.set('queue', options.queue);
    if (options.type) query.set('type', options.type);
    if (options.state) query.set('state', options.state);
    if (options.orderBy) query.set('order_by', options.orderBy);
    if (options.pageToken) query.set('page_token', options.pageToken);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    return this.openapi.listTasks({ query }) as Promise<Array<Task<P, R>>>;
  }

  async getTask<P = unknown, R = unknown>(taskId: string): Promise<Task<P, R>> {
    return this.openapi.getTask({ pathParameters: { taskId } }) as Promise<Task<P, R>>;
  }

  async getTaskEvents(taskId: string): Promise<TaskEvent[]> {
    return this.openapi.listTaskEvents({ pathParameters: { taskId } });
  }

  async createSchedule<Args extends unknown[] = unknown[]>(request: CreateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.openapi.createSchedule({ body: request }) as Promise<Schedule<Args>>;
  }

  async listSchedules<Args extends unknown[] = unknown[]>(options: {
    namespace?: string;
    state?: ScheduleState;
    pageToken?: string;
    limit?: number;
    offset?: number;
  } = {}): Promise<Array<Schedule<Args>>> {
    const query = new URLSearchParams();
    if (options.namespace) query.set('namespace', options.namespace);
    if (options.state) query.set('state', options.state);
    if (options.pageToken) query.set('page_token', options.pageToken);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    return this.openapi.listSchedules({ query }) as Promise<Array<Schedule<Args>>>;
  }

  async getSchedule<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.openapi.getSchedule({ pathParameters: { scheduleId } }) as Promise<Schedule<Args>>;
  }

  async updateSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: UpdateScheduleRequest<Args>): Promise<Schedule<Args>> {
    return this.openapi.updateSchedule({ pathParameters: { scheduleId }, body: request }) as Promise<Schedule<Args>>;
  }

  async deleteSchedule<Args extends unknown[] = unknown[]>(scheduleId: string): Promise<Schedule<Args>> {
    return this.openapi.deleteSchedule({ pathParameters: { scheduleId } }) as Promise<Schedule<Args>>;
  }

  async pauseSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: PauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.openapi.pauseSchedule({ pathParameters: { scheduleId }, body: request }) as Promise<Schedule<Args>>;
  }

  async unpauseSchedule<Args extends unknown[] = unknown[]>(scheduleId: string, request: UnpauseScheduleRequest = {}): Promise<Schedule<Args>> {
    return this.openapi.unpauseSchedule({ pathParameters: { scheduleId }, body: request }) as Promise<Schedule<Args>>;
  }

  async triggerSchedule<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: TriggerScheduleRequest = {},
  ): Promise<TriggerScheduleResponse<Args, R>> {
    return this.openapi.triggerSchedule({ pathParameters: { scheduleId }, body: request }) as Promise<TriggerScheduleResponse<Args, R>>;
  }

  async backfillSchedule<Args extends unknown[] = unknown[], R = unknown>(
    scheduleId: string,
    request: BackfillScheduleRequest,
  ): Promise<BackfillScheduleResponse<Args, R>> {
    return this.openapi.backfillSchedule({ pathParameters: { scheduleId }, body: request }) as Promise<BackfillScheduleResponse<Args, R>>;
  }

  async listWorkflows<R = unknown>(options: {
    namespace?: string;
    id?: string;
    runId?: string;
    type?: string;
    queue?: string;
    agentId?: string;
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
    if (options.agentId) query.set('agent_id', options.agentId);
    if (options.state) query.set('state', options.state);
    if (options.query) query.set('query', options.query);
    if (options.orderBy) query.set('order_by', options.orderBy);
    if (options.pageToken) query.set('page_token', options.pageToken);
    if (options.limit != null) query.set('limit', String(options.limit));
    if (options.offset != null) query.set('offset', String(options.offset));
    for (const [key, value] of Object.entries(options.searchAttributes ?? {})) {
      query.set(`search.${key}`, String(value));
    }
    return this.openapi.listWorkflows({ query }) as Promise<Array<WorkflowExecution<R>>>;
  }

  async countWorkflows(options: {
    namespace?: string;
    id?: string;
    runId?: string;
    type?: string;
    queue?: string;
    agentId?: string;
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
    if (options.agentId) query.set('agent_id', options.agentId);
    if (options.state) query.set('state', options.state);
    if (options.query) query.set('query', options.query);
    for (const [key, value] of Object.entries(options.searchAttributes ?? {})) {
      query.set(`search.${key}`, String(value));
    }
    return this.openapi.countWorkflows({ query });
  }

  async getWorkflow<R = unknown>(workflowId: string): Promise<WorkflowExecution<R>> {
    return this.openapi.getWorkflow({ pathParameters: { workflowId } }) as Promise<WorkflowExecution<R>>;
  }

  async getWorkflowHistory(workflowId: string): Promise<WorkflowHistoryEvent[]> {
    return this.openapi.listWorkflowHistory({ pathParameters: { workflowId } });
  }

  async signalWorkflow<Args extends unknown[] = unknown[]>(workflowId: string, request: SignalWorkflowRequest<Args>): Promise<WorkflowHistoryEvent> {
    return this.openapi.signalWorkflow({ pathParameters: { workflowId }, body: request });
  }

  async signalWithStartWorkflow<WorkflowArgs extends unknown[] = unknown[], SignalArgs extends unknown[] = unknown[], R = unknown>(
    workflowId: string,
    request: SignalWithStartWorkflowRequest<WorkflowArgs, SignalArgs>,
  ): Promise<SignalWithStartWorkflowResponse<WorkflowArgs, R>> {
    if (!this.hasAgentRuntimeCredentials()) {
      throw new Error('postgrip-agent: signal-with-start can only run from a managed runtime; submit workflow.runtime to an agent pool');
    }
    return this.openapi.signalWithStartWorkflow({ pathParameters: { workflowId }, body: request }) as Promise<SignalWithStartWorkflowResponse<WorkflowArgs, R>>;
  }

  async cancelWorkflow(workflowId: string, request: CancelWorkflowRequest = {}): Promise<WorkflowHistoryEvent> {
    return this.openapi.cancelWorkflow({ pathParameters: { workflowId }, body: request });
  }

  async terminateWorkflow(workflowId: string, request: TerminateWorkflowRequest = {}): Promise<WorkflowHistoryEvent> {
    return this.openapi.terminateWorkflow({ pathParameters: { workflowId }, body: request });
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
    const response = await this.openapi.pollAgentTask({
      query,
      signal: options.signal,
    }) as PollTaskResponse<P, R>;
    return response.task;
  }

  async heartbeatTask(taskId: string, agentId: string, event?: TaskEventInput): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.openapi.heartbeatAgentTask({
      pathParameters: { taskId },
      query: new URLSearchParams({ agent_id: agentId }),
      body: { event },
    });
  }

  async appendTaskEvent(taskId: string, agentId: string, event: TaskEventInput): Promise<TaskEvent> {
    await this.ensureAgentSession({ agentId });
    return this.openapi.appendAgentTaskEvent({
      pathParameters: { taskId },
      query: new URLSearchParams({ agent_id: agentId }),
      body: { event },
    });
  }

  async completeTask<R = unknown>(taskId: string, agentId: string, result: TaskResult<R>): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.openapi.completeAgentTask({
      pathParameters: { taskId },
      query: new URLSearchParams({ agent_id: agentId }),
      body: { result },
    });
  }

  async blockTask(taskId: string, agentId: string, reason?: string): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.openapi.blockAgentTask({
      pathParameters: { taskId },
      query: new URLSearchParams({ agent_id: agentId }),
      body: { reason },
    });
  }

  async failTask<R = unknown>(taskId: string, agentId: string, error: string, result?: TaskResult<R>): Promise<Task> {
    await this.ensureAgentSession({ agentId });
    return this.openapi.failAgentTask({
      pathParameters: { taskId },
      query: new URLSearchParams({ agent_id: agentId }),
      body: { error, result },
    });
  }

  private async refreshAgentSession(): Promise<void> {
    if (this.agentAuth.refreshToken) {
      const session = await this.openapi.refreshAgentSession({
        body: { refreshToken: this.agentAuth.refreshToken },
      }) as AgentSessionResponse;
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
    return this.openapi.createSandbox({ body: request });
  }

  async listSandboxes(): Promise<Sandbox[]> {
    // The endpoint returns an envelope, not a bare array.
    const response = await this.openapi.listSandboxes() as SandboxListResponse;
    return response?.sandboxes ?? [];
  }

  async getSandbox(sandboxId: string, signal?: AbortSignal): Promise<Sandbox> {
    return this.openapi.getSandbox({ pathParameters: { sandboxId }, signal });
  }

  async startSandbox(sandboxId: string): Promise<Sandbox> {
    return this.openapi.startSandbox({ pathParameters: { sandboxId } });
  }

  async stopSandbox(sandboxId: string): Promise<Sandbox> {
    return this.openapi.stopSandbox({ pathParameters: { sandboxId } });
  }

  async deleteSandbox(sandboxId: string): Promise<Sandbox> {
    return this.openapi.deleteSandbox({ pathParameters: { sandboxId } });
  }

  async createSandboxSession(
    sandboxId: string,
    request: CreateSandboxSessionRequest = {},
  ): Promise<CreateSandboxSessionResponse> {
    return this.openapi.createSandboxSession({ pathParameters: { sandboxId }, body: request });
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
    if (metadata.repositoryName) headers.set(UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REPOSITORY, metadata.repositoryName);
    if (metadata.revision) headers.set(UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REVISION, metadata.revision);

    const operation = resolveOpenAPIOperation('uploadWorkspace');
    const response = await this.fetchImpl(`${this.baseUrl}${operation.path}`, {
      method: operation.method,
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

  /** Headers for a management-authenticated custom transport such as the sandbox relay. */
  managementHeaders(): Headers {
    const headers = new Headers(this.headers);
    if (this.authToken && !headers.has('Authorization')) {
      headers.set('Authorization', `Bearer ${this.authToken}`);
    }
    return headers;
  }

  /** Management Authorization header value, for compatibility with earlier relay adapters. */
  authorizationHeader(): string | undefined {
    return this.managementHeaders().get('Authorization') ?? undefined;
  }

  private async requestOpenAPI<
    I extends OpenAPIOperationId,
    T = OpenAPIResponseBody<I>,
  >(
    id: I,
    options: OpenAPIRequestOptions<I> = {} as OpenAPIRequestOptions<I>,
  ): Promise<T> {
    const operation = resolveOpenAPIOperation(
      id,
      options.pathParameters as Readonly<Record<string, string>> | undefined,
      encodeOpenAPIQuery(options.query),
    );
    const agentAuth = operation.authLane === 'agent'
      || (operation.authLane === 'either' && this.hasAgentRuntimeCredentials());
    return this.request(operation.method, operation.path, options.body, options.signal, {
      agentAuth,
      signing: operation.signing,
    });
  }

  private async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal, options: { agentAuth?: boolean; signing?: string } = {}): Promise<T> {
    const useAgentAuth = options.agentAuth === true;
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
    if (useAgentAuth && options.signing === 'agent-task-v1' && this.agentSigningKey) {
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
