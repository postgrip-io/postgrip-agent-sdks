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
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen

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
    if base.startswith("https://"):
        origin = "wss://" + base[len("https://"):]
    elif base.startswith("http://"):
        origin = "ws://" + base[len("http://"):]
    elif base.startswith(("wss://", "ws://")):
        origin = base
    else:
        raise ValueError(f"postgrip-agent: sandbox relay base must be http(s) or ws(s): {base_url}")
    return (
        f"{origin}/api/v1/sandbox-sessions/{quote(session_id, safe='')}"
        f"/connect?ticket={quote(ticket, safe='')}"
    )


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
        return self.connection.request("POST", "/api/v1/sandboxes", request)

    def list(self) -> list[dict[str, Any]]:
        """List live sandboxes.

        The endpoint returns an envelope, not a bare array; decoding it as an
        array would silently yield nothing.
        """
        response = self.connection.request("GET", "/api/v1/sandboxes") or {}
        return response.get("sandboxes") or []

    def get(self, sandbox_id: str) -> dict[str, Any]:
        return self.connection.request("GET", _sandbox_path(sandbox_id))

    def start(self, sandbox_id: str) -> dict[str, Any]:
        return self.connection.request("POST", _sandbox_path(sandbox_id) + "/start")

    def stop(self, sandbox_id: str) -> dict[str, Any]:
        return self.connection.request("POST", _sandbox_path(sandbox_id) + "/stop")

    def delete(self, sandbox_id: str) -> dict[str, Any]:
        return self.connection.request("DELETE", _sandbox_path(sandbox_id))

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
        return self.connection.request("POST", _sandbox_path(sandbox_id) + "/sessions", body)

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
        if self.connection.auth_token and "Authorization" not in headers:
            headers["Authorization"] = f"Bearer {self.connection.auth_token}"
        if repository_name:
            headers["X-PostGrip-Repository"] = repository_name
        if revision:
            headers["X-PostGrip-Revision"] = revision

        request = Request(
            self.connection.address + "/api/v1/workspaces",
            data=data,
            method="POST",
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
            time.sleep(poll_interval)

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
        headers = {}
        if self.connection.auth_token:
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


def _sandbox_path(sandbox_id: str) -> str:
    if not sandbox_id:
        # Otherwise this builds /api/v1/sandboxes/ and hits the collection
        # route, which is a confusing 404 or worse a list.
        raise ValueError("postgrip-agent: sandbox id is required")
    return f"/api/v1/sandboxes/{quote(sandbox_id, safe='')}"
