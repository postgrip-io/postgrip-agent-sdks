# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Python SDK for the PostGrip Agent runtime service. Mirrors `agent-sdk-go` and `agent-sdk-typescript`; wire-shape source of truth lives in [`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) (Go) and is hand-mirrored into `src/postgrip_agent/types.py`. The mirror is enforced by a CI drift check that fetches `agent-sdk-protocol/tools/check_drift.py` from `main` and compares struct/field names.

The package is `postgrip_agent` (importable name; PyPI distribution is `postgrip-agent`). API shape follows the Temporal Python SDK: `Client.connect`, `Agent`, `@workflow.defn` / `@activity.defn`, `await ctx.execute_activity(...)`.

## Commands

```sh
# Install in dev mode (with src/ layout means PYTHONPATH must include src for direct test runs)
pip install -e .

# Run all tests
PYTHONPATH=src python -m unittest discover -s test

# Run a single test file or test method
PYTHONPATH=src python -m unittest test.test_python_sdk
PYTHONPATH=src python -m unittest test.test_python_sdk.TestX.test_specific_thing

# Build wheel locally (matches CI's check)
python -m pip wheel --no-deps . -w /tmp/wheel
```

CI (`.github/workflows/ci.yml`) runs the wheel build + test matrix on Python 3.11 / 3.12 / 3.13, plus the cross-language drift guard. The drift job pulls `tools/check_drift.py` from `agent-sdk-protocol` and compares this repo's `types.py` against Go (and TS via `--from-github`). A drift failure means either a wire change here is missing the Go side, or this PR shouldn't be touching wire shapes.

## Architecture

### Module layout

```
src/postgrip_agent/
├─ __init__.py        Re-exports the public surface; this is the customer-facing namespace.
├─ client.py          Client, Connection, TaskClient, WorkflowClient, ScheduleClient, WorkflowHandle
├─ agent.py           Agent — the polling worker. (Equivalent of Go's worker.Worker.)
├─ worker.py          Backwards-compat alias to Agent (Worker = Agent). Legacy import name.
├─ workflow.py        Decorators (@defn, @run, @signal, @query, @update), workflow-side runtime,
│                     context-var stash, sandbox checks for nondeterministic APIs.
├─ activity.py        Activity decorators + contextvar-stashed runtime, milestone / heartbeat
├─ errors.py          ApplicationFailure, CancelledFailure, TimeoutFailure, TaskFailedError
└─ types.py           Wire-format dataclasses / TypedDicts mirroring agent-sdk-protocol/types.go
```

`__init__.py` is the customer-facing import surface. New public types must be added there explicitly; rely on `__all__` to keep the exposed surface deliberate.

### How the workflow runtime works

Same model as the Go SDK, just async-Python-flavored. Each workflow task lease:

1. Agent fetches the workflow's full durable history.
2. Agent constructs a workflow runtime (`_WorkflowRuntime` in `workflow.py`) wired to the history cursor.
3. Agent invokes the customer's `@workflow.run` coroutine.
4. Calls inside the coroutine (`workflow.execute_activity`, `workflow.sleep`, `workflow.execute_child`, signal/query/update channel reads) consult the cursor first.
   - History records this command, completed → return persisted result.
   - History records it, in-flight → `await` suspends until the runtime resolves the awaited future.
   - History exhausted → schedule a new command and suspend.
   - Mismatch with recorded command → raise a non-retryable `ApplicationFailure` tagged `WorkflowDeterminismViolation`.

When the coroutine returns (or raises), the agent reports the result back as a task completion. **Suspension is via Python `await` semantics, not via raising a sentinel** — that's the key difference from Go, where the body returns an error sentinel. Don't try to "fix" suspension by raising; it's load-bearing that the coroutine yields control back to the agent's event loop instead.

### `ContinueAsNewCommand` is raised, not returned

Unlike `Suspended`, `ContinueAsNew` *is* a sentinel exception (`workflow.ContinueAsNewCommand`). Customer workflow code calls `workflow.continue_as_new(...)` which raises this; the agent catches it and translates to a runtime-service ContinueAsNewResult on completion. Don't change this to a return value — `continue_as_new` from inside nested helpers / scopes works only because the exception propagates.

### Sandbox checks

`workflow.py` has an AST-walking sandbox that rejects common nondeterministic APIs (`time.time()`, `time.sleep()`, `asyncio.sleep()`, `random.*()`, `uuid.uuid4()`) at workflow definition load time. It runs once when `@workflow.defn` is applied and raises `ApplicationFailure` if it sees one of these. **Customer workflows must use `workflow.now()`, `workflow.sleep()`, explicit IDs, or activity calls instead.** The sandbox is a static check — it can't catch dynamically-imported nondeterministic code, so document the rule prominently in customer-facing material rather than relying on the sandbox as a hard guarantee.

### Activity context

`activity.py` uses `contextvars.ContextVar` to stash an `_ActivityRuntime` per in-flight activity invocation. Helpers (`activity.heartbeat`, `activity.milestone`, `activity.info`) read from the context var. Calling them outside an activity raises a `RuntimeError` rather than silently no-oping. The contextvar is set up by `agent.py` immediately before invoking the customer's activity coroutine.

### Custom decoder semantics

`types.py` carries dataclasses that round-trip with the Go wire structs. Some Go fields use `*time.Time` (RFC3339 string on the wire); the Python side must accept both RFC3339 and `null`/missing without crashing. Don't assume any timestamp is always present.

## Polyglot mirror

This is one of four repos sharing the runtime contract:

- [`postgrip-io/agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) — wire-shape source of truth (Go).
- [`postgrip-io/agent-sdk-go`](https://github.com/postgrip-io/agent-sdk-go) — Go SDK; imports protocol directly.
- [`postgrip-io/agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript) — TS SDK; mirrors types in `src/types.ts`.
- [`postgrip-io/agent-sdk-python`](https://github.com/postgrip-io/agent-sdk-python) — this repo; mirrors types in `src/postgrip_agent/types.py`.

Wire-shape changes need to land in protocol + each mirror in coordinated PRs. The drift guard catches name-level disagreement; it does **not** catch type-level drift (int vs str, optional vs required) — those need human review.

## Distribution

PyPI package is `postgrip-agent` (note the dash; the Python module name `postgrip_agent` uses an underscore per PEP 8). Releases are gated on a git tag (`v*`) which fires `.github/workflows/publish.yml` to build and upload to PyPI via [trusted publishing](https://docs.pypi.org/trusted-publishers/) — no API tokens stored in the repo. The trusted publisher must be configured **once** on PyPI under the project's Settings → Publishing tab (workflow file path: `.github/workflows/publish.yml`, environment: `pypi`).

To cut a release:

```sh
git tag -a v0.X.Y -m "v0.X.Y — short summary"
git push origin v0.X.Y
gh release create v0.X.Y --title "v0.X.Y" --notes "..."
```

The publish workflow watches for tags matching `v*` and uploads on success. The version in `pyproject.toml` should match the tag (or use a dynamic versioning plugin — currently it's hardcoded to `0.1.0`, bump together with the tag).

## Docs

The customer-facing docs site is built with MkDocs Material and deployed to GitHub Pages via `.github/workflows/docs.yml`. Source lives in `docs/` plus `mkdocs.yml`. Pages auto-deploy on pushes to `main` that touch those paths.
