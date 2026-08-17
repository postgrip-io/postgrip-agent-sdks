# PostGrip Agent Python SDK

[![Docs](https://img.shields.io/badge/docs-site-2563EB?logo=readthedocs&logoColor=white)](https://postgrip-io.github.io/agent-sdk-python/)
[![PyPI version](https://img.shields.io/pypi/v/postgrip-agent.svg)](https://pypi.org/project/postgrip-agent/)
[![Python versions](https://img.shields.io/pypi/pyversions/postgrip-agent.svg)](https://pypi.org/project/postgrip-agent/)
[![CI](https://github.com/postgrip-io/agent-sdk-python/actions/workflows/ci.yml/badge.svg)](https://github.com/postgrip-io/agent-sdk-python/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/postgrip-io/agent-sdk-python.svg)](LICENSE)

Python SDK for defining, submitting, and executing PostGrip workflows. In production, SDK workflow runtimes are supervised by an existing PostGrip agent: the host agent launches the runtime, injects delegated credentials, and keeps generic operational tasks separate from workflow/activity task polling. Client-side SDK code submits `workflow.runtime` tasks to an existing agent pool; it does not enroll or spawn standalone PostGrip agents. Public distribution mirrors remain at [`agent-sdk-go`](https://github.com/postgrip-io/agent-sdk-go), [`agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript), and [`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol).

**Docs:** [postgrip-io.github.io/agent-sdk-python](https://postgrip-io.github.io/agent-sdk-python/) — quick start, workflow runtime, API guide.

**Current release:** `0.11.0`

Install from PyPI after publishing:

```bash
pip install postgrip-agent
```

For local development from a clone of this repository:

```bash
pip install -e .
PYTHONPATH=src python -m unittest discover -s test
```

## Layout

```text
src/postgrip_agent/   # Python package — Connection / Client / workflow runtime
test/                 # unittest-style tests
doc/                  # reserved for longer-form prose docs
.github/workflows/    # CI: build wheel + run tests on 3.11 / 3.12 / 3.13
```

The package exposes a Temporal-style Python API:

```python
import asyncio
from datetime import timedelta

from postgrip_agent import Client, Agent, activity, workflow


@activity.defn
async def greet(name: str) -> str:
    return f"Hello {name}"


@workflow.defn(name="SayHelloWorkflow")
class SayHelloWorkflow:
    @workflow.run
    async def run(self, name: str) -> str:
        return await workflow.execute_activity(
            greet,
            name,
            schedule_to_close_timeout=timedelta(seconds=10),
        )


async def main() -> None:
    # This process is launched by a PostGrip host agent from a workflow.runtime
    # task. The host injects POSTGRIP_AGENT_ID and delegated runtime credentials.
    client = await Client.connect()
    agent = Agent(
        client,
        task_queue="default",
        workflows=[SayHelloWorkflow],
        activities=[greet],
    )
    result = await agent.run_until(
        client.execute_workflow(
            SayHelloWorkflow,
            "PostGrip",
            id="say-hello",
            task_queue="default",
            ui={
                "displayName": "Say hello to PostGrip",
                "description": "Shown on the PostGrip Agents activity tab.",
                "details": {"sdk": "python"},
                "tags": ["demo"],
            },
        )
    )
    print(result)


asyncio.run(main())
```

`ui` is SDK-owned console metadata. It is persisted inside workflow memo as `postgrip.ui`, so the Agents activity tab can show a friendly label, description, details, and tags while `memo` remains available for your own data.

Submit that runtime to an existing agent pool from your client process:

```python
import asyncio
import os

from postgrip_agent import Client


async def submit_runtime() -> None:
    client = await Client.connect(
        # Agent token from Settings > Organization > Agent tokens.
        headers={"Authorization": f"Bearer {os.environ['POSTGRIP_AGENT_TOKEN']}"},
    )
    client.task.workflow_runtime(
        queue="default",
        command="python",
        args=["-m", "myapp.workflow_runtime"],
        runtime_queue="default",
        env={"POSTGRIP_EXAMPLE_RUN_LABEL": "PostGrip"},
    )


asyncio.run(submit_runtime())
```

To run the runtime from an image instead of a host command, pass `image` — and
optionally an `isolation` floor of `"container"` (the default) or `"microvm"`:

```python
client.task.workflow_runtime(
    queue="default",
    image="ghcr.io/example/workflow-runtime:1.4.0",
    isolation="microvm",
    runtime_queue="default",
)
```

`isolation` is a floor, not an exact match: `"container"` work may be scheduled
onto a stronger tier, but `"microvm"` work is never downgraded — it only leases
to agents advertising that tier. It requires `image`; a command-only runtime
executes directly on the agent host, which honors no isolation floor, and the
orchestrator rejects that combination at enqueue.

This SDK targets the PostGrip Agent runtime API, not a Temporal server. It follows the familiar Temporal Python shape for client, agent, workflow, and activity code while using PostGrip Agent task queues and workflow history underneath.

Implemented workflow APIs include durable activity scheduling/replay, durable timers via `workflow.sleep()`, query/signal/update handler replay, child workflow scheduling/replay, continue-as-new, cancellation scopes, and command-order determinism checks for activities, timers, and children.

The Python agent supports bounded concurrent task execution with `max_concurrent_tasks` defaulting to `4`, graceful shutdown draining for in-flight tasks with an optional timeout, and automatic lease renewal for workflow, query, update, and activity tasks. Activities and workflows can emit ordered milestones with `activity.milestone("step name", index=1, total=10)` or `workflow.milestone(...)`; activities can also attach task output with `activity.stdout(...)` and `activity.stderr(...)`. Clients can stream those task events with `handle.watch_events()` or `client.task.watch_events(task_id)`. Workflow execution also performs sandbox checks that reject common nondeterministic APIs such as `time.time()`, `time.sleep()`, `asyncio.sleep()`, `random.*()`, and `uuid.uuid4()`; use `workflow.now()`, `workflow.sleep()`, explicit IDs, or activities for those operations.

## Sandboxes

Sandboxes are persistent development environments assigned to one of your
agents. `client.sandbox` covers the lifecycle plus interactive and
non-interactive execution.

Sandbox endpoints use a **management token**, not an agent token — pass
`auth_token` (or set `POSTGRIP_TOKEN`). Treat the value as opaque; the console
issues a bare hex string with no prefix.

```python
from postgrip_agent import Client, Connection

client = Client(Connection(
    address="https://agents.example.com",
    auth_token=os.environ["POSTGRIP_TOKEN"],
))

workspace = client.sandbox.upload_workspace(
    archive_bytes, repository_name="my-repo", revision=revision
)
box = client.sandbox.create({
    "name": "task-1",
    "image": "postgrip/sandbox:1",
    "workspaceId": workspace["id"],
    "setupCommand": ["/bin/sh", "setup.sh"],
})

# create() returns as soon as the record exists; the sandbox is not up yet.
box = client.sandbox.wait_until_running(box["id"])

exit_code, output = client.sandbox.exec(box["id"], ["pytest", "-q"])

client.sandbox.delete(box["id"])
```

`wait_until_running` treats readiness as `observedState == "running"` **and**
`observedGeneration >= generation`. A "running" reading can predate a start or
stop the assigned agent hasn't observed yet, so state alone can hand back a
sandbox that is about to stop. It raises immediately if the sandbox reaches
`failed`, carrying the sandbox's own failure message, and a timeout names the
last observed state.

### Interactive sessions

Sessions speak WebSocket, which is an **optional dependency** so the base
install stays light for callers who only use the HTTP APIs:

```sh
pip install "postgrip-agent[sandbox]"
```

```python
with client.sandbox.open_session(box["id"], kind="pty", rows=40, columns=120) as session:
    session.send(b"ls -la\n")
    for chunk in session.read_all():
        sys.stdout.buffer.write(chunk)
    print("exit:", session.exit_code)
```

Three relay properties that are not obvious:

- **stdout and stderr are interleaved.** The relay carries one byte stream, so
  they cannot be separated client-side.
- **There is no resize channel.** `rows`/`columns` are fixed at session
  creation; resizing your terminal mid-session cannot reach the sandbox.
- **Exit codes arrive as the WebSocket close status** (`4000 + code`), not in
  the stream. `exit_code` is `None` when the close carried no code — that means
  the transport ended, not that the process succeeded.

Writes must stay at or below `SANDBOX_RELAY_MAX_FRAME_BYTES` (1 MiB); larger
frames raise locally rather than having the relay close the session.

Public protocol types are available from `postgrip_agent.types` and are re-exported from `postgrip_agent`, including `Task`, `TaskEvent`, `WorkflowExecution`, `WorkflowHistoryEvent`, `RetryPolicy`, schedule request/response types, and workflow payload definitions. The package includes `py.typed` so type checkers can consume those annotations. For SDK applications, the documented client-side submission path is `client.task.workflow_runtime(...)`; workflow and activity tasks are then coordinated by the managed runtime launched on the host agent.

Package validation:

```bash
python -m pip wheel --no-deps postgrip-agent/python -w /tmp/postgrip-agent-wheel
PYTHONPATH=postgrip-agent/python python -m unittest discover -s postgrip-agent/python/tests
```
