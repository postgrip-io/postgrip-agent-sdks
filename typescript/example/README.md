# example/

Runnable examples that exercise the PostGrip Agent TypeScript SDK end-to-end
against a live runtime service. Running an example locally submits a managed
`workflow.runtime` task to an existing agent pool. The SDK runtime worker path
runs only when a PostGrip host agent launches the example and injects
delegated runtime credentials.

## greeting

A managed-runtime demo: the local process submits the runtime command to an
agent pool, and the host-launched runtime registers one activity and one
workflow function.

```sh
cp example/.env.example .env
# edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
bun run example/greeting.ts
```

The generated `.env` file is ignored by git. The committed
`example/.env.example` contains placeholders only.

Optional overrides:

| Variable                       | Default                |
|:-------------------------------|:-----------------------|
| `POSTGRIP_AGENT_TASK_QUEUE`    | `typescript-example`   |

When a PostGrip host agent launches the example as a `workflow.runtime` task,
it injects delegated session credentials. The SDK does not enroll standalone
agents.

## In-repo development

The example imports from the published package name `@postgrip/agent`. To run
it directly out of this repo without installing from npm, run `bun link` from
the repo root once and `bun link @postgrip/agent` from your example workspace,
or replace the import path with `../src/index.ts`.
