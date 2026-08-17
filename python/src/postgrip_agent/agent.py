from __future__ import annotations

import asyncio
import inspect
import os
import threading
import time
import uuid
from typing import Any, Awaitable, Callable, Iterable

from . import activity, workflow
from .client import Client, Connection
from .errors import ApplicationFailure, CancelledFailure

WORKFLOW_RUNTIME_TASK_TYPES = ["workflow:", "activity:", "query:", "update:"]


class Agent:
    def __init__(
        self,
        client: Client | Connection | None = None,
        *,
        connection: Connection | None = None,
        task_queue: str | None = None,
        workflows: dict[str, Callable[..., Any] | type] | Iterable[Callable[..., Any] | type],
        activities: dict[str, Callable[..., Any]] | Iterable[Callable[..., Any]] | None = None,
        namespace: str = "default",
        identity: str | None = None,
        name: str | None = None,
        host: str | None = None,
        poll_interval: float = 1,
        max_concurrent_tasks: int = 4,
    ):
        self.connection = _connection_from_client(client, connection)
        self.task_queue = task_queue or os.environ.get("POSTGRIP_AGENT_TASK_QUEUE")
        if not self.task_queue:
            raise TypeError("task_queue is required")
        self.workflows = _workflow_registry(workflows)
        self.activities = _activity_registry(activities or {})
        self.namespace = os.environ.get("POSTGRIP_AGENT_NAMESPACE", namespace) if namespace == "default" else namespace
        managed_runtime = os.environ.get("POSTGRIP_AGENT_MANAGED_RUNTIME") == "true"
        if not managed_runtime:
            raise RuntimeError("postgrip-agent: Agent workers must be launched by a PostGrip host agent as managed workflow runtimes; submit workflow.runtime work to an agent pool instead")
        self.identity = identity or os.environ.get("POSTGRIP_AGENT_ID") or f"py-agent-{uuid.uuid4()}"
        self.connection.configure_agent_auth(
            agent_id=self.identity,
            agent_name=name,
            agent_host=host,
            namespace=self.namespace,
            queue=self.task_queue,
            access_token=os.environ.get("POSTGRIP_AGENT_ACCESS_TOKEN"),
            refresh_token=os.environ.get("POSTGRIP_AGENT_REFRESH_TOKEN"),
            access_expires_at=os.environ.get("POSTGRIP_AGENT_ACCESS_EXPIRES_AT"),
            signing_private_key=os.environ.get("POSTGRIP_AGENT_SIGNING_PRIVATE_KEY"),
        )
        self.poll_interval = poll_interval
        self.max_concurrent_tasks = max(1, int(max_concurrent_tasks))
        self._stopping = False
        self._shutdown_timeout: float | None = None
        self._in_flight: set[asyncio.Task[None]] = set()

    @classmethod
    async def create(cls, **options: Any) -> "Agent":
        agent = cls(**options)
        await asyncio.to_thread(agent.connection.health)
        return agent

    def shutdown(self, *, timeout: float | None = None) -> None:
        self._stopping = True
        self._shutdown_timeout = timeout

    async def run(self) -> None:
        try:
            while not self._stopping:
                self._reap_finished_tasks()
                if len(self._in_flight) >= self.max_concurrent_tasks:
                    await self._wait_for_task_slot()
                    continue
                try:
                    task = await asyncio.to_thread(
                        self.connection.poll_task,
                        namespace=self.namespace,
                        queue=self.task_queue,
                        agent_id=self.identity,
                        wait_seconds=max(1, int(self.poll_interval)),
                        task_types=WORKFLOW_RUNTIME_TASK_TYPES,
                    )
                except Exception as exc:
                    if self._stopping:
                        break
                    print(f"postgrip-agent: poll failed: {exc}", flush=True)
                    await asyncio.sleep(self.poll_interval)
                    continue
                if not task:
                    await asyncio.sleep(self.poll_interval)
                    continue
                self._start_task(task)
        finally:
            self._stopping = True
            await self._drain_in_flight(self._shutdown_timeout)

    def run_sync(self) -> None:
        asyncio.run(self.run())

    async def run_until(self, awaitable: Awaitable[Any]) -> Any:
        agent_task = asyncio.create_task(self.run())
        try:
            return await awaitable
        finally:
            self.shutdown()
            await agent_task

    async def _execute(self, task: dict[str, Any]) -> None:
        task_type = task["type"]
        if task_type.startswith("workflow:") or task_type.startswith("query:") or task_type.startswith("update:"):
            await self._with_lease_renewal(task, lambda: self._execute_workflow_family_task(task))
            return
        if task_type.startswith("activity:"):
            await self._with_lease_renewal(task, lambda: self._execute_activity_task(task))
            return
        await self._fail_task(task, f"task type {task_type} is not supported by the Python agent", ApplicationFailure.non_retryable_failure(f"task type {task_type} is not supported by the Python agent", "UnsupportedTaskType", task_type))

    async def _execute_workflow_family_task(self, task: dict[str, Any]) -> None:
        if task["type"].startswith("query:"):
            await self._execute_query_task(task)
            return
        if task["type"].startswith("update:"):
            await self._execute_update_task(task)
            return
        await self._execute_workflow_task(task)

    async def _execute_workflow_task(self, task: dict[str, Any]) -> None:
        started_at = _now()
        try:
            payload = task.get("payload") or {}
            workflow_type = payload.get("workflowType") or task["type"].removeprefix("workflow:")
            target = self.workflows.get(workflow_type)
            if target is None:
                raise ApplicationFailure.non_retryable_failure(f"workflow {workflow_type} is not registered", "WorkflowNotRegistered", workflow_type)
            workflow_id = payload.get("workflowId") or task["id"]
            workflow_run_id = payload.get("runId") or workflow_id
            history = await asyncio.to_thread(self.connection.get_workflow_history, workflow_run_id)
            replay = WorkflowReplay(history)
            await self._emit(task["id"], "started", "workflow", f"started workflow {workflow_type}", {"workflowType": workflow_type})
            value = await self._run_workflow(target, task, payload, replay)
            await self._emit(task["id"], "completed", "workflow", f"completed workflow {workflow_type}")
            await self._complete_task(task, {
                "value": value,
                "message": "workflow completed",
                "started_at": started_at,
                "finished_at": _now(),
            })
        except WorkflowBlocked as exc:
            await self._block_task(task, str(exc))
        except workflow.ContinueAsNewCommand as exc:
            await self._complete_workflow_as_continued(task, exc, started_at)
        except Exception as exc:
            await self._emit(task["id"], "failed", "workflow", str(exc))
            await self._fail_task(task, str(exc), exc, started_at=started_at)

    async def _execute_query_task(self, task: dict[str, Any]) -> None:
        started_at = _now()
        payload = task.get("payload") or {}
        try:
            workflow_type = payload.get("workflowType")
            query_name = payload.get("queryName")
            if not payload.get("workflowId") or not workflow_type or not query_name:
                raise ApplicationFailure.non_retryable_failure("invalid workflow query payload", "InvalidWorkflowQueryPayload")
            target = self.workflows.get(workflow_type)
            if target is None:
                raise ApplicationFailure.non_retryable_failure(f"workflow {workflow_type} is not registered", "WorkflowNotRegistered", workflow_type)
            source_workflow = await asyncio.to_thread(self.connection.get_workflow, payload.get("workflowRunId") or payload["workflowId"])
            source_task = await asyncio.to_thread(self.connection.get_task, source_workflow["task_id"])
            history = await asyncio.to_thread(self.connection.get_workflow_history, payload.get("workflowRunId") or payload["workflowId"])
            replay = WorkflowReplay(history)
            await self._emit(task["id"], "started", "query", f"started query {query_name}", {"workflowType": workflow_type})
            try:
                await self._run_workflow(target, source_task, source_task.get("payload") or {}, replay, query_mode=True)
            except WorkflowQueryReady:
                pass
            handler = replay.query_handler(query_name)
            if handler is None:
                raise ApplicationFailure.non_retryable_failure(f"query {query_name} is not registered", "QueryNotRegistered", query_name)
            value = await _maybe_await(handler(*(payload.get("args") or [])))
            await self._complete_task(task, {
                "value": value,
                "message": "query completed",
                "started_at": started_at,
                "finished_at": _now(),
            })
        except Exception as exc:
            await self._fail_task(task, str(exc), exc, started_at=started_at)

    async def _execute_update_task(self, task: dict[str, Any]) -> None:
        started_at = _now()
        payload = task.get("payload") or {}
        try:
            workflow_type = payload.get("workflowType")
            update_name = payload.get("updateName")
            if not payload.get("workflowId") or not workflow_type or not update_name:
                raise ApplicationFailure.non_retryable_failure("invalid workflow update payload", "InvalidWorkflowUpdatePayload")
            target = self.workflows.get(workflow_type)
            if target is None:
                raise ApplicationFailure.non_retryable_failure(f"workflow {workflow_type} is not registered", "WorkflowNotRegistered", workflow_type)
            source_workflow = await asyncio.to_thread(self.connection.get_workflow, payload.get("workflowRunId") or payload["workflowId"])
            source_task = await asyncio.to_thread(self.connection.get_task, source_workflow["task_id"])
            history = await asyncio.to_thread(self.connection.get_workflow_history, payload.get("workflowRunId") or payload["workflowId"])
            replay = WorkflowReplay(history)
            await self._emit(task["id"], "started", "update", f"started update {update_name}", {"workflowType": workflow_type})
            try:
                await self._run_workflow(target, source_task, source_task.get("payload") or {}, replay, query_mode=True)
            except WorkflowQueryReady:
                pass
            handler = replay.update_handler(update_name)
            if handler is None:
                raise ApplicationFailure.non_retryable_failure(f"update {update_name} is not registered", "UpdateNotRegistered", update_name)
            value = await _maybe_await(handler(*(payload.get("args") or [])))
            await self._complete_task(task, {
                "value": value,
                "message": "update completed",
                "started_at": started_at,
                "finished_at": _now(),
            })
        except Exception as exc:
            await self._fail_task(task, str(exc), exc, started_at=started_at)

    async def _execute_activity_task(self, task: dict[str, Any]) -> None:
        started_at = _now()
        payload = task.get("payload") or {}
        activity_type = payload.get("activityType") or task["type"].removeprefix("activity:")
        target = self.activities.get(activity_type)
        try:
            if target is None:
                raise ApplicationFailure.non_retryable_failure(f"activity {activity_type} is not registered", "ActivityNotRegistered", activity_type)
            await self._emit(task["id"], "started", "activity", f"started activity {activity_type}", {"activityType": activity_type})
            value = await self._run_activity(target, task, activity_type, payload.get("args") or [], timeout_seconds=task.get("lease_timeout_seconds") or None)
            await self._emit(task["id"], "completed", "activity", f"completed activity {activity_type}")
            await self._complete_task(task, {
                "value": value,
                "message": "activity completed",
                "started_at": started_at,
                "finished_at": _now(),
            })
        except Exception as exc:
            await self._emit(task["id"], "failed", "activity", str(exc))
            await self._fail_task(task, str(exc), exc, started_at=started_at)

    async def _run_workflow(self, target: Callable[..., Any] | type, task: dict[str, Any], payload: dict[str, Any], replay: "WorkflowReplay", *, query_mode: bool = False) -> Any:
        workflow.validate_workflow_sandbox(target)
        workflow_type = payload.get("workflowType") or task["type"].removeprefix("workflow:")
        if inspect.isclass(target):
            instance = target()
            run_method = workflow.workflow_run_method(target)
            workflow_fn = getattr(instance, run_method.__name__)
        else:
            workflow_fn = target

        runtime = workflow._WorkflowRuntime(
            workflow_id=payload.get("workflowId") or task["id"],
            workflow_run_id=payload.get("runId") or payload.get("workflowId") or task["id"],
            task_queue=task["queue"],
            workflow_type=workflow_type,
            execute_activity=lambda name, args, options: self._replay_activity_for_query(replay, name) if query_mode else self._execute_activity_command(task, replay, name, args, options),
            execute_child=lambda name, args, options: self._replay_child_for_query(replay, name) if query_mode else self._execute_child_command(task, replay, name, args, options),
            continue_as_new=lambda name, args, options: (_raise_query_ready("workflow continued as new") if query_mode else (_raise_continue_as_new(name, args, options))),
            sleep=lambda ms, scope: self._replay_timer_for_query(replay) if query_mode else self._execute_timer_command(task, replay, ms, scope),
            condition=lambda predicate, timeout_ms, scope: self._replay_condition_for_query(predicate) if query_mode else self._execute_condition_command(task, replay, predicate, timeout_ms, scope),
            cancellation_requested=replay.is_cancellation_requested,
            set_handler=lambda kind, name, handler: replay.set_handler(kind, name, handler),
            emit=lambda event: self._emit(task["id"], event.get("kind") or "progress", event.get("stage"), event.get("message"), event.get("details")) if not query_mode else _noop_emit(),
        )
        return await workflow.run_in_workflow_runtime(runtime, lambda: workflow_fn(*(payload.get("args") or [])))

    async def _execute_activity_command(self, workflow_task: dict[str, Any], replay: "WorkflowReplay", name: str, args: list[Any], options: dict[str, Any]) -> Any:
        self._throw_if_cancelled(replay, options.get("cancellation_scope"))
        scheduled = replay.next_activity(name)
        if scheduled and scheduled.get("task_id"):
            task = await asyncio.to_thread(self.connection.get_task, scheduled["task_id"])
            if task["state"] == "succeeded":
                return (task.get("result") or {}).get("value")
            if task["state"] == "failed":
                if replay.is_activity_canceled(scheduled):
                    raise CancelledFailure(replay.activity_cancellation_reason(scheduled))
                if replay.has_activity_retry_scheduled(scheduled):
                    return await self._execute_activity_command(workflow_task, replay, name, args, options)
                raise ApplicationFailure(str(task.get("error") or "activity failed"), type="ActivityFailure")
            raise WorkflowBlocked(f"waiting for activity {name}")
        payload = workflow_task.get("payload") or {}
        await asyncio.to_thread(self.connection.enqueue_task, {
            "type": f"activity:{name}",
            "namespace": workflow_task["namespace"],
            "queue": workflow_task["queue"],
            "payload": {
                "activityType": name,
                "workflowId": payload.get("workflowId") or workflow_task["id"],
                "workflowRunId": payload.get("runId"),
                "workflowTaskId": workflow_task["id"],
                "attempt": 1,
                "cancellationType": options.get("cancellation_type") or options.get("cancellationType"),
                "retry": options.get("retry"),
                "args": args,
            },
            "lease_timeout_seconds": _timeout_seconds(
                options.get("start_to_close_timeout")
                or options.get("schedule_to_close_timeout")
                or options.get("start_to_close_timeout_ms")
                or options.get("schedule_to_close_timeout_ms")
            ),
        })
        raise WorkflowBlocked(f"scheduled activity {name}")

    async def _execute_timer_command(self, workflow_task: dict[str, Any], replay: "WorkflowReplay", duration_ms: int, cancellation_scope: str = "cancellable") -> None:
        self._throw_if_cancelled(replay, cancellation_scope)
        started = replay.next_timer(duration_ms)
        if started and started.get("task_id"):
            if replay.is_timer_fired(started):
                return
            raise WorkflowBlocked("waiting for timer")
        payload = workflow_task.get("payload") or {}
        await asyncio.to_thread(self.connection.enqueue_task, {
            "type": "timer",
            "namespace": workflow_task["namespace"],
            "queue": workflow_task["queue"],
            "payload": {
                "workflowId": payload.get("workflowId") or workflow_task["id"],
                "workflowRunId": payload.get("runId"),
                "workflowTaskId": workflow_task["id"],
                "timerId": str(uuid.uuid4()),
                "durationMs": duration_ms,
                "fireAt": _iso_after_ms(duration_ms),
            },
        })
        raise WorkflowBlocked(f"scheduled timer for {duration_ms}ms")

    async def _execute_child_command(self, workflow_task: dict[str, Any], replay: "WorkflowReplay", workflow_type: str, args: list[Any], options: dict[str, Any]) -> Any:
        self._throw_if_cancelled(replay, options.get("cancellation_scope"))
        started = replay.next_child(workflow_type)
        if started:
            child_workflow_id = _child_workflow_id_from_event(started)
            if not child_workflow_id:
                raise ApplicationFailure.non_retryable_failure("child workflow history is missing child workflow ID", "InvalidChildWorkflowHistory")
            child = await asyncio.to_thread(self.connection.get_workflow, _child_run_id_from_event(started) or child_workflow_id)
            if child["state"] == "succeeded":
                return (child.get("result") or {}).get("value")
            if child["state"] == "failed":
                raise ApplicationFailure(str(child.get("error") or "child workflow failed"), type="ChildWorkflowFailure")
            raise WorkflowBlocked(f"waiting for child workflow {workflow_type}")
        payload = workflow_task.get("payload") or {}
        child_workflow_id = options.get("workflow_id") or options.get("workflowId") or str(uuid.uuid4())
        await asyncio.to_thread(self.connection.enqueue_task, {
            "type": f"workflow:{workflow_type}",
            "namespace": workflow_task["namespace"],
            "queue": options.get("task_queue") or options.get("taskQueue") or workflow_task["queue"],
            "payload": {
                "namespace": workflow_task["namespace"],
                "workflowType": workflow_type,
                "workflowId": child_workflow_id,
                "parentWorkflowId": payload.get("workflowId") or workflow_task["id"],
                "parentWorkflowRunId": payload.get("runId"),
                "parentWorkflowTaskId": workflow_task["id"],
                "parentCancellationType": options.get("cancellation_type") or options.get("cancellationType"),
                "runTimeoutMs": _duration_ms_nullable(options.get("workflow_run_timeout") or options.get("workflow_run_timeout_ms")),
                "retry": options.get("retry"),
                "args": args,
            },
            "lease_timeout_seconds": options.get("lease_timeout_seconds") or options.get("leaseTimeoutSeconds"),
        })
        raise WorkflowBlocked(f"scheduled child workflow {workflow_type}")

    async def _execute_condition_command(self, workflow_task: dict[str, Any], replay: "WorkflowReplay", predicate: Callable[[], bool], timeout_ms: int | None, cancellation_scope: str) -> bool:
        self._throw_if_cancelled(replay, cancellation_scope)
        if predicate():
            return True
        if timeout_ms is None:
            raise WorkflowBlocked("waiting for condition")
        await self._execute_timer_command(workflow_task, replay, timeout_ms, cancellation_scope)
        return predicate()

    async def _replay_activity_for_query(self, replay: "WorkflowReplay", name: str) -> Any:
        if replay.is_cancellation_requested():
            raise WorkflowQueryReady("workflow cancelled")
        scheduled = replay.next_activity(name)
        if not scheduled or not scheduled.get("task_id"):
            raise WorkflowQueryReady()
        task = await asyncio.to_thread(self.connection.get_task, scheduled["task_id"])
        if task["state"] == "succeeded":
            return (task.get("result") or {}).get("value")
        if task["state"] == "failed":
            if replay.is_activity_canceled(scheduled):
                raise WorkflowQueryReady("activity cancelled")
            if replay.has_activity_retry_scheduled(scheduled):
                return await self._replay_activity_for_query(replay, name)
            raise ApplicationFailure(str(task.get("error") or "activity failed"), type="ActivityFailure")
        raise WorkflowQueryReady(f"waiting for activity {name}")

    async def _replay_timer_for_query(self, replay: "WorkflowReplay") -> None:
        if replay.is_cancellation_requested():
            raise WorkflowQueryReady("workflow cancelled")
        started = replay.next_timer()
        if not started or not started.get("task_id") or not replay.is_timer_fired(started):
            raise WorkflowQueryReady("waiting for timer")

    async def _replay_child_for_query(self, replay: "WorkflowReplay", name: str) -> Any:
        if replay.is_cancellation_requested():
            raise WorkflowQueryReady("workflow cancelled")
        started = replay.next_child(name)
        if not started:
            raise WorkflowQueryReady()
        child_workflow_id = _child_workflow_id_from_event(started)
        if not child_workflow_id:
            raise ApplicationFailure.non_retryable_failure("child workflow history is missing child workflow ID", "InvalidChildWorkflowHistory")
        child = await asyncio.to_thread(self.connection.get_workflow, _child_run_id_from_event(started) or child_workflow_id)
        if child["state"] == "succeeded":
            return (child.get("result") or {}).get("value")
        if child["state"] == "failed":
            raise ApplicationFailure(str(child.get("error") or "child workflow failed"), type="ChildWorkflowFailure")
        raise WorkflowQueryReady(f"waiting for child workflow {name}")

    async def _replay_condition_for_query(self, predicate: Callable[[], bool]) -> bool:
        if predicate():
            return True
        raise WorkflowQueryReady("waiting for condition")

    async def _run_activity(self, target: Callable[..., Any], task: dict[str, Any], activity_type: str, args: list[Any], *, timeout_seconds: float | None = None) -> Any:
        async def heartbeat(details: dict[str, Any] | None) -> None:
            await asyncio.to_thread(self.connection.heartbeat_task, task["id"], self.identity, {
                "kind": "heartbeat",
                "stage": "activity",
                "message": f"activity {activity_type} heartbeat",
                "details": details,
            })

        async def emit(event: dict[str, Any]) -> None:
            await asyncio.to_thread(self.connection.append_task_event, task["id"], self.identity, event)

        runtime = activity._ActivityRuntime(task_id=task["id"], activity_type=activity_type, heartbeat=heartbeat, emit=emit)
        coro = activity.run_in_activity_runtime(runtime, lambda: target(*args))
        if timeout_seconds and timeout_seconds > 0:
            return await asyncio.wait_for(coro, timeout=timeout_seconds)
        return await coro

    async def _complete_workflow_as_continued(self, task: dict[str, Any], command: workflow.ContinueAsNewCommand, started_at: str) -> None:
        payload = task.get("payload") or {}
        current_workflow_id = payload.get("workflowId") or task["id"]
        next_workflow_id = command.options.get("workflow_id") or command.options.get("workflowId") or f"{current_workflow_id}-continue-{uuid.uuid4()}"
        task_queue = command.options.get("task_queue") or command.options.get("taskQueue") or task["queue"]
        next_task = await asyncio.to_thread(self.connection.enqueue_task, {
            "type": f"workflow:{command.workflow_type}",
            "queue": task_queue,
            "namespace": task["namespace"],
            "payload": {
                "namespace": task["namespace"],
                "workflowType": command.workflow_type,
                "workflowId": next_workflow_id,
                "continuedFromWorkflowId": current_workflow_id,
                "runTimeoutMs": _duration_ms_nullable(command.options.get("workflow_run_timeout") or command.options.get("workflow_run_timeout_ms")),
                "retry": command.options.get("retry"),
                "args": list(command.args),
            },
            "lease_timeout_seconds": command.options.get("lease_timeout_seconds") or command.options.get("leaseTimeoutSeconds"),
        })
        await self._complete_task(task, {
            "message": "workflow continued as new",
            "continue_as_new": {
                "workflow_id": next_workflow_id,
                "workflow_type": command.workflow_type,
                "task_queue": task_queue,
                "task_id": next_task["id"],
            },
            "started_at": started_at,
            "finished_at": _now(),
        })

    async def _with_lease_renewal(self, task: dict[str, Any], fn: Callable[[], Awaitable[None]]) -> None:
        stopped = asyncio.Event()
        interval = heartbeat_interval(float(task.get("lease_timeout_seconds") or 30))

        async def renew_loop() -> None:
            while not stopped.is_set():
                try:
                    await asyncio.wait_for(stopped.wait(), timeout=interval)
                    return
                except asyncio.TimeoutError:
                    pass
                try:
                    await asyncio.to_thread(self.connection.heartbeat_task, task["id"], self.identity, {
                        "kind": "heartbeat",
                        "stage": "lease",
                        "message": f"renewed {task['type']} lease",
                    })
                except Exception as exc:
                    if is_terminal_task_mutation(exc):
                        return

        renew_task = asyncio.create_task(renew_loop())
        try:
            await fn()
        finally:
            stopped.set()
            await renew_task

    async def _emit(self, task_id: str, kind: str, stage: str | None = None, message: str | None = None, details: dict[str, Any] | None = None) -> None:
        try:
            await asyncio.to_thread(self.connection.append_task_event, task_id, self.identity, {"kind": kind, "stage": stage, "message": message, "details": details})
        except Exception:
            pass

    async def _complete_task(self, task: dict[str, Any], result: dict[str, Any]) -> None:
        try:
            await asyncio.to_thread(self.connection.complete_task, task["id"], self.identity, result)
        except Exception as exc:
            if not is_terminal_task_mutation(exc):
                raise

    async def _block_task(self, task: dict[str, Any], reason: str) -> None:
        try:
            await asyncio.to_thread(self.connection.block_task, task["id"], self.identity, reason)
        except Exception as exc:
            if not is_terminal_task_mutation(exc):
                raise

    async def _fail_task(self, task: dict[str, Any], message: str, exc: Exception, *, started_at: str | None = None) -> None:
        try:
            await asyncio.to_thread(self.connection.fail_task, task["id"], self.identity, message, {
                "message": message,
                "failure": failure_info(exc),
                "started_at": started_at or _now(),
                "finished_at": _now(),
            })
        except Exception as fail_exc:
            if not is_terminal_task_mutation(fail_exc):
                raise

    def _throw_if_cancelled(self, replay: "WorkflowReplay", cancellation_scope: str | None = "cancellable") -> None:
        if cancellation_scope == "non_cancellable" or not replay.is_cancellation_requested():
            return
        raise CancelledFailure(replay.cancellation_reason())

    def _start_task(self, task: dict[str, Any]) -> None:
        running = asyncio.create_task(self._execute(task))
        self._in_flight.add(running)
        running.add_done_callback(self._in_flight.discard)

    def _reap_finished_tasks(self) -> None:
        for running in tuple(self._in_flight):
            if not running.done():
                continue
            self._in_flight.discard(running)
            running.exception()

    async def _wait_for_task_slot(self) -> None:
        if not self._in_flight:
            return
        done, _ = await asyncio.wait(self._in_flight, return_when=asyncio.FIRST_COMPLETED)
        for running in done:
            self._in_flight.discard(running)
            running.exception()

    async def _drain_in_flight(self, timeout: float | None = None) -> None:
        if not self._in_flight:
            return
        pending = tuple(self._in_flight)
        self._in_flight.clear()
        drained = asyncio.gather(*pending, return_exceptions=True)
        if timeout is None or timeout < 0:
            await drained
            return
        try:
            await asyncio.wait_for(asyncio.shield(drained), timeout=timeout)
        except asyncio.TimeoutError:
            pass


class WorkflowBlocked(Exception):
    pass


class WorkflowQueryReady(Exception):
    pass


class WorkflowReplay:
    def __init__(self, history: list[dict[str, Any]]):
        self.history = history
        self.activities = [event for event in history if event.get("type") == "ActivityTaskScheduled"]
        self.timers = [event for event in history if event.get("type") == "TimerStarted"]
        self.signals = [event for event in history if event.get("type") == "WorkflowSignaled"]
        self.children = [event for event in history if event.get("type") == "ChildWorkflowExecutionStarted"]
        self.completed_updates = [event for event in history if event.get("type") == "WorkflowUpdateCompleted"]
        self.cancellation = next((event for event in history if event.get("type") == "WorkflowCancellationRequested"), None)
        self.activity_index = 0
        self.timer_index = 0
        self.child_index = 0
        self.query_handlers: dict[str, Callable[..., Any]] = {}
        self.update_handlers: dict[str, Callable[..., Any]] = {}

    def next_activity(self, activity_type: str) -> dict[str, Any] | None:
        event = self.activities[self.activity_index] if self.activity_index < len(self.activities) else None
        self.activity_index += 1
        if event and (event.get("attributes") or {}).get("activity_type") != activity_type:
            raise _determinism_violation(f"activity command changed at index {self.activity_index}: history={(event.get('attributes') or {}).get('activity_type')} replay={activity_type}")
        return event

    def next_timer(self, duration_ms: int | None = None) -> dict[str, Any] | None:
        event = self.timers[self.timer_index] if self.timer_index < len(self.timers) else None
        self.timer_index += 1
        history_duration = (event.get("attributes") or {}).get("duration_ms") if event else None
        if event and duration_ms is not None and history_duration is not None and round(duration_ms) != history_duration:
            raise _determinism_violation(f"timer command changed at index {self.timer_index}: history={history_duration} replay={round(duration_ms)}")
        return event

    def next_child(self, workflow_type: str) -> dict[str, Any] | None:
        event = self.children[self.child_index] if self.child_index < len(self.children) else None
        self.child_index += 1
        if event and (event.get("attributes") or {}).get("workflow_type") != workflow_type:
            raise _determinism_violation(f"child workflow command changed at index {self.child_index}: history={(event.get('attributes') or {}).get('workflow_type')} replay={workflow_type}")
        return event

    def is_timer_fired(self, timer_started: dict[str, Any]) -> bool:
        return any(event.get("type") == "TimerFired" and event.get("task_id") == timer_started.get("task_id") for event in self.history)

    def is_activity_canceled(self, activity_scheduled: dict[str, Any]) -> bool:
        return any(event.get("type") == "ActivityTaskCanceled" and event.get("task_id") == activity_scheduled.get("task_id") for event in self.history)

    def has_activity_retry_scheduled(self, activity_scheduled: dict[str, Any]) -> bool:
        return any(event.get("type") == "ActivityTaskRetryScheduled" and (event.get("attributes") or {}).get("previous_task") == activity_scheduled.get("task_id") for event in self.history)

    def activity_cancellation_reason(self, activity_scheduled: dict[str, Any]) -> str:
        event = next((candidate for candidate in self.history if candidate.get("type") == "ActivityTaskCanceled" and candidate.get("task_id") == activity_scheduled.get("task_id")), None)
        reason = (event.get("attributes") or {}).get("reason") if event else None
        return reason if isinstance(reason, str) and reason else "activity cancellation requested"

    def set_handler(self, kind: str, name: str, handler: Callable[..., Any]) -> None:
        if kind == "signal":
            for event in self.signals:
                if (event.get("attributes") or {}).get("signal_name") == name:
                    handler(*((event.get("attributes") or {}).get("args") or []))
            return
        if kind == "update":
            self.update_handlers[name] = handler
            for event in self.completed_updates:
                if (event.get("attributes") or {}).get("update_name") == name:
                    handler(*((event.get("attributes") or {}).get("args") or []))
            return
        self.query_handlers[name] = handler

    def query_handler(self, name: str) -> Callable[..., Any] | None:
        return self.query_handlers.get(name)

    def update_handler(self, name: str) -> Callable[..., Any] | None:
        return self.update_handlers.get(name)

    def is_cancellation_requested(self) -> bool:
        return self.cancellation is not None

    def cancellation_reason(self) -> str:
        reason = (self.cancellation.get("attributes") or {}).get("reason") if self.cancellation else None
        return reason if isinstance(reason, str) and reason else "workflow cancellation requested"


def failure_info(exc: Exception) -> dict[str, Any]:
    if isinstance(exc, ApplicationFailure):
        return {
            "message": str(exc),
            "type": exc.type,
            "non_retryable": exc.non_retryable,
            "details": exc.details,
        }
    if isinstance(exc, CancelledFailure):
        return {"message": str(exc), "type": "CancelledFailure", "non_retryable": True}
    return {"message": str(exc), "type": exc.__class__.__name__}


def heartbeat_interval(lease_timeout_seconds: float) -> float:
    if lease_timeout_seconds <= 1:
        return 0.5
    return max(0.5, lease_timeout_seconds / 3)


def is_terminal_task_mutation(exc: Exception) -> bool:
    message = str(exc)
    return "terminal" in message or "not leased" in message


def _connection_from_client(client: Client | Connection | None, connection: Connection | None) -> Connection:
    if connection is not None:
        return connection
    if isinstance(client, Client):
        return client.connection
    if isinstance(client, Connection):
        return client
    raise ValueError("Agent requires a Client or Connection")




def _workflow_registry(entries: dict[str, Callable[..., Any] | type] | Iterable[Callable[..., Any] | type]) -> dict[str, Callable[..., Any] | type]:
    if isinstance(entries, dict):
        return dict(entries)
    return {workflow.workflow_name(entry): entry for entry in entries}


def _activity_registry(entries: dict[str, Callable[..., Any]] | Iterable[Callable[..., Any]]) -> dict[str, Callable[..., Any]]:
    if isinstance(entries, dict):
        return dict(entries)
    return {activity.activity_name(entry): entry for entry in entries}


def _now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _iso_after_ms(duration_ms: int) -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + (duration_ms / 1000)))


def _timeout_seconds(value: Any) -> int | None:
    duration = _duration_ms_nullable(value)
    if duration is None:
        return None
    return max(1, round(duration / 1000))


def _duration_ms_nullable(value: Any) -> int | None:
    if value is None:
        return None
    if hasattr(value, "total_seconds"):
        return int(value.total_seconds() * 1000)
    return int(value)


def _child_workflow_id_from_event(event: dict[str, Any]) -> str | None:
    value = (event.get("attributes") or {}).get("child_workflow_id")
    return value if isinstance(value, str) else None


def _child_run_id_from_event(event: dict[str, Any]) -> str | None:
    value = (event.get("attributes") or {}).get("child_run_id")
    return value if isinstance(value, str) else None


def _determinism_violation(message: str) -> ApplicationFailure:
    return ApplicationFailure.non_retryable_failure(message, "DeterminismViolation")


def _raise_continue_as_new(workflow_type: str, args: list[Any], options: dict[str, Any]) -> None:
    raise workflow.ContinueAsNewCommand(workflow_type, args, options)


def _raise_query_ready(message: str = "query handlers ready") -> None:
    raise WorkflowQueryReady(message)


async def _maybe_await(value: Any) -> Any:
    if inspect.isawaitable(value):
        return await value
    return value


async def _noop_emit() -> None:
    return None
