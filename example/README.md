# example/

Runnable examples that exercise the PostGrip Agent Go SDK end-to-end against a
live runtime service. Running an example locally submits a managed
`workflow.runtime` task to an existing agent pool. The SDK worker path runs
only when a PostGrip host agent launches the example and injects delegated
runtime credentials.

## greeting

A managed-runtime demo: the local process submits the runtime command to an
agent pool, and the host-launched runtime registers one activity and one
workflow.

```sh
cp example/.env.example .env
# edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
go run ./example/greeting
```

The generated `.env` file is ignored by git. The committed
`example/.env.example` contains placeholders only.

Optional overrides:

| Variable                       | Default              |
|:-------------------------------|:---------------------|
| `POSTGRIP_AGENT_TASK_QUEUE`    | `go-example`         |
| `POSTGRIP_AGENT_ID`            | `go-example-agent`   |

When a PostGrip host agent launches this example as a `workflow.runtime` task,
it injects `POSTGRIP_AGENT_MANAGED_RUNTIME=true`, delegated session tokens, and
the agent signing key. The SDK does not enroll standalone agents.
