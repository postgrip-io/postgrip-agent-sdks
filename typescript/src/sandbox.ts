import type { Connection } from './connection.js';
import {
  CONNECT_SANDBOX_SESSION_PATH_SESSION_ID,
  CONNECT_SANDBOX_SESSION_QUERY_TICKET,
  resolveOpenAPIOperation,
} from './generated/openapi.js';
import type {
  CreateSandboxSessionRequest,
  CreateSandboxSessionResponse,
  Sandbox,
  SandboxCreateRequest,
  SandboxSessionKind,
  SandboxWorkspace,
} from './types.js';

/**
 * Bound on a single frame through the session relay, in either direction. A
 * peer writing more has its session closed rather than forwarded, so chunk
 * writes at or below this.
 */
export const SANDBOX_RELAY_MAX_FRAME_BYTES = 1 << 20;

/**
 * Exec exit codes arrive as the WebSocket close status, `4000 + code`. A close
 * outside this range is a transport failure, not a process exit.
 */
export const SANDBOX_EXEC_CLOSE_STATUS_BASE = 4000;
export const SANDBOX_EXEC_CLOSE_STATUS_MAX = SANDBOX_EXEC_CLOSE_STATUS_BASE + 255;

/** Decodes a process exit code from a WebSocket close status. */
export function sandboxExecExitCode(closeStatus: number): number | undefined {
  if (closeStatus < SANDBOX_EXEC_CLOSE_STATUS_BASE || closeStatus > SANDBOX_EXEC_CLOSE_STATUS_MAX) {
    return undefined;
  }
  return closeStatus - SANDBOX_EXEC_CLOSE_STATUS_BASE;
}

export interface SandboxWaitOptions {
  /** Defaults to 120_000 ms. */
  timeoutMs?: number;
  /** Defaults to 1_000 ms. */
  pollIntervalMs?: number;
  signal?: AbortSignal;
}

export interface SandboxSessionOptions {
  command?: string[];
  /**
   * PTY size, defaulting to 24x80. Fixed for the session's life — the relay
   * has no resize channel, so a terminal resized mid-session cannot tell the
   * sandbox.
   */
  rows?: number;
  columns?: number;
  /**
   * Overrides where the WebSocket dials, defaulting to the connection's base
   * URL. Needed when the API is reached through a proxy that doesn't forward
   * WebSocket upgrades — the relay is deliberately not proxied.
   */
  relayBaseUrl?: string;
  /** Supply a WebSocket implementation when the runtime lacks a global one. */
  webSocketImpl?: typeof WebSocket;
}

/**
 * A live sandbox session: a raw bidirectional byte stream plus the process
 * exit code once it closes.
 *
 * There is no framing. For `pty` this is terminal traffic; for `exec` it is
 * the process's stdout and stderr **interleaved on one stream** — the relay
 * multiplexes both, so they cannot be separated client-side.
 */
export class SandboxSession {
  private readonly closed: Promise<number | undefined>;

  constructor(private readonly socket: WebSocket) {
    this.closed = new Promise((resolve) => {
      socket.addEventListener('close', (event) => resolve(sandboxExecExitCode(event.code)), { once: true });
    });
  }

  /** Sends bytes to the sandbox's stdin. */
  send(data: Uint8Array | string): void {
    const size = typeof data === 'string' ? Buffer.byteLength(data) : data.byteLength;
    if (size > SANDBOX_RELAY_MAX_FRAME_BYTES) {
      throw new Error(
        `postgrip-agent: sandbox write of ${size} bytes exceeds the relay frame limit ` +
          `(${SANDBOX_RELAY_MAX_FRAME_BYTES}); chunk your writes`,
      );
    }
    this.socket.send(data);
  }

  /** Registers a handler for sandbox output. */
  onData(handler: (chunk: Uint8Array) => void): void {
    this.socket.addEventListener('message', (event: MessageEvent) => {
      const data = event.data;
      if (data instanceof ArrayBuffer) handler(new Uint8Array(data));
      else if (data instanceof Uint8Array) handler(data);
      else if (typeof data === 'string') handler(new TextEncoder().encode(data));
    });
  }

  /**
   * Resolves with the process exit code when the session ends.
   *
   * Resolves `undefined` when the close status carried no exit code — which
   * means the transport ended rather than the process, and must not be read as
   * a successful run.
   */
  async exitCode(): Promise<number | undefined> {
    return this.closed;
  }

  /** Closes the session immediately. */
  close(): void {
    this.socket.close();
  }
}

/**
 * Sandbox lifecycle and execution.
 *
 * Sandbox endpoints authenticate on the management lane: construct the
 * Connection with `authToken` set to a management token. An agent access token
 * is rejected by every endpoint here.
 */
export class SandboxClient {
  constructor(private readonly connection: Connection) {}

  async create(request: SandboxCreateRequest): Promise<Sandbox> {
    return this.connection.createSandbox(request);
  }

  async list(): Promise<Sandbox[]> {
    return this.connection.listSandboxes();
  }

  async get(sandboxId: string, signal?: AbortSignal): Promise<Sandbox> {
    return this.connection.getSandbox(requireSandboxId(sandboxId), signal);
  }

  async start(sandboxId: string): Promise<Sandbox> {
    return this.connection.startSandbox(requireSandboxId(sandboxId));
  }

  async stop(sandboxId: string): Promise<Sandbox> {
    return this.connection.stopSandbox(requireSandboxId(sandboxId));
  }

  async delete(sandboxId: string): Promise<Sandbox> {
    return this.connection.deleteSandbox(requireSandboxId(sandboxId));
  }

  async uploadWorkspace(
    archive: BodyInit,
    metadata: { repositoryName?: string; revision?: string } = {},
  ): Promise<SandboxWorkspace> {
    return this.connection.uploadWorkspace(archive, metadata);
  }

  async createSession(
    sandboxId: string,
    request: CreateSandboxSessionRequest = {},
  ): Promise<CreateSandboxSessionResponse> {
    return this.connection.createSandboxSession(requireSandboxId(sandboxId), request);
  }

  /**
   * Polls until the sandbox is ready, fails, or the wait expires.
   *
   * Readiness is `observedState === 'running'` **and**
   * `observedGeneration >= generation`. State alone can predate a start or
   * stop the assigned agent hasn't observed yet, so checking only the state
   * can return a sandbox that is about to stop.
   */
  async waitUntilRunning(sandboxId: string, options: SandboxWaitOptions = {}): Promise<Sandbox> {
    const timeoutMs = options.timeoutMs ?? 120_000;
    const pollIntervalMs = options.pollIntervalMs ?? 1_000;
    const deadline = Date.now() + timeoutMs;
    let last: Sandbox | undefined;

    for (;;) {
      options.signal?.throwIfAborted();
      // Bound the request itself, not just the gaps between requests. Without
      // a signal a stalled management call stays pending forever, and the loop
      // never reaches the deadline check below — so waitUntilRunning could
      // outlive its own timeout indefinitely. The signal combines the caller's
      // with what remains of the deadline, so whichever comes first wins.
      const remaining = deadline - Date.now();
      const pollSignal = combineAbortSignals(options.signal, remaining > 0 ? remaining : 1);
      const record = await this.get(sandboxId, pollSignal);
      last = record;
      // Both readiness and failure are read at the current generation. A
      // `failed` left over from the previous generation does not describe the
      // state just requested, so treating it as terminal made a start issued
      // against a failed sandbox reject during the reconciliation window —
      // exactly when a caller is trying to recover.
      const observed = (record.observedGeneration ?? 0) >= (record.generation ?? 0);
      if (record.observedState === 'running' && observed) {
        return record;
      }
      if (record.observedState === 'failed' && observed) {
        throw new Error(
          `postgrip-agent: sandbox ${sandboxId} failed: ` +
            (record.failureMessage || record.failureCode || 'no failure detail reported'),
        );
      }
      if (Date.now() >= deadline) {
        throw new Error(
          `postgrip-agent: sandbox ${sandboxId} was not running within ${timeoutMs}ms ` +
            `(last observed state: ${last?.observedState ?? 'unknown'})`,
        );
      }
      await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
    }
  }

  /**
   * Creates a session and dials the relay.
   *
   * The sandbox must already be running; while it comes up the server rejects
   * session creation with a retryable 400, so call `waitUntilRunning` first.
   * The ticket is single-use and short-lived, which is why creating and
   * dialling are one call.
   */
  async openSession(
    sandboxId: string,
    kind: SandboxSessionKind,
    options: SandboxSessionOptions = {},
  ): Promise<SandboxSession> {
    if (kind === 'exec' && (!options.command || options.command.length === 0)) {
      throw new Error('postgrip-agent: sandbox exec requires a command');
    }
    // Resolved before the session is created, not after. Creating first meant
    // that on a runtime with no global WebSocket the server-side session was
    // already made — and for an exec session the command may have started —
    // before this threw. The caller then saw an error, never the output, and
    // retrying with an implementation supplied would run a side-effecting
    // command a second time.
    const WebSocketImpl = options.webSocketImpl ?? globalThis.WebSocket;
    if (!WebSocketImpl) {
      throw new Error(
        'postgrip-agent: no WebSocket implementation available; pass webSocketImpl (Node 22+ has a global WebSocket)',
      );
    }
    const session = await this.createSession(sandboxId, {
      kind,
      command: options.command,
      rows: options.rows,
      columns: options.columns,
    });
    const url = sandboxRelayUrl(
      options.relayBaseUrl ?? this.connection.baseUrl,
      session.id!,
      session.ticket!,
    );
    // The ticket authorizes the session; the management credential still
    // authenticates the request. Browsers cannot set headers on a WebSocket
    // handshake, which is why the ticket is in the query string.
    const socket = new WebSocketImpl(url);
    socket.binaryType = 'arraybuffer';
    await new Promise<void>((resolve, reject) => {
      socket.addEventListener('open', () => resolve(), { once: true });
      socket.addEventListener('error', () => reject(new Error('postgrip-agent: sandbox relay dial failed')), {
        once: true,
      });
    });
    return new SandboxSession(socket);
  }

  /**
   * Runs a command in the sandbox and resolves with its exit code and output.
   *
   * `output` carries stdout and stderr interleaved — the relay is one stream.
   */
  async exec(
    sandboxId: string,
    command: string[],
    options: Omit<SandboxSessionOptions, 'command'> = {},
  ): Promise<{ exitCode: number | undefined; output: Uint8Array }> {
    const session = await this.openSession(sandboxId, 'exec', { ...options, command });
    const chunks: Uint8Array[] = [];
    session.onData((chunk) => chunks.push(chunk));
    const exitCode = await session.exitCode();
    let total = 0;
    for (const c of chunks) total += c.byteLength;
    const output = new Uint8Array(total);
    let offset = 0;
    for (const c of chunks) {
      output.set(c, offset);
      offset += c.byteLength;
    }
    return { exitCode, output };
  }
}

/** Builds the ws(s):// relay URL from an http(s) API base URL. */
export function sandboxRelayUrl(baseUrl: string, sessionId: string, ticket: string): string {
  const base = baseUrl.trim().replace(/\/+$/, '');
  let origin: string;
  if (base.startsWith('https://')) origin = `wss://${base.slice('https://'.length)}`;
  else if (base.startsWith('http://')) origin = `ws://${base.slice('http://'.length)}`;
  else if (base.startsWith('wss://') || base.startsWith('ws://')) origin = base;
  else throw new Error(`postgrip-agent: sandbox relay base must be http(s) or ws(s): ${baseUrl}`);
  const operation = resolveOpenAPIOperation(
    'connectSandboxSession',
    { [CONNECT_SANDBOX_SESSION_PATH_SESSION_ID]: sessionId },
    new URLSearchParams({ [CONNECT_SANDBOX_SESSION_QUERY_TICKET]: ticket }),
  );
  return `${origin}${operation.path}`;
}

function requireSandboxId(sandboxId: string): string {
  if (!sandboxId) {
    // Otherwise this builds /api/v1/sandboxes/ and hits the collection route.
    throw new Error('postgrip-agent: sandbox id is required');
  }
  return sandboxId;
}

/**
 * A signal that aborts on the caller's signal or after `timeoutMs`, whichever
 * comes first.
 *
 * `AbortSignal.any` and `AbortSignal.timeout` are both Node 20+ / modern
 * browsers, which this package already targets, but they are guarded anyway:
 * on a runtime without them, falling back to the caller's signal alone leaves
 * the previous behaviour rather than throwing at an unrelated call site.
 */
function combineAbortSignals(signal: AbortSignal | undefined, timeoutMs: number): AbortSignal | undefined {
  if (typeof AbortSignal?.timeout !== 'function') {
    return signal;
  }
  const deadlineSignal = AbortSignal.timeout(timeoutMs);
  if (!signal) {
    return deadlineSignal;
  }
  if (typeof AbortSignal.any !== 'function') {
    return signal;
  }
  return AbortSignal.any([signal, deadlineSignal]);
}
