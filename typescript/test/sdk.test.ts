import { describe, expect, it, vi } from 'vitest';
import { Client } from '../src/client';
import { Connection } from '../src/connection';
import { Agent } from '../src/agent';
import { activityStderr, activityStdout, Worker as IndexWorker } from '../src/index';
import { Worker as ModuleWorker } from '../src/worker';
import { defineQuery } from '../src/workflow';
import type { EnqueueTaskRequest, Task, WorkflowExecution, WorkflowRuntimePayload } from '../src/types';

function workflowTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    namespace: 'default',
    queue: 'default',
    type: 'workflow:ExampleWorkflow',
    payload: {
      workflowType: 'ExampleWorkflow',
      workflowId: 'workflow-1',
      runId: 'run-1',
      args: [],
    },
    state: 'queued',
    attempt: 1,
    lease_timeout_seconds: 30,
    created_at: '2026-04-22T00:00:00Z',
    updated_at: '2026-04-22T00:00:00Z',
    ...overrides,
  } as Task;
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void;
  const promise = new Promise<void>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

describe('PostGrip Agent TypeScript Connection', () => {
  it('keeps deprecated Worker imports as Agent aliases', () => {
    expect(IndexWorker).toBe(Agent);
    expect(ModuleWorker).toBe(Agent);
  });

  it('preserves Headers instances and adds JSON content type for request bodies', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => (
      new Response(JSON.stringify({ id: 'task-1' }), {
        status: 202,
        headers: { 'Content-Type': 'application/json' },
      })
    ));
    const headers = new Headers([['authorization', 'Bearer token']]);
    const connection = await Connection.connect({
      baseUrl: 'http://agent.test/',
      headers,
      fetch: vi.fn<typeof fetch>(async (input, init) => {
        if (String(input).endsWith('/healthz')) {
          return new Response(JSON.stringify({ status: 'ok' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return fetchMock(input, init);
      }),
    });

    await connection.enqueueTask({ type: 'noop', payload: { ok: true } });

    const requestHeaders = fetchMock.mock.calls[0][1]?.headers as Headers;
    expect(requestHeaders.get('authorization')).toBe('Bearer token');
    expect(requestHeaders.get('content-type')).toBe('application/json');
  });

  it('preserves tuple-array headers', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      if (String(input).endsWith('/healthz')) {
        return new Response(JSON.stringify({ status: 'ok' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      const headers = init?.headers as Headers;
      return new Response(JSON.stringify({ trace: headers.get('x-trace-id') }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    });

    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      headers: [['x-trace-id', 'trace-1']],
      fetch: fetchMock,
    });

    const response = await connection.createNamespace('tenant-a');

    expect(response).toEqual({ trace: 'trace-1' });
  });

  it('uses managed agent tokens only for agent task routes', async () => {
    const seenAuth: Record<string, string | null> = {};
    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      headers: { Authorization: 'Bearer management-token' },
      agentAuth: {
        accessToken: 'agent-access-token',
        refreshToken: 'agent-refresh-token',
        accessExpiresAt: '2999-01-01T00:00:00Z',
      },
      fetch: vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input));
        const headers = init?.headers as Headers;
        seenAuth[url.pathname] = headers.get('authorization');
        if (url.pathname === '/healthz') {
          return new Response(JSON.stringify({ status: 'ok' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.pathname === '/api/v1/agent/poll') {
          return new Response(JSON.stringify({}), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return new Response(JSON.stringify({ id: 'task-1' }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    });

    await connection.enqueueTask({ type: 'noop' });
    await connection.pollTask({ queue: 'default', agentId: 'agent-1', waitSeconds: 0 });

    expect(seenAuth['/api/v1/tasks']).toBe('Bearer agent-access-token');
    expect(seenAuth['/api/v1/agent/poll']).toBe('Bearer agent-access-token');
  });

  it('uses canonical agentId auth and poll options', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input));
      if (url.pathname === '/healthz') {
        return new Response(JSON.stringify({ status: 'ok' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.pathname === '/api/v1/agent/poll') {
        expect(url.searchParams.get('agent_id')).toBe('agent-1');
        return new Response(JSON.stringify({ task: null }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      throw new Error(`unexpected request ${url.pathname}`);
    });

    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      agentAuth: {
        agentId: 'agent-1',
        accessToken: 'agent-access-token',
        refreshToken: 'agent-refresh-token',
        accessExpiresAt: '2999-01-01T00:00:00Z',
      },
      fetch: fetchMock,
    });

    await expect(connection.pollTask({ queue: 'default', agentId: 'agent-1', waitSeconds: 0 })).resolves.toBeNull();
  });

  it('maps deprecated workerId options to canonical agent poll options', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input));
      if (url.pathname === '/healthz') {
        return new Response(JSON.stringify({ status: 'ok' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (url.pathname === '/api/v1/agent/poll') {
        expect(url.searchParams.get('agent_id')).toBe('agent-compat');
        return new Response(JSON.stringify({ task: null }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      throw new Error(`unexpected request ${url.pathname}`);
    });

    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      agentAuth: {
        workerId: 'agent-compat',
        accessToken: 'agent-access-token',
        refreshToken: 'agent-refresh-token',
        accessExpiresAt: '2999-01-01T00:00:00Z',
      },
      fetch: fetchMock,
    });

    await expect(connection.pollTask({ queue: 'default', workerId: 'agent-compat', waitSeconds: 0 })).resolves.toBeNull();
  });

  it('blocks workflow-family task submission outside managed runtimes', async () => {
    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      fetch: vi.fn<typeof fetch>(async (input) => {
        const url = new URL(String(input));
        if (url.pathname === '/healthz') {
          return new Response(JSON.stringify({ status: 'ok' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        throw new Error(`unexpected request ${url.pathname}`);
      }),
    });

    await expect(connection.enqueueTask({ type: 'workflow:ExampleWorkflow' }))
      .rejects.toThrow('workflow.runtime');
  });

  it('keeps workflow.runtime as the external workflow submission path', async () => {
    let seenBody: EnqueueTaskRequest | undefined;
    const connection = await Connection.connect({
      baseUrl: 'http://agent.test',
      headers: { Authorization: 'Bearer management-token' },
      fetch: vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input));
        if (url.pathname === '/healthz') {
          return new Response(JSON.stringify({ status: 'ok' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.pathname === '/api/v1/tasks') {
          seenBody = JSON.parse(String(init?.body ?? '{}')) as EnqueueTaskRequest;
          return new Response(JSON.stringify(workflowTask({
            id: 'runtime-task',
            type: 'workflow.runtime',
            payload: seenBody.payload,
          })), {
            status: 202,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        throw new Error(`unexpected request ${url.pathname}`);
      }),
    });
    const client = new Client({ connection });

    const task = await client.task.workflowRuntime({
      queue: 'default',
      command: 'sh',
      args: ['-lc', 'echo runtime'],
    });

    expect(task.id).toBe('runtime-task');
    expect(seenBody?.type).toBe('workflow.runtime');
    expect((seenBody?.payload as WorkflowRuntimePayload | undefined)?.queue).toMatch(/^postgrip-runtime-/);
    expect((seenBody?.payload as WorkflowRuntimePayload | undefined)?.queue).not.toBe('default');
  });
});

describe('PostGrip Agent TypeScript Client', () => {
  it('starts workflows with Temporal-style metadata and retry options', async () => {
    async function ExampleWorkflow(name: string): Promise<string> {
      return `hello ${name}`;
    }
    const enqueued: EnqueueTaskRequest[] = [];
    const connection = {
      enqueueTask: vi.fn(async (request: EnqueueTaskRequest) => {
        enqueued.push(request);
        return workflowTask({
          id: 'workflow-task-1',
          type: request.type,
          namespace: request.namespace,
          queue: request.queue,
          payload: { ...(request.payload as object), runId: 'run-1' },
          lease_timeout_seconds: request.lease_timeout_seconds ?? 30,
        });
      }),
    };
    const client = new Client({ connection: connection as unknown as Connection });

    const handle = await client.workflow.start(ExampleWorkflow, {
      workflowId: 'workflow-1',
      taskQueue: 'workflow-queue',
      namespace: 'tenant-a',
      args: ['PostGrip'],
      memo: { owner: 'docs' },
      ui: {
        displayName: 'Example greeting',
        description: 'Started from the SDK test',
        details: { customer: 'acme', attempt: 1 },
        tags: ['sdk', 'demo'],
      },
      searchAttributes: { customer: 'acme' },
      retry: { maximumAttempts: 3, nonRetryableErrorTypes: ['BadInput'] },
      leaseTimeoutSeconds: 12,
    });

    expect(handle.workflowId).toBe('workflow-1');
    expect(handle.runId).toBe('run-1');
    expect(enqueued[0]).toMatchObject({
      namespace: 'tenant-a',
      queue: 'workflow-queue',
      type: 'workflow:ExampleWorkflow',
      lease_timeout_seconds: 12,
    });
    expect(enqueued[0].payload).toMatchObject({
      workflowId: 'workflow-1',
      workflowType: 'ExampleWorkflow',
      args: ['PostGrip'],
      memo: {
        owner: 'docs',
        'postgrip.ui': {
          displayName: 'Example greeting',
          description: 'Started from the SDK test',
          details: { customer: 'acme', attempt: 1 },
          tags: ['sdk', 'demo'],
        },
      },
      searchAttributes: { customer: 'acme' },
      retry: { maximumAttempts: 3, nonRetryableErrorTypes: ['BadInput'] },
    });
  });

  it('targets workflow queries by run id when a handle has one', async () => {
    const answer = defineQuery<string>('answer');
    const queryTask = workflowTask({
      id: 'query-task-1',
      type: 'query:ExampleWorkflow',
      state: 'succeeded',
      result: { value: 'ready' },
    });
    const enqueued: EnqueueTaskRequest[] = [];
    const workflow: WorkflowExecution = {
      id: 'workflow-1',
      run_id: 'run-77',
      task_id: 'workflow-task-1',
      namespace: 'tenant-a',
      queue: 'workflow-queue',
      type: 'ExampleWorkflow',
      state: 'running',
      created_at: '2026-04-22T00:00:00Z',
      updated_at: '2026-04-22T00:00:00Z',
    } as WorkflowExecution;
    const connection = {
      getWorkflow: vi.fn(async (id: string) => {
        expect(id).toBe('run-77');
        return workflow;
      }),
      enqueueTask: vi.fn(async (request: EnqueueTaskRequest) => {
        enqueued.push(request);
        return { ...queryTask, payload: request.payload };
      }),
      getTask: vi.fn(async () => queryTask),
    };
    const client = new Client({ connection: connection as unknown as Connection });
    const handle = client.workflow.getHandle<string>('workflow-1', {
      runId: 'run-77',
      workflowType: 'ExampleWorkflow',
    });

    await expect(handle.query(answer)).resolves.toBe('ready');

    expect(enqueued[0]).toMatchObject({
      namespace: 'tenant-a',
      queue: 'workflow-queue',
      type: 'query:ExampleWorkflow',
    });
    expect(enqueued[0].payload).toMatchObject({
      workflowId: 'workflow-1',
      workflowRunId: 'run-77',
      queryName: 'answer',
    });
  });

  it('maps createWorkflowSchedule convenience options to server schedule requests', async () => {
    async function ScheduledWorkflow(): Promise<void> {}
    const createSchedule = vi.fn(async (request) => ({
      id: request.id ?? 'schedule-1',
      namespace: request.namespace ?? 'default',
      state: 'active',
      spec: request.spec,
      action: request.action,
      created_at: '2026-04-22T00:00:00Z',
      updated_at: '2026-04-22T00:00:00Z',
    }));
    const client = new Client({ connection: { createSchedule } as unknown as Connection });

    await client.schedule.createWorkflowSchedule({
      scheduleId: 'schedule-1',
      namespace: 'tenant-a',
      workflow: ScheduledWorkflow,
      taskQueue: 'scheduled',
      args: [],
      intervalSeconds: 60,
      timezone: 'America/Los_Angeles',
      jitterSeconds: 5,
      catchUpWindowSeconds: 300,
      missedRunPolicy: 'skip',
      workflowId: 'scheduled-workflow',
      overlapPolicy: 'skip',
      ui: {
        displayName: 'Nightly scheduled workflow',
        details: { owner: 'ops' },
      },
    });

    expect(createSchedule).toHaveBeenCalledWith(expect.objectContaining({
      id: 'schedule-1',
      namespace: 'tenant-a',
      overlap_policy: 'skip',
      spec: expect.objectContaining({
        interval_seconds: 60,
        timezone: 'America/Los_Angeles',
        jitter_seconds: 5,
        catch_up_window_seconds: 300,
        missed_run_policy: 'skip',
      }),
      action: expect.objectContaining({
        namespace: 'tenant-a',
        queue: 'scheduled',
        workflowType: 'ScheduledWorkflow',
        workflowId: 'scheduled-workflow',
        memo: {
          'postgrip.ui': {
            displayName: 'Nightly scheduled workflow',
            details: { owner: 'ops' },
          },
        },
      }),
    }));
  });
});

describe('PostGrip Agent TypeScript Agent', () => {
  it('emits activity stdout and stderr events with task context', async () => {
    const previousManagedRuntime = process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
    process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = 'true';
    const connection = {
      health: vi.fn(async () => ({ status: 'ok' })),
      heartbeatTask: vi.fn(async () => workflowTask({ state: 'leased' })),
      appendTaskEvent: vi.fn(async () => ({ id: 'event-1' })),
      completeTask: vi.fn(async () => workflowTask({ state: 'succeeded' })),
      failTask: vi.fn(async () => workflowTask({ state: 'failed' })),
    };
    let agent: Agent;
    try {
      agent = await Agent.create({
        connection: connection as unknown as Connection,
        taskQueue: 'default',
        workflows: {},
        activities: {
          async processStep(name: string): Promise<string> {
            await activityStdout(`processed ${name}\n`, { stage: 'processStep', details: { name } });
            await activityStderr('diagnostic line\n');
            return 'done';
          },
        },
      });
    } finally {
      if (previousManagedRuntime == null) {
        delete process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
      } else {
        process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = previousManagedRuntime;
      }
    }

    await (agent as unknown as { executeTask(task: Task): Promise<void> }).executeTask(workflowTask({
      id: 'activity-task-1',
      type: 'activity:processStep',
      payload: { activityType: 'processStep', args: ['customers'] },
    }));

    expect(connection.appendTaskEvent).toHaveBeenCalledWith(
      'activity-task-1',
      expect.stringMatching(/^ts-agent-/),
      expect.objectContaining({
        kind: 'stdout',
        stream: 'stdout',
        data: 'processed customers\n',
        stage: 'processStep',
        details: { name: 'customers' },
      }),
    );
    expect(connection.appendTaskEvent).toHaveBeenCalledWith(
      'activity-task-1',
      expect.stringMatching(/^ts-agent-/),
      expect.objectContaining({
        kind: 'stderr',
        stream: 'stderr',
        data: 'diagnostic line\n',
        stage: 'activity',
      }),
    );
  });

  it('keeps activity output scoped across concurrent async activity tasks', async () => {
    const previousManagedRuntime = process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
    process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = 'true';
    const first = deferred();
    const second = deferred();
    const connection = {
      health: vi.fn(async () => ({ status: 'ok' })),
      heartbeatTask: vi.fn(async () => workflowTask({ state: 'leased' })),
      appendTaskEvent: vi.fn(async (taskId: string, _agentId: string, event: Record<string, unknown>) => ({
        id: 'event-1',
        task_id: taskId,
        ...event,
      })),
      completeTask: vi.fn(async () => workflowTask({ state: 'succeeded' })),
      failTask: vi.fn(async () => workflowTask({ state: 'failed' })),
    };
    let agent: Agent;
    try {
      agent = await Agent.create({
        connection: connection as unknown as Connection,
        taskQueue: 'default',
        workflows: {},
        activities: {
          async processStep(name: string): Promise<string> {
            await (name === 'first' ? first.promise : second.promise);
            await activityStdout(`${name}\n`);
            return name;
          },
        },
      });
    } finally {
      if (previousManagedRuntime == null) {
        delete process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
      } else {
        process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = previousManagedRuntime;
      }
    }

    const execute = (agent as unknown as { executeTask(task: Task): Promise<void> }).executeTask.bind(agent);
    const firstExecution = execute(workflowTask({
      id: 'activity-task-1',
      type: 'activity:processStep',
      payload: { activityType: 'processStep', args: ['first'] },
    }));
    const secondExecution = execute(workflowTask({
      id: 'activity-task-2',
      type: 'activity:processStep',
      payload: { activityType: 'processStep', args: ['second'] },
    }));

    await Promise.resolve();
    await Promise.resolve();
    first.resolve();
    await Promise.resolve();
    second.resolve();
    await Promise.all([firstExecution, secondExecution]);

    const stdoutCalls = connection.appendTaskEvent.mock.calls.filter((call) => call[2]?.kind === 'stdout');
    expect(stdoutCalls).toHaveLength(2);
    expect(stdoutCalls).toEqual(expect.arrayContaining([
      ['activity-task-1', expect.stringMatching(/^ts-agent-/), expect.objectContaining({ data: 'first\n' })],
      ['activity-task-2', expect.stringMatching(/^ts-agent-/), expect.objectContaining({ data: 'second\n' })],
    ]));
  });

  it('fails unsupported task types instead of completing them', async () => {
    const previousManagedRuntime = process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
    process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = 'true';
    const connection = {
      health: vi.fn(async () => ({ status: 'ok' })),
      heartbeatTask: vi.fn(async () => workflowTask({ state: 'leased' })),
      appendTaskEvent: vi.fn(async () => ({ id: 'event-1' })),
      failTask: vi.fn(async () => workflowTask({ state: 'failed' })),
    };
    let agent: Agent;
    try {
      agent = await Agent.create({
        connection: connection as unknown as Connection,
        taskQueue: 'default',
        workflows: {},
      });
    } finally {
      if (previousManagedRuntime == null) {
        delete process.env.POSTGRIP_AGENT_MANAGED_RUNTIME;
      } else {
        process.env.POSTGRIP_AGENT_MANAGED_RUNTIME = previousManagedRuntime;
      }
    }

    await (agent as unknown as { executeTask(task: Task): Promise<void> }).executeTask(workflowTask({
      id: 'unknown-task-1',
      type: 'noop',
      payload: {},
    }));

    expect(connection.failTask).toHaveBeenCalledWith(
      'unknown-task-1',
      expect.stringMatching(/^ts-agent-/),
      expect.stringContaining('unsupported TypeScript task type noop'),
      expect.objectContaining({
        failure: expect.objectContaining({
          type: 'UnsupportedTaskType',
          non_retryable: true,
        }),
      }),
    );
  });
});
