from __future__ import annotations

import asyncio
import io
import os
import time
import unittest
from datetime import timedelta
from unittest.mock import patch
from urllib.error import HTTPError

from postgrip_agent import Client, Connection, Task, Agent, Worker, WorkflowExecution, activity, types, workflow


class FakeConnection(Connection):
    def __init__(self):
        super().__init__("http://agent.test")
        self.enqueued: list[dict] = []
        self.completed: list[tuple[str, dict]] = []
        self.failed: list[tuple[str, str, dict | None]] = []
        self.blocked: list[tuple[str, str | None]] = []
        self.heartbeats: list[tuple[str, str, dict | None]] = []
        self.events: list[tuple[str, str, dict]] = []
        self.tasks: dict[str, dict] = {}
        self.workflows: dict[str, dict] = {}
        self.histories: dict[str, list[dict]] = {}

    def enqueue_task(self, request: dict) -> dict:
        task_id = f"task-{len(self.enqueued) + 1}"
        task = {
            "id": task_id,
            "namespace": request.get("namespace", "default"),
            "queue": request.get("queue", "default"),
            "type": request["type"],
            "payload": request.get("payload") or {},
            "state": "queued",
            "lease_timeout_seconds": request.get("lease_timeout_seconds") or 30,
        }
        if task["type"].startswith("workflow:"):
            task["payload"].setdefault("runId", f"run-{len(self.workflows) + 1}")
        self.tasks[task_id] = task
        self.enqueued.append(request)
        return task

    def get_task(self, task_id: str) -> dict:
        return self.tasks[task_id]

    def get_workflow(self, workflow_id_or_run_id: str) -> dict:
        return self.workflows[workflow_id_or_run_id]

    def get_workflow_history(self, workflow_id_or_run_id: str) -> list[dict]:
        return self.histories.get(workflow_id_or_run_id, [])

    def complete_task(self, task_id: str, agent_id: str, result: dict) -> dict:
        self.completed.append((task_id, result))
        task = self.tasks.get(task_id, {"id": task_id})
        task["state"] = "succeeded"
        task["result"] = result
        return task

    def block_task(self, task_id: str, agent_id: str, reason: str | None = None) -> dict:
        self.blocked.append((task_id, reason))
        task = self.tasks.get(task_id, {"id": task_id})
        task["state"] = "blocked"
        return task

    def fail_task(self, task_id: str, agent_id: str, error: str, result: dict | None = None) -> dict:
        self.failed.append((task_id, error, result))
        task = self.tasks.get(task_id, {"id": task_id})
        task["state"] = "failed"
        task["error"] = error
        return task

    def heartbeat_task(self, task_id: str, agent_id: str, event: dict | None = None) -> dict:
        self.heartbeats.append((task_id, agent_id, event))
        return {"id": task_id, "state": "leased"}

    def append_task_event(self, task_id: str, agent_id: str, event: dict) -> dict:
        self.events.append((task_id, agent_id, event))
        return {"id": f"event-{len(self.events)}", "task_id": task_id, **event}

    def get_task_events(self, task_id: str) -> list[dict]:
        return [
            {"id": f"event-{index + 1}", "task_id": event_task_id, **event}
            for index, (event_task_id, _agent_id, event) in enumerate(self.events)
            if event_task_id == task_id
        ]


class PythonSdkTests(unittest.TestCase):
    def setUp(self) -> None:
        self._previous_managed_runtime = os.environ.get("POSTGRIP_AGENT_MANAGED_RUNTIME")
        os.environ["POSTGRIP_AGENT_MANAGED_RUNTIME"] = "true"

    def tearDown(self) -> None:
        if self._previous_managed_runtime is None:
            os.environ.pop("POSTGRIP_AGENT_MANAGED_RUNTIME", None)
        else:
            os.environ["POSTGRIP_AGENT_MANAGED_RUNTIME"] = self._previous_managed_runtime

    def test_deprecated_worker_import_is_agent_alias(self):
        self.assertIs(Worker, Agent)

    def test_connection_accepts_agent_id_keyword(self):
        connection = Connection("http://agent.test", agent_id="agent-1")

        self.assertEqual(connection._agent_id, "agent-1")

    def test_configure_agent_auth_accepts_agent_id_keyword(self):
        connection = Connection("http://agent.test")

        connection.configure_agent_auth(agent_id="agent-1", namespace="tenant-a", queue="priority")

        self.assertEqual(connection._agent_id, "agent-1")
        self.assertEqual(connection._agent_namespace, "tenant-a")
        self.assertEqual(connection._agent_queue, "priority")

    def test_ensure_agent_session_uses_agent_id(self):
        connection = Connection("http://agent.test", agent_refresh_token="refresh-token")
        requests: list[tuple[str, str, dict | None, bool]] = []

        def fake_request(method: str, path: str, body: dict | None = None, *, agent_auth: bool = False, signing: str = "") -> dict:
            requests.append((method, path, body, agent_auth))
            if path == "/api/v1/agent/session/refresh":
                return {
                    "agentId": "agent-1",
                    "accessToken": "access-token",
                    "refreshToken": "refresh-token",
                    "accessExpiresAt": "2999-01-01T00:00:00Z",
                }
            raise AssertionError(f"unexpected request path {path}")

        connection._request = fake_request  # type: ignore[method-assign]

        self.assertTrue(connection.ensure_agent_session(agent_id="agent-1"))
        self.assertEqual(connection._agent_id, "agent-1")
        self.assertEqual(requests[0][1], "/api/v1/agent/session/refresh")
        self.assertEqual(requests[0][2]["refreshToken"], "refresh-token")

    def test_worker_id_keyword_maps_to_canonical_agent_payload(self):
        connection = Connection("http://agent.test", agent_refresh_token="refresh-token")
        requests: list[tuple[str, str, dict | None, bool]] = []

        def fake_request(method: str, path: str, body: dict | None = None, *, agent_auth: bool = False, signing: str = "") -> dict:
            requests.append((method, path, body, agent_auth))
            if path == "/api/v1/agent/session/refresh":
                return {
                    "agentId": "agent-compat",
                    "accessToken": "access-token",
                    "refreshToken": "refresh-token",
                    "accessExpiresAt": "2999-01-01T00:00:00Z",
                }
            if path.startswith("/api/v1/agent/poll?"):
                return {"task": None}
            raise AssertionError(f"unexpected request path {path}")

        connection._request = fake_request  # type: ignore[method-assign]

        self.assertTrue(connection.ensure_agent_session(worker_id="agent-compat"))
        self.assertEqual(connection._agent_id, "agent-compat")
        self.assertEqual(requests[0][2]["refreshToken"], "refresh-token")
        self.assertNotIn("workerId", requests[0][2])
        self.assertIsNone(connection.poll_task(namespace="default", queue="default", worker_id="agent-compat"))
        poll_path = next(path for _method, path, _body, _agent_auth in requests if path.startswith("/api/v1/agent/poll?"))
        self.assertIn("agent_id=agent-compat", poll_path)

    def test_task_helpers_accept_agent_id_keyword(self):
        connection = Connection(
            "http://agent.test",
            agent_access_token="access-token",
            agent_refresh_token="refresh-token",
            agent_access_expires_at="2999-01-01T00:00:00Z",
        )
        requests: list[tuple[str, str, dict | None, bool]] = []

        def fake_request(method: str, path: str, body: dict | None = None, *, agent_auth: bool = False, signing: str = "") -> dict:
            requests.append((method, path, body, agent_auth))
            if path.startswith("/api/v1/agent/tasks/task-1/"):
                return {"id": "task-1"}
            if path.startswith("/api/v1/agent/poll?"):
                return {"task": {"id": "task-1"}}
            raise AssertionError(f"unexpected request path {path}")

        connection._request = fake_request  # type: ignore[method-assign]

        self.assertEqual(connection.complete_task("task-1", agent_id="agent-1", result={"ok": True})["id"], "task-1")
        self.assertEqual(connection.block_task("task-1", agent_id="agent-1", reason="wait")["id"], "task-1")
        self.assertEqual(connection.fail_task("task-1", agent_id="agent-1", error="boom")["id"], "task-1")
        self.assertEqual(connection.heartbeat_task("task-1", agent_id="agent-1", event={"kind": "stdout"})["id"], "task-1")
        self.assertEqual(connection.append_task_event("task-1", agent_id="agent-1", event={"kind": "stdout"})["id"], "task-1")
        self.assertEqual(connection.poll_task(namespace="default", queue="default", agent_id="agent-1")["id"], "task-1")

        task_paths = [path for _method, path, _body, _agent_auth in requests if path.startswith("/api/v1/agent/tasks/task-1/")]
        self.assertIn("/api/v1/agent/tasks/task-1/complete?agent_id=agent-1", task_paths)
        self.assertIn("/api/v1/agent/tasks/task-1/block?agent_id=agent-1", task_paths)
        self.assertIn("/api/v1/agent/tasks/task-1/fail?agent_id=agent-1", task_paths)
        self.assertIn("/api/v1/agent/tasks/task-1/heartbeat?agent_id=agent-1", task_paths)
        self.assertIn("/api/v1/agent/tasks/task-1/events?agent_id=agent-1", task_paths)
        poll_path = next(path for _method, path, _body, _agent_auth in requests if path.startswith("/api/v1/agent/poll?"))
        self.assertIn("agent_id=agent-1", poll_path)

    def test_gone_poll_response_preserves_shutdown_directive(self):
        connection = Connection(
            "http://agent.test/orchestrator",
            agent_id="agent-1",
            agent_access_token="access-token",
            agent_refresh_token="refresh-token",
            agent_access_expires_at="2999-01-01T00:00:00Z",
        )
        error = HTTPError(
            "http://agent.test/orchestrator/api/v1/agent/poll",
            410,
            "Gone",
            {},
            io.BytesIO(b'{"directive":{"type":"shutdown","subject":"agent"}}'),
        )
        with patch("postgrip_agent.client.urlopen", side_effect=error):
            response = connection.poll_task_response(
                namespace="default",
                queue="default",
                agent_id="agent-1",
            )
        self.assertEqual(response["directive"], {"type": "shutdown", "subject": "agent"})

    def test_task_helpers_preserve_required_payload_arguments(self):
        connection = Connection("http://agent.test")

        with self.assertRaises(TypeError):
            connection.complete_task("task-1", agent_id="agent-1")

        with self.assertRaises(TypeError):
            connection.fail_task("task-1", agent_id="agent-1")

        with self.assertRaises(TypeError):
            connection.append_task_event("task-1", agent_id="agent-1")

    def test_workflow_runtime_defaults_to_isolated_child_queue(self):
        connection = FakeConnection()
        client = Client(connection)

        task = client.task.workflow_runtime(command="sh", args=["-lc", "echo runtime"], queue="default")

        self.assertEqual(task["id"], "task-1")
        self.assertEqual(connection.enqueued[0]["type"], "workflow.runtime")
        runtime_queue = connection.enqueued[0]["payload"]["queue"]
        self.assertTrue(runtime_queue.startswith("postgrip-runtime-"))
        self.assertNotEqual(runtime_queue, "default")

    def test_workflow_activity_command_schedules_and_blocks(self):
        @activity.defn
        async def greet(name: str) -> str:
            return f"Hello {name}"

        @workflow.defn(name="SayHelloWorkflow")
        class SayHelloWorkflow:
            @workflow.run
            async def run(self, name: str) -> str:
                return await workflow.execute_activity(greet, name, start_to_close_timeout=timedelta(seconds=10))

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows=[SayHelloWorkflow], activities=[greet])
        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:SayHelloWorkflow",
            "payload": {
                "workflowType": "SayHelloWorkflow",
                "workflowId": "workflow-1",
                "runId": "run-1",
                "args": ["PostGrip"],
            },
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.enqueued[0]["type"], "activity:greet")
        self.assertEqual(connection.enqueued[0]["payload"]["args"], ["PostGrip"])
        self.assertEqual(connection.enqueued[0]["lease_timeout_seconds"], 10)
        self.assertEqual(connection.blocked[0][0], "workflow-task-1")

    def test_workflow_replays_completed_activity_result(self):
        @activity.defn
        async def greet(name: str) -> str:
            return f"Hello {name}"

        @workflow.defn(name="SayHelloWorkflow")
        class SayHelloWorkflow:
            @workflow.run
            async def run(self, name: str) -> str:
                return await workflow.execute_activity(greet, name)

        connection = FakeConnection()
        connection.histories["run-1"] = [{
            "type": "ActivityTaskScheduled",
            "task_id": "activity-task-1",
            "attributes": {"activity_type": "greet"},
        }]
        connection.tasks["activity-task-1"] = {
            "id": "activity-task-1",
            "state": "succeeded",
            "result": {"value": "Hello PostGrip"},
        }
        agent = Agent(connection=connection, task_queue="default", workflows=[SayHelloWorkflow], activities=[greet])
        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:SayHelloWorkflow",
            "payload": {
                "workflowType": "SayHelloWorkflow",
                "workflowId": "workflow-1",
                "runId": "run-1",
                "args": ["PostGrip"],
            },
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.completed[0][0], "workflow-task-1")
        self.assertEqual(connection.completed[0][1]["value"], "Hello PostGrip")

    def test_workflow_sleep_schedules_durable_timer_and_replays_fired_timer(self):
        @workflow.defn(name="TimerWorkflow")
        class TimerWorkflow:
            @workflow.run
            async def run(self) -> str:
                await workflow.sleep(timedelta(seconds=2))
                return "awake"

        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:TimerWorkflow",
            "payload": {"workflowType": "TimerWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
            "lease_timeout_seconds": 30,
        }

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows=[TimerWorkflow], activities={})
        asyncio.run(agent._execute(task))

        self.assertEqual(connection.enqueued[0]["type"], "timer")
        self.assertEqual(connection.enqueued[0]["payload"]["durationMs"], 2000)
        self.assertEqual(connection.blocked[0][0], "workflow-task-1")

        connection = FakeConnection()
        connection.histories["run-1"] = [
            {"type": "TimerStarted", "task_id": "timer-task-1", "attributes": {"duration_ms": 2000}},
            {"type": "TimerFired", "task_id": "timer-task-1", "attributes": {}},
        ]
        agent = Agent(connection=connection, task_queue="default", workflows=[TimerWorkflow], activities={})
        asyncio.run(agent._execute(task))

        self.assertEqual(connection.completed[0][1]["value"], "awake")

    def test_child_workflow_schedules_and_replays_by_child_run_id(self):
        @workflow.defn(name="ChildWorkflow")
        class ChildWorkflow:
            @workflow.run
            async def run(self, name: str) -> str:
                return f"child {name}"

        @workflow.defn(name="ParentWorkflow")
        class ParentWorkflow:
            @workflow.run
            async def run(self) -> str:
                return await workflow.execute_child(
                    ChildWorkflow,
                    "PostGrip",
                    workflow_id="child-workflow-id",
                    task_queue="children",
                )

        task = {
            "id": "parent-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:ParentWorkflow",
            "payload": {"workflowType": "ParentWorkflow", "workflowId": "parent-workflow", "runId": "parent-run", "args": []},
            "lease_timeout_seconds": 30,
        }

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows=[ParentWorkflow, ChildWorkflow], activities={})
        asyncio.run(agent._execute(task))

        self.assertEqual(connection.enqueued[0]["type"], "workflow:ChildWorkflow")
        self.assertEqual(connection.enqueued[0]["queue"], "children")
        self.assertEqual(connection.enqueued[0]["payload"]["workflowId"], "child-workflow-id")
        self.assertEqual(connection.blocked[0][1], "scheduled child workflow ChildWorkflow")

        connection = FakeConnection()
        connection.histories["parent-run"] = [{
            "type": "ChildWorkflowExecutionStarted",
            "task_id": "child-task-1",
            "attributes": {
                "workflow_type": "ChildWorkflow",
                "child_workflow_id": "child-workflow-id",
                "child_run_id": "child-run-2",
            },
        }]
        connection.workflows["child-run-2"] = {
            "id": "child-workflow-id",
            "run_id": "child-run-2",
            "task_id": "child-task-1",
            "state": "succeeded",
            "result": {"value": "child PostGrip"},
        }
        agent = Agent(connection=connection, task_queue="default", workflows=[ParentWorkflow, ChildWorkflow], activities={})
        asyncio.run(agent._execute(task))

        self.assertEqual(connection.completed[0][1]["value"], "child PostGrip")

    def test_continue_as_new_enqueues_next_run_and_completes_with_follow_pointer(self):
        @workflow.defn(name="ContinueWorkflow")
        class ContinueWorkflow:
            @workflow.run
            async def run(self) -> str:
                workflow.continue_as_new(
                    "ContinueWorkflow",
                    "next",
                    workflow_id="workflow-continued",
                    task_queue="continued",
                    lease_timeout_seconds=9,
                )
                return "unreachable"

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows=[ContinueWorkflow], activities={})
        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:ContinueWorkflow",
            "payload": {"workflowType": "ContinueWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.enqueued[0]["type"], "workflow:ContinueWorkflow")
        self.assertEqual(connection.enqueued[0]["queue"], "continued")
        self.assertEqual(connection.enqueued[0]["payload"]["workflowId"], "workflow-continued")
        self.assertEqual(connection.enqueued[0]["payload"]["args"], ["next"])
        self.assertEqual(connection.enqueued[0]["lease_timeout_seconds"], 9)
        self.assertEqual(connection.completed[0][1]["continue_as_new"]["task_id"], "task-1")

    def test_signal_history_and_update_history_are_replayed_into_handlers(self):
        approve_signal = workflow.define_signal("approve")
        approver_query = workflow.define_query("approver")
        rename_update = workflow.define_update("rename")

        @workflow.defn(name="ApprovalWorkflow")
        class ApprovalWorkflow:
            @workflow.run
            async def run(self) -> str:
                approved_by = ""

                def approve(name: str) -> str:
                    nonlocal approved_by
                    approved_by = name
                    return approved_by

                workflow.set_handler(approve_signal, approve)
                workflow.set_handler(rename_update, approve)
                workflow.set_handler(approver_query, lambda: approved_by)
                await workflow.condition(lambda: False)
                return approved_by

        connection = FakeConnection()
        connection.workflows["run-1"] = {
            "id": "workflow-1",
            "run_id": "run-1",
            "task_id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "ApprovalWorkflow",
            "state": "running",
        }
        connection.tasks["workflow-task-1"] = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:ApprovalWorkflow",
            "payload": {"workflowType": "ApprovalWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
        }
        connection.histories["run-1"] = [
            {"type": "WorkflowSignaled", "attributes": {"signal_name": "approve", "args": ["Ada"]}},
            {"type": "WorkflowUpdateCompleted", "attributes": {"update_name": "rename", "args": ["Grace"]}},
        ]
        agent = Agent(connection=connection, task_queue="default", workflows=[ApprovalWorkflow], activities={})

        asyncio.run(agent._execute({
            "id": "query-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "query:ApprovalWorkflow",
            "payload": {
                "workflowId": "workflow-1",
                "workflowRunId": "run-1",
                "workflowType": "ApprovalWorkflow",
                "queryName": "approver",
                "args": [],
            },
            "lease_timeout_seconds": 30,
        }))
        asyncio.run(agent._execute({
            "id": "update-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "update:ApprovalWorkflow",
            "payload": {
                "workflowId": "workflow-1",
                "workflowRunId": "run-1",
                "workflowType": "ApprovalWorkflow",
                "updateName": "rename",
                "args": ["Linus"],
            },
            "lease_timeout_seconds": 30,
        }))

        self.assertEqual(connection.completed[0], ("query-task-1", {
            "value": "Grace",
            "message": "query completed",
            "started_at": connection.completed[0][1]["started_at"],
            "finished_at": connection.completed[0][1]["finished_at"],
        }))
        self.assertEqual(connection.completed[1][0], "update-task-1")
        self.assertEqual(connection.completed[1][1]["value"], "Linus")

    def test_cancellation_scope_allows_non_cancellable_cleanup(self):
        @workflow.defn(name="CleanupWorkflow")
        class CleanupWorkflow:
            @workflow.run
            async def run(self) -> str:
                async def cleanup() -> None:
                    await workflow.sleep(timedelta(seconds=1))

                await workflow.CancellationScope.non_cancellable(cleanup)
                return "cleaned"

        connection = FakeConnection()
        connection.histories["run-1"] = [{
            "type": "WorkflowCancellationRequested",
            "attributes": {"reason": "operator requested"},
        }]
        agent = Agent(connection=connection, task_queue="default", workflows=[CleanupWorkflow], activities={})
        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:CleanupWorkflow",
            "payload": {"workflowType": "CleanupWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.enqueued[0]["type"], "timer")
        self.assertEqual(connection.enqueued[0]["payload"]["durationMs"], 1000)
        self.assertEqual(connection.blocked[0][0], "workflow-task-1")

    def test_client_connect_style_start_workflow_uses_temporal_id_alias(self):
        @workflow.defn
        class ExampleWorkflow:
            @workflow.run
            async def run(self) -> str:
                return "done"

        connection = FakeConnection()
        client = Client(connection)

        handle = asyncio.run(client.start_workflow(
            ExampleWorkflow,
            id="workflow-id",
            task_queue="queue-a",
            namespace="tenant-a",
            memo={"owner": "docs"},
            ui={
                "displayName": "Example workflow",
                "description": "Visible in the console",
                "details": {"customer": "cust-1", "attempt": 1},
                "tags": ["sdk", "demo"],
            },
            search_attributes={"customer": "cust-1"},
        ))

        self.assertEqual(handle.workflow_id, "workflow-id")
        self.assertEqual(handle.run_id, "run-1")
        self.assertEqual(connection.enqueued[0]["queue"], "queue-a")
        self.assertEqual(connection.enqueued[0]["payload"]["workflowType"], "ExampleWorkflow")
        self.assertEqual(connection.enqueued[0]["payload"]["memo"], {
            "owner": "docs",
            "postgrip.ui": {
                "displayName": "Example workflow",
                "description": "Visible in the console",
                "details": {"customer": "cust-1", "attempt": 1},
                "tags": ["sdk", "demo"],
            },
        })
        self.assertEqual(connection.enqueued[0]["payload"]["searchAttributes"], {"customer": "cust-1"})

    def test_query_replay_invokes_registered_handler(self):
        answer_query = workflow.define_query("answer")

        @workflow.defn(name="QueryWorkflow")
        class QueryWorkflow:
            @workflow.run
            async def run(self) -> str:
                workflow.set_handler(answer_query, lambda: "ready")
                await workflow.condition(lambda: False)
                return "done"

        connection = FakeConnection()
        connection.workflows["run-1"] = {
            "id": "workflow-1",
            "run_id": "run-1",
            "task_id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "QueryWorkflow",
            "state": "running",
        }
        connection.tasks["workflow-task-1"] = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:QueryWorkflow",
            "payload": {"workflowType": "QueryWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
        }
        agent = Agent(connection=connection, task_queue="default", workflows=[QueryWorkflow], activities={})
        query_task = {
            "id": "query-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "query:QueryWorkflow",
            "payload": {
                "workflowId": "workflow-1",
                "workflowRunId": "run-1",
                "workflowType": "QueryWorkflow",
                "queryName": "answer",
                "args": [],
            },
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(query_task))

        self.assertEqual(connection.completed[0][0], "query-task-1")
        self.assertEqual(connection.completed[0][1]["value"], "ready")

    def test_unsupported_agent_task_fails_instead_of_completing(self):
        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows={}, activities={})
        task = {"id": "task-1", "queue": "default", "type": "noop", "payload": {}}

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.failed[0][0], "task-1")
        self.assertIn("not supported", connection.failed[0][1])

    def test_agent_stops_when_poll_directs_shutdown(self):
        connection = FakeConnection()
        polls = 0

        def poll_task_response(**_options):
            nonlocal polls
            polls += 1
            return {"directive": {"type": "shutdown"}}

        connection.poll_task_response = poll_task_response  # type: ignore[method-assign]
        agent = Agent(
            connection=connection,
            task_queue="default",
            workflows={},
            activities={},
            max_concurrent_tasks=1,
        )

        asyncio.run(agent.run())

        self.assertTrue(agent._stopping)
        self.assertEqual(polls, 1)

    def test_public_type_exports_and_agent_defaults_match_typescript(self):
        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows={}, activities={})

        self.assertEqual(agent.max_concurrent_tasks, 4)
        self.assertIs(Task, types.Task)
        self.assertIs(WorkflowExecution, types.WorkflowExecution)

        agent.shutdown(timeout=0.01)
        self.assertTrue(agent._stopping)
        self.assertEqual(agent._shutdown_timeout, 0.01)

    def test_activity_tasks_renew_lease_while_running(self):
        @activity.defn
        async def slow_activity() -> str:
            await asyncio.sleep(0.7)
            return "done"

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows={}, activities=[slow_activity])
        task = {
            "id": "activity-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "activity:slow_activity",
            "payload": {"activityType": "slow_activity", "args": []},
            "lease_timeout_seconds": 1,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.completed[0][0], "activity-task-1")
        self.assertGreaterEqual(len(connection.heartbeats), 1)

    def test_activity_milestone_emits_sequential_step_event(self):
        @activity.defn
        async def import_customer() -> str:
            await activity.milestone("import customers", index=1, total=10)
            return "done"

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows={}, activities=[import_customer])
        task = {
            "id": "activity-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "activity:import_customer",
            "payload": {"activityType": "import_customer", "args": []},
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        milestone = next(event for _task_id, _agent_id, event in connection.events if event["kind"] == "milestone")
        self.assertEqual(milestone["kind"], "milestone")
        self.assertEqual(milestone["message"], "step 1/10 completed import customers")
        self.assertEqual(milestone["details"]["index"], 1)
        self.assertEqual(milestone["details"]["total"], 10)
        self.assertEqual(milestone["details"]["status"], "completed")

    def test_activity_stdout_stderr_emit_task_output_events(self):
        @activity.defn
        async def process_step(name: str) -> str:
            await activity.stdout(f"processed {name}\n", stage="processStep", details={"name": name})
            await activity.stderr("diagnostic line\n")
            return "done"

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows={}, activities=[process_step])
        task = {
            "id": "activity-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "activity:process_step",
            "payload": {"activityType": "process_step", "args": ["customers"]},
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        stdout = next(event for _task_id, _agent_id, event in connection.events if event["kind"] == "stdout")
        stderr = next(event for _task_id, _agent_id, event in connection.events if event["kind"] == "stderr")
        self.assertEqual(stdout["stream"], "stdout")
        self.assertEqual(stdout["data"], "processed customers\n")
        self.assertEqual(stdout["stage"], "processStep")
        self.assertEqual(stdout["details"], {"name": "customers"})
        self.assertEqual(stderr["stream"], "stderr")
        self.assertEqual(stderr["data"], "diagnostic line\n")
        self.assertEqual(stderr["stage"], "activity")

    def test_workflow_sandbox_rejects_nondeterministic_api_calls(self):
        @workflow.defn(name="ClockWorkflow")
        class ClockWorkflow:
            @workflow.run
            async def run(self) -> float:
                return time.time()

        connection = FakeConnection()
        agent = Agent(connection=connection, task_queue="default", workflows=[ClockWorkflow], activities={})
        task = {
            "id": "workflow-task-1",
            "namespace": "default",
            "queue": "default",
            "type": "workflow:ClockWorkflow",
            "payload": {"workflowType": "ClockWorkflow", "workflowId": "workflow-1", "runId": "run-1", "args": []},
            "lease_timeout_seconds": 30,
        }

        asyncio.run(agent._execute(task))

        self.assertEqual(connection.failed[0][0], "workflow-task-1")
        self.assertIn("workflow sandbox rejected", connection.failed[0][1])


if __name__ == "__main__":
    unittest.main()


class TypesExportSurfaceTest(unittest.TestCase):
    """``types.__all__`` is a ``globals()`` comprehension, so it only sees what
    is defined above it. It used to sit above the sandbox section, which left
    every sandbox type absent from ``from postgrip_agent.types import *`` while
    every other public type was present — invisible unless you tried the star
    import."""

    def test_sandbox_types_are_exported(self):
        expected = [
            "Sandbox",
            "SandboxBackend",
            "SandboxCreateRequest",
            "SandboxDesiredState",
            "SandboxListResponse",
            "SandboxObservedState",
            "SandboxWorkspace",
            "CreateSandboxSessionRequest",
            "CreateSandboxSessionResponse",
        ]
        missing = [name for name in expected if name not in types.__all__]
        self.assertEqual(missing, [], f"missing from types.__all__: {missing}")

    def test_every_public_name_is_exported(self):
        """Catches the general case rather than today's list: any public name
        declared below the ``__all__`` assignment would be dropped the same
        way."""
        public = {
            name
            for name in vars(types)
            if not name.startswith("_") and name not in {"annotations"}
        }
        self.assertEqual(sorted(public - set(types.__all__)), [])

    def test_star_import_reaches_the_sandbox_types(self):
        namespace: dict[str, object] = {}
        exec("from postgrip_agent.types import *", namespace)
        self.assertIn("Sandbox", namespace)
        self.assertIn("SandboxCreateRequest", namespace)


class SandboxClientDetailsTest(unittest.TestCase):
    def test_relay_url_accepts_mixed_case_schemes(self):
        """URL schemes are case-insensitive and urllib accepts `HTTPS://` for
        every other call, so rejecting it here made opening a session the one
        operation a mixed-case address broke."""
        from postgrip_agent.sandbox import sandbox_relay_url

        for base in ("HTTPS://agents.example.com", "Https://agents.example.com"):
            self.assertTrue(
                sandbox_relay_url(base, "ses_1", "t").startswith("wss://agents.example.com/"),
                f"{base} did not map to wss://",
            )
        self.assertTrue(sandbox_relay_url("HTTP://127.0.0.1:4100", "ses_1", "t").startswith("ws://"))
        self.assertTrue(sandbox_relay_url("WSS://agents.example.com", "ses_1", "t").startswith("wss://"))
        with self.assertRaises(ValueError):
            sandbox_relay_url("ftp://nope", "ses_1", "t")
        with self.assertRaises(ValueError):
            sandbox_relay_url("agents.example.com", "ses_1", "t")

    def test_authorization_header_detected_regardless_of_case(self):
        """`urllib` normalizes header names, so a lowercase spelling plus a
        configured token collapsed into one header with the *added* token
        winning — inverting the documented explicit-header precedence."""
        from postgrip_agent.client import has_authorization_header

        self.assertTrue(has_authorization_header({"authorization": "Bearer a"}))
        self.assertTrue(has_authorization_header({"Authorization": "Bearer a"}))
        self.assertTrue(has_authorization_header({"AUTHORIZATION": "Bearer a"}))
        self.assertFalse(has_authorization_header({"X-Other": "v"}))
        self.assertFalse(has_authorization_header({}))

    def test_explicit_lowercase_authorization_is_not_overridden(self):
        conn = Connection("http://agent.test", auth_token="token-from-config",
                          headers={"authorization": "Bearer explicit"})
        headers = dict(conn.headers)
        from postgrip_agent.client import has_authorization_header

        self.assertTrue(has_authorization_header(headers))
        self.assertEqual(headers["authorization"], "Bearer explicit")

    def test_relay_dial_carries_configured_headers(self):
        """A caller authenticating via `Connection(headers=...)` got an
        unauthenticated relay dial, so the dial failed *after* the single-use
        ticket had already been spent."""
        from postgrip_agent.sandbox import SandboxClient

        conn = Connection("http://agent.test", headers={
            "Authorization": "Bearer explicit",
            "X-Gateway-Token": "gw",
        })
        client = SandboxClient(conn)
        captured: dict[str, dict[str, str]] = {}

        def fake_create_session(sandbox_id, **kwargs):
            return {"id": "ses_1", "ticket": "pgss_t"}

        class FakeSession:
            def __init__(self, url, headers):
                captured["headers"] = headers

        client.create_session = fake_create_session  # type: ignore[method-assign]
        import postgrip_agent.sandbox as sandbox_module

        original = sandbox_module.SandboxSession
        sandbox_module.SandboxSession = FakeSession  # type: ignore[misc]
        try:
            client.open_session("sbx_1", kind="exec", command=["true"])
        finally:
            sandbox_module.SandboxSession = original  # type: ignore[misc]

        self.assertEqual(captured["headers"].get("Authorization"), "Bearer explicit")
        self.assertEqual(captured["headers"].get("X-Gateway-Token"), "gw")

    def test_wait_until_running_clamps_the_poll_sleep(self):
        """A poll_interval larger than the time remaining postponed the next
        deadline check by the whole interval, so the advertised timeout was not
        the one kept."""
        from postgrip_agent.sandbox import SandboxClient

        conn = Connection("http://agent.test")
        client = SandboxClient(conn)
        client.get = lambda sandbox_id: {  # type: ignore[method-assign]
            "id": sandbox_id, "observedState": "provisioning",
            "generation": 1, "observedGeneration": 1,
        }
        started = time.monotonic()
        with self.assertRaises(TimeoutError):
            client.wait_until_running("sbx_1", timeout=0.2, poll_interval=30.0)
        self.assertLess(time.monotonic() - started, 5.0, "slept the poll interval instead of the timeout")
