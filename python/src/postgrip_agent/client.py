from __future__ import annotations

import asyncio
import json
import os
import threading
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Callable
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from . import _signing
from .errors import TaskFailedError, TimeoutFailure
from .openapi import (
    OpenAPIClient,
    OperationId,
    openapi_auth_lane_for_request,
    resolve_openapi_operation,
)
from .types import IsolationTier
from .workflow import workflow_name

_MISSING = object()
_POSTGRIP_UI_MEMO_KEY = "postgrip.ui"
DEFAULT_ADDRESS = "https://agentorchestrator1.postgrip.io"


def has_authorization_header(headers: dict[str, str]) -> bool:
    """Whether *headers* already carries an Authorization header, any casing.

    HTTP header names are case-insensitive and ``urllib.request.Request``
    normalizes them, so a caller passing ``{"authorization": "Bearer …"}`` — a
    perfectly valid spelling — did not match an exact-case ``"Authorization"``
    check. The configured token was then added under the capitalized name, both
    collapsed to one header, and the *added* one won: the explicit-header
    precedence this SDK documents was inverted, and a request could go out
    authenticated as the wrong tenant.
    """
    return any(name.lower() == "authorization" for name in headers)


class Connection:
    def __init__(
        self,
        address: str | None = None,
        *,
        timeout: float = 30,
        headers: dict[str, str] | None = None,
        auth_token: str | None = None,
        agent_id: str | None = None,
        worker_id: str | None = None,
        agent_name: str | None = None,
        agent_host: str | None = None,
        agent_namespace: str = "default",
        agent_queue: str = "default",
        agent_access_token: str | None = None,
        agent_refresh_token: str | None = None,
        agent_access_expires_at: str | datetime | None = None,
        agent_signing_private_key: str | None = None,
    ):
        if not address:
            address = os.environ.get("POSTGRIP_AGENTORCHESTRATOR_URL") or DEFAULT_ADDRESS
        if "://" not in address:
            address = f"http://{address}"
        self.address = address.rstrip("/")
        self.timeout = timeout
        self.headers = dict(headers or {})
        # Management token, sent on every management-lane request. Required for
        # the sandbox APIs, which reject agent tokens. Opaque: the console
        # issues a bare hex string with no prefix, so there is nothing to
        # validate.
        self.auth_token = (auth_token or os.environ.get("POSTGRIP_TOKEN") or "").strip() or None
        self._agent_id = agent_id or worker_id or os.environ.get("POSTGRIP_AGENT_ID")
        self._agent_name = agent_name
        self._agent_host = agent_host
        self._agent_namespace = os.environ.get("POSTGRIP_AGENT_NAMESPACE", agent_namespace) if agent_namespace == "default" else agent_namespace
        self._agent_queue = os.environ.get("POSTGRIP_AGENT_TASK_QUEUE", agent_queue) if agent_queue == "default" else agent_queue
        self._agent_access_token: str | None = agent_access_token or os.environ.get("POSTGRIP_AGENT_ACCESS_TOKEN")
        self._agent_refresh_token: str | None = agent_refresh_token or os.environ.get("POSTGRIP_AGENT_REFRESH_TOKEN")
        self._agent_access_expires_at = _parse_timestamp(agent_access_expires_at or os.environ.get("POSTGRIP_AGENT_ACCESS_EXPIRES_AT"))
        self._agent_lock = threading.RLock()
        self._agent_refresh_lock = threading.Lock()
        # Ed25519 keypair injected by the host agent for managed workflow
        # runtimes.
        signing_private_key = agent_signing_private_key or os.environ.get("POSTGRIP_AGENT_SIGNING_PRIVATE_KEY")
        self._agent_sign_priv: Ed25519PrivateKey | None = _signing.decode_private_key(signing_private_key) if signing_private_key else None
        self.openapi = OpenAPIClient(self._request_openapi)

    @classmethod
    async def connect(cls, address: str | None = None, **options: Any) -> "Connection":
        connection = cls(address, **options)
        await asyncio.to_thread(connection.health)
        return connection

    def configure_agent_auth(
        self,
        *,
        agent_id: str | None = None,
        worker_id: str | None = None,
        agent_name: str | None = None,
        agent_host: str | None = None,
        namespace: str | None = None,
        queue: str | None = None,
        access_token: str | None = None,
        refresh_token: str | None = None,
        access_expires_at: str | datetime | None = None,
        signing_private_key: str | None = None,
    ) -> None:
        with self._agent_lock:
            resolved_agent_id = agent_id or worker_id
            if resolved_agent_id:
                self._agent_id = resolved_agent_id
            if agent_name:
                self._agent_name = agent_name
            if agent_host:
                self._agent_host = agent_host
            if namespace:
                self._agent_namespace = namespace
            if queue:
                self._agent_queue = queue
            if access_token:
                self._agent_access_token = access_token
            if refresh_token:
                self._agent_refresh_token = refresh_token
            if access_expires_at:
                self._agent_access_expires_at = _parse_timestamp(access_expires_at)
            if signing_private_key:
                self._agent_sign_priv = _signing.decode_private_key(signing_private_key)

    def request(self, method: str, path: str, body: Any = None, *, agent_auth: bool = False) -> Any:
        # Raw agent-authenticated requests preserve the legacy signing default.
        signing = "agent-task-v1" if agent_auth else ""
        return self._request(method, path, body, agent_auth=agent_auth, signing=signing)

    def _request_openapi(
        self,
        operation_id: OperationId,
        body: Any = None,
        *,
        path_parameters: dict[str, str] | None = None,
        query: dict[str, Any] | None = None,
    ) -> Any:
        """Resolve generated HTTP metadata, retaining the custom transport."""
        operation = resolve_openapi_operation(operation_id, path_parameters, query)
        agent_auth = operation.auth_lane == "agent" or (
            operation.auth_lane == "either" and self._has_agent_runtime_credentials()
        )
        return self._request(
            operation.method,
            operation.path,
            body,
            agent_auth=agent_auth,
            signing=operation.signing,
        )

    def _request(
        self,
        method: str,
        path: str,
        body: Any = None,
        *,
        agent_auth: bool = False,
        signing: str = "",
    ) -> Any:
        data = b"" if body is None else json.dumps(body).encode()
        auth_lane = openapi_auth_lane_for_request(method, path)
        # Preserve the raw request API's opt-in behavior for agent-only paths;
        # generated wrappers pass agent_auth explicitly. "Either" operations
        # retain their historical automatic lane selection.
        use_agent_auth = agent_auth or (
            auth_lane == "either" and self._has_agent_runtime_credentials()
        )
        if use_agent_auth:
            self.ensure_agent_session()
        headers = dict(self.headers)
        # Some CDN front-ends block urllib's default UA. Always send a stable
        # SDK identifier so behavior is consistent across local and proxied
        # deployments.
        headers.setdefault("User-Agent", "postgrip-agent-python")
        if use_agent_auth and self._agent_access_token:
            headers["Authorization"] = f"Bearer {self._agent_access_token}"
        elif not use_agent_auth and self.auth_token and not has_authorization_header(headers):
            # An explicitly supplied header wins, so existing callers that
            # hand-build one keep working.
            headers["Authorization"] = f"Bearer {self.auth_token}"
        if body is not None:
            headers["Content-Type"] = "application/json"
        if use_agent_auth and signing == "agent-task-v1" and self._agent_sign_priv is not None:
            split = urlsplit(self.address + path)
            ts = int(time.time())
            headers[_signing.HEADER_AGENT_SIGNATURE_TIMESTAMP] = str(ts)
            headers[_signing.HEADER_AGENT_SIGNATURE_KEY_ID] = _signing.public_key_id(self._agent_sign_priv.public_key())
            headers[_signing.HEADER_AGENT_SIGNATURE] = _signing.sign_request(
                self._agent_sign_priv, method, split.path, split.query, ts, data,
            )
        # urllib treats `data=b""` as a GET-with-body, which is wrong; pass None for empty bodies.
        request = Request(self.address + path, data=(data or None) if body is not None else None, method=method, headers=headers)
        try:
            with urlopen(request, timeout=self.timeout) as response:
                raw = response.read()
        except HTTPError as exc:
            raise RuntimeError(exc.read().decode() or str(exc)) from exc
        return json.loads(raw.decode()) if raw else None

    def ensure_agent_session(self, *, namespace: str | None = None, queue: str | None = None, agent_id: str | None = None, worker_id: str | None = None) -> bool:
        with self._agent_lock:
            if namespace:
                self._agent_namespace = namespace
            if queue:
                self._agent_queue = queue
            resolved_agent_id = agent_id or worker_id
            if resolved_agent_id:
                self._agent_id = resolved_agent_id
            if self._agent_access_token and self._agent_access_expires_at > time.time() + 30:
                return True

        with self._agent_refresh_lock:
            with self._agent_lock:
                if self._agent_access_token and self._agent_access_expires_at > time.time() + 30:
                    return True
                refresh_token = self._agent_refresh_token

            if refresh_token:
                self._apply_agent_session(
                    self.openapi.refresh_agent_session({"refreshToken": refresh_token})
                )
                return True

            raise RuntimeError("postgrip-agent: managed runtime credentials are required; submit workflow.runtime work to a host agent instead of enrolling SDK agents")

    def _has_agent_runtime_credentials(self) -> bool:
        with self._agent_lock:
            return bool(self._agent_access_token or self._agent_refresh_token)

    def _apply_agent_session(self, session: dict[str, Any]) -> None:
        with self._agent_lock:
            self._agent_id = session.get("agentId") or self._agent_id
            self._agent_access_token = session.get("accessToken")
            self._agent_refresh_token = session.get("refreshToken")
            self._agent_access_expires_at = _parse_timestamp(session.get("accessExpiresAt"))

    def health(self) -> dict[str, Any]:
        return self.openapi.health()

    def ready(self) -> dict[str, Any]:
        return self.openapi.ready()

    def list_namespaces(self) -> list[dict[str, Any]]:
        return self.openapi.list_namespaces()

    def create_namespace(self, name: str) -> dict[str, Any]:
        return self.openapi.create_namespace({"name": name})

    def compact(self, *, retention_seconds: int = 0) -> dict[str, Any]:
        """Compact global state using a connection with the global admin token."""
        return self.openapi.compact({"retention_seconds": retention_seconds})

    def enqueue_task(self, request: dict[str, Any]) -> dict[str, Any]:
        if _runtime_only_task_type(str(request.get("type") or "")) and not self._has_agent_runtime_credentials():
            raise RuntimeError("postgrip-agent: workflow tasks can only be enqueued from a managed runtime; submit workflow.runtime to an agent pool")
        return self.openapi.enqueue_task(request)

    def list_tasks(self, **options: Any) -> list[dict[str, Any]]:
        return self.openapi.list_tasks(**_query_options(options))

    def get_task(self, task_id: str) -> dict[str, Any]:
        return self.openapi.get_task(task_id)

    def get_task_events(self, task_id: str) -> list[dict[str, Any]]:
        return self.openapi.list_task_events(task_id)

    def complete_task(self, task_id: str, agent_id: str | None = None, result: dict[str, Any] | object = _MISSING, *, worker_id: str | None = None) -> dict[str, Any]:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        if result is _MISSING:
            raise TypeError("result is required")
        self.ensure_agent_session(agent_id=resolved_agent_id)
        return self.openapi.complete_agent_task(task_id, {"result": result}, agent_id=resolved_agent_id)

    def block_task(self, task_id: str, agent_id: str | None = None, reason: str | None = None, *, worker_id: str | None = None) -> dict[str, Any]:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        self.ensure_agent_session(agent_id=resolved_agent_id)
        return self.openapi.block_agent_task(task_id, {"reason": reason}, agent_id=resolved_agent_id)

    def fail_task(self, task_id: str, agent_id: str | None = None, error: str | object = _MISSING, result: dict[str, Any] | None = None, *, worker_id: str | None = None) -> dict[str, Any]:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        if error is _MISSING:
            raise TypeError("error is required")
        self.ensure_agent_session(agent_id=resolved_agent_id)
        return self.openapi.fail_agent_task(task_id, {"error": error, "result": result}, agent_id=resolved_agent_id)

    def heartbeat_task(self, task_id: str, agent_id: str | None = None, event: dict[str, Any] | None = None, *, worker_id: str | None = None) -> dict[str, Any]:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        self.ensure_agent_session(agent_id=resolved_agent_id)
        return self.openapi.heartbeat_agent_task(task_id, {"event": event}, agent_id=resolved_agent_id)

    def append_task_event(self, task_id: str, agent_id: str | None = None, event: dict[str, Any] | object = _MISSING, *, worker_id: str | None = None) -> dict[str, Any]:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        if event is _MISSING:
            raise TypeError("event is required")
        self.ensure_agent_session(agent_id=resolved_agent_id)
        return self.openapi.append_agent_task_event(task_id, {"event": event}, agent_id=resolved_agent_id)

    def poll_task(self, *, namespace: str, queue: str, agent_id: str | None = None, worker_id: str | None = None, wait_seconds: int = 20, task_types: list[str] | tuple[str, ...] | None = None) -> dict[str, Any] | None:
        resolved_agent_id = agent_id or worker_id
        if not resolved_agent_id:
            raise TypeError("agent_id is required")
        self.ensure_agent_session(namespace=namespace, queue=queue, agent_id=resolved_agent_id)
        response = self.openapi.poll_agent_task(
            namespace=namespace,
            queue=queue,
            agent_id=resolved_agent_id,
            wait_seconds=wait_seconds,
            task_types=",".join(task_types) if task_types else None,
        )
        return response.get("task")

    def get_workflow(self, workflow_id_or_run_id: str) -> dict[str, Any]:
        return self.openapi.get_workflow(workflow_id_or_run_id)

    def get_workflow_history(self, workflow_id_or_run_id: str) -> list[dict[str, Any]]:
        return self.openapi.list_workflow_history(workflow_id_or_run_id)

    def list_workflows(self, **options: Any) -> list[dict[str, Any]]:
        query_options, search = _generated_workflow_query(options)
        return self.openapi.list_workflows(search=search, **query_options)

    def count_workflows(self, **options: Any) -> int:
        query_options, search = _generated_workflow_query(options)
        return int(self.openapi.count_workflows(search=search, **query_options)["count"])

    def signal_workflow(self, workflow_id_or_run_id: str, name: str, args: list[Any] | None = None) -> dict[str, Any]:
        return self.openapi.signal_workflow(workflow_id_or_run_id, {"name": name, "args": args or []})

    def signal_with_start_workflow(self, workflow_id: str, request: dict[str, Any]) -> dict[str, Any]:
        if not self._has_agent_runtime_credentials():
            raise RuntimeError("postgrip-agent: signal-with-start can only run from a managed runtime; submit workflow.runtime to an agent pool")
        return self.openapi.signal_with_start_workflow(workflow_id, request)

    def cancel_workflow(self, workflow_id_or_run_id: str, reason: str | None = None) -> dict[str, Any]:
        return self.openapi.cancel_workflow(workflow_id_or_run_id, {"reason": reason or ""})

    def terminate_workflow(self, workflow_id_or_run_id: str, reason: str | None = None) -> dict[str, Any]:
        return self.openapi.terminate_workflow(workflow_id_or_run_id, {"reason": reason or ""})

    def create_schedule(self, request: dict[str, Any]) -> dict[str, Any]:
        return self.openapi.create_schedule(request)

    def list_schedules(self, **options: Any) -> list[dict[str, Any]]:
        return self.openapi.list_schedules(**_query_options(options))

    def get_schedule(self, schedule_id: str) -> dict[str, Any]:
        return self.openapi.get_schedule(schedule_id)

    def update_schedule(self, schedule_id: str, request: dict[str, Any]) -> dict[str, Any]:
        return self.openapi.update_schedule(schedule_id, request)

    def delete_schedule(self, schedule_id: str) -> dict[str, Any]:
        return self.openapi.delete_schedule(schedule_id)

    def pause_schedule(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.openapi.pause_schedule(schedule_id, request or {})

    def unpause_schedule(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.openapi.unpause_schedule(schedule_id, request or {})

    def trigger_schedule(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.openapi.trigger_schedule(schedule_id, request or {})

    def backfill_schedule(self, schedule_id: str, request: dict[str, Any]) -> dict[str, Any]:
        return self.openapi.backfill_schedule(schedule_id, request)


class Client:
    def __init__(self, connection: Connection | None = None):
        self.connection = connection or Connection()
        self.workflow = WorkflowClient(self.connection)
        self.task = TaskClient(self.connection)
        self.schedule = ScheduleClient(self.connection)
        # Imported here rather than at module scope: sandbox.py imports
        # Connection for typing only, and a top-level import would cycle.
        from .sandbox import SandboxClient

        self.sandbox = SandboxClient(self.connection)

    @classmethod
    async def connect(cls, address: str | None = None, **options: Any) -> "Client":
        return cls(await Connection.connect(address, **options))

    async def start_workflow(self, workflow: Callable[..., Any] | str, *args: Any, **options: Any) -> "WorkflowHandle":
        return await asyncio.to_thread(self.workflow.start, workflow, args=list(args), **_normalize_temporal_start_options(options))

    async def execute_workflow(self, workflow: Callable[..., Any] | str, *args: Any, **options: Any) -> Any:
        timeout = options.pop("timeout", None)
        handle = await self.start_workflow(workflow, *args, **options)
        return await handle.result(timeout=timeout)

    def get_workflow_handle(self, workflow_id: str, *, run_id: str | None = None, workflow_type: str = "unknown") -> "WorkflowHandle":
        return self.workflow.get_handle(workflow_id, run_id=run_id, workflow_type=workflow_type)


class WorkflowClient:
    def __init__(self, connection: Connection):
        self.connection = connection

    def start(self, workflow: Callable[..., Any] | str, *, workflow_id: str | None = None, task_queue: str = "default", namespace: str = "default", args: list[Any] | None = None, **options: Any) -> "WorkflowHandle":
        workflow_type = workflow_name(workflow)
        if not workflow_type:
            raise ValueError("workflow type is required")
        workflow_id = workflow_id or str(uuid.uuid4())
        task = self.connection.enqueue_task({
            "namespace": namespace,
            "queue": task_queue,
            "type": f"workflow:{workflow_type}",
            "payload": {
                "namespace": namespace,
                "workflowType": workflow_type,
                "workflowId": workflow_id,
                "workflowIdReusePolicy": options.get("workflow_id_reuse_policy"),
                "runTimeoutMs": _duration_ms(options.get("workflow_run_timeout") or options.get("workflow_run_timeout_ms")),
                "retry": options.get("retry"),
                "memo": _memo_with_workflow_ui(options.get("memo"), options.get("ui")),
                "searchAttributes": options.get("search_attributes"),
                "args": args or [],
            },
            "lease_timeout_seconds": options.get("lease_timeout_seconds", 0),
        })
        return WorkflowHandle(self.connection, workflow_id, workflow_type, task_id=task["id"], run_id=(task.get("payload") or {}).get("runId"))

    def execute(self, workflow: Callable[..., Any] | str, **options: Any) -> Any:
        timeout = options.pop("timeout", None)
        return self.start(workflow, **options).result_sync(timeout=timeout)

    def signal_with_start(self, workflow: Callable[..., Any] | str, *, signal: str | Any, signal_args: list[Any] | None = None, workflow_id: str | None = None, task_queue: str = "default", namespace: str = "default", args: list[Any] | None = None, **options: Any) -> "WorkflowHandle":
        workflow_type = workflow_name(workflow)
        if not workflow_type:
            raise ValueError("workflow type is required")
        workflow_id = workflow_id or str(uuid.uuid4())
        signal_name = signal if isinstance(signal, str) else signal.name
        response = self.connection.signal_with_start_workflow(workflow_id, {
            "namespace": namespace,
            "queue": task_queue,
            "workflowType": workflow_type,
            "workflowId": workflow_id,
            "workflowIdReusePolicy": options.get("workflow_id_reuse_policy"),
            "lease_timeout_seconds": options.get("lease_timeout_seconds", 0),
            "runTimeoutMs": _duration_ms(options.get("workflow_run_timeout") or options.get("workflow_run_timeout_ms")),
            "retry": options.get("retry"),
            "memo": _memo_with_workflow_ui(options.get("memo"), options.get("ui")),
            "searchAttributes": options.get("search_attributes"),
            "args": args or [],
            "signal": {"name": signal_name, "args": signal_args or []},
        })
        workflow_result = response["workflow"]
        return WorkflowHandle(
            self.connection,
            workflow_result["id"],
            workflow_result["type"],
            task_id=response["task"]["id"],
            run_id=workflow_result.get("run_id"),
        )

    def get_handle(self, workflow_id: str, *, run_id: str | None = None, workflow_type: str = "unknown") -> "WorkflowHandle":
        return WorkflowHandle(self.connection, workflow_id, workflow_type, run_id=run_id)

    def list(self, **options: Any) -> list[dict[str, Any]]:
        return self.connection.list_workflows(**_workflow_query_options(options))

    def count(self, **options: Any) -> int:
        return self.connection.count_workflows(**_workflow_query_options(options))


@dataclass
class WorkflowHandle:
    connection: Connection
    workflow_id: str
    workflow_type: str = "unknown"
    task_id: str | None = None
    run_id: str | None = None

    async def describe(self) -> dict[str, Any]:
        return await asyncio.to_thread(self.describe_sync)

    def describe_sync(self) -> dict[str, Any]:
        workflow = self.connection.get_workflow(self.run_id or self.workflow_id)
        self.task_id = workflow["task_id"]
        self.run_id = workflow.get("run_id")
        self.workflow_type = workflow["type"]
        return workflow

    async def result(self, *, timeout: float | None = None, poll_interval: float = 1) -> Any:
        return await asyncio.to_thread(self.result_sync, timeout=timeout, poll_interval=poll_interval)

    def result_sync(self, *, timeout: float | None = None, poll_interval: float = 1) -> Any:
        started = time.time()
        task_id = self.task_id or self.describe_sync()["task_id"]
        while True:
            task = self.connection.get_task(task_id)
            if task["state"] == "succeeded":
                result = task.get("result") or {}
                next_task_id = (result.get("continue_as_new") or {}).get("task_id")
                if next_task_id:
                    task_id = next_task_id
                    self.task_id = task_id
                    continue
                return result.get("value")
            if task["state"] == "failed":
                workflow = self.connection.get_workflow(self.run_id or self.workflow_id)
                if workflow["state"] == "running" and workflow["task_id"] != task_id:
                    task_id = workflow["task_id"]
                    self.task_id = task_id
                    continue
                raise TaskFailedError(task["id"], task.get("error") or "workflow failed")
            if timeout is not None and time.time() - started > timeout:
                raise TimeoutFailure(f"workflow {self.workflow_id} timed out")
            time.sleep(poll_interval)

    async def history(self) -> list[dict[str, Any]]:
        return await asyncio.to_thread(self.history_sync)

    def history_sync(self) -> list[dict[str, Any]]:
        return self.connection.get_workflow_history(self.run_id or self.workflow_id)

    async def events(self) -> list[dict[str, Any]]:
        task_id = self.task_id or self.describe_sync()["task_id"]
        return await asyncio.to_thread(self.connection.get_task_events, task_id)

    async def watch_events(self, *, poll_interval: float = 1) -> Any:
        task_id = self.task_id or self.describe_sync()["task_id"]
        async for event in _watch_task_events(self.connection, task_id, poll_interval=poll_interval):
            yield event

    async def signal(self, name: str | Any, *args: Any) -> None:
        await asyncio.to_thread(self.signal_sync, name, *args)

    def signal_sync(self, name: str | Any, *args: Any) -> None:
        signal_name = name if isinstance(name, str) else name.name
        self.connection.signal_workflow(self.run_id or self.workflow_id, signal_name, list(args))

    async def cancel(self, reason: str | None = None) -> None:
        await asyncio.to_thread(self.connection.cancel_workflow, self.run_id or self.workflow_id, reason)

    async def terminate(self, reason: str | None = None) -> None:
        await asyncio.to_thread(self.connection.terminate_workflow, self.run_id or self.workflow_id, reason)

    async def query(self, name: str | Any, *args: Any, timeout: float | None = None) -> Any:
        query_name = name if isinstance(name, str) else name.name
        workflow = await self.describe()
        task = await asyncio.to_thread(self.connection.enqueue_task, {
            "namespace": workflow["namespace"],
            "queue": workflow["queue"],
            "type": f"query:{workflow['type']}",
            "payload": {
                "workflowId": workflow["id"],
                "workflowRunId": workflow.get("run_id"),
                "workflowType": workflow["type"],
                "queryName": query_name,
                "args": list(args),
            },
        })
        return await _wait_for_task(self.connection, task["id"], timeout=timeout, failure_message="workflow query failed")

    async def execute_update(self, name: str | Any, *args: Any, timeout: float | None = None) -> Any:
        handle = await self.start_update(name, *args)
        return await handle.result(timeout=timeout)

    async def start_update(self, name: str | Any, *args: Any) -> "WorkflowUpdateHandle":
        update_name = name if isinstance(name, str) else name.name
        workflow = await self.describe()
        task = await asyncio.to_thread(self.connection.enqueue_task, {
            "namespace": workflow["namespace"],
            "queue": workflow["queue"],
            "type": f"update:{workflow['type']}",
            "payload": {
                "workflowId": workflow["id"],
                "workflowRunId": workflow.get("run_id"),
                "workflowType": workflow["type"],
                "updateName": update_name,
                "args": list(args),
            },
        })
        return WorkflowUpdateHandle(self.connection, task["id"])


@dataclass
class WorkflowUpdateHandle:
    connection: Connection
    update_id: str

    async def result(self, *, timeout: float | None = None, poll_interval: float = 0.05) -> Any:
        return await _wait_for_task(self.connection, self.update_id, timeout=timeout, poll_interval=poll_interval, failure_message="workflow update failed")

    async def events(self) -> list[dict[str, Any]]:
        return await asyncio.to_thread(self.connection.get_task_events, self.update_id)

    async def watch_events(self, *, poll_interval: float = 1) -> Any:
        async for event in _watch_task_events(self.connection, self.update_id, poll_interval=poll_interval):
            yield event


class TaskClient:
    def __init__(self, connection: Connection):
        self.connection = connection

    def enqueue(self, *, type: str, namespace: str = "default", queue: str = "default", payload: Any = None, lease_timeout_seconds: int = 0) -> dict[str, Any]:
        return self.connection.enqueue_task({
            "namespace": namespace,
            "queue": queue,
            "type": type,
            "payload": payload,
            "lease_timeout_seconds": lease_timeout_seconds,
        })

    def shell_exec(self, *, command: str, args: list[str] | None = None, env: dict[str, str] | None = None, working_dir: str | None = None, timeout_seconds: int | None = None, queue: str = "default", namespace: str = "default") -> dict[str, Any]:
        return self.enqueue(
            namespace=namespace,
            queue=queue,
            type="shell.exec",
            payload={
                "command": command,
                "args": args or [],
                "env": env or {},
                "working_dir": working_dir,
                "timeout_seconds": timeout_seconds,
            },
        )

    def container_exec(
        self,
        *,
        image: str,
        command: str | None = None,
        args: list[str] | None = None,
        env: dict[str, str] | None = None,
        working_dir: str | None = None,
        pull_policy: str | None = None,
        timeout_seconds: int | None = None,
        queue: str = "default",
        namespace: str = "default",
    ) -> dict[str, Any]:
        """Enqueue a ``container.exec`` task.

        Mirrors :meth:`shell_exec` but runs the command inside a per-task
        container that the Go agent launches via its docker CLI. Requires
        the agent to be on the docker socket proxy network (DOCKER_HOST
        set on the agent process).
        """
        # Drop None fields so the agent's payload sees absent keys instead
        # of explicit nulls — pull_policy default is server-side ("missing"),
        # and command/working_dir absence means "use the image defaults".
        payload: dict[str, Any] = {"image": image}
        if command is not None:
            payload["command"] = command
        if args is not None:
            payload["args"] = args
        if env is not None:
            payload["env"] = env
        if working_dir is not None:
            payload["working_dir"] = working_dir
        if pull_policy is not None:
            payload["pull_policy"] = pull_policy
        if timeout_seconds is not None:
            payload["timeout_seconds"] = timeout_seconds
        return self.enqueue(
            namespace=namespace,
            queue=queue,
            type="container.exec",
            payload=payload,
        )

    def workflow_runtime(
        self,
        *,
        command: str | None = None,
        image: str | None = None,
        args: list[str] | None = None,
        env: dict[str, str] | None = None,
        working_dir: str | None = None,
        pull_policy: str | None = None,
        timeout_seconds: int | None = None,
        isolation: IsolationTier | None = None,
        queue: str = "default",
        namespace: str = "default",
        runtime_queue: str | None = None,
        runtime_namespace: str | None = None,
        runtime_id: str | None = None,
        lease_timeout_seconds: int = 0,
    ) -> dict[str, Any]:
        """Enqueue a managed SDK runtime on an existing host-agent pool.

        ``isolation`` is the isolation floor the workload requires, one of
        ``"container"`` (the default) or ``"microvm"``. It is a floor, not an
        exact match: container work may be scheduled onto a stronger tier,
        microvm work is never downgraded. It requires ``image`` — the
        orchestrator rejects an isolation floor on a command-only runtime,
        which would execute directly on the agent host honoring no floor.
        """
        payload: dict[str, Any] = {}
        if image is not None:
            payload["image"] = image
        if command is not None:
            payload["command"] = command
        if args is not None:
            payload["args"] = args
        if env is not None:
            payload["env"] = env
        if working_dir is not None:
            payload["working_dir"] = working_dir
        if pull_policy is not None:
            payload["pull_policy"] = pull_policy
        if timeout_seconds is not None:
            payload["timeout_seconds"] = timeout_seconds
        if isolation is not None:
            payload["isolation"] = isolation
        payload["queue"] = runtime_queue or f"postgrip-runtime-{uuid.uuid4().hex[:16]}"
        if runtime_namespace is not None:
            payload["namespace"] = runtime_namespace
        if runtime_id is not None:
            payload["runtime_id"] = runtime_id
        return self.enqueue(
            namespace=namespace,
            queue=queue,
            type="workflow.runtime",
            payload=payload,
            lease_timeout_seconds=lease_timeout_seconds,
        )

    def noop(self, *, queue: str = "default", namespace: str = "default") -> dict[str, Any]:
        return self.enqueue(namespace=namespace, queue=queue, type="noop")

    def events(self, task_id: str) -> list[dict[str, Any]]:
        return self.connection.get_task_events(task_id)

    async def watch_events(self, task_id: str, *, poll_interval: float = 1) -> Any:
        async for event in _watch_task_events(self.connection, task_id, poll_interval=poll_interval):
            yield event


class ScheduleClient:
    def __init__(self, connection: Connection):
        self.connection = connection

    def create(self, request: dict[str, Any]) -> dict[str, Any]:
        return self.connection.create_schedule(request)

    def create_workflow_schedule(self, *, workflow: Callable[..., Any] | str, schedule_id: str | None = None, namespace: str = "default", task_queue: str = "default", args: list[Any] | None = None, interval_seconds: int | None = None, cron: str | None = None, calendar: dict[str, Any] | None = None, timezone: str | None = None, jitter_seconds: int | None = None, catch_up_window_seconds: int | None = None, missed_run_policy: str | None = None, start_at: datetime | str | None = None, workflow_id: str | None = None, workflow_id_reuse_policy: str | None = None, overlap_policy: str | None = None, workflow_run_timeout_ms: int | None = None, retry: dict[str, Any] | None = None, memo: dict[str, Any] | None = None, search_attributes: dict[str, Any] | None = None, ui: dict[str, Any] | None = None) -> dict[str, Any]:
        workflow_type = workflow_name(workflow)
        if isinstance(start_at, datetime):
            start_at = start_at.isoformat()
        return self.create({
            "id": schedule_id,
            "namespace": namespace,
            "overlap_policy": overlap_policy,
            "spec": {
                "interval_seconds": interval_seconds,
                "cron": cron,
                "calendar": calendar,
                "timezone": timezone,
                "jitter_seconds": jitter_seconds,
                "catch_up_window_seconds": catch_up_window_seconds,
                "missed_run_policy": missed_run_policy,
                "start_at": start_at,
            },
            "action": {
                "namespace": namespace,
                "queue": task_queue,
                "workflowType": workflow_type,
                "workflowId": workflow_id,
                "workflowIdReusePolicy": workflow_id_reuse_policy,
                "runTimeoutMs": workflow_run_timeout_ms,
                "retry": retry,
                "memo": _memo_with_workflow_ui(memo, ui),
                "searchAttributes": search_attributes,
                "args": args or [],
            },
        })

    def list(self, **options: Any) -> list[dict[str, Any]]:
        return self.connection.list_schedules(**options)

    def get(self, schedule_id: str) -> dict[str, Any]:
        return self.connection.get_schedule(schedule_id)

    def update(self, schedule_id: str, request: dict[str, Any]) -> dict[str, Any]:
        return self.connection.update_schedule(schedule_id, request)

    def delete(self, schedule_id: str) -> dict[str, Any]:
        return self.connection.delete_schedule(schedule_id)

    def pause(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.connection.pause_schedule(schedule_id, request)

    def unpause(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.connection.unpause_schedule(schedule_id, request)

    def trigger(self, schedule_id: str, request: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.connection.trigger_schedule(schedule_id, request)

    def backfill(self, schedule_id: str, request: dict[str, Any]) -> dict[str, Any]:
        return self.connection.backfill_schedule(schedule_id, request)


async def _wait_for_task(connection: Connection, task_id: str, *, timeout: float | None, poll_interval: float = 0.05, failure_message: str) -> Any:
    started = time.time()
    while True:
        task = await asyncio.to_thread(connection.get_task, task_id)
        if task["state"] == "succeeded":
            return (task.get("result") or {}).get("value")
        if task["state"] == "failed":
            raise TaskFailedError(task["id"], task.get("error") or failure_message)
        if timeout is not None and time.time() - started > timeout:
            raise TimeoutFailure(f"task {task_id} timed out")
        await asyncio.sleep(poll_interval)


async def _watch_task_events(connection: Connection, task_id: str, *, poll_interval: float) -> Any:
    seen: set[str] = set()
    while True:
        events = await asyncio.to_thread(connection.get_task_events, task_id)
        for event in events:
            event_id = str(event.get("id") or "")
            if event_id not in seen:
                seen.add(event_id)
                yield event
        task = await asyncio.to_thread(connection.get_task, task_id)
        if task["state"] in {"succeeded", "failed"}:
            return
        await asyncio.sleep(poll_interval)


def _runtime_only_task_type(task_type: str) -> bool:
    task_type = task_type.strip()
    return (
        task_type == "timer"
        or task_type.startswith("workflow:")
        or task_type.startswith("activity:")
        or task_type.startswith("query:")
        or task_type.startswith("update:")
    )


def _query_options(options: dict[str, Any]) -> dict[str, Any]:
    query: dict[str, Any] = {}
    for key, value in options.items():
        if value is None:
            continue
        if key == "search_attributes":
            for search_key, search_value in value.items():
                query[f"search.{search_key}"] = search_value
            continue
        query[key] = value
    return query


def _generated_workflow_query(options: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any] | None]:
    query = _query_options(options)
    search = {
        key.removeprefix("search."): value
        for key, value in list(query.items())
        if key.startswith("search.")
    }
    for key in search:
        query.pop(f"search.{key}", None)
    return query, search or None


def _workflow_query_options(options: dict[str, Any]) -> dict[str, Any]:
    mapping = {
        "workflow_id": "id",
        "run_id": "run_id",
        "workflow_type": "type",
        "task_queue": "queue",
        "order_by": "order_by",
        "page_token": "page_token",
        "search_attributes": "search_attributes",
    }
    return {mapping.get(key, key): value for key, value in options.items() if value is not None}


def _memo_with_workflow_ui(memo: dict[str, Any] | None, ui: dict[str, Any] | None) -> dict[str, Any] | None:
    if not ui:
        return memo
    ui_memo = _workflow_ui_memo(ui)
    if not ui_memo:
        return memo
    return {**(memo or {}), _POSTGRIP_UI_MEMO_KEY: ui_memo}


def _workflow_ui_memo(ui: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    display_name = _clean_string(ui.get("displayName") or ui.get("display_name"))
    if display_name:
        out["displayName"] = display_name
    description = _clean_string(ui.get("description"))
    if description:
        out["description"] = description
    details = ui.get("details")
    if isinstance(details, dict):
        clean_details = {str(key).strip(): value for key, value in details.items() if str(key).strip()}
        if clean_details:
            out["details"] = clean_details
    tags = ui.get("tags")
    if isinstance(tags, list):
        clean_tags = [_clean_string(tag) for tag in tags]
        clean_tags = [tag for tag in clean_tags if tag]
        if clean_tags:
            out["tags"] = clean_tags
    return out


def _clean_string(value: Any) -> str | None:
    if isinstance(value, str):
        trimmed = value.strip()
        return trimmed or None
    return None


def _normalize_temporal_start_options(options: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(options)
    if "id" in normalized and "workflow_id" not in normalized:
        normalized["workflow_id"] = normalized.pop("id")
    return normalized


def _duration_ms(value: Any) -> int | None:
    if value is None:
        return None
    if hasattr(value, "total_seconds"):
        return int(value.total_seconds() * 1000)
    return int(value)


def _parse_timestamp(value: Any) -> float:
    if isinstance(value, datetime):
        parsed = value
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=timezone.utc)
        return parsed.timestamp()
    if not isinstance(value, str) or not value:
        return 0.0
    timestamp = value
    if timestamp.endswith("Z"):
        timestamp = timestamp[:-1] + "+00:00"
    if "." in timestamp:
        prefix, suffix = timestamp.split(".", 1)
        offset = ""
        for marker in ("+", "-"):
            marker_index = suffix.find(marker)
            if marker_index > 0:
                offset = suffix[marker_index:]
                suffix = suffix[:marker_index]
                break
        timestamp = f"{prefix}.{suffix[:6]}{offset}"
    try:
        parsed = datetime.fromisoformat(timestamp)
    except ValueError:
        return 0.0
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.timestamp()
