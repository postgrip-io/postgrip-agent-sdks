---
title: Installation
layout: default
nav_order: 2
---

# Installation

The SDK lives at `go.postgrip.io/sdk`. Its public source mirror is at [`postgrip-io/agent-sdk-go`](https://github.com/postgrip-io/agent-sdk-go).

```sh
go get go.postgrip.io/sdk@latest
```

To pin a specific version:

```sh
go get go.postgrip.io/sdk@v0.11.0
```

## Requirements

- Go 1.25 or newer (the module's `go.mod` declares 1.25).
- A reachable PostGrip Agent runtime service. The default address is `https://agentorchestrator.postgrip.app`.

## What gets imported

The module declares one top-level Go package per sub-directory. You import only what you use:

```go
import (
    "go.postgrip.io/sdk/client"   // Connection + Client + sub-clients
    "go.postgrip.io/sdk/worker"   // Worker (only if you run one)
    "go.postgrip.io/sdk/workflow" // workflow.Context (only if you write workflows)
    "go.postgrip.io/sdk/activity" // activity helpers (only if you write activities)
    "go.postgrip.io/sdk/failure"  // structured failure types
)
```

A program that submits a managed `workflow.runtime` task — no workflow code, no
runtime worker — needs only `client`.

{: .note }
> Always install through `go.postgrip.io/sdk`; it is the stable public module path regardless of where the source repository is hosted.

## Running against a local agent

For local development, point the SDK at a runtime service running on your machine:

```go
conn, _ := client.NewConnection(client.ConnectionOptions{
    Address: "http://127.0.0.1:4100",
})
```

`Address` defaults to PostGrip Cloud if empty. For local or self-hosted development, pass `Address` explicitly or set `POSTGRIP_AGENTORCHESTRATOR_URL`.
