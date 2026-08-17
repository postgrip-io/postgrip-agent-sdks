# PostGrip Agent — Python SDK

Run shell commands, container workloads, and durable workflows on the PostGrip Agent runtime service from your Python code.

**Current release:** `0.11.0`

[Quick start →](quickstart.md){ .md-button .md-button--primary }
[GitHub](https://github.com/postgrip-io/agent-sdk-python){ .md-button }
[PyPI](https://pypi.org/project/postgrip-agent/){ .md-button }

---

## What this is

A Python library that lets you talk to the PostGrip Agent runtime service. You enqueue tasks (shell commands, containers, workflows, schedules) from a `Client`, or you run an `Agent` that picks up tasks and dispatches them to your registered `@workflow.defn` and `@activity.defn` functions.

The shape mirrors the Temporal Python SDK on purpose: workflows are async classes decorated with `@workflow.defn` and `@workflow.run`; activities are plain async functions decorated with `@activity.defn`; the agent registers both and runs the polling loop. If you've used Temporal in Python, the surface should feel immediate.

```python
from postgrip_agent import Client, Agent, activity, workflow


@activity.defn
async def greet(name: str) -> str:
    return f"Hello {name}"


@workflow.defn
class SayHelloWorkflow:
    @workflow.run
    async def run(self, name: str) -> str:
        return await workflow.execute_activity(greet, name)
```

## Polyglot

This SDK is one of three. The Go and TypeScript siblings implement the same model against the same wire protocol, so a workflow started by a Python client can be picked up by a Go agent and vice versa.

- [agent-sdk-go](https://github.com/postgrip-io/agent-sdk-go)
- [agent-sdk-typescript](https://github.com/postgrip-io/agent-sdk-typescript)
- [agent-sdk-protocol](https://github.com/postgrip-io/agent-sdk-protocol) — the shared wire shapes (Go, with hand-mirrored TS / Python definitions)

## Where to next

- [Installation](installation.md) — `pip install` and version requirements.
- [Quick start](quickstart.md) — copy-paste examples for enqueueing tasks and running an agent.
- [Workflow runtime](workflow-runtime.md) — the durable replay model in depth: history cursor, suspension, determinism rules, sandbox, ContinueAsNew.
- [API](api.md) — the public surface re-exported from `postgrip_agent`.
