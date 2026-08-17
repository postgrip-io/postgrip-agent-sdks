// Package sdk is a placeholder for the PostGrip Agent Go SDK module. The
// real entry points live in sub-packages — pick the ones your code needs:
//
//   - [client] — Connection, Client, Task / Workflow / Schedule sub-clients,
//     and the input shapes for enqueueing tasks and starting workflows.
//   - [worker] — Worker that polls the runtime service and dispatches leased
//     tasks to registered workflow.Func and activity.Func bodies.
//   - [workflow] — workflow.Context, option structs (ActivityOptions,
//     ChildWorkflowOptions, ContinueAsNewOptions), SignalChannel, and the
//     workflow.Func / Registry shapes.
//   - [activity] — activity.Func, Info, GetInfo, Heartbeat, Milestone for
//     customer activity bodies.
//   - [failure] — structured failure types (Application, Cancelled, Timeout,
//     TaskFailed) and the FromInfo / ToInfo wire converters.
//
// Mirrors the [TypeScript SDK] and [Python SDK]; wire shapes come from the
// shared [protocol package] so all four packages agree on the
// runtime contract.
//
// Quick start — submit a workflow runtime:
//
//	import (
//	    "context"
//	    "github.com/postgrip-io/postgrip-agent-sdks/go/client"
//	)
//
//	conn, _ := client.NewConnection(client.ConnectionOptions{Address: "http://127.0.0.1:4100", AuthToken: token})
//	c := client.New(conn)
//	task, err := c.Task.WorkflowRuntime(ctx, client.WorkflowRuntimeInput{
//	    Queue: "default", Command: "./workflow-runtime", RuntimeQueue: "default",
//	})
//
// Quick start — run a managed workflow runtime worker:
//
//	import (
//	    "context"
//	    "github.com/postgrip-io/postgrip-agent-sdks/go/activity"
//	    "github.com/postgrip-io/postgrip-agent-sdks/go/worker"
//	)
//
//	w, _ := worker.New(worker.Options{
//	    Connection: conn,
//	    Queue:      "default",
//	    Activities: activity.Registry{
//	        "GreetActivity": func(ctx context.Context, args []any) (any, error) {
//	            return "hello, " + args[0].(string), nil
//	        },
//	    },
//	})
//	_ = w.Run(ctx)
//
// [TypeScript SDK]: https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/typescript
// [Python SDK]: https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/python
// [protocol package]: https://github.com/postgrip-io/postgrip-agent-sdks/tree/main/protocol
package sdk
