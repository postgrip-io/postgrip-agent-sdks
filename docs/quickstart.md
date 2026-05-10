---
title: Quick start
layout: default
nav_order: 3
---

# Quick start

Two examples: enqueueing a task as a client, and running a worker that registers a workflow + activity.

## Enqueue a task

A program that just hands work to the runtime service needs only the `client` package.

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

    // container.exec — runs in a per-task container the agent launches via
    // its docker CLI. Polyglot without bloating the agent image.
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

{: .note }
> `container.exec` requires the agent process to have `DOCKER_HOST` set so the container runs through the worker stack's docker socket proxy. Containers run with `--rm --network=none`, no host volume mounts, and the same env-key allowlist as `shell.exec`.

## Wait for a result

`TaskClient.Result` blocks until the task reaches a terminal state and unmarshals the result value into your target:

```go
var output map[string]any
if err := c.Task.Result(ctx, task.ID, &output); err != nil {
    log.Fatal(err)
}
log.Println("result:", output)
```

Polling cadence is 500ms; pass a context with a deadline if you want to cap how long you wait.

## Run a worker

Workers register workflow and activity functions, then poll the runtime service for tasks to dispatch.

```go
package main

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

func main() {
    // This process is launched by a PostGrip host agent as workflow.runtime.
    // The host injects POSTGRIP_AGENT_ID, POSTGRIP_AGENT_ACCESS_TOKEN,
    // POSTGRIP_AGENT_REFRESH_TOKEN, and POSTGRIP_AGENT_SIGNING_PRIVATE_KEY.
    conn, err := client.NewConnection(client.ConnectionOptions{
        Address: "http://127.0.0.1:4100",
    })
    if err != nil {
        log.Fatal(err)
    }

    w, err := worker.New(worker.Options{
        Connection: conn,
        Queue:      "default",
        Workflows:  workflow.Registry{"Greet": GreetWorkflow},
        Activities: activity.Registry{"GreetActivity": GreetActivity},
    })
    if err != nil {
        log.Fatal(err)
    }
    if err := w.Run(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

The worker loops forever inside the managed runtime, leasing tasks from the configured queue, heartbeating each leased task on a timer derived from its lease timeout, and dispatching to your registered functions. `Run` returns when the context is cancelled or `Worker.Shutdown` is called. Client code should submit the runtime with `client.Task.WorkflowRuntime`; the SDK does not enroll standalone agents.

## Start a workflow

From the client side, start the workflow you registered above:

```go
c := client.New(conn)
handle, err := c.Workflow.Start(ctx, "Greet", client.WorkflowStartOptions{
    Args: []any{"world"},
})
if err != nil {
    log.Fatal(err)
}

var greeting string
if err := handle.Result(ctx, &greeting); err != nil {
    log.Fatal(err)
}
log.Println(greeting) // hello, world
```

`Start` returns a `WorkflowHandle` you can use to wait for the result, send signals, query state, cancel, terminate, or read the durable history.

## Where to next

- [Packages]({{ "/packages" | relative_url }}) — what each sub-package owns and how they wire together.
- [Workflow runtime]({{ "/workflow-runtime" | relative_url }}) — the durable replay model: how `Sleep` / `ExecuteActivity` / signals work under the hood, what determinism means, and when to use `ContinueAsNew`.
