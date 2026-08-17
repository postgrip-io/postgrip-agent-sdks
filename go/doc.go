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
// Mirrors the TS [agent-sdk-typescript] and Python [agent-sdk-python] SDKs;
// wire shapes come from [agent-sdk-protocol] so all four SDKs agree on the
// runtime contract.
//
// Quick start — submit a workflow runtime:
//
//	import (
//	    "context"
//	    "go.postgrip.io/sdk/client"
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
//	    "go.postgrip.io/sdk/activity"
//	    "go.postgrip.io/sdk/worker"
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
// [agent-sdk-typescript]: https://github.com/postgrip-io/agent-sdk-typescript
// [agent-sdk-python]: https://github.com/postgrip-io/agent-sdk-python
// [agent-sdk-protocol]: https://github.com/postgrip-io/agent-sdk-protocol
package sdk
