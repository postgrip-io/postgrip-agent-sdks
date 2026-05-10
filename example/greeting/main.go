// Greeting is an end-to-end runnable example for the PostGrip Agent Go SDK.
//
// It registers one activity (`Greet`) and one workflow (`GreetingWorkflow`),
// starts a worker that polls the runtime service for tasks on this example's
// queue, then enqueues a workflow execution from the same process and waits
// for the result. Useful as a smoke test of a live runtime, and as the
// minimal "client + worker in one process" shape new SDK users start from.
//
// Run:
//
//	export POSTGRIP_AGENT_LIVE_SERVER_URL=https://postgrip.app
//	export POSTGRIP_AGENT_AUTH_TOKEN=...           # management-side bearer token
//	export POSTGRIP_AGENT_ENROLLMENT_KEY=...       # local standalone only
//	go run ./example/greeting
//
// In production the PostGrip host agent launches this runtime and injects a
// delegated agent session. `POSTGRIP_AGENT_ENROLLMENT_KEY` is only for local
// standalone runs where no host agent is supervising the runtime.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.postgrip.io/sdk/activity"
	"go.postgrip.io/sdk/client"
	"go.postgrip.io/sdk/worker"
	"go.postgrip.io/sdk/workflow"
)

func main() {
	address := envOr("POSTGRIP_AGENTORCHESTRATOR_URL", envOr("POSTGRIP_AGENT_LIVE_SERVER_URL", "https://agentorchestrator.postgrip.app"))
	authToken := os.Getenv("POSTGRIP_AGENT_AUTH_TOKEN")
	queue := envOr("POSTGRIP_AGENT_TASK_QUEUE", "go-example")
	agentID := envOr("POSTGRIP_AGENT_ID", "go-example-agent")

	conn, err := client.NewConnection(client.ConnectionOptions{
		Address:        address,
		AuthToken:      authToken,
		AgentID:        agentID,
		AgentNamespace: client.DefaultNamespace,
		AgentQueue:     queue,
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}

	activities := activity.Registry{
		"Greet": func(ctx context.Context, args []any) (any, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("Greet: expected one arg, got %d", len(args))
			}
			name, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("Greet: arg 0 is %T, expected string", args[0])
			}
			return fmt.Sprintf("Hello, %s", name), nil
		},
	}

	workflows := workflow.Registry{
		"GreetingWorkflow": func(ctx workflow.Context, args []any) (any, error) {
			var greeting string
			if err := ctx.ExecuteActivity("Greet", args, &greeting, nil); err != nil {
				return nil, err
			}
			return greeting, nil
		},
	}

	w, err := worker.New(worker.Options{
		Connection: conn,
		AgentID:    agentID,
		Queue:      queue,
		Workflows:  workflows,
		Activities: activities,
	})
	if err != nil {
		log.Fatalf("worker.New: %v", err)
	}

	rootCtx, cancelRoot := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelRoot()

	workerErr := make(chan error, 1)
	go func() { workerErr <- w.Run(rootCtx) }()

	startCtx, cancelStart := context.WithTimeout(rootCtx, 60*time.Second)
	defer cancelStart()

	c := client.New(conn)
	workflowID := fmt.Sprintf("go-example-%d", time.Now().UnixNano())
	handle, err := c.Workflow.Start(startCtx, "GreetingWorkflow", client.WorkflowStartOptions{
		WorkflowID: workflowID,
		TaskQueue:  queue,
		Args:       []any{"PostGrip"},
	})
	if err != nil {
		log.Fatalf("Workflow.Start: %v", err)
	}
	log.Printf("started workflow id=%s task=%s", handle.WorkflowID, handle.TaskID)

	var greeting string
	if err := handle.Result(startCtx, &greeting); err != nil {
		log.Fatalf("handle.Result: %v", err)
	}
	log.Printf("workflow result: %q", greeting)

	cancelRoot()
	if err := w.Shutdown(context.Background(), 5*time.Second); err != nil {
		log.Printf("worker shutdown: %v", err)
	}
	if err := <-workerErr; err != nil && err != context.Canceled {
		log.Printf("worker exit: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
