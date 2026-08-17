# example/

Runnable examples that exercise the PostGrip Agent Python SDK end-to-end
against a live runtime service. Running an example locally submits a managed
`workflow.runtime` task to an existing agent pool. The SDK runtime worker path
runs only when a PostGrip host agent launches the example and injects
delegated runtime credentials.

## greeting

A managed-runtime demo: the local process submits the runtime command to an
agent pool, and the host-launched runtime registers one activity and one
workflow class.

```sh
pip install -e .

cp example/.env.example .env
# edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
python -m example.greeting
```

The generated `.env` file is ignored by git. The committed
`example/.env.example` contains placeholders only.

Optional overrides:

| Variable                       | Default            |
|:-------------------------------|:-------------------|
| `POSTGRIP_AGENT_TASK_QUEUE`    | `python-example`   |

When a PostGrip host agent launches the example as a `workflow.runtime` task,
it injects delegated session credentials. The SDK does not enroll standalone
agents.
