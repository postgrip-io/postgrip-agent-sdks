import unittest

from postgrip_agent.openapi import (
    OPENAPI_CLIENT_OPERATION_COUNT,
    OPENAPI_OPERATION_COUNT,
    EnqueueTaskRequestBody,
    EnqueueTaskResponseBody,
    OpenAPIClient,
    OperationId,
    resolve_openapi_operation,
)


class OpenAPIOperationTests(unittest.TestCase):
    def test_resolves_path_query_and_security_metadata(self) -> None:
        operation = resolve_openapi_operation(
            OperationId.COMPLETE_AGENT_TASK,
            {"taskId": "task/one?"},
            {"agent_id": "agent one"},
        )
        self.assertEqual(operation.method, "POST")
        self.assertEqual(
            operation.path,
            "/api/v1/agent/tasks/task%2Fone%3F/complete?agent_id=agent+one",
        )
        self.assertEqual(operation.auth_lane, "agent")
        self.assertEqual(operation.signing, "agent-task-v1")
        self.assertEqual(operation.request_schema, "CompleteTaskRequest")
        self.assertEqual(operation.response_schema, "Task")
        poll = resolve_openapi_operation(OperationId.POLL_AGENT_TASK)
        self.assertEqual(poll.auth_lane, "agent")
        self.assertEqual(poll.signing, "")

    def test_rejects_missing_path_parameter(self) -> None:
        with self.assertRaisesRegex(ValueError, "taskId"):
            resolve_openapi_operation(OperationId.GET_TASK)

    def test_generates_request_and_response_payload_types(self) -> None:
        self.assertEqual(OPENAPI_OPERATION_COUNT, 42)
        self.assertEqual(OPENAPI_CLIENT_OPERATION_COUNT, 40)
        request: EnqueueTaskRequestBody = {"type": "noop"}
        response: EnqueueTaskResponseBody = {
            "id": "task-1",
            "type": "noop",
            "state": "queued",
            "namespace": "default",
            "queue": "default",
            "attempt": 0,
            "lease_timeout_seconds": 30,
            "created_at": "2026-08-17T00:00:00Z",
            "updated_at": "2026-08-17T00:00:00Z",
        }
        self.assertEqual(request["type"], response["type"])
        self.assertEqual(EnqueueTaskRequestBody.__required_keys__, frozenset({"type"}))
        self.assertIn("namespace", EnqueueTaskRequestBody.__optional_keys__)

    def test_exposes_generated_typed_client(self) -> None:
        calls: list[tuple[OperationId, object]] = []

        def transport(operation_id: OperationId, body: object = None, **_: object) -> object:
            calls.append((operation_id, body))
            return {"name": "generated", "created_at": "now", "updated_at": "now"}

        response = OpenAPIClient(transport).create_namespace({"name": "generated"})
        self.assertEqual(response["name"], "generated")
        self.assertEqual(calls, [(OperationId.CREATE_NAMESPACE, {"name": "generated"})])


if __name__ == "__main__":
    unittest.main()
