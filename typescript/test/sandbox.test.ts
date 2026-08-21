import { describe, expect, it, vi } from 'vitest';
import type { AddressInfo } from 'node:net';
import { WebSocketServer } from 'ws';
import { Client } from '../src/client';
import { Connection } from '../src/connection';
import {
  SANDBOX_EXEC_CLOSE_STATUS_BASE,
  SANDBOX_RELAY_MAX_FRAME_BYTES,
  SandboxSession,
  sandboxExecExitCode,
  sandboxRelayUrl,
} from '../src/sandbox';
import type { Sandbox } from '../src/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function sandboxClient(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const fetchImpl = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) =>
    handler(String(input), init),
  );
  const connection = await Connection.connect({
    baseUrl: 'https://agents.example.com',
    authToken: 'mgmt-token',
    fetch: fetchImpl as unknown as typeof fetch,
  });
  // Connection.connect() performs a /healthz handshake; drop it so tests can
  // assert on the sandbox requests alone.
  fetchImpl.mockClear();
  return { client: new Client({ connection }), fetchImpl };
}

describe('sandbox client', () => {
  // The endpoint returns {"sandboxes":[...]}, not a bare array. Decoding it as
  // an array yields an empty list with no error, which reads as "none".
  it('unwraps the list envelope', async () => {
    const { client } = await sandboxClient(() =>
      jsonResponse({ sandboxes: [{ id: 'sbx_1' }, { id: 'sbx_2' }] }),
    );
    const sandboxes = await client.sandbox.list();
    expect(sandboxes.map((s) => s.id)).toEqual(['sbx_1', 'sbx_2']);
  });

  // Sandbox endpoints are management-lane; an agent token is rejected.
  it('sends the management token', async () => {
    let auth: string | null = null;
    const { client } = await sandboxClient((_url, init) => {
      auth = new Headers(init.headers).get('Authorization');
      return jsonResponse({ id: 'sbx_1' });
    });
    await client.sandbox.get('sbx_1');
    expect(auth).toBe('Bearer mgmt-token');
  });

  it('uses the right method and path per lifecycle action', async () => {
    const seen: string[] = [];
    const { client } = await sandboxClient((url, init) => {
      const pathname = new URL(url).pathname;
      // mockClear() resets the spy, not this closure's own log.
      if (pathname !== '/healthz') seen.push(`${init.method} ${pathname}`);
      return jsonResponse({ id: 'sbx_1' });
    });
    await client.sandbox.start('sbx_1');
    await client.sandbox.stop('sbx_1');
    await client.sandbox.delete('sbx_1');
    expect(seen).toEqual([
      'POST /api/v1/sandboxes/sbx_1/start',
      'POST /api/v1/sandboxes/sbx_1/stop',
      'DELETE /api/v1/sandboxes/sbx_1',
    ]);
  });

  it('rejects a blank sandbox id before issuing a request', async () => {
    const { client, fetchImpl } = await sandboxClient(() => jsonResponse({}));
    await expect(client.sandbox.get('')).rejects.toThrow(/sandbox id is required/);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  // Readiness is state AND generation: a "running" reading at a generation the
  // agent hasn't observed describes a sandbox that is about to change again.
  it('waits for the observed generation to catch up', async () => {
    let calls = 0;
    const { client } = await sandboxClient(() => {
      calls += 1;
      const record: Partial<Sandbox> =
        calls === 1
          ? { id: 'sbx_1', observedState: 'running', generation: 2, observedGeneration: 1 }
          : { id: 'sbx_1', observedState: 'running', generation: 2, observedGeneration: 2 };
      return jsonResponse(record);
    });
    const record = await client.sandbox.waitUntilRunning('sbx_1', { pollIntervalMs: 1 });
    expect(calls).toBeGreaterThan(1);
    expect(record.observedGeneration).toBe(2);
  });

  it('fails fast with the sandbox failure message', async () => {
    const { client } = await sandboxClient(() =>
      jsonResponse({
        id: 'sbx_1',
        observedState: 'failed',
        failureCode: 'setup_failed',
        failureMessage: 'setup.sh exited 1',
      }),
    );
    await expect(
      client.sandbox.waitUntilRunning('sbx_1', { timeoutMs: 30_000, pollIntervalMs: 1 }),
    ).rejects.toThrow(/setup\.sh exited 1/);
  });

  it('names the last observed state when the wait expires', async () => {
    const { client } = await sandboxClient(() =>
      jsonResponse({ id: 'sbx_1', observedState: 'provisioning', generation: 1, observedGeneration: 1 }),
    );
    await expect(
      client.sandbox.waitUntilRunning('sbx_1', { timeoutMs: 20, pollIntervalMs: 1 }),
    ).rejects.toThrow(/provisioning/);
  });

  // The upload is a raw body with metadata in headers — not multipart.
  it('uploads a workspace as a raw body with metadata headers', async () => {
    let init: RequestInit | undefined;
    let path = '';
    const { client } = await sandboxClient((url, requestInit) => {
      init = requestInit;
      path = new URL(url).pathname;
      return jsonResponse({ id: 'wsp_1', sha256: 'abc' }, 201);
    });
    const workspace = await client.sandbox.uploadWorkspace('GZIPPED', {
      repositoryName: 'my-repo',
      revision: 'deadbeef',
    });
    expect(path).toBe('/api/v1/workspaces');
    expect(init?.body).toBe('GZIPPED');
    const headers = new Headers(init?.headers);
    expect(headers.get('Content-Type')).toBe('application/gzip');
    expect(headers.get('X-PostGrip-Repository')).toBe('my-repo');
    expect(headers.get('X-PostGrip-Revision')).toBe('deadbeef');
    expect(workspace.id).toBe('wsp_1');
  });

  it('omits blank workspace metadata rather than sending empty headers', async () => {
    let init: RequestInit | undefined;
    const { client } = await sandboxClient((_url, requestInit) => {
      init = requestInit;
      return jsonResponse({ id: 'wsp_1' }, 201);
    });
    await client.sandbox.uploadWorkspace('x');
    const headers = new Headers(init?.headers);
    expect(headers.has('X-PostGrip-Repository')).toBe(false);
    expect(headers.has('X-PostGrip-Revision')).toBe(false);
  });

  it('refuses an exec session with no command before calling the server', async () => {
    const { client, fetchImpl } = await sandboxClient(() => jsonResponse({}));
    await expect(client.sandbox.openSession('sbx_1', 'exec', {})).rejects.toThrow(/requires a command/);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('rejects oversized exec stdin before creating a remote session', async () => {
    const { client, fetchImpl } = await sandboxClient(() =>
      jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
    );
    await expect(
      client.sandbox.exec('sbx_1', ['cat'], {
        stdin: new Uint8Array(SANDBOX_RELAY_MAX_FRAME_BYTES + 1),
      }),
    ).rejects.toThrow(/exceeds the relay frame limit/);
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('closes a remote exec when sending stdin or EOF throws', async () => {
    for (const failingSend of [1, 2]) {
      const { client } = await sandboxClient(() =>
        jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
      );
      let sends = 0;
      const close = vi.fn();
      class FailingWebSocket extends EventTarget {
        binaryType: BinaryType = 'blob';
        send(): void {
          sends += 1;
          if (sends === failingSend) throw new Error(`send ${failingSend} failed`);
        }
        close(): void {
          close();
        }
      }
      const socket = new FailingWebSocket();
      const run = client.sandbox.exec('sbx_1', ['cat'], {
        stdin: 'hello',
        webSocketFactory: () => {
          setTimeout(() => socket.dispatchEvent(new Event('open')), 0);
          return socket as unknown as WebSocket;
        },
      });
      await expect(run).rejects.toThrow(`send ${failingSend} failed`);
      expect(close).toHaveBeenCalledOnce();
    }
  });

  it('authenticates the default Node WebSocket relay dial', async () => {
    const relay = new WebSocketServer({ host: '127.0.0.1', port: 0 });
    await new Promise<void>((resolve) => relay.once('listening', resolve));
    const port = (relay.address() as AddressInfo).port;
    let authorization: string | undefined;
    relay.once('connection', (socket, request) => {
      authorization = request.headers.authorization;
      socket.close(SANDBOX_EXEC_CLOSE_STATUS_BASE);
    });

    try {
      const { client } = await sandboxClient(() =>
        jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
      );
      const session = await client.sandbox.openSession('sbx_1', 'exec', {
        command: ['true'],
        relayBaseUrl: `http://127.0.0.1:${port}`,
      });
      expect(authorization).toBe('Bearer mgmt-token');
      await session.exitCode();
    } finally {
      await new Promise<void>((resolve, reject) =>
        relay.close((error) => (error ? reject(error) : resolve())),
      );
    }
  });

  it('sends stdin EOF and keeps reading until the exec exit status', async () => {
    const relay = new WebSocketServer({ host: '127.0.0.1', port: 0 });
    await new Promise<void>((resolve) => relay.once('listening', resolve));
    const port = (relay.address() as AddressInfo).port;
    let stdin = Buffer.alloc(0);
    let eofFrames = 0;
    relay.once('connection', (socket) => {
      socket.on('message', (data, isBinary) => {
        if (!isBinary) return;
        const chunk = Buffer.from(data as ArrayBuffer);
        if (chunk.byteLength === 0) {
          eofFrames += 1;
          socket.send(Buffer.from('cat output'));
          socket.close(SANDBOX_EXEC_CLOSE_STATUS_BASE);
          return;
        }
        stdin = Buffer.concat([stdin, chunk]);
      });
    });

    try {
      const { client } = await sandboxClient(() =>
        jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
      );
      const result = await client.sandbox.exec('sbx_1', ['cat'], {
        relayBaseUrl: `http://127.0.0.1:${port}`,
        stdin: 'hello',
      });
      expect(stdin.toString()).toBe('hello');
      expect(eofFrames).toBe(1);
      expect(result.exitCode).toBe(0);
      expect(new TextDecoder().decode(result.output)).toBe('cat output');
    } finally {
      await new Promise<void>((resolve, reject) =>
        relay.close((error) => (error ? reject(error) : resolve())),
      );
    }
  });
});

describe('sandbox relay contract', () => {
  it('derives the ws(s) relay URL and escapes both parts', () => {
    expect(sandboxRelayUrl('https://agents.example.com', 'ses_1', 'pgss_t')).toBe(
      'wss://agents.example.com/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t',
    );
    expect(sandboxRelayUrl('http://127.0.0.1:4100/', 'ses_1', 'pgss_t')).toBe(
      'ws://127.0.0.1:4100/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t',
    );
    const escaped = sandboxRelayUrl('https://x.example', 'ses/1', 'a b&c=d');
    expect(escaped).toContain('ses%2F1');
    expect(escaped).not.toContain('a b&c=d');
    expect(() => sandboxRelayUrl('ftp://nope', 'ses_1', 't')).toThrow(/http\(s\) or ws\(s\)/);
  });

  // Exit codes ride in the close status. Reading a transport close as an exit
  // code would report a network fault as a successful run.
  it('decodes exit codes only inside the reserved close-status range', () => {
    expect(sandboxExecExitCode(SANDBOX_EXEC_CLOSE_STATUS_BASE)).toBe(0);
    expect(sandboxExecExitCode(SANDBOX_EXEC_CLOSE_STATUS_BASE + 3)).toBe(3);
    expect(sandboxExecExitCode(SANDBOX_EXEC_CLOSE_STATUS_BASE + 255)).toBe(255);
    expect(sandboxExecExitCode(SANDBOX_EXEC_CLOSE_STATUS_BASE + 256)).toBeUndefined();
    expect(sandboxExecExitCode(1000)).toBeUndefined(); // normal closure
    expect(sandboxExecExitCode(1008)).toBeUndefined(); // policy violation / expiry
  });

  it('states the relay frame bound', () => {
    expect(SANDBOX_RELAY_MAX_FRAME_BYTES).toBe(1 << 20);
  });

  it('reserves empty binary messages for closeInput', () => {
    const sent: Array<string | ArrayBufferLike | Blob | ArrayBufferView> = [];
    class FakeWebSocket extends EventTarget {
      send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
        sent.push(data);
      }
      close(): void {}
    }
    const session = new SandboxSession(new FakeWebSocket() as unknown as WebSocket);
    session.send('hello');
    session.send('');
    session.closeInput();
    session.closeInput();

    expect(sent).toHaveLength(2);
    expect(sent[0]).toBeInstanceOf(Uint8Array);
    expect(new TextDecoder().decode(sent[0] as Uint8Array)).toBe('hello');
    expect((sent[1] as Uint8Array).byteLength).toBe(0);
    expect(() => session.send('late')).toThrow(/input is closed/);
  });
});

describe('sandbox wait and session preconditions', () => {
  // A `failed` reading from the previous generation does not describe the
  // state just requested. Treating it as terminal made a start issued against
  // a failed sandbox reject during the reconciliation window — precisely when
  // the caller is trying to recover.
  it('ignores a failure from a superseded generation', async () => {
    let call = 0;
    const { client } = await sandboxClient(() => {
      call += 1;
      // First poll: stale failure from generation 1 while 2 is in flight.
      if (call === 1) {
        return jsonResponse({
          id: 'sbx_1', observedState: 'failed', generation: 2, observedGeneration: 1,
          failureMessage: 'stale failure',
        });
      }
      return jsonResponse({ id: 'sbx_1', observedState: 'running', generation: 2, observedGeneration: 2 });
    });

    const record = await client.sandbox.waitUntilRunning('sbx_1', {
      timeoutMs: 5_000,
      pollIntervalMs: 1,
    });
    expect(record.observedState).toBe('running');
    expect(call).toBeGreaterThan(1);
  });

  // A failure at the current generation is still terminal.
  it('rejects a failure at the current generation', async () => {
    const { client } = await sandboxClient(() =>
      jsonResponse({
        id: 'sbx_1', observedState: 'failed', generation: 1, observedGeneration: 1,
        failureMessage: 'image pull failed',
      }),
    );
    await expect(
      client.sandbox.waitUntilRunning('sbx_1', { timeoutMs: 5_000, pollIntervalMs: 1 }),
    ).rejects.toThrow(/image pull failed/);
  });

  // Without a signal on the request, a stalled management call stays pending
  // forever and the loop never reaches its own deadline check.
  it('bounds each poll by the remaining timeout', async () => {
    const { client } = await sandboxClient((url, init) => {
      // connect() performs a /healthz handshake, which must still complete.
      // Only the sandbox poll stalls — that is the case under test.
      if (url.includes('/healthz')) {
        return jsonResponse({ status: 'ok' });
      }
      return new Promise<Response>((_resolve, reject) => {
        // Never settles on its own; only the signal can end it.
        init.signal?.addEventListener('abort', () =>
          reject(Object.assign(new Error('aborted'), { name: 'AbortError' })),
        );
      });
    });
    const started = Date.now();
    await expect(
      client.sandbox.waitUntilRunning('sbx_1', { timeoutMs: 150, pollIntervalMs: 10 }),
    ).rejects.toThrow();
    expect(Date.now() - started).toBeLessThan(5_000);
  });

  it('rejects browser token auth before creating a single-use session', async () => {
    const { client, fetchImpl } = await sandboxClient(() =>
      jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
    );
    const nodeVersion = Object.getOwnPropertyDescriptor(process.versions, 'node');
    Object.defineProperty(process.versions, 'node', { ...nodeVersion, value: undefined });
    try {
      await expect(
        client.sandbox.openSession('sbx_1', 'exec', { command: ['true'] }),
      ).rejects.toThrow(/browser WebSocket cannot send configured relay headers/);
      expect(fetchImpl, 'a session was created before the browser auth check').not.toHaveBeenCalled();
    } finally {
      if (nodeVersion) Object.defineProperty(process.versions, 'node', nodeVersion);
    }
  });

  it('passes management headers to a custom WebSocket factory', async () => {
    const { client } = await sandboxClient(() =>
      jsonResponse({ id: 'ses_1', ticket: 'pgss_t' }, 201),
    );
    let receivedHeaders: Readonly<Record<string, string>> | undefined;
    class FakeWebSocket extends EventTarget {
      binaryType: BinaryType = 'blob';
      send(): void {}
      close(): void {}
    }
    const socket = new FakeWebSocket();
    const opening = client.sandbox.openSession('sbx_1', 'exec', {
      command: ['true'],
      webSocketFactory: (_url, connectOptions) => {
        receivedHeaders = connectOptions.headers;
        setTimeout(() => socket.dispatchEvent(new Event('open')), 0);
        return socket as unknown as WebSocket;
      },
    });
    await opening;
    expect(receivedHeaders?.authorization).toBe('Bearer mgmt-token');
  });
});
