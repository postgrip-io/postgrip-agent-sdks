"""Long-running sequential example.

Running it locally submits a workflow.runtime task to an existing agent pool.
When the host agent launches the runtime, it runs five workflows back-to-back,
each chaining five activity calls separated by durable timers.

Run::

    cp example/.env.example .env
    # edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
    python -m example.longrun

The SDK does not enroll standalone agents; host agents inject delegated
managed-runtime credentials.
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import time
import uuid
from datetime import timedelta

from postgrip_agent import Agent, Client, activity, workflow

from example.env import load_example_env

load_example_env()


def env_int(name: str, fallback: int) -> int:
    value = os.environ.get(name)
    if not value:
        return fallback
    try:
        parsed = int(value)
    except ValueError:
        print(f"invalid {name}={value!r}; using {fallback}", flush=True)
        return fallback
    if parsed <= 0:
        print(f"invalid {name}={value!r}; using {fallback}", flush=True)
        return fallback
    return parsed


def env_int_any(names: list[str], fallback: int) -> int:
    for name in names:
        if os.environ.get(name):
            return env_int(name, fallback)
    return fallback


def env_any(names: list[str], fallback: str) -> str:
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
    return fallback


def env_optional(names: list[str]) -> str | None:
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
    return None


def slug(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", value.lower()).strip("-") or "run"


STEPS_PER_WORKFLOW = env_int_any(["POSTGRIP_EXAMPLE_STEPS", "SDK_EXAMPLE_STEPS"], 5)
WORKFLOW_RUNS = env_int_any(["POSTGRIP_EXAMPLE_WORKFLOW_RUNS", "SDK_EXAMPLE_WORKFLOW_RUNS"], 5)
STEP_SLEEP_SECONDS = env_int_any(["POSTGRIP_EXAMPLE_STEP_SLEEP_SECONDS", "SDK_EXAMPLE_STEP_SLEEP_SECONDS"], 13)
WORKFLOW_TIMEOUT_SECONDS = env_int_any(["POSTGRIP_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS", "SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS"], 5 * 60)
RUN_LABEL = env_any(["POSTGRIP_EXAMPLE_RUN_LABEL", "SDK_EXAMPLE_RUN_LABEL"], "PostGrip")
DEFAULT_RUNTIME_IMAGE = "python:3.13-slim"
DEFAULT_RUNTIME_COMMAND = "sh"
DEFAULT_RUNTIME_REF = "da9cb81c9dc6f05efaf5e856248224d2ae06d173"
DEFAULT_RUNTIME_ARGS = [
    "-lc",
    "python -m pip install cryptography >/dev/null && python -c \"import os,urllib.request,zipfile,io; ref=os.environ.get('SDK_EXAMPLE_RUNTIME_REF','da9cb81c9dc6f05efaf5e856248224d2ae06d173'); data=urllib.request.urlopen(f'https://github.com/postgrip-io/agent-sdk-python/archive/{ref}.zip').read(); zipfile.ZipFile(io.BytesIO(data)).extractall('/tmp')\" && cd /tmp/agent-sdk-python-* && PYTHONPATH=src python -m example.longrun",
]


@activity.defn(name="processStep")
async def process_step(name: str, step: int) -> str:
    result = f"processed step {step} for {name}"
    await activity.stdout(f"{result}\n", stage="processStep", details={"step": step, "name": name})
    return result


@workflow.defn(name="LongRunningWorkflow")
class LongRunningWorkflow:
    @workflow.run
    async def run(self, name: str, steps: int) -> str:
        for i in range(1, steps + 1):
            await workflow.execute_activity("processStep", name, i)
            await workflow.sleep(timedelta(seconds=STEP_SLEEP_SECONDS))
        return f"completed {steps} steps for {name}"


async def main() -> None:
    if os.environ.get("POSTGRIP_AGENT_MANAGED_RUNTIME") != "true":
        await submit_managed_runtime()
        return

    address = (
        os.environ.get("POSTGRIP_AGENTORCHESTRATOR_URL")
        or os.environ.get("POSTGRIP_AGENT_LIVE_SERVER_URL")
        or "https://agentorchestrator.postgrip.app"
    )
    queue = os.environ.get("POSTGRIP_AGENT_TASK_QUEUE", "python-longrun")
    agent_id = os.environ.get("POSTGRIP_AGENT_ID", "python-longrun-agent")

    client = await Client.connect(address, headers=agent_token_headers())
    agent = Agent(
        client,
        identity=agent_id,
        name=agent_id,
        task_queue=queue,
        workflows=[LongRunningWorkflow],
        activities=[process_step],
        max_concurrent_tasks=4,
    )

    async def run_all() -> None:
        overall_start = time.monotonic()
        for i in range(1, WORKFLOW_RUNS + 1):
            run_start = time.monotonic()
            workflow_id = f"python-longrun-{slug(RUN_LABEL)}-{uuid.uuid4()}-{i}"
            print(f"[{i}/{WORKFLOW_RUNS}] starting {workflow_id}", flush=True)
            result = await client.execute_workflow(
                LongRunningWorkflow,
                f"{RUN_LABEL}-{i}",
                STEPS_PER_WORKFLOW,
                id=workflow_id,
                task_queue=queue,
                ui={
                    "displayName": f"{RUN_LABEL} long run #{i}",
                    "description": f"Runs {STEPS_PER_WORKFLOW} steps with {STEP_SLEEP_SECONDS}s sleeps between steps.",
                    "details": {
                        "sdk": "python",
                        "steps": STEPS_PER_WORKFLOW,
                        "sleepSeconds": STEP_SLEEP_SECONDS,
                    },
                    "tags": ["sdk-ui-demo", "python"],
                },
                timeout=WORKFLOW_TIMEOUT_SECONDS,
            )
            print(f"[{i}/{WORKFLOW_RUNS}] {workflow_id} -> {result!r} ({int(time.monotonic() - run_start)}s)", flush=True)
        print(f"done — {WORKFLOW_RUNS} workflows in {int(time.monotonic() - overall_start)}s", flush=True)

    await agent.run_until(run_all())


async def submit_managed_runtime() -> None:
    address = (
        os.environ.get("POSTGRIP_AGENTORCHESTRATOR_URL")
        or os.environ.get("POSTGRIP_AGENT_LIVE_SERVER_URL")
        or "https://agentorchestrator.postgrip.app"
    )
    client = await Client.connect(address, headers=agent_token_headers())
    queue = env_any(["POSTGRIP_EXAMPLE_RUNTIME_QUEUE", "SDK_EXAMPLE_RUNTIME_QUEUE"], "default")
    runtime_queue = env_any(
        ["POSTGRIP_EXAMPLE_RUNTIME_CHILD_QUEUE", "SDK_EXAMPLE_RUNTIME_CHILD_QUEUE"],
        f"sdk-runtime-{slug(RUN_LABEL)}-{uuid.uuid4().hex[:8]}",
    )
    args_json = os.environ.get("SDK_EXAMPLE_RUNTIME_ARGS_JSON") or os.environ.get("POSTGRIP_EXAMPLE_RUNTIME_ARGS_JSON")
    args = json.loads(args_json) if args_json else DEFAULT_RUNTIME_ARGS
    if not isinstance(args, list) or any(not isinstance(item, str) for item in args):
        raise RuntimeError("SDK_EXAMPLE_RUNTIME_ARGS_JSON must be a JSON array of strings")
    task = client.task.workflow_runtime(
        queue=queue,
        runtime_queue=runtime_queue,
        image=env_any(["POSTGRIP_EXAMPLE_RUNTIME_IMAGE", "SDK_EXAMPLE_RUNTIME_IMAGE"], DEFAULT_RUNTIME_IMAGE),
        command=env_any(["POSTGRIP_EXAMPLE_RUNTIME_COMMAND", "SDK_EXAMPLE_RUNTIME_COMMAND"], DEFAULT_RUNTIME_COMMAND),
        args=args,
        working_dir=env_optional(["POSTGRIP_EXAMPLE_RUNTIME_WORKING_DIR", "SDK_EXAMPLE_RUNTIME_WORKING_DIR"]),
        pull_policy=env_optional(["POSTGRIP_EXAMPLE_RUNTIME_PULL_POLICY", "SDK_EXAMPLE_RUNTIME_PULL_POLICY"]),
        timeout_seconds=env_int_any(["POSTGRIP_EXAMPLE_RUNTIME_TIMEOUT_SECONDS", "SDK_EXAMPLE_RUNTIME_TIMEOUT_SECONDS"], 900),
        lease_timeout_seconds=env_int_any(["POSTGRIP_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS", "SDK_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS"], 30),
        env={
            "SDK_EXAMPLE_RUN_LABEL": RUN_LABEL,
            "SDK_EXAMPLE_WORKFLOW_RUNS": str(WORKFLOW_RUNS),
            "SDK_EXAMPLE_STEPS": str(STEPS_PER_WORKFLOW),
            "SDK_EXAMPLE_STEP_SLEEP_SECONDS": str(STEP_SLEEP_SECONDS),
            "SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS": str(WORKFLOW_TIMEOUT_SECONDS),
            "SDK_EXAMPLE_RUNTIME_REF": env_any(["POSTGRIP_EXAMPLE_RUNTIME_REF", "SDK_EXAMPLE_RUNTIME_REF"], DEFAULT_RUNTIME_REF),
        },
    )
    print(f"submitted managed workflow runtime task={task['id']} queue={queue} runtime_queue={runtime_queue}", flush=True)


def agent_token_headers() -> dict[str, str]:
    token = os.environ.get("POSTGRIP_AGENT_TOKEN")
    return {"Authorization": f"Bearer {token}"} if token else {}


if __name__ == "__main__":
    asyncio.run(main())
