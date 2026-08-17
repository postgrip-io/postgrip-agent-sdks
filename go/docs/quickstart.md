---
title: Quick start
layout: default
nav_order: 3
---

# Quick start

Two pieces make up the normal SDK flow: a client submits a managed
`workflow.runtime` task to an existing PostGrip agent pool, and that managed
runtime registers workflow and activity functions.

## Submit a workflow runtime

A client process uses an Agent token from Settings > Organization > Agent tokens
and submits a `workflow.runtime` task. The host PostGrip agent launches the
runtime process and injects delegated credentials.

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/postgrip-io/postgrip-agent-sdks/go/client"
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

{: .note }
> The SDK does not enroll standalone PostGrip agents. It submits workflow runtimes to agent pools that are already enrolled in PostGrip.

## Run a managed workflow runtime worker

The runtime process is launched by a host agent from the `workflow.runtime`
task. Inside that process, a worker registers workflow and activity functions,
then polls for workflow/activity tasks using delegated credentials.

```go
package main

import (
    "context"
    "log"

    "github.com/postgrip-io/postgrip-agent-sdks/go/activity"
    "github.com/postgrip-io/postgrip-agent-sdks/go/client"
    "github.com/postgrip-io/postgrip-agent-sdks/go/worker"
    "github.com/postgrip-io/postgrip-agent-sdks/go/workflow"
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
    conn, err := client.NewConnection(client.ConnectionOptions{})
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

The worker loops forever inside the managed runtime, leasing tasks from the configured queue, heartbeating each leased task on a timer derived from its lease timeout, and dispatching to your registered functions. `Run` returns when the context is cancelled or `Worker.Shutdown` is called. Client code should submit the runtime with `client.Task.WorkflowRuntime`.

## Start a workflow

From inside the managed runtime, start the workflow you registered above:

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
