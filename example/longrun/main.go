// Longrun is a stress example: it runs five workflows sequentially against
// the runtime service, where each workflow internally chains five activity
// calls separated by 13-second durable timers. Each workflow lasts ~65–75
// seconds, so a full run takes ~5–6 minutes and exercises the replay,
// suspension, and durable-timer paths repeatedly.
//
// Run:
//
//	export POSTGRIP_AGENTORCHESTRATOR_URL=https://agentorchestrator.postgrip.app
//	export POSTGRIP_AGENT_AUTH_TOKEN=...           # management bearer
//	export POSTGRIP_AGENT_ENROLLMENT_KEY=...       # local standalone only
//	go run ./example/longrun
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

const (
	stepsPerWorkflow  = 5
	workflowRuns      = 5
	stepSleepDuration = 13 * time.Second
)

func main() {
	address := envOr("POSTGRIP_AGENTORCHESTRATOR_URL", envOr("POSTGRIP_AGENT_LIVE_SERVER_URL", "https://agentorchestrator.postgrip.app"))
	authToken := os.Getenv("POSTGRIP_AGENT_AUTH_TOKEN")
	queue := envOr("POSTGRIP_AGENT_TASK_QUEUE", "go-longrun")
	agentID := envOr("POSTGRIP_AGENT_ID", "go-longrun-agent")

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
		"ProcessStep": func(ctx context.Context, args []any) (any, error) {
			if len(args) < 2 {
				return nil, fmt.Errorf("ProcessStep: expected 2 args, got %d", len(args))
			}
			name, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("ProcessStep: arg 0 is %T, expected string", args[0])
			}
			step, ok := args[1].(float64)
			if !ok {
				return nil, fmt.Errorf("ProcessStep: arg 1 is %T, expected number", args[1])
			}
			return fmt.Sprintf("processed step %d for %s", int(step), name), nil
		},
	}

	workflows := workflow.Registry{
		"LongRunningWorkflow": func(ctx workflow.Context, args []any) (any, error) {
			name := args[0].(string)
			steps := int(args[1].(float64))
			for i := 1; i <= steps; i++ {
				var status string
				if err := ctx.ExecuteActivity("ProcessStep", []any{name, i}, &status, nil); err != nil {
					return nil, err
				}
				if err := ctx.Sleep(stepSleepDuration); err != nil {
					return nil, err
				}
			}
			return fmt.Sprintf("completed %d steps for %s", steps, name), nil
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

	c := client.New(conn)
	overallStart := time.Now()

	for i := 1; i <= workflowRuns; i++ {
		runStart := time.Now()
		workflowID := fmt.Sprintf("go-longrun-%d-%d", time.Now().UnixNano(), i)
		runCtx, cancelRun := context.WithTimeout(rootCtx, 5*time.Minute)
		handle, err := c.Workflow.Start(runCtx, "LongRunningWorkflow", client.WorkflowStartOptions{
			WorkflowID: workflowID,
			TaskQueue:  queue,
			Args:       []any{fmt.Sprintf("PostGrip-%d", i), stepsPerWorkflow},
		})
		if err != nil {
			cancelRun()
			log.Fatalf("[%d/%d] Workflow.Start: %v", i, workflowRuns, err)
		}
		log.Printf("[%d/%d] started %s", i, workflowRuns, workflowID)
		var result string
		if err := handle.Result(runCtx, &result); err != nil {
			cancelRun()
			log.Fatalf("[%d/%d] handle.Result: %v", i, workflowRuns, err)
		}
		cancelRun()
		log.Printf("[%d/%d] %s -> %q (%s)", i, workflowRuns, workflowID, result, time.Since(runStart).Round(time.Second))
	}
	log.Printf("done — %d workflows in %s", workflowRuns, time.Since(overallStart).Round(time.Second))

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
