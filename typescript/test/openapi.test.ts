import { describe, expect, expectTypeOf, it } from 'vitest';

import {
  OPENAPI_CLIENT_OPERATION_COUNT,
  OPENAPI_OPERATION_COUNT,
  resolveOpenAPIOperation,
  OpenAPIClient,
  type OpenAPIPollTaskResponse,
  type OpenAPIRequestBody,
  type OpenAPIQueryParameters,
  type OpenAPIResponseBody,
  type OpenAPISandbox,
  type OpenAPITransport,
} from '../src/index.js';

describe('generated OpenAPI operations', () => {
  it('resolves path parameters, queries, and security metadata', () => {
    const operation = resolveOpenAPIOperation(
      'completeAgentTask',
      { taskId: 'task/one?' },
      new URLSearchParams({ agent_id: 'agent one' }),
    );
    expect(operation).toMatchObject({
      method: 'POST',
      path: '/api/v1/agent/tasks/task%2Fone%3F/complete?agent_id=agent+one',
      authLane: 'agent',
      signing: 'agent-task-v1',
      requestSchema: 'CompleteTaskRequest',
      responseSchema: 'Task',
    });
    const pollOperation = resolveOpenAPIOperation('pollAgentTask');
    expect(pollOperation.authLane).toBe('agent');
    expect(pollOperation.signing).toBeUndefined();
    expect(resolveOpenAPIOperation('compact').authLane).toBe('global-admin');
  });

  it('rejects missing path parameters', () => {
    expect(() => resolveOpenAPIOperation('getTask')).toThrow(/taskId/);
  });

  it('generates request and response payload types', () => {
    expect(OPENAPI_OPERATION_COUNT).toBe(45);
    expect(OPENAPI_CLIENT_OPERATION_COUNT).toBe(43);
    expectTypeOf<OpenAPIRequestBody<'enqueueTask'>>().toMatchTypeOf<{ type: string }>();
    expectTypeOf<OpenAPIResponseBody<'enqueueTask'>>().toHaveProperty('lease_timeout_seconds');
    expectTypeOf<Parameters<OpenAPIClient['pollAgentTask']>[0]>().toMatchTypeOf<{
      readonly query: URLSearchParams | { readonly queue: string };
    }>();
    expectTypeOf<OpenAPIQueryParameters<'listTasks'>>().toHaveProperty('order_by');
    expectTypeOf<OpenAPIQueryParameters<'listTasks'>>().toHaveProperty('page_token');
    expectTypeOf<OpenAPIQueryParameters<'listSchedules'>>().toHaveProperty('page_token');
    expectTypeOf<OpenAPIQueryParameters<'listWorkflows'>>().toHaveProperty('agent_id');
    expectTypeOf<OpenAPIQueryParameters<'countWorkflows'>>().toHaveProperty('agent_id');
    expectTypeOf<OpenAPIQueryParameters<'pollAgentTask'>>().toHaveProperty('version');
    expectTypeOf<OpenAPIQueryParameters<'pollAgentTask'>>().toHaveProperty('log_level');
    expectTypeOf<OpenAPIQueryParameters<'pollAgentTask'>['capabilities']>()
      .toEqualTypeOf<Array<string> | undefined>();
    expectTypeOf<Parameters<OpenAPIClient['pauseSchedule']>[0]>().toMatchTypeOf<{
      readonly body: OpenAPIRequestBody<'pauseSchedule'>;
    }>();
    expectTypeOf<OpenAPIPollTaskResponse['task']>().toEqualTypeOf<
      OpenAPIResponseBody<'getTask'> | null | undefined
    >();
    expectTypeOf<OpenAPISandbox['expiresAt']>().toEqualTypeOf<string | null | undefined>();
    expectTypeOf<OpenAPISandbox['lastActivityAt']>().toEqualTypeOf<string | null | undefined>();
    expectTypeOf<OpenAPISandbox['stoppedAt']>().toEqualTypeOf<string | null | undefined>();
    expectTypeOf<OpenAPISandbox['deletedAt']>().toEqualTypeOf<string | null | undefined>();
  });

  it('exposes a generated typed client facade', async () => {
    const calls: Array<{ id: string; body: unknown }> = [];
    const transport = (async (id: string, options: { body?: unknown }) => {
      calls.push({ id, body: options.body });
      return { name: 'generated', created_at: 'now', updated_at: 'now' };
    }) as OpenAPITransport;
    const response = await new OpenAPIClient(transport).createNamespace({ body: { name: 'generated' } });
    expect(response.name).toBe('generated');
    expect(calls).toEqual([{ id: 'createNamespace', body: { name: 'generated' } }]);
  });
});
