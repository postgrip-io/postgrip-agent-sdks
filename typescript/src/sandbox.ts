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

export interface SandboxWebSocketConnectOptions {
  /** Connection-level headers, including the management Authorization header. */
  readonly headers: Readonly<Record<string, string>>;
}

/** Creates an authenticated WebSocket for a sandbox relay connection. */
export type SandboxWebSocketFactory = (
  url: string,
  options: SandboxWebSocketConnectOptions,
) => WebSocket;

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
  /**
   * Overrides WebSocket construction. The factory receives every configured
   * connection header, including the management Authorization credential.
   */
  webSocketFactory?: SandboxWebSocketFactory;
  /**
   * Legacy constructor override. Header-capable Node implementations receive
   * the connection headers in their constructor options. Prefer
   * `webSocketFactory` for new integrations.
   */
  webSocketImpl?: typeof WebSocket;
}

/**
 * A live sandbox session: a raw bidirectional byte stream plus the process
 * exit code once it closes.
 *
 * For `pty` this is terminal traffic; for `exec` it is the process's stdout
 * and stderr **interleaved on one stream** — the relay multiplexes both, so
 * they cannot be separated client-side. Message boundaries are otherwise
 * opaque, except that `closeInput` sends the reserved zero-length binary
 * stdin-EOF message.
 */
export class SandboxSession {
  private readonly closed: Promise<number | undefined>;
  private inputClosed = false;

  constructor(private readonly socket: WebSocket) {
    this.closed = new Promise((resolve) => {
      socket.addEventListener('close', (event) => resolve(sandboxExecExitCode(event.code)), { once: true });
    });
  }

  /** Sends bytes to the sandbox's stdin. */
  send(data: Uint8Array | string): void {
    if (this.inputClosed) {
      throw new Error('postgrip-agent: sandbox session input is closed');
    }
    const bytes = sandboxInputBytes(data);
    // An empty binary message is the stdin-EOF control. Preserve send's byte
    // stream semantics by making empty writes no-ops; closeInput owns it.
    if (bytes.byteLength === 0) return;
    this.socket.send(bytes);
  }

  /** Signals stdin EOF while keeping output and exit-status delivery open. */
  closeInput(): void {
    if (this.inputClosed) return;
    this.socket.send(new Uint8Array(0));
    this.inputClosed = true;
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

export interface SandboxExecOptions extends Omit<SandboxSessionOptions, 'command'> {
  /** Bytes delivered to stdin before the SDK signals EOF. Defaults to empty. */
  stdin?: Uint8Array | string;
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
    const relayHeaders: Record<string, string> = {};
    this.connection.managementHeaders().forEach((value, name) => {
      relayHeaders[name] = value;
    });
    Object.freeze(relayHeaders);
    // Resolve the transport before creating the single-use server session. A
    // missing implementation must not burn a ticket or start an exec command.
    const createWebSocket = await resolveSandboxWebSocketFactory(options, relayHeaders);
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
    // The ticket selects and authorizes the session; the management credential
    // separately authenticates the request. Both are required by the contract.
    const socket = createWebSocket(url, { headers: relayHeaders });
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
    options: SandboxExecOptions = {},
  ): Promise<{ exitCode: number | undefined; output: Uint8Array }> {
    const { stdin, ...sessionOptions } = options;
    // Validate deterministic local failures before createSession starts a
    // remote command and consumes its single-use relay ticket.
    const stdinBytes = stdin === undefined ? undefined : sandboxInputBytes(stdin);
    const session = await this.openSession(sandboxId, 'exec', { ...sessionOptions, command });
    const chunks: Uint8Array[] = [];
    session.onData((chunk) => chunks.push(chunk));
    try {
      if (stdinBytes !== undefined) session.send(stdinBytes);
      session.closeInput();
    } catch (error) {
      // A socket can fail after the remote exec has started. The caller has no
      // session handle from this convenience method, so tear it down here.
      try {
        session.close();
      } catch {
        // Preserve the stdin setup failure; close is best-effort cleanup.
      }
      throw error;
    }
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

function sandboxInputBytes(data: Uint8Array | string): Uint8Array {
  const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data;
  if (bytes.byteLength > SANDBOX_RELAY_MAX_FRAME_BYTES) {
    throw new Error(
      `postgrip-agent: sandbox write of ${bytes.byteLength} bytes exceeds the relay frame limit ` +
        `(${SANDBOX_RELAY_MAX_FRAME_BYTES}); chunk your writes`,
    );
  }
  return bytes;
}

type HeaderCapableWebSocketConstructor = new (
  url: string,
  protocols?: string | string[],
  options?: { headers?: Readonly<Record<string, string>> },
) => WebSocket;

async function resolveSandboxWebSocketFactory(
  options: SandboxSessionOptions,
  headers: Readonly<Record<string, string>>,
): Promise<SandboxWebSocketFactory> {
  if (options.webSocketFactory) {
    return options.webSocketFactory;
  }
  if (options.webSocketImpl) {
    const WebSocketImpl = options.webSocketImpl as unknown as HeaderCapableWebSocketConstructor;
    return (url, connectOptions) => new WebSocketImpl(url, [], { headers: connectOptions.headers });
  }

  // The standard WebSocket API cannot attach an Authorization header. Use the
  // header-capable `ws` implementation on Node/Bun even when a global
  // WebSocket exists (Node 22+), otherwise token-authenticated relays fail.
  if (typeof process !== 'undefined' && process.versions?.node) {
    const { WebSocket: NodeWebSocket } = await import('ws');
    return (url, connectOptions) => new NodeWebSocket(url, {
      headers: { ...connectOptions.headers },
    }) as unknown as WebSocket;
  }

  const WebSocketImpl = globalThis.WebSocket;
  if (!WebSocketImpl) {
    throw new Error(
      'postgrip-agent: no WebSocket implementation available; pass webSocketFactory',
    );
  }
  if (Object.keys(headers).length > 0) {
    throw new Error(
      'postgrip-agent: browser WebSocket cannot send configured relay headers; ' +
        'authenticate with same-origin cookies or pass webSocketFactory in a header-capable runtime',
    );
  }
  return (url) => new WebSocketImpl(url);
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
