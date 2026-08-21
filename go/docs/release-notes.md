---
title: Release notes
layout: default
nav_order: 7
---

# Release notes

## Unreleased

## v0.12.1

- Changes the default service address to `https://agentorchestrator1.postgrip.io`.
  The previous default, `agentorchestrator.postgrip.app`, does not resolve, so
  any client constructed without an explicit address or environment override
  was contacting a host that does not exist. Callers that already pass an
  address, or set `POSTGRIP_AGENTORCHESTRATOR_URL`, are unaffected.

## v0.12.0

This release completes the hard cutover to the public SDK monorepo.

- Changes the module path to `github.com/postgrip-io/postgrip-agent-sdks/go`.
- Pins the consolidated protocol module at `protocol/v0.3.0`.
- Moves source, releases, CI, and documentation to the monorepo.
- Removes the retired `go.postgrip.io/sdk` vanity-module dependency.

## v0.11.0

This release documents and tags the current managed workflow runtime model for the Go SDK.

- Adds the `workflow.runtime` submission path through `client.Task.WorkflowRuntime`.
- Documents delegated runtime credentials for managed runtimes launched by a host PostGrip agent.
- Documents SDK-owned workflow UI metadata stored under `postgrip.ui`.
- Covers workflow history replay, schedules, child workflows, continue-as-new, signals, cancellation, activity heartbeats, milestones, stdout/stderr task events, structured failures, and determinism checks.
- Keeps the Go module tag aligned with the published `v0.11.0` GitHub release.
