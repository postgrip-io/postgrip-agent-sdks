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
all four packages agree on the runtime contract.

**Docs:** [postgrip-io.github.io/agent-sdk-go](https://postgrip-io.github.io/agent-sdk-go/) — quick start, package guide, workflow runtime model.
**API:** [pkg.go.dev/go.postgrip.io/sdk](https://pkg.go.dev/go.postgrip.io/sdk) — auto-generated godoc.

**Current release:** `v0.11.0`

```sh
go get go.postgrip.io/sdk
```

The SDK is split into focused sub-packages — pick the ones your code needs:

| Package                                             | Purpose                                                                       |
| --------------------------------------------------- | ----------------------------------------------------------------------------- |
| [`client`](./client)     | `Connection`, `Client`, `Task` / `Workflow` / `Schedule` sub-clients, input shapes. |
| [`worker`](./worker)     | Workflow runtime that polls workflow/activity task families and dispatches registered bodies. |
| [`workflow`](./workflow) | `workflow.Context`, option structs, `SignalChannel`, `workflow.Func` / `Registry`.   |
| [`activity`](./activity) | `activity.Func`, `Info`, `GetInfo`, `Heartbeat`, `Milestone`, `Stdout`, `Stderr`.     |
| [`failure`](./failure)   | Structured failures: `Application`, `Cancelled`, `Timeout`, `TaskFailed`.            |

## Quick start — submit a workflow runtime

Client-side SDK code submits a `workflow.runtime` task to an existing PostGrip
agent pool. The host agent launches your runtime process and injects delegated
credentials; the SDK does not enroll or spawn standalone PostGrip agents.

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
        // Agent token from Settings > Organization > Agent tokens.
        AuthToken: os.Getenv("POSTGRIP_AGENT_TOKEN"),
    })
    if err != nil {
        log.Fatal(err)
    }
    c := client.New(conn)

    task, err := c.Task.WorkflowRuntime(context.Background(), client.WorkflowRuntimeInput{
        Queue:        "default",
        Command:      "./workflow-runtime",
        RuntimeQueue: "default",
        Env: map[string]string{
            "POSTGRIP_EXAMPLE_RUN_LABEL": "PostGrip",
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Println("submitted workflow runtime", task.ID)
}
```

To run the runtime from an image instead of a host command, set `Image` — and
optionally an `Isolation` floor of `client.IsolationTierContainer` (the
default) or `client.IsolationTierMicroVM`:

```go
_, err = c.Task.WorkflowRuntime(ctx, client.WorkflowRuntimeInput{
    Queue:        "default",
    Image:        "ghcr.io/example/workflow-runtime:1.4.0",
    Isolation:    client.IsolationTierMicroVM,
    RuntimeQueue: "default",
})
```

`Isolation` is a floor, not an exact match: container work may be scheduled
onto a stronger tier, but microvm work is never downgraded — it only leases to
agents advertising that tier. It requires `Image`; a command-only runtime
executes directly on the agent host, which honors no isolation floor, and the
orchestrator rejects that combination at enqueue.

## Sandboxes

Sandboxes are persistent development environments assigned to one of your
agents. `client.Sandbox` covers the full lifecycle plus interactive and
non-interactive execution.

Sandbox endpoints use a **management token**, not an agent token — set
`AuthToken` on the connection.

```go
conn, err := client.NewConnection(client.ConnectionOptions{
    Address:   "https://agents.example.com",
    AuthToken: os.Getenv("POSTGRIP_TOKEN"), // opaque; no prefix to check
})
c := client.New(conn)

// Ship the working tree, then build a sandbox from it.
ws, err := c.Sandbox.UploadWorkspace(ctx, archive, "my-repo", revision)
box, err := c.Sandbox.Create(ctx, client.SandboxCreateRequest{
    Name:        "task-1",
    Image:       "postgrip/sandbox:1",
    WorkspaceID: ws.ID,
    SetupCommand: []string{"/bin/sh", "setup.sh"},
})

// Create returns as soon as the record exists; the sandbox is not up yet.
box, err = c.Sandbox.WaitUntilRunning(ctx, box.ID, client.SandboxWaitOptions{})

code, err := c.Sandbox.Exec(ctx, box.ID, []string{"go", "test", "./..."}, nil, os.Stdout)

_, err = c.Sandbox.Delete(ctx, box.ID)
```

`WaitUntilRunning` treats readiness as `observedState == running` **and**
`observedGeneration >= generation`. A "running" reading can predate a start or
stop the assigned agent hasn't observed yet, so state alone can hand back a
sandbox that is about to stop. It also returns immediately if the sandbox
reaches `failed`, carrying the sandbox's own failure message.

### Interactive sessions

```go
stream, err := c.Sandbox.OpenSandboxSession(ctx, box.ID, client.SandboxSessionKindPTY,
    client.SandboxSessionOptions{Rows: 40, Columns: 120})
defer stream.Close()

go io.Copy(stream, os.Stdin)
io.Copy(os.Stdout, stream)
```

Three properties of the relay worth knowing, because they are not obvious:

- **stdout and stderr are interleaved.** The relay carries one byte stream; a
  client cannot separate them.
- **There is no resize channel.** `Rows`/`Columns` are fixed when the session
  is created, so a terminal resize mid-session cannot reach the sandbox.
- **Exit codes arrive as the WebSocket close status** (`4000+code`), not in the
  stream. `Exec` decodes this; a close outside that range is a transport
  failure and surfaces as an error rather than as an exit code.

Single writes must stay at or below `client.SandboxRelayMaxFrameBytes` (1 MiB);
larger frames are refused locally rather than silently closing the session.

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
        // Agent token from Settings > Organization > Agent tokens.
        AuthToken: os.Getenv("POSTGRIP_AGENT_TOKEN"),
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
| `client.Task`       | `WorkflowRuntime`, `Get`, `List`, `Events`, `Result`, `WatchEvents`                 |
| `client.Workflow`   | `Start`, `SignalWithStart`, `GetHandle`                                                |
| `client.WorkflowHandle` | `Result`, `Describe`, `Signal`, `Cancel`, `Terminate`, `History`                   |
| `client.Schedule`   | `Create`, `List`, `Get`, `Update`, `Pause`, `Unpause`, `Trigger`, `Backfill`, `Delete` |
| `workflow.Context`  | `Now`, `Logger`, `Sleep`, `ExecuteActivity`, `ExecuteChildWorkflow`, `GetSignalChannel`, `SetQueryHandler`, `SetUpdateHandler`, `Milestone`, `ContinueAsNew` |
| activity helpers    | `activity.GetInfo`, `activity.Heartbeat`, `activity.Milestone`, `activity.Stdout`, `activity.Stderr` |
| `worker.Worker`     | `Run`, `Shutdown`                                                                      |

## Status

- Client-side SDK submissions should use `client.Task.WorkflowRuntime` to hand
  a managed runtime to an existing agent pool. Workflow starts, handles, and
  schedules are at parity with the TS / Python SDKs.
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
