import io
import json
import unittest
from unittest.mock import patch

from postgrip_agent import (
    SANDBOX_EXEC_CLOSE_STATUS_BASE,
    SANDBOX_RELAY_MAX_FRAME_BYTES,
    Client,
    Connection,
    sandbox_exec_exit_code,
    sandbox_is_ready,
    sandbox_relay_url,
)


class FakeResponse:
    def __init__(self, payload):
        self._raw = json.dumps(payload).encode()

    def read(self):
        return self._raw

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class SandboxClientTests(unittest.TestCase):
    def setUp(self):
        self.calls = []
        self.connection = Connection(address="https://agents.example.com", auth_token="mgmt-token")
        self.client = Client(self.connection)

    def _patch(self, payload):
        def fake_urlopen(request, timeout=None):
            self.calls.append(request)
            return FakeResponse(payload)

        return patch("postgrip_agent.client.urlopen", fake_urlopen)

    # The endpoint returns {"sandboxes": [...]}, not a bare array. Decoding it
    # as an array yields nothing with no error, which reads as "none".
    def test_list_unwraps_the_envelope(self):
        with self._patch({"sandboxes": [{"id": "sbx_1"}, {"id": "sbx_2"}]}):
            got = self.client.sandbox.list()
        self.assertEqual([s["id"] for s in got], ["sbx_1", "sbx_2"])

    # Sandbox endpoints are management-lane; an agent token is rejected.
    def test_sends_the_management_token(self):
        with self._patch({"id": "sbx_1"}):
            self.client.sandbox.get("sbx_1")
        self.assertEqual(self.calls[0].get_header("Authorization"), "Bearer mgmt-token")

    def test_lifecycle_methods_and_paths(self):
        with self._patch({"id": "sbx_1"}):
            self.client.sandbox.start("sbx_1")
            self.client.sandbox.stop("sbx_1")
            self.client.sandbox.delete("sbx_1")
        seen = [(r.get_method(), r.full_url) for r in self.calls]
        self.assertEqual(
            seen,
            [
                ("POST", "https://agents.example.com/api/v1/sandboxes/sbx_1/start"),
                ("POST", "https://agents.example.com/api/v1/sandboxes/sbx_1/stop"),
                ("DELETE", "https://agents.example.com/api/v1/sandboxes/sbx_1"),
            ],
        )

    def test_blank_sandbox_id_is_rejected_before_any_request(self):
        with self._patch({}):
            with self.assertRaises(ValueError):
                self.client.sandbox.get("")
        self.assertEqual(self.calls, [])

    def test_exec_session_requires_a_command(self):
        with self._patch({}):
            with self.assertRaises(ValueError):
                self.client.sandbox.create_session("sbx_1", kind="exec")
            with self.assertRaises(ValueError):
                self.client.sandbox.create_session("sbx_1", kind="port")
        self.assertEqual(self.calls, [])

    # Readiness is state AND generation: a "running" reading at a generation
    # the agent has not observed describes a sandbox about to change again.
    def test_wait_until_running_requires_the_observed_generation(self):
        responses = [
            {"id": "sbx_1", "observedState": "running", "generation": 2, "observedGeneration": 1},
            {"id": "sbx_1", "observedState": "running", "generation": 2, "observedGeneration": 2},
        ]

        def fake_urlopen(request, timeout=None):
            index = min(len(self.calls), len(responses) - 1)
            self.calls.append(request)
            return FakeResponse(responses[index])

        with patch("postgrip_agent.client.urlopen", fake_urlopen):
            record = self.client.sandbox.wait_until_running("sbx_1", poll_interval=0.001)
        self.assertGreater(len(self.calls), 1)
        self.assertEqual(record["observedGeneration"], 2)

    def test_wait_until_running_fails_fast_with_the_failure_message(self):
        payload = {
            "id": "sbx_1",
            "observedState": "failed",
            "failureCode": "setup_failed",
            "failureMessage": "setup.sh exited 1",
        }
        with self._patch(payload):
            with self.assertRaises(RuntimeError) as ctx:
                self.client.sandbox.wait_until_running("sbx_1", timeout=30, poll_interval=0.001)
        self.assertIn("setup.sh exited 1", str(ctx.exception))

    def test_wait_until_running_timeout_names_the_last_state(self):
        payload = {"id": "sbx_1", "observedState": "provisioning", "generation": 1, "observedGeneration": 1}
        with self._patch(payload):
            with self.assertRaises(TimeoutError) as ctx:
                self.client.sandbox.wait_until_running("sbx_1", timeout=0.02, poll_interval=0.001)
        self.assertIn("provisioning", str(ctx.exception))

    # The upload is a raw body with metadata in headers — not multipart.
    def test_upload_workspace_sends_raw_bytes_and_metadata_headers(self):
        captured = {}

        def fake_urlopen(request, timeout=None):
            captured["request"] = request
            return FakeResponse({"id": "wsp_1", "sha256": "abc"})

        with patch("postgrip_agent.sandbox.urlopen", fake_urlopen):
            out = self.client.sandbox.upload_workspace(
                b"GZIPPED", repository_name="my-repo", revision="deadbeef"
            )
        request = captured["request"]
        self.assertEqual(request.full_url, "https://agents.example.com/api/v1/workspaces")
        self.assertEqual(request.data, b"GZIPPED")
        self.assertEqual(request.get_header("Content-type"), "application/gzip")
        self.assertEqual(request.get_header("X-postgrip-repository"), "my-repo")
        self.assertEqual(request.get_header("X-postgrip-revision"), "deadbeef")
        self.assertEqual(request.get_header("Authorization"), "Bearer mgmt-token")
        self.assertEqual(out["id"], "wsp_1")

    def test_upload_workspace_omits_blank_metadata(self):
        captured = {}

        def fake_urlopen(request, timeout=None):
            captured["request"] = request
            return FakeResponse({"id": "wsp_1"})

        with patch("postgrip_agent.sandbox.urlopen", fake_urlopen):
            self.client.sandbox.upload_workspace(b"x")
        request = captured["request"]
        self.assertIsNone(request.get_header("X-postgrip-repository"))
        self.assertIsNone(request.get_header("X-postgrip-revision"))

    def test_upload_workspace_streams_file_like_inputs(self):
        class TrackingArchive(io.BytesIO):
            def __init__(self, value):
                super().__init__(value)
                self.read_calls = 0

            def read(self, size=-1):
                self.read_calls += 1
                return super().read(size)

        archive = TrackingArchive(b"GZIPPED-IN-CHUNKS")
        captured = {}

        def fake_urlopen(request, timeout=None):
            captured["same_object"] = request.data is archive
            captured["reads_before_transport"] = archive.read_calls
            chunks = []
            while chunk := request.data.read(4):
                chunks.append(chunk)
            captured["body"] = b"".join(chunks)
            return FakeResponse({"id": "wsp_1"})

        with patch("postgrip_agent.sandbox.urlopen", fake_urlopen):
            self.client.sandbox.upload_workspace(archive)

        self.assertTrue(captured["same_object"])
        self.assertEqual(captured["reads_before_transport"], 0)
        self.assertEqual(captured["body"], b"GZIPPED-IN-CHUNKS")
        self.assertGreater(archive.read_calls, 1)


class SandboxRelayContractTests(unittest.TestCase):
    def test_relay_url_scheme_and_escaping(self):
        self.assertEqual(
            sandbox_relay_url("https://agents.example.com", "ses_1", "pgss_t"),
            "wss://agents.example.com/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t",
        )
        self.assertEqual(
            sandbox_relay_url("http://127.0.0.1:4100/", "ses_1", "pgss_t"),
            "ws://127.0.0.1:4100/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t",
        )
        escaped = sandbox_relay_url("https://x.example", "ses/1", "a b&c=d")
        self.assertIn("ses%2F1", escaped)
        self.assertNotIn("a b&c=d", escaped)
        with self.assertRaises(ValueError):
            sandbox_relay_url("ftp://nope", "ses_1", "t")

    # Exit codes ride in the close status. Reading a transport close as an exit
    # code would report a network fault as a successful run.
    def test_exit_codes_decode_only_inside_the_reserved_range(self):
        self.assertEqual(sandbox_exec_exit_code(SANDBOX_EXEC_CLOSE_STATUS_BASE), 0)
        self.assertEqual(sandbox_exec_exit_code(SANDBOX_EXEC_CLOSE_STATUS_BASE + 3), 3)
        self.assertEqual(sandbox_exec_exit_code(SANDBOX_EXEC_CLOSE_STATUS_BASE + 255), 255)
        self.assertIsNone(sandbox_exec_exit_code(SANDBOX_EXEC_CLOSE_STATUS_BASE + 256))
        self.assertIsNone(sandbox_exec_exit_code(1000))  # normal closure
        self.assertIsNone(sandbox_exec_exit_code(1008))  # policy violation / expiry

    def test_readiness_requires_both_state_and_generation(self):
        self.assertFalse(
            sandbox_is_ready({"observedState": "running", "generation": 2, "observedGeneration": 1})
        )
        self.assertTrue(
            sandbox_is_ready({"observedState": "running", "generation": 2, "observedGeneration": 2})
        )
        self.assertFalse(
            sandbox_is_ready({"observedState": "stopped", "generation": 1, "observedGeneration": 1})
        )

    def test_relay_frame_bound_is_stated(self):
        self.assertEqual(SANDBOX_RELAY_MAX_FRAME_BYTES, 1 << 20)


if __name__ == "__main__":
    unittest.main()
