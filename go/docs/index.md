---
title: Home
layout: default
nav_order: 1
---

# PostGrip Agent — Go SDK
{: .fs-9 }

Run shell commands, container workloads, and durable workflows on the PostGrip Agent runtime service from your Go code.
{: .fs-6 .fw-300 }

**Current release:** `v0.12.0`

[Quick start →]({{ "/quickstart" | relative_url }}){: .btn .btn-primary .fs-5 .mb-4 .mb-md-0 .mr-2 }
[GitHub](https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/go){: .btn .fs-5 .mb-4 .mb-md-0 }
[pkg.go.dev](https://pkg.go.dev/github.com/postgrip-io/postgrip-agent-sdks/go){: .btn .fs-5 .mb-4 .mb-md-0 }

---

## What this is

A Go library that lets you talk to the PostGrip Agent runtime service. You enqueue tasks (shell commands, containers, workflows, schedules), or you run a worker that picks up tasks and executes your registered workflow / activity functions.

The SDK ships as five focused sub-packages under `github.com/postgrip-io/postgrip-agent-sdks/go`. You only import what you need — most apps that just enqueue tasks need only `client`; only worker processes need `worker` + `workflow` + `activity`.

| Package    | What's in it                                                                        |
|:-----------|:------------------------------------------------------------------------------------|
| `client`   | `Connection`, `Client`, the `Task` / `Workflow` / `Schedule` sub-clients.           |
| `worker`   | The polling agent that leases tasks and dispatches your registered functions.       |
| `workflow` | The `Context` interface workflows receive, plus options and signal channels.        |
| `activity` | The `Func` shape for activities and helpers (`GetInfo`, `Heartbeat`, `Milestone`, `Stdout`, `Stderr`). |
| `failure`  | Structured failure types (`Application`, `Cancelled`, `Timeout`, `TaskFailed`).     |

## Polyglot

This SDK is one of three. The TypeScript and Python siblings implement the same model against the same wire protocol, so a workflow started by a Python client can be picked up by a Go worker and vice versa.

- [agent-sdk-typescript](https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/typescript)
- [agent-sdk-python](https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/python)
- [agent-sdk-protocol](https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/protocol) — the shared wire shapes

## Where to next

- [Installation]({{ "/installation" | relative_url }}) — `go get` and module-path notes.
- [Quick start]({{ "/quickstart" | relative_url }}) — copy-paste examples for enqueueing tasks and running a worker.
- [Packages]({{ "/packages" | relative_url }}) — what each sub-package owns and how they fit together.
- [Workflow runtime]({{ "/workflow-runtime" | relative_url }}) — the durable replay model in depth: suspension, determinism, ContinueAsNew.
- [API reference]({{ "/api" | relative_url }}) — pointer to the auto-generated godoc on pkg.go.dev.
