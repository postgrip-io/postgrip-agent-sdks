# PostGrip Agent — Go SDK

Go SDK for the PostGrip Agent runtime service. Mirrors
[`agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript)
and [`agent-sdk-python`](https://github.com/postgrip-io/agent-sdk-python) so
polyglot deployments can pick whichever language fits each task fleet. Wire
shapes come from
[`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) so
all four repos agree on the runtime contract.

```sh
go get github.com/postgrip-io/agent-sdk-go/src
```

## Quick start — enqueue a task

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/postgrip-io/agent-sdk-go/src"
)

func main() {
    conn, err := sdk.NewConnection(sdk.ConnectionOptions{
        Address:   "http://127.0.0.1:4100",
        AuthToken: os.Getenv("POSTGRIP_AGENT_AUTH_TOKEN"),
    })
    if err != nil {
        log.Fatal(err)
    }
    client := sdk.NewClient(conn)

    // shell.exec — runs whatever's on the agent's PATH
    task, err := client.Task.ShellExec(context.Background(), sdk.ShellExecInput{
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
    _, err = client.Task.ContainerExec(context.Background(), sdk.ContainerExecInput{
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
    "log"

    "github.com/postgrip-io/agent-sdk-go/src"
)

// Activities are plain Go functions. The first arg is a regular
// context.Context that honors the runtime service's cancel/heartbeat-loss
// signals; the second is the deserialized argument list.
func GreetActivity(ctx context.Context, args []any) (any, error) {
    name, _ := args[0].(string)
    return "hello, " + name, nil
}

// Workflows take an sdk.Context that gives durable Sleep, ExecuteActivity,
// ExecuteChildWorkflow, signals, queries, and updates.
func GreetWorkflow(ctx sdk.Context, args []any) (any, error) {
    var greeting string
    if err := ctx.ExecuteActivity("GreetActivity", args, &greeting, nil); err != nil {
        return nil, err
    }
    return greeting, nil
}

func runWorker(ctx context.Context, conn *sdk.Connection) error {
    w, err := sdk.NewWorker(sdk.WorkerOptions{
        Connection: conn,
        AgentID:    "worker-1",
        Queue:      "default",
        Workflows:  sdk.WorkflowRegistry{"Greet": GreetWorkflow},
        Activities: sdk.ActivityRegistry{"GreetActivity": GreetActivity},
    })
    if err != nil {
        return err
    }
    return w.Run(ctx)
}

func startGreet(ctx context.Context, conn *sdk.Connection) error {
    client := sdk.NewClient(conn)
    handle, err := client.Workflow.Start(ctx, "Greet", sdk.WorkflowStartOptions{
        Args: []any{"world"},
    })
    if err != nil {
        return err
    }
    var result string
    if err := handle.Result(ctx, &result); err != nil {
        return err
    }
    log.Println(result)
    return nil
}
```

## Surface

| Group           | Methods                                                                                |
| --------------- | -------------------------------------------------------------------------------------- |
| `Client.Task`   | `Enqueue`, `ShellExec`, `ContainerExec`, `Noop`, `Get`, `List`, `Events`, `Result`, `WatchEvents` |
| `Client.Workflow` | `Start`, `SignalWithStart`, `GetHandle`                                              |
| `WorkflowHandle` | `Result`, `Describe`, `Signal`, `Cancel`, `Terminate`, `History`                       |
| `Client.Schedule` | `Create`, `List`, `Get`, `Update`, `Pause`, `Unpause`, `Trigger`, `Backfill`, `Delete` |
| `sdk.Context` (workflow) | `Now`, `Logger`, `Sleep`, `ExecuteActivity`, `ExecuteChildWorkflow`, `GetSignalChannel`, `SetQueryHandler`, `SetUpdateHandler`, `Milestone`, `ContinueAsNew` |
| activity helpers | `GetActivityInfo`, `Heartbeat`, `ActivityMilestone`                                   |
| `Worker`        | `Run`, `Shutdown`                                                                      |

## Status

- The lower-level task surface (`Client.Task.*`, including `ContainerExec`),
  `Client.Workflow.Start`, `WorkflowHandle.*`, and `Client.Schedule.*` are at
  parity with the TS / Python SDKs.
- `Worker` polls, leases, dispatches `noop`, `activity:*`, and `workflow:*`
  tasks. Activity execution honors heartbeats, milestones, and structured
  failures (`ApplicationFailure`, `CancelledFailure`, `TimeoutFailure`).
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
    `SignalChannel` buffers on each replay. `Receive` drains the buffer
    and suspends when empty.
  - `WorkflowCancellationRequested` short-circuits subsequent commands with
    `CancelledFailure`.
- Query / update task types (`query:*`, `update:*`) still surface as
  unsupported — query/update handler invocation against a paused workflow
  isn't yet wired through Worker.

## Layout

```text
src/                  # Go package "sdk" — Connection / Client / Worker / replay runtime + tests
test/                 # reserved for future black-box / integration tests
doc/                  # reserved for longer-form prose docs
.github/workflows/    # CI: gofmt + go vet + go test
```

The package files live under `src/` to match the layout of
`agent-sdk-protocol`, `agent-sdk-typescript`, and `agent-sdk-python`.
The declared package stays `sdk`, so consumer code references
`sdk.Client` etc. — only the `/src` segment in the import path is new.

## Development

```sh
go test ./src/...
```
