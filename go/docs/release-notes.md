---
title: Release notes
layout: default
nav_order: 7
---

# Release notes

## v0.11.0

This release documents and tags the current managed workflow runtime model for the Go SDK.

- Adds the `workflow.runtime` submission path through `client.Task.WorkflowRuntime`.
- Documents delegated runtime credentials for managed runtimes launched by a host PostGrip agent.
- Documents SDK-owned workflow UI metadata stored under `postgrip.ui`.
- Covers workflow history replay, schedules, child workflows, continue-as-new, signals, cancellation, activity heartbeats, milestones, stdout/stderr task events, structured failures, and determinism checks.
- Keeps the Go module tag aligned with the published `v0.11.0` GitHub release.
