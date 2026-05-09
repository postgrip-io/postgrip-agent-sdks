# PostGrip Agent — Go SDK

Go SDK for the PostGrip Agent runtime service. Mirrors
[`agent-sdk-typescript`](https://github.com/postgrip-io/agent-sdk-typescript)
and [`agent-sdk-python`](https://github.com/postgrip-io/agent-sdk-python) so
polyglot deployments can pick whichever language fits each task fleet. Wire
shapes come from
[`agent-sdk-protocol`](https://github.com/postgrip-io/agent-sdk-protocol) so
all four repos agree on the runtime contract.

```sh
go get go.postgrip.io/sdk
```

The SDK is split into focused sub-packages — pick the ones your code needs:

| Package                                             | Purpose                                                                       |
| --------------------------------------------------- | ----------------------------------------------------------------------------- |
| [`client`](./client)     | `Connection`, `Client`, `Task` / `Workflow` / `Schedule` sub-clients, input shapes. |
| [`worker`](./worker)     | `Worker` that polls, leases, and dispatches tasks to your registered bodies.        |
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
    "log"

    "go.postgrip.io/sdk/activity"
    "go.postgrip.io/sdk/client"
    "go.postgrip.io/sdk/worker"
    "go.postgrip.io/sdk/workflow"
)

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
        AgentID:    "worker-1",
        Queue:      "default",
        Workflows:  workflow.Registry{"Greet": GreetWorkflow},
        Activities: activity.Registry{"GreetActivity": GreetActivity},
    })
    if err != nil {
        return err
    }
    return w.Run(ctx)
}

func startGreet(ctx context.Context, conn *client.Connection) error {
    c := client.New(conn)
    handle, err := c.Workflow.Start(ctx, "Greet", client.WorkflowStartOptions{
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

| Group               | Methods                                                                                |
| ------------------- | -------------------------------------------------------------------------------------- |
| `client.Task`       | `Enqueue`, `ShellExec`, `ContainerExec`, `Noop`, `Get`, `List`, `Events`, `Result`, `WatchEvents` |
| `client.Workflow`   | `Start`, `SignalWithStart`, `GetHandle`                                                |
| `client.WorkflowHandle` | `Result`, `Describe`, `Signal`, `Cancel`, `Terminate`, `History`                   |
| `client.Schedule`   | `Create`, `List`, `Get`, `Update`, `Pause`, `Unpause`, `Trigger`, `Backfill`, `Delete` |
| `workflow.Context`  | `Now`, `Logger`, `Sleep`, `ExecuteActivity`, `ExecuteChildWorkflow`, `GetSignalChannel`, `SetQueryHandler`, `SetUpdateHandler`, `Milestone`, `ContinueAsNew` |
| activity helpers    | `activity.GetInfo`, `activity.Heartbeat`, `activity.Milestone`                         |
| `worker.Worker`     | `Run`, `Shutdown`                                                                      |

## Status

- The lower-level task surface (`client.Task.*`, including `ContainerExec`),
  `client.Workflow.Start`, `WorkflowHandle.*`, and `client.Schedule.*` are at
  parity with the TS / Python SDKs.
- `worker.Worker` polls, leases, dispatches `noop`, `activity:*`, and
  `workflow:*` tasks. Activity execution honors heartbeats, milestones, and
  structured failures (`failure.Application`, `failure.Cancelled`,
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
