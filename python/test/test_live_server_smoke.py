from __future__ import annotations

import asyncio
import os
import unittest
import uuid

from postgrip_agent import Client, Agent, activity, workflow


@activity.defn(name="live_greet")
async def live_greet(name: str) -> str:
    return f"Hello {name}"


@workflow.defn(name="LiveGreetingWorkflow")
class LiveGreetingWorkflow:
    @workflow.run
    async def run(self, name: str) -> str:
        return await workflow.execute_activity(live_greet, name)


@unittest.skipUnless(
    os.environ.get("POSTGRIP_AGENT_LIVE_SERVER_URL")
    and os.environ.get("POSTGRIP_AGENT_TOKEN"),
    "set POSTGRIP_AGENT_LIVE_SERVER_URL and POSTGRIP_AGENT_TOKEN to run live server smoke",
)
class LiveServerSmokeTests(unittest.TestCase):
    def test_execute_workflow_with_activity_against_live_server(self):
        async def run_smoke() -> str:
            address = os.environ["POSTGRIP_AGENT_LIVE_SERVER_URL"]
            auth_token = os.environ["POSTGRIP_AGENT_TOKEN"]
            queue = f"python-live-{uuid.uuid4()}"
            headers = {"Authorization": f"Bearer {auth_token}"}
            client = await Client.connect(address, headers=headers)
            agent = Agent(
                client,
                task_queue=queue,
                workflows=[LiveGreetingWorkflow],
                activities=[live_greet],
                poll_interval=0.05,
                max_concurrent_tasks=4,
            )
            return await agent.run_until(
                client.execute_workflow(
                    LiveGreetingWorkflow,
                    "PostGrip",
                    id=f"python-sdk-smoke-{uuid.uuid4()}",
                    task_queue=queue,
                    timeout=15,
                )
            )

        self.assertEqual(asyncio.run(run_smoke()), "Hello PostGrip")


if __name__ == "__main__":
    unittest.main()
