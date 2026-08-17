# Quick start

Two pieces make up the normal SDK flow: a client submits a managed
`workflow.runtime` task to an existing PostGrip agent pool, and that managed
runtime registers workflow and activity functions.

## Submit a workflow runtime

A client process uses an Agent token from Settings > Organization > Agent tokens
and submits a `workflow.runtime` task. The host PostGrip agent launches the
runtime process and injects delegated credentials.

```python
import asyncio
import os
from postgrip_agent import Client


async def main() -> None:
    client = await Client.connect(
        # Agent token from Settings > Organization > Agent tokens.
        headers={"Authorization": f"Bearer {os.environ['POSTGRIP_AGENT_TOKEN']}"},
    )

    task = client.task.workflow_runtime(
        queue="default",
        command="python",
        args=["-m", "myapp.workflow_runtime"],
        runtime_queue="default",
        env={"POSTGRIP_EXAMPLE_RUN_LABEL": "PostGrip"},
    )
    print("submitted workflow runtime", task["id"])


asyncio.run(main())
```

!!! note
    The SDK does not enroll standalone PostGrip agents. It submits workflow runtimes to agent pools that are already enrolled in PostGrip.

## Run a managed workflow runtime worker

The runtime process is launched by a host agent from the `workflow.runtime`
task. Inside that process, an SDK `Agent` registers workflow and activity
functions, then polls for workflow/activity tasks using delegated credentials.

```python
import asyncio
from datetime import timedelta

from postgrip_agent import Client, Agent, activity, workflow


# Activities are plain async functions. Use any standard Python; the
# agent passes a per-task contextvar so activity.heartbeat / milestone
# / stdout / stderr / info work from inside the function body.
@activity.defn
async def greet(name: str) -> str:
    await activity.milestone("greeting", index=1, total=1)
    return f"Hello, {name}"


# Workflows are classes with @workflow.run as the entrypoint coroutine.
# Inside the body, use workflow.execute_activity, workflow.sleep,
# workflow.execute_child for durable operations.
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
    # The host agent injects delegated runtime credentials.
    client = await Client.connect()

    agent = Agent(
        client,
        task_queue="default",
        workflows=[SayHelloWorkflow],
        activities=[greet],
    )

    # run_until pairs starting the runtime worker with awaiting a workflow
    # handle. For long-lived runtimes, use agent.run() and wire your own
    # shutdown signaling.
    handle = await client.execute_workflow(
        SayHelloWorkflow,
        "PostGrip",
        id="say-hello",
        task_queue="default",
    )
    result = await agent.run_until(handle)
    print(result)


asyncio.run(main())
```

The SDK `Agent` loops forever inside the managed runtime, leasing tasks from the configured queue, heartbeating each leased task, and dispatching to your registered functions. Concurrency is bounded by `max_concurrent_tasks` (default 4).

## Start a workflow from elsewhere

From the client side, start the workflow you registered above and wait for its result:

```python
handle = await client.execute_workflow(
    SayHelloWorkflow,
    "world",
    id="say-hello-2",
    task_queue="default",
)

result: str = await handle.result()
print(result)  # Hello, world
```

`execute_workflow` returns a `WorkflowHandle` — use it to wait, signal, query, update, cancel, terminate, or read history.

## Streaming events

Tasks emit ordered events (started / heartbeat / milestone / progress / stdout / stderr / completed / failed). To stream them as they land:

```python
async for event in handle.watch_events():
    print(event.kind, event.message)
```

The async generator closes when the task reaches a terminal state.

## Where to next

- [Workflow runtime](workflow-runtime.md) — the durable replay model: how `execute_activity` / `sleep` work under the hood, what determinism means, the sandbox, signals and queries, ContinueAsNew.
- [API](api.md) — the public surface re-exported from `postgrip_agent`.
