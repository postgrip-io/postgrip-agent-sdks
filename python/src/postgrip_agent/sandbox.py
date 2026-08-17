"""Sandbox platform client.

Sandboxes are persistent development environments assigned to one of your
agents. These endpoints authenticate on the *management* lane — construct the
Connection with ``auth_token`` set to a management token. An agent access token
is rejected by every endpoint here.
"""

from __future__ import annotations

import time
from typing import IO, TYPE_CHECKING, Any, Iterable
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from .client import has_authorization_header
from .openapi import (
    CONNECT_SANDBOX_SESSION_PATH_SESSION_ID,
    CONNECT_SANDBOX_SESSION_QUERY_TICKET,
    OperationId,
    UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REPOSITORY,
    UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REVISION,
    resolve_openapi_operation,
)

if TYPE_CHECKING:  # pragma: no cover - import cycle only matters for typing
    from .client import Connection

#: Bound on a single frame through the session relay, in either direction. A
#: peer writing more has its session closed rather than forwarded, so chunk
#: writes at or below this.
SANDBOX_RELAY_MAX_FRAME_BYTES = 1 << 20

#: Exec exit codes arrive as the WebSocket close status, ``4000 + code``. A
#: close outside this range is a transport failure, not a process exit.
SANDBOX_EXEC_CLOSE_STATUS_BASE = 4000
SANDBOX_EXEC_CLOSE_STATUS_MAX = SANDBOX_EXEC_CLOSE_STATUS_BASE + 255

#: Server cap on a workspace archive upload; larger uploads are rejected 413.
SANDBOX_WORKSPACE_MAX_UPLOAD_BYTES = 512 << 20


def sandbox_exec_exit_code(close_status: int) -> int | None:
    """Decode a process exit code from a WebSocket close status.

    Returns ``None`` when the status carried no exit code — that means the
    transport ended rather than the process, and must not be read as success.
    """
    if close_status < SANDBOX_EXEC_CLOSE_STATUS_BASE or close_status > SANDBOX_EXEC_CLOSE_STATUS_MAX:
        return None
    return close_status - SANDBOX_EXEC_CLOSE_STATUS_BASE


def sandbox_relay_url(base_url: str, session_id: str, ticket: str) -> str:
    """Build the ``ws(s)://`` relay URL from an ``http(s)`` API base URL."""
    base = base_url.strip().rstrip("/")
    # URL schemes are case-insensitive (RFC 3986 §3.1), and urllib accepts
    # `HTTPS://…` for every other request this SDK makes. Matching the literal
    # prefix rejected an address that works everywhere else, which made opening
    # a session the single operation a mixed-case address broke.
    scheme, separator, remainder = base.partition("://")
    scheme = scheme.lower()
    if separator and scheme == "https":
        origin = "wss://" + remainder
    elif separator and scheme == "http":
        origin = "ws://" + remainder
    elif separator and scheme in ("wss", "ws"):
        origin = f"{scheme}://{remainder}"
    else:
        raise ValueError(f"postgrip-agent: sandbox relay base must be http(s) or ws(s): {base_url}")
    operation = resolve_openapi_operation(
        OperationId.CONNECT_SANDBOX_SESSION,
        {CONNECT_SANDBOX_SESSION_PATH_SESSION_ID: session_id},
        {CONNECT_SANDBOX_SESSION_QUERY_TICKET: ticket},
    )
    return origin + operation.path


def sandbox_is_ready(record: dict[str, Any]) -> bool:
    """Readiness is observed state AND generation.

    A ``running`` reading can predate a start or stop the assigned agent has
    not observed yet, so state alone can describe a sandbox that is about to
    change again.
    """
    return (
        record.get("observedState") == "running"
        and int(record.get("observedGeneration") or 0) >= int(record.get("generation") or 0)
    )


class SandboxClient:
    """Sandbox lifecycle and execution."""

    def __init__(self, connection: "Connection"):
        self.connection = connection

    # --- lifecycle ---------------------------------------------------------

    def create(self, request: dict[str, Any]) -> dict[str, Any]:
        """Provision a sandbox. Returns once the record exists — the sandbox is
        not yet running, so follow with :meth:`wait_until_running`."""
        return self.connection.openapi.create_sandbox(request)

    def list(self) -> list[dict[str, Any]]:
        """List live sandboxes.

        The endpoint returns an envelope, not a bare array; decoding it as an
        array would silently yield nothing.
        """
        response = self.connection.openapi.list_sandboxes() or {}
        return response.get("sandboxes") or []

    def get(self, sandbox_id: str) -> dict[str, Any]:
        _require_sandbox_id(sandbox_id)
        return self.connection.openapi.get_sandbox(sandbox_id)

    def start(self, sandbox_id: str) -> dict[str, Any]:
        _require_sandbox_id(sandbox_id)
        return self.connection.openapi.start_sandbox(sandbox_id)

    def stop(self, sandbox_id: str) -> dict[str, Any]:
        _require_sandbox_id(sandbox_id)
        return self.connection.openapi.stop_sandbox(sandbox_id)

    def delete(self, sandbox_id: str) -> dict[str, Any]:
        _require_sandbox_id(sandbox_id)
        return self.connection.openapi.delete_sandbox(sandbox_id)

    def create_session(
        self,
        sandbox_id: str,
        *,
        kind: str = "pty",
        command: list[str] | None = None,
        rows: int | None = None,
        columns: int | None = None,
    ) -> dict[str, Any]:
        """Mint a single-use relay ticket.

        The sandbox must already be running; while it comes up the server
        returns a retryable 400, so call :meth:`wait_until_running` first.
        """
        if kind not in ("pty", "exec"):
            raise ValueError("postgrip-agent: sandbox session kind must be 'pty' or 'exec'")
        if kind == "exec" and not command:
            raise ValueError("postgrip-agent: sandbox exec requires a command")
        body: dict[str, Any] = {"kind": kind}
        if command is not None:
            body["command"] = command
        if rows is not None:
            body["rows"] = rows
        if columns is not None:
            body["columns"] = columns
        _require_sandbox_id(sandbox_id)
        return self.connection.openapi.create_sandbox_session(sandbox_id, body)

    def upload_workspace(
        self,
        archive: bytes | IO[bytes],
        *,
        repository_name: str | None = None,
        revision: str | None = None,
    ) -> dict[str, Any]:
        """Upload a gzipped tar archive; its ``id`` goes in ``workspaceId``.

        The body is the raw archive — not multipart. Uploading identical bytes
        twice returns the *pre-existing* record rather than creating a second
        one, so do not assume a fresh id per upload.
        """
        data = archive if isinstance(archive, bytes) else archive.read()
        headers = dict(self.connection.headers)
        headers.setdefault("User-Agent", "postgrip-agent-python")
        headers["Content-Type"] = "application/gzip"
        if self.connection.auth_token and not has_authorization_header(headers):
            headers["Authorization"] = f"Bearer {self.connection.auth_token}"
        if repository_name:
            headers[UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REPOSITORY] = repository_name
        if revision:
            headers[UPLOAD_WORKSPACE_HEADER_X_POST_GRIP_REVISION] = revision

        operation = resolve_openapi_operation(OperationId.UPLOAD_WORKSPACE)
        if not operation.streaming_request:
            raise RuntimeError("postgrip-agent: workspace upload is not a streaming OpenAPI operation")
        request = Request(
            self.connection.address + operation.path,
            data=data,
            method=operation.method,
            headers=headers,
        )
        try:
            with urlopen(request, timeout=self.connection.timeout) as response:
                raw = response.read()
        except HTTPError as exc:
            raise RuntimeError(exc.read().decode() or str(exc)) from exc
        import json

        return json.loads(raw.decode()) if raw else {}

    def wait_until_running(
        self,
        sandbox_id: str,
        *,
        timeout: float = 120.0,
        poll_interval: float = 1.0,
    ) -> dict[str, Any]:
        """Poll until the sandbox is ready, fails, or the wait expires.

        Fails fast on ``failed`` with the sandbox's own message rather than
        burning the timeout, and the timeout error names the last observed
        state — "timed out" alone does not say whether the sandbox was still
        scheduling, mid-setup, or never picked up by an agent.
        """
        deadline = time.monotonic() + timeout
        last: dict[str, Any] = {}
        while True:
            record = self.get(sandbox_id)
            last = record
            if sandbox_is_ready(record):
                return record
            if record.get("observedState") == "failed":
                detail = (
                    record.get("failureMessage")
                    or record.get("failureCode")
                    or "no failure detail reported"
                )
                raise RuntimeError(f"postgrip-agent: sandbox {sandbox_id} failed: {detail}")
            if time.monotonic() >= deadline:
                raise TimeoutError(
                    f"postgrip-agent: sandbox {sandbox_id} was not running within {timeout}s "
                    f"(last observed state: {last.get('observedState', 'unknown')})"
                )
            # Never sleep past the deadline. An unclamped sleep meant a
            # poll_interval larger than the time remaining postponed the next
            # deadline check by the whole interval — `timeout=5,
            # poll_interval=60` blocked for about a minute before raising, so
            # the timeout this call advertises was not the one it kept.
            time.sleep(min(poll_interval, max(0.0, deadline - time.monotonic())))

    # --- sessions ----------------------------------------------------------

    def open_session(
        self,
        sandbox_id: str,
        *,
        kind: str = "pty",
        command: list[str] | None = None,
        rows: int | None = None,
        columns: int | None = None,
        relay_base_url: str | None = None,
    ) -> "SandboxSession":
        """Create a session and dial the relay.

        Requires the optional ``websockets`` dependency::

            pip install "postgrip-agent[sandbox]"
        """
        session = self.create_session(
            sandbox_id, kind=kind, command=command, rows=rows, columns=columns
        )
        url = sandbox_relay_url(
            relay_base_url or self.connection.address, session["id"], session["ticket"]
        )
        # Start from the connection's configured headers rather than only its
        # auth_token. A caller authenticating through
        # `Connection(headers={"Authorization": ...})` — a supported way to do
        # it, and the way a header-authenticating gateway is fed — got an
        # unauthenticated relay dial, so session creation succeeded and the
        # dial failed *after* burning the single-use ticket.
        headers = dict(self.connection.headers)
        if self.connection.auth_token and not has_authorization_header(headers):
            # The ticket authorizes the session; the management credential
            # still authenticates the request. Both are required.
            headers["Authorization"] = f"Bearer {self.connection.auth_token}"
        return SandboxSession(url, headers)

    def exec(
        self,
        sandbox_id: str,
        command: list[str],
        *,
        stdin: bytes | None = None,
        relay_base_url: str | None = None,
    ) -> tuple[int | None, bytes]:
        """Run a command and return ``(exit_code, output)``.

        ``output`` carries stdout and stderr *interleaved* — the relay is a
        single stream, so they cannot be separated client-side. ``exit_code``
        is ``None`` when the close carried no code, meaning the transport ended
        rather than the process.

        .. warning::

           **Commands that read stdin to EOF will hang.** There is no
           end-of-input signal on the wire. The agent hands the relay
           connection to the process as its stdin directly, so that stdin
           reaches EOF only when the whole session closes — which is also what
           carries the exit status back. Sending ``stdin`` here therefore
           delivers the bytes but tells the sandbox nothing more, and a command
           that reads until EOF (``cat``, ``sort``,
           ``python -c 'import sys; sys.stdin.read()'``) waits for input while
           this call waits for output.

           Commands that read a bounded amount, and commands that read nothing,
           are unaffected. Closing this properly needs a half-close on the wire,
           which is a protocol change rather than an SDK one.
        """
        session = self.open_session(
            sandbox_id, kind="exec", command=command, relay_base_url=relay_base_url
        )
        with session:
            if stdin:
                session.send(stdin)
            chunks = list(session.read_all())
            return session.exit_code, b"".join(chunks)


class SandboxSession:
    """A live sandbox session: a raw bidirectional byte stream.

    There is no framing and no resize channel; rows and columns are fixed when
    the session is created.
    """

    def __init__(self, url: str, headers: dict[str, str]):
        try:
            from websockets.sync.client import connect  # type: ignore[import-not-found]
        except ImportError as exc:  # pragma: no cover - dependency guard
            raise RuntimeError(
                "postgrip-agent: sandbox sessions need the 'websockets' package; "
                'install with pip install "postgrip-agent[sandbox]"'
            ) from exc
        self._socket = connect(url, additional_headers=headers, max_size=None)
        self.exit_code: int | None = None

    def send(self, data: bytes | str) -> None:
        size = len(data.encode()) if isinstance(data, str) else len(data)
        if size > SANDBOX_RELAY_MAX_FRAME_BYTES:
            raise ValueError(
                f"postgrip-agent: sandbox write of {size} bytes exceeds the relay frame "
                f"limit ({SANDBOX_RELAY_MAX_FRAME_BYTES}); chunk your writes"
            )
        self._socket.send(data)

    def read_all(self) -> Iterable[bytes]:
        """Yield output until the session closes, recording the exit code."""
        from websockets.exceptions import ConnectionClosed  # type: ignore[import-not-found]

        try:
            for message in self._socket:
                yield message if isinstance(message, bytes) else message.encode()
        except ConnectionClosed as exc:
            self.exit_code = sandbox_exec_exit_code(exc.rcvd.code if exc.rcvd else 0)
        else:
            close_code = getattr(self._socket, "close_code", None)
            if close_code is not None:
                self.exit_code = sandbox_exec_exit_code(close_code)

    def close(self) -> None:
        self._socket.close()

    def __enter__(self) -> "SandboxSession":
        return self

    def __exit__(self, *exc_info: object) -> None:
        self.close()


def _require_sandbox_id(sandbox_id: str) -> None:
    if not sandbox_id:
        # Otherwise this builds /api/v1/sandboxes/ and hits the collection
        # route, which is a confusing 404 or worse a list.
        raise ValueError("postgrip-agent: sandbox id is required")
