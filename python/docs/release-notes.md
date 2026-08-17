# Release notes

## 0.12.0

This release completes the hard cutover to the public SDK monorepo.

- Moves source, release automation, CI, and documentation to the monorepo.
- Regenerates the OpenAPI-owned models and typed client from the canonical contract.
- Retains the existing `postgrip-agent` PyPI package name and public API.

## 0.11.0

This release documents and packages the current managed workflow runtime model for the Python SDK.

- Adds the `workflow.runtime` submission path through `client.task.workflow_runtime(...)`.
- Documents delegated runtime credentials through `Client.connect()` inside managed runtimes.
- Documents SDK-owned workflow UI metadata stored under `postgrip.ui`.
- Covers workflow history replay, schedules, child workflows, continue-as-new, signals, queries, updates, cancellation, activity heartbeats, milestones, stdout/stderr task events, typed protocol objects, and `py.typed` packaging.
- Keeps the package version aligned with the published `v0.11.0` GitHub release.
