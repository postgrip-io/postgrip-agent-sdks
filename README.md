# PostGrip Agent — Go SDK

[![Docs](https://img.shields.io/badge/docs-postgrip--io.github.io-2563EB?logo=github&logoColor=white)](https://postgrip-io.github.io/agent-sdk-go/)
[![Go Reference](https://pkg.go.dev/badge/go.postgrip.io/sdk.svg)](https://pkg.go.dev/go.postgrip.io/sdk)
[![CI](https://github.com/postgrip-io/agent-sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/postgrip-io/agent-sdk-go/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/postgrip-io/agent-sdk-go?label=release&color=2563EB)](https://github.com/postgrip-io/agent-sdk-go/releases)
[![License](https://img.shields.io/github/license/postgrip-io/agent-sdk-go?color=2563EB)](LICENSE)

Go SDK for defining, submitting, and executing PostGrip workflows. In production,
SDK workflow runtimes are supervised by an existing PostGrip agent: the host
agent launches the runtime, injects delegated credentials, and keeps generic
operational tasks separate from workflow/activity polling. Mirrors
[`agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript)
and [`agent-sdk-python`](https://github.com/postgrip-io/agent-sdk-python) so
polyglot deployments can pick whichever language fits each task fleet. Wire
shapes come from
[`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) so
all four repos agree on the runtime contract.

**Docs:** [postgrip-io.github.io/agent-sdk-go](https://postgrip-io.github.io/agent-sdk-go) — quick start, package guide, workflow runtime model.
**API:** [pkg.go.dev/go.postgrip.io/sdk](https://pkg.go.dev/go.postgrip.io/sdk) — auto-generated godoc.

```sh
go get go.postgrip.io/sdk
```

The SDK is split into focused sub-packages — pick the ones your code needs:

| Package                                             | Purpose                                                                       |
| --------------------------------------------------- | ----------------------------------------------------------------------------- |
| [`client`](./client)     | `Connection`, `Client`, `Task` / `Workflow` / `Schedule` sub-clients, input shapes. |
| [`worker`](./worker)     | Workflow runtime that polls workflow/activity task families and dispatches registered bodies. |
| [`workflow`](./workflow) | `workflow.Context`, option structs, `SignalChannel`, `workflow.Func` / `Registry`.   |
| [`activity`](./activity) | `activity.Func`, `Info`, `GetInfo`, `Heartbeat`, `Milestone`.                        |
| [`failure`](./failure)   | Structured failures: `Application`, `Cancelled`, `Timeout`, `TaskFailed`.            |

## Quick start — enqueue a task

```go
package main

import (
    "context"
    "log"
    "os"

    "go.postgrip.io/sdk/client"
)

func main() {
    conn, err := client.NewConnection(client.ConnectionOptions{
        Address:   "http://127.0.0.1:4100",
        AuthToken: os.Getenv("POSTGRIP_AGENT_AUTH_TOKEN"),
    })
    if err != nil {
        log.Fatal(err)
    }
    c := client.New(conn)

    // shell.exec — runs whatever's on the agent's PATH.
    task, err := c.Task.ShellExec(context.Background(), client.ShellExecInput{
        Queue:   "default",
        Command: "echo",
        Args:    []string{"hello from agent"},
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Println("enqueued", task.ID)

    // container.exec — runs in a per-task container the Go agent launches
    // via its docker CLI. Polyglot without bloating the agent image.
    _, err = c.Task.ContainerExec(context.Background(), client.ContainerExecInput{
        Queue:   "default",
        Image:   "node:22-alpine",
        Command: "node",
        Args:    []string{"-e", "console.log('hi from node')"},
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

`container.exec` requires the agent process to have `DOCKER_HOST` set so the
container runs through the worker stack's docker socket proxy. The container
runs with `--rm --network=none`, no host volume mounts, and the same env-key
allowlist as `shell.exec`.

## Workflows and activities

```go
import (
    "context"

    "go.postgrip.io/sdk/activity"
    "go.postgrip.io/sdk/client"
    "go.postgrip.io/sdk/worker"
    "go.postgrip.io/sdk/workflow"
)

// This process is launched by a PostGrip host agent from a workflow.runtime task.
// The host injects POSTGRIP_AGENT_ID and delegated runtime credentials.

// Activities are plain Go functions. The first arg is a regular
// context.Context that honors the runtime service's cancel/heartbeat-loss
// signals; the second is the deserialized argument list.
func GreetActivity(ctx context.Context, args []any) (any, error) {
    name, _ := args[0].(string)
    return "hello, " + name, nil
}

// Workflows take a workflow.Context that gives durable Sleep,
// ExecuteActivity, ExecuteChildWorkflow, signals, queries, and updates.
func GreetWorkflow(ctx workflow.Context, args []any) (any, error) {
    var greeting string
    if err := ctx.ExecuteActivity("GreetActivity", args, &greeting, nil); err != nil {
        return nil, err
    }
    return greeting, nil
}

func runWorker(ctx context.Context, conn *client.Connection) error {
    w, err := worker.New(worker.Options{
        Connection: conn,
        Queue:      "default",
        Workflows:  workflow.Registry{"Greet": GreetWorkflow},
        Activities: activity.Registry{"GreetActivity": GreetActivity},
    })
    if err != nil {
        return err
    }
    return w.Run(ctx)
}
```

Workflow starts can attach SDK-owned console metadata. The SDK persists it inside
workflow memo as `postgrip.ui`, so the Agents activity tab can show a friendly
label, description, details, and tags while `Memo` remains available for your own
data.

```go
handle, err := c.Workflow.Start(ctx, "Greet", client.WorkflowStartOptions{
    WorkflowID: "greet-postgrip",
    TaskQueue:  "default",
    Args:       []any{"PostGrip"},
    UI: &client.WorkflowUIMetadata{
        DisplayName: "Say hello to PostGrip",
        Description: "Shown on the PostGrip Agents activity tab.",
        Details: map[string]any{
            "sdk": "go",
        },
        Tags: []string{"demo"},
    },
})
```

Submit that runtime to an existing agent pool from your client process:

```go
import (
    "context"
    "os"

    "go.postgrip.io/sdk/client"
)

func submitRuntime(ctx context.Context) error {
    conn, err := client.NewConnection(client.ConnectionOptions{
        Address:   "https://agentorchestrator.postgrip.app",
        AuthToken: os.Getenv("POSTGRIP_AGENT_AUTH_TOKEN"),
    })
    if err != nil {
        return err
    }
    c := client.New(conn)
    _, err = c.Task.WorkflowRuntime(ctx, client.WorkflowRuntimeInput{
        Queue:        "default",
        Command:      "./workflow-runtime",
        RuntimeQueue: "default",
        Env: map[string]string{
            "POSTGRIP_EXAMPLE_RUN_LABEL": "PostGrip",
        },
    })
    return err
}
```

## Surface

| Group               | Methods                                                                                |
| ------------------- | -------------------------------------------------------------------------------------- |
| `client.Task`       | `Enqueue`, `WorkflowRuntime`, `ShellExec`, `ContainerExec`, `Noop`, `Get`, `List`, `Events`, `Result`, `WatchEvents` |
| `client.Workflow`   | `Start`, `SignalWithStart`, `GetHandle`                                                |
| `client.WorkflowHandle` | `Result`, `Describe`, `Signal`, `Cancel`, `Terminate`, `History`                   |
| `client.Schedule`   | `Create`, `List`, `Get`, `Update`, `Pause`, `Unpause`, `Trigger`, `Backfill`, `Delete` |
| `workflow.Context`  | `Now`, `Logger`, `Sleep`, `ExecuteActivity`, `ExecuteChildWorkflow`, `GetSignalChannel`, `SetQueryHandler`, `SetUpdateHandler`, `Milestone`, `ContinueAsNew` |
| activity helpers    | `activity.GetInfo`, `activity.Heartbeat`, `activity.Milestone`                         |
| `worker.Worker`     | `Run`, `Shutdown`                                                                      |

## Status

- The lower-level task surface (`client.Task.*`, including `WorkflowRuntime`
  and `ContainerExec`), `client.Workflow.Start`, `WorkflowHandle.*`, and `client.Schedule.*` are at
  parity with the TS / Python SDKs.
- `worker.Worker` polls workflow task families (`workflow:*`, `activity:*`,
  `query:*`, `update:*`). Activity execution honors heartbeats, milestones,
  and structured failures (`failure.Application`, `failure.Cancelled`,
  `failure.Timeout`).
- Workflow execution does deterministic replay against durable history,
  matching the TS / Python SDKs:
  - Each lease, the Worker fetches the workflow's full history, builds a
    replay state, and runs the workflow body. Calls to `Sleep`,
    `ExecuteActivity`, and `ExecuteChildWorkflow` consult the replay first
    — completed commands return their persisted results, in-flight ones
    return a suspend sentinel, and history exhaustion schedules a fresh
    command and suspends.
  - Suspension marks the workflow task **blocked** rather than failed; the
    runtime service redelivers when dependencies resolve and the body
    re-runs from the top with fuller history.
  - Determinism violations (workflow asks for a different activity name /
    child workflow type / timer duration than what's recorded) raise a
    non-retryable `WorkflowDeterminismViolation` failure so misbehaving
    workflow code is caught early.
  - Signals delivered via `WorkflowSignaled` history events are seeded into
    `workflow.SignalChannel` buffers on each replay. `Receive` drains the
    buffer and suspends when empty.
  - `WorkflowCancellationRequested` short-circuits subsequent commands with
    `failure.Cancelled`.
- Query / update task types (`query:*`, `update:*`) still surface as
  unsupported — query/update handler invocation against a paused workflow
  isn't yet wired through Worker.

## Layout

```text
client/             # Connection, Client, Task / Workflow / Schedule sub-clients
worker/             # Worker, workflow.Context implementation, dispatch
workflow/           # Context interface, options, Func, SignalChannel
activity/           # Func, Info, GetInfo, Heartbeat, Milestone
failure/            # Application / Cancelled / Timeout / TaskFailed
internal/replay/    # Workflow replay engine (cursor over durable history)
test/               # Reserved for future black-box / integration tests
doc/                # Reserved for longer-form prose docs
.github/workflows/  # CI: gofmt + go vet + go test
```

## Development

```sh
go test ./...
```
