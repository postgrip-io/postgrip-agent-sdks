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
// Quick start — enqueue a task:
//
//	import (
//	    "context"
//	    "go.postgrip.io/sdk/client"
//	)
//
//	conn, _ := client.NewConnection(client.ConnectionOptions{Address: "http://127.0.0.1:4100", AuthToken: token})
//	c := client.New(conn)
//	task, err := c.Task.ContainerExec(ctx, client.ContainerExecInput{
//	    Image: "node:22-alpine", Command: "node",
//	    Args:  []string{"-e", "console.log('hi')"},
//	})
//
// Quick start — run a worker that registers an activity:
//
//	import (
//	    "context"
//	    "go.postgrip.io/sdk/activity"
//	    "go.postgrip.io/sdk/worker"
//	)
//
//	w, _ := worker.New(worker.Options{
//	    Connection: conn,
//	    AgentID:    "agent-1",
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
