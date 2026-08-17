# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

TypeScript SDK for the PostGrip Agent runtime service. Mirrors `agent-sdk-go` and `agent-sdk-python`; wire-shape source of truth lives in [`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) (Go) and is hand-mirrored into `src/types.ts`. The mirror is enforced by a CI drift check that fetches `agent-sdk-protocol/tools/check_drift.py` from `main` and compares struct/field names.

The npm package is `@postgrip/agent` (scoped under `@postgrip` org). API shape follows the Temporal TypeScript SDK: `Connection.connect`, `Client`, `Agent`, **plain async functions for workflows** (no class decorators — TS uses functions, unlike Python's `@workflow.defn class`), `proxyActivities<typeof activities>(...)` for typed activity stubs.

## Commands

```sh
# Install dependencies (Bun is the dev runtime; tsc + vitest do the actual work)
bun install --frozen-lockfile

# Type-check only
bun run typecheck

# Compile dist/
bun run build

# Run all tests
bun run test

# Run a single test file or test name
bun run test src/foo.test.ts
bunx vitest run -t "specific test name"
```

CI (`.github/workflows/ci.yml`) runs `typecheck` + `build` + `test` on Ubuntu with Bun, plus the cross-language drift guard. The drift job pulls `tools/check_drift.py` from `agent-sdk-protocol` and compares this repo's `src/types.ts` against Go (and Python via `--from-github`). A drift failure means either a wire change here is missing the Go side, or this PR shouldn't be touching wire shapes.

## Architecture

### Module layout

```
src/
├─ index.ts          Re-exports the public surface; this is the customer-facing namespace.
├─ client.ts         Client, Connection (re-exported from connection.ts), TaskClient,
│                    WorkflowClient, ScheduleClient, WorkflowHandle, WorkflowUpdateHandle.
├─ connection.ts     HTTP transport (Connection, ConnectionOptions).
├─ agent.ts          Agent class — the polling worker. Exported a second time as `Worker` for
│                    code mirroring Temporal's naming.
├─ workflow.ts       Workflow runtime: workflowInfo, sleep, condition, cancellationRequested,
│                    proxyActivities, executeChild, continueAsNew, defineSignal/Query/Update,
│                    setHandler, milestone, validateWorkflowSandbox, CancellationScope class.
├─ activity.ts       Activity runtime: activityInfo, heartbeat, milestone (re-exported as
│                    `activityMilestone` from index.ts to avoid clash with workflow.milestone).
├─ errors.ts         ApplicationFailure, CancelledFailure, TimeoutFailure, TaskFailedError,
│                    PostGripAgentError.
└─ types.ts          Wire-format types mirroring agent-sdk-protocol/types.go.
```

`index.ts` is the customer-facing import surface. Re-export new public names through `index.ts` explicitly; the `exports` field in `package.json` exposes only `./dist/index.js` so anything not re-exported from there is effectively private.

### Workflow shape: functions, not classes

Unlike the Python SDK (where workflows are classes with `@workflow.defn` and `@workflow.run`), TS workflows are **plain async functions**:

```ts
import { proxyActivities } from '@postgrip/agent';

const { greet } = proxyActivities<typeof activities>({
  startToCloseTimeoutMs: 10_000,
  retry: { maximumAttempts: 3 },
});

export async function greetingWorkflow(name: string): Promise<string> {
  return greet(name);
}
```

`proxyActivities<T>(...)` returns a typed object whose methods schedule activities and return promises. The type parameter is `typeof activities` so the activity bag's call signatures are propagated to the proxy. **Don't break this typing**: any accidental `any` widens the customer's type-safety story.

Signal / query / update handlers register via `defineSignal/Query/Update(name)` returning a definition, then `setHandler(definition, handler)` inside the workflow body wires it up. Same pattern as Python. There is no `@signal` / `@query` / `@update` decorator.

### How the workflow runtime works

Same model as Go and Python, just JS-async-flavored. Each workflow task lease:

1. Agent fetches the workflow's full durable history.
2. Agent constructs a workflow runtime (`WorkflowRuntime` in `workflow.ts`) wired to the history cursor.
3. Agent invokes the customer's workflow function (the registered `WorkflowFunction`).
4. Awaits inside the workflow that touch workflow APIs (`sleep`, proxy-activity calls, `executeChild`, `condition`, signal handler awaits) consult the cursor first.
   - History records this command, completed → resolve the awaited promise with the persisted result.
   - History records it, in-flight → the promise stays pending; the agent reports the task as blocked and waits for redelivery.
   - History exhausted → schedule a new command and let the promise stay pending.
   - Mismatch with recorded command → throw a non-retryable `ApplicationFailure` tagged `WorkflowDeterminismViolation`.

Suspension is via JS promise pendingness — the workflow function never resolves until its dependencies are recorded. Don't try to "fix" this by throwing; the agent infrastructure relies on the function's promise staying pending so the surrounding event loop can yield.

### `ContinueAsNewCommand` is thrown, not returned

`continueAsNew(workflowFn, ...args)` throws a `ContinueAsNewCommand` exception. The agent catches it and translates to a runtime-service `ContinueAsNewResult`. Don't `try/catch` it from inside the workflow body — let it propagate out.

### Sandbox checks

`validateWorkflowSandbox(workflow)` is exported but defensive — it walks the workflow body's text looking for nondeterministic API patterns (`Math.random`, `Date.now`, `setTimeout`, etc.) and throws `ApplicationFailure` if found. **It's a guard rail, not a hard guarantee** — dynamically-imported nondeterministic code can slip through. Customer workflows must use `workflowInfo().now`, `sleep`, deterministic IDs, or activity calls instead.

### Activity context via async-local-storage-like mechanism

`activityInfo()`, `heartbeat()`, and `activityMilestone()` (the workflow-side `milestone` is re-exported under that name from `index.ts`) read runtime state set up by the agent before invoking the activity body. Calling them outside an activity throws.

The custom `milestone` re-export in `index.ts`:

```ts
export { activityInfo, heartbeat, milestone as activityMilestone } from './activity.js';
```

— so customers write `import { milestone, activityMilestone } from '@postgrip/agent'`, where `milestone` is the workflow one and `activityMilestone` is the activity one. Don't simplify these to a single `milestone` export — they have different signatures and different runtime expectations.

## Polyglot mirror

This is one of four repos sharing the runtime contract:

- [`postgrip-io/agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) — wire-shape source of truth (Go).
- [`postgrip-io/agent-sdk-go`](https://github.com/postgrip-io/agent-sdk-go) — Go SDK; imports protocol directly.
- [`postgrip-io/agent-sdk-python`](https://github.com/postgrip-io/agent-sdk-python) — Python SDK; mirrors types in `src/postgrip_agent/types.py`.
- [`postgrip-io/agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript) — this repo; mirrors types in `src/types.ts`.

Wire-shape changes need to land in protocol + each mirror in coordinated PRs. The drift guard catches name-level disagreement; it does **not** catch type-level drift (number vs string, optional vs required) — those need human review.

## Distribution

The npm package is `@postgrip/agent`. Releases are gated on a git tag (`v*`) which fires `.github/workflows/publish.yml` to build and publish via [npm trusted publishing](https://docs.npmjs.com/trusted-publishers) — no `NPM_TOKEN` stored in the repo. The trusted publisher must be configured **once** on npmjs.com under the package's Settings → Publishing tab, with this exact configuration:

- Package name: `@postgrip/agent`
- Repository owner: `postgrip-io`
- Repository: `agent-sdk-typescript`
- Workflow filename: `publish.yml`
- Environment: `npm`

The publish step uses `npm publish --provenance --access public`, which requires the workflow's `id-token: write` permission (set at workflow level) and includes [SLSA build provenance](https://docs.npmjs.com/generating-provenance-statements) on the published artifact.

To cut a release:

```sh
# Bump version in package.json to match the tag, then:
git tag -a v0.X.Y -m "v0.X.Y — short summary"
git push origin v0.X.Y
gh release create v0.X.Y --title "v0.X.Y" --notes "..."
```

The publish workflow watches for tags matching `v*` and uploads on success. The `version` in `package.json` must match the tag (or the publish step fails with a name/version mismatch).

## Docs

The customer-facing docs site is built with MkDocs Material and deployed to GitHub Pages via `.github/workflows/docs.yml`. Source lives in `docs/` plus `mkdocs.yml`. Pages auto-deploy on pushes to `main` that touch those paths. We use MkDocs (not VitePress / Docusaurus) for parity with the Python SDK's docs site — both build identically in CI with a single Python-only step.
