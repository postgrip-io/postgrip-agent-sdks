import { describe, expect, it } from 'vitest';

import { resolveOpenAPIOperation } from '../src/generated/openapi.js';

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
    });
    const pollOperation = resolveOpenAPIOperation('pollAgentTask');
    expect(pollOperation.authLane).toBe('agent');
    expect(pollOperation.signing).toBeUndefined();
  });

  it('rejects missing path parameters', () => {
    expect(() => resolveOpenAPIOperation('getTask')).toThrow(/taskId/);
  });
});
