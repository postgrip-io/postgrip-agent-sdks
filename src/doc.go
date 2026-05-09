// Package sdk is the Go SDK for the PostGrip Agent runtime service.
//
// It mirrors the surface of the TypeScript and Python SDKs that live next to
// it (postgrip-agent/typescript and postgrip-agent/python). Customer code
// imports this package to:
//
//   - Enqueue tasks against the runtime service (shell.exec, container.exec,
//     noop, and arbitrary task types) via [Client.Task].
//   - Start, signal, query, update, cancel, and inspect workflows via
//     [Client.Workflow].
//   - Manage scheduled workflows via [Client.Schedule].
//   - Run a customer-side worker that registers Go workflow/activity
//     functions and processes leased tasks via [Worker].
//
// The companion Go binary at postgrip-agent/cmd/postgrip-agent is the
// system-side worker that handles shell.exec and container.exec execution.
// This SDK is for *customer* worker processes that want to register their own
// workflows and activities in Go.
//
// Example — enqueue a container.exec task:
//
//	conn, _ := sdk.NewConnection(sdk.ConnectionOptions{Address: "http://127.0.0.1:4100", AuthToken: token})
//	client := sdk.NewClient(conn)
//	task, err := client.Task.ContainerExec(ctx, sdk.ContainerExecInput{
//	    Image:   "node:22-alpine",
//	    Command: "node",
//	    Args:    []string{"-e", "console.log('hi')"},
//	})
//
// Example — run a worker that registers an activity:
//
//	w := sdk.NewWorker(sdk.WorkerOptions{
//	    Connection: conn,
//	    Queue:      "default",
//	    Activities: map[string]sdk.ActivityFunc{
//	        "GreetActivity": func(ctx context.Context, args []any) (any, error) {
//	            return "hello, " + args[0].(string), nil
//	        },
//	    },
//	})
//	_ = w.Run(ctx)
package sdk
