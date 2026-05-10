// Greeting is an end-to-end runnable example for the PostGrip Agent Go SDK.
//
// Running it locally submits a workflow.runtime task to an existing agent
// pool. When the host agent launches the runtime, it registers one activity
// (`Greet`) and one workflow (`GreetingWorkflow`), then runs that workflow
// with delegated agent credentials.
//
// Run:
//
//	export POSTGRIP_AGENT_LIVE_SERVER_URL=https://postgrip.app
//	export POSTGRIP_AGENT_AUTH_TOKEN=...           # management-side bearer token
//	export SDK_EXAMPLE_RUNTIME_ARGS_JSON='["-lc","./path/to/greeting"]'
//	go run ./example/greeting
//
// The SDK does not enroll standalone agents; host agents inject delegated
// managed-runtime credentials.
package main

import (
	"context"
	"encoding/json"
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
	tenantID := os.Getenv("POSTGRIP_AGENT_TENANT_ID")
	queue := envOr("POSTGRIP_AGENT_TASK_QUEUE", "go-example")
	agentID := envOr("POSTGRIP_AGENT_ID", "go-example-agent")

	conn, err := client.NewConnection(client.ConnectionOptions{
		Address:        address,
		AuthToken:      authToken,
		Headers:        tenantHeader(tenantID),
		AgentID:        agentID,
		AgentNamespace: client.DefaultNamespace,
		AgentQueue:     queue,
	})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}

	if os.Getenv("POSTGRIP_AGENT_MANAGED_RUNTIME") != "true" {
		submitManagedRuntime(context.Background(), conn)
		return
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
		Args:       []any{envOr("SDK_EXAMPLE_GREETING_NAME", "PostGrip")},
		UI: &client.WorkflowUIMetadata{
			DisplayName: "Go greeting example",
			Description: "Started from the Go SDK greeting example.",
			Details: map[string]any{
				"sdk": "go",
			},
			Tags: []string{"sdk-ui-demo", "go"},
		},
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

func tenantHeader(tenantID string) map[string]string {
	if tenantID == "" {
		return nil
	}
	return map[string]string{"x-postgrip-agent-tenant-id": tenantID}
}

func submitManagedRuntime(ctx context.Context, conn *client.Connection) {
	argsJSON := envOr("SDK_EXAMPLE_RUNTIME_ARGS_JSON", os.Getenv("POSTGRIP_EXAMPLE_RUNTIME_ARGS_JSON"))
	if argsJSON == "" {
		log.Fatalf("SDK_EXAMPLE_RUNTIME_ARGS_JSON is required to submit this runtime to an agent pool")
	}
	var args []string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		log.Fatalf("invalid SDK_EXAMPLE_RUNTIME_ARGS_JSON: %v", err)
	}
	queue := envOr("SDK_EXAMPLE_RUNTIME_QUEUE", envOr("POSTGRIP_EXAMPLE_RUNTIME_QUEUE", client.DefaultQueue))
	runtimeQueue := envOr("SDK_EXAMPLE_RUNTIME_CHILD_QUEUE", envOr("POSTGRIP_EXAMPLE_RUNTIME_CHILD_QUEUE", queue))
	task, err := client.New(conn).Task.WorkflowRuntime(ctx, client.WorkflowRuntimeInput{
		Namespace:           client.DefaultNamespace,
		Queue:               queue,
		Command:             envOr("SDK_EXAMPLE_RUNTIME_COMMAND", envOr("POSTGRIP_EXAMPLE_RUNTIME_COMMAND", "sh")),
		Args:                args,
		RuntimeQueue:        runtimeQueue,
		WorkingDir:          envOr("SDK_EXAMPLE_RUNTIME_WORKING_DIR", os.Getenv("POSTGRIP_EXAMPLE_RUNTIME_WORKING_DIR")),
		TimeoutSeconds:      300,
		LeaseTimeoutSeconds: 30,
		Env: map[string]string{
			"SDK_EXAMPLE_GREETING_NAME": envOr("SDK_EXAMPLE_GREETING_NAME", "PostGrip"),
		},
	})
	if err != nil {
		log.Fatalf("submit workflow runtime: %v", err)
	}
	log.Printf("submitted managed workflow runtime task=%s queue=%s runtime_queue=%s", task.ID, queue, runtimeQueue)
}
