---
title: Installation
layout: default
nav_order: 2
---

# Installation

The SDK lives at `github.com/postgrip-io/postgrip-agent-sdks/go`; its source is the [`go/`](https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/go) directory of the SDK monorepo.

```sh
go get github.com/postgrip-io/postgrip-agent-sdks/go@latest
```

To pin a specific version:

```sh
go get github.com/postgrip-io/postgrip-agent-sdks/go@v0.12.0
```

## Requirements

- Go 1.25 or newer (the module's `go.mod` declares 1.25).
- A reachable PostGrip Agent runtime service. The default address is `https://agentorchestrator.postgrip.app`.

## What gets imported

The module declares one top-level Go package per sub-directory. You import only what you use:

```go
import (
    "github.com/postgrip-io/postgrip-agent-sdks/go/client"   // Connection + Client + sub-clients
    "github.com/postgrip-io/postgrip-agent-sdks/go/worker"   // Worker (only if you run one)
    "github.com/postgrip-io/postgrip-agent-sdks/go/workflow" // workflow.Context (only if you write workflows)
    "github.com/postgrip-io/postgrip-agent-sdks/go/activity" // activity helpers (only if you write activities)
    "github.com/postgrip-io/postgrip-agent-sdks/go/failure"  // structured failure types
)
```

A program that submits a managed `workflow.runtime` task — no workflow code, no
runtime worker — needs only `client`.

{: .note }
> The legacy `go.postgrip.io/sdk` module path is retired. Use the GitHub monorepo path shown above.

## Running against a local agent

For local development, point the SDK at a runtime service running on your machine:

```go
conn, _ := client.NewConnection(client.ConnectionOptions{
    Address: "http://127.0.0.1:4100",
})
```

`Address` defaults to PostGrip Cloud if empty. For local or self-hosted development, pass `Address` explicitly or set `POSTGRIP_AGENTORCHESTRATOR_URL`.
