# Installation

```sh
pip install postgrip-agent
```

To pin a specific version:

```sh
pip install postgrip-agent==0.11.0
```

## Requirements

- **Python 3.11 or newer.** The SDK uses [`asyncio`](https://docs.python.org/3/library/asyncio.html), `contextvars`, and structural typing patterns that target 3.11 / 3.12 / 3.13.
- A reachable PostGrip Agent runtime service. The default address is `https://agentorchestrator.postgrip.app`.

## Importing

The PyPI distribution name (`postgrip-agent`) uses a dash; the importable Python module name (`postgrip_agent`) uses an underscore — that's the standard PEP 8 mapping.

```python
import postgrip_agent
from postgrip_agent import Client, Agent, activity, workflow
```

The package re-exports its public surface from `postgrip_agent.__init__`, so most code will reach for the top-level names. The lower-level `Connection`, sub-clients (`TaskClient`, `WorkflowClient`, `ScheduleClient`), and wire types (`Task`, `WorkflowExecution`, etc.) are also available there.

## Local development from a clone

```sh
git clone https://github.com/postgrip-io/agent-sdk-python
cd agent-sdk-python
pip install -e .
PYTHONPATH=src python -m unittest discover -s test
```

The repo uses a `src/` layout, so direct test runs need `PYTHONPATH=src`. CI does the same.

## Type checking

The package ships with [PEP 561](https://peps.python.org/pep-0561/)-compliant type information (`py.typed` marker file). [mypy](https://mypy-lang.org/) and [pyright](https://github.com/microsoft/pyright) will see the annotations on `Client`, `Agent`, workflow/activity decorators, and every wire type.

## Running against a local agent

For local development, point the SDK at a runtime service running on your machine:

```python
import os
from postgrip_agent import Client

client = await Client.connect(
    "http://127.0.0.1:4100",
    # Agent token from Settings > Organization > Agent tokens.
    headers={"Authorization": f"Bearer {os.environ['POSTGRIP_AGENT_TOKEN']}"},
)
```

`Client.connect` defaults to PostGrip Cloud if the address is omitted. For local or self-hosted development, pass the address explicitly as shown above or set `POSTGRIP_AGENTORCHESTRATOR_URL`.
