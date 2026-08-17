import unittest

from postgrip_agent.generated.openapi import OperationId, resolve_openapi_operation


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
        poll = resolve_openapi_operation(OperationId.POLL_AGENT_TASK)
        self.assertEqual(poll.auth_lane, "agent")
        self.assertEqual(poll.signing, "")

    def test_rejects_missing_path_parameter(self) -> None:
        with self.assertRaisesRegex(ValueError, "taskId"):
            resolve_openapi_operation(OperationId.GET_TASK)


if __name__ == "__main__":
    unittest.main()
