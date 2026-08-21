# Release notes

## Unreleased

## 0.12.3

- Adds the `stdin` exec option and `SandboxSession.closeInput()` so commands
  that consume input through EOF can finish without closing their output side.
- Preserves valid fast-process exit statuses when the EOF control races the
  remote close, with cleanup for genuine stdin setup failures.
- Standardizes the published package metadata on Apache-2.0 licensing.

## 0.12.2

- Adds a prominent installation section to the npm README.
- Corrects the repository layout and documents the implemented cancellation
  scope support and active Vitest suite.

## 0.12.1

- Changes the default service address to `https://agentorchestrator1.postgrip.io`.
  The previous default, `agentorchestrator.postgrip.app`, does not resolve, so
  any client constructed without an explicit address or environment override
  was contacting a host that does not exist. Callers that already pass an
  address, or set `POSTGRIP_AGENTORCHESTRATOR_URL`, are unaffected.
- Authenticates sandbox WebSocket relay handshakes with the configured
  management token and custom connection headers on Node and Bun.
- Adds `webSocketFactory` for custom authenticated relay transports and reports
  the native browser WebSocket header limitation before creating a session.

## 0.12.0

This release completes the hard cutover to the public SDK monorepo.

- Moves source, release automation, CI, and documentation to the monorepo.
- Regenerates the OpenAPI-owned models and typed client from the canonical contract.
- Retains the existing `@postgrip/agent` npm package name and public API.

## 0.11.0

This release documents and packages the current managed workflow runtime model for the TypeScript SDK.

- Adds the `workflow.runtime` submission path for handing SDK runtimes to an existing PostGrip agent pool.
- Documents delegated runtime credentials through `Connection.connect()` inside managed runtimes.
- Documents SDK-owned workflow UI metadata stored under `postgrip.ui`.
- Covers workflow history replay, schedules, child workflows, continue-as-new, signals, queries, updates, cancellation, activity heartbeats, milestones, and task output events.
- Keeps the package version aligned with the published `v0.11.0` GitHub release.
