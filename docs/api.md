---
title: API reference
layout: default
nav_order: 6
---

# API reference

The full, auto-generated Go API documentation lives on [pkg.go.dev](https://pkg.go.dev/go.postgrip.io/sdk). Every public type, function, method, and constant is rendered from the source godoc comments and stays in sync with each released version.

[Open pkg.go.dev/go.postgrip.io/sdk →](https://pkg.go.dev/go.postgrip.io/sdk){: .btn .btn-primary .fs-5 .mb-4 .mb-md-0 .mr-2 }

## Direct links to each package

| Package    | godoc                                                                          |
|:-----------|:--------------------------------------------------------------------------------|
| `client`   | [pkg.go.dev/go.postgrip.io/sdk/client](https://pkg.go.dev/go.postgrip.io/sdk/client)    |
| `worker`   | [pkg.go.dev/go.postgrip.io/sdk/worker](https://pkg.go.dev/go.postgrip.io/sdk/worker)    |
| `workflow` | [pkg.go.dev/go.postgrip.io/sdk/workflow](https://pkg.go.dev/go.postgrip.io/sdk/workflow) |
| `activity` | [pkg.go.dev/go.postgrip.io/sdk/activity](https://pkg.go.dev/go.postgrip.io/sdk/activity) |
| `failure`  | [pkg.go.dev/go.postgrip.io/sdk/failure](https://pkg.go.dev/go.postgrip.io/sdk/failure)  |

## Source

The repository is at [github.com/postgrip-io/agent-sdk-go](https://github.com/postgrip-io/agent-sdk-go). Issues, PRs, and release notes live there.

## Versioning

The SDK uses Go modules and semantic versioning. Pin to a specific version in your `go.mod`:

```sh
go get go.postgrip.io/sdk@v0.11.0
```

While the version stays at `v0.x.x`, the public API may evolve. Pin to a tag for stability and read release notes when upgrading.

[All releases →](https://github.com/postgrip-io/agent-sdk-go/releases){: .btn .fs-5 .mb-4 .mb-md-0 }
