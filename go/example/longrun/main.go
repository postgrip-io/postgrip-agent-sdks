// Longrun is a stress example. Running it locally submits a workflow.runtime
// task to an existing agent pool. When the host agent launches the runtime,
// it runs five workflows sequentially, where each workflow internally chains
// five activity calls separated by 13-second durable timers.
//
// Run:
//
//	cp example/.env.example .env
//	# edit .env and set POSTGRIP_AGENT_TOKEN to your Agent token
//	go run ./example/longrun
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.postgrip.io/sdk/activity"
	"go.postgrip.io/sdk/client"
	"go.postgrip.io/sdk/worker"
	"go.postgrip.io/sdk/workflow"
)

const (
	defaultStepsPerWorkflow = 5
	defaultWorkflowRuns     = 5
	defaultStepSleepSeconds = 13
	defaultRunTimeout       = 5 * time.Minute
	defaultRuntimeImage     = "golang:1.25-bookworm"
	defaultRuntimeCommand   = "sh"
	defaultRuntimeRef       = "8b4e5df94c646350b51c0162d7030b1d38830f73"
)

var defaultRuntimeArgs = []string{
	"-lc",
	`git init /tmp/agent-sdk-go && cd /tmp/agent-sdk-go && git remote add origin https://github.com/postgrip-io/agent-sdk-go && git fetch --depth 1 origin "${SDK_EXAMPLE_RUNTIME_REF:-8b4e5df94c646350b51c0162d7030b1d38830f73}" && git checkout --detach FETCH_HEAD && PATH=/usr/local/go/bin:$PATH go run ./example/longrun`,
}

func main() {
	loadExampleEnv()

	address := envOr("POSTGRIP_AGENTORCHESTRATOR_URL", envOr("POSTGRIP_AGENT_LIVE_SERVER_URL", "https://agentorchestrator.postgrip.app"))
	authToken := os.Getenv("POSTGRIP_AGENT_TOKEN")
	queue := envOr("POSTGRIP_AGENT_TASK_QUEUE", "go-longrun")
	agentID := envOr("POSTGRIP_AGENT_ID", "go-longrun-agent")
	stepsPerWorkflow := envIntAny([]string{"POSTGRIP_EXAMPLE_STEPS", "SDK_EXAMPLE_STEPS"}, defaultStepsPerWorkflow)
	workflowRuns := envIntAny([]string{"POSTGRIP_EXAMPLE_WORKFLOW_RUNS", "SDK_EXAMPLE_WORKFLOW_RUNS"}, defaultWorkflowRuns)
	stepSleepDuration := time.Duration(envIntAny([]string{"POSTGRIP_EXAMPLE_STEP_SLEEP_SECONDS", "SDK_EXAMPLE_STEP_SLEEP_SECONDS"}, defaultStepSleepSeconds)) * time.Second
	stepSleepSeconds := int(stepSleepDuration / time.Second)
	runLabel := envOrAny([]string{"POSTGRIP_EXAMPLE_RUN_LABEL", "SDK_EXAMPLE_RUN_LABEL"}, "PostGrip")
	runTimeout := time.Duration(envIntAny([]string{"POSTGRIP_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS", "SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS"}, int(defaultRunTimeout/time.Second))) * time.Second

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

	if os.Getenv("POSTGRIP_AGENT_MANAGED_RUNTIME") != "true" {
		submitManagedRuntime(rootSignalContext(), conn)
		return
	}

	activities := activity.Registry{
		"processStep": func(ctx context.Context, args []any) (any, error) {
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
			result := fmt.Sprintf("processed step %d for %s", int(step), name)
			if err := activity.Stdout(ctx, result+"\n", activity.OutputOptions{
				Stage: "processStep",
				Details: map[string]any{
					"step": int(step),
					"name": name,
				},
			}); err != nil {
				return nil, err
			}
			return result, nil
		},
	}

	workflows := workflow.Registry{
		"LongRunningWorkflow": func(ctx workflow.Context, args []any) (any, error) {
			name := args[0].(string)
			steps := int(args[1].(float64))
			for i := 1; i <= steps; i++ {
				var status string
				if err := ctx.ExecuteActivity("processStep", []any{name, i}, &status, nil); err != nil {
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
		workflowID := fmt.Sprintf("go-longrun-%s-%d-%d", slug(runLabel), time.Now().UnixNano(), i)
		runCtx, cancelRun := context.WithTimeout(rootCtx, runTimeout)
		handle, err := c.Workflow.Start(runCtx, "LongRunningWorkflow", client.WorkflowStartOptions{
			WorkflowID: workflowID,
			TaskQueue:  queue,
			Args:       []any{fmt.Sprintf("%s-%d", runLabel, i), stepsPerWorkflow},
			UI: &client.WorkflowUIMetadata{
				DisplayName: fmt.Sprintf("%s long run #%d", runLabel, i),
				Description: fmt.Sprintf("Runs %d steps with %ds sleeps between steps.", stepsPerWorkflow, stepSleepSeconds),
				Details: map[string]any{
					"sdk":          "go",
					"steps":        stepsPerWorkflow,
					"sleepSeconds": stepSleepSeconds,
				},
				Tags: []string{"sdk-ui-demo", "go"},
			},
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

func envOrAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}

func envIntAny(keys []string, fallback int) int {
	for _, key := range keys {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			log.Printf("invalid %s=%q; using %d", key, value, fallback)
			return fallback
		}
		return parsed
	}
	return fallback
}

func envBoolAny(keys []string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func rootSignalContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx
}

func submitManagedRuntime(ctx context.Context, conn *client.Connection) {
	command := envOr("SDK_EXAMPLE_RUNTIME_COMMAND", envOr("POSTGRIP_EXAMPLE_RUNTIME_COMMAND", defaultRuntimeCommand))
	args := envJSONStringsAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_ARGS_JSON", "SDK_EXAMPLE_RUNTIME_ARGS_JSON"}, defaultRuntimeArgs)
	runLabel := envOrAny([]string{"POSTGRIP_EXAMPLE_RUN_LABEL", "SDK_EXAMPLE_RUN_LABEL"}, "PostGrip")
	runtimeQueue := envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_QUEUE", "SDK_EXAMPLE_RUNTIME_QUEUE"}, client.DefaultQueue)
	childQueue := envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_CHILD_QUEUE", "SDK_EXAMPLE_RUNTIME_CHILD_QUEUE"}, fmt.Sprintf("sdk-runtime-%s-%d", slug(runLabel), time.Now().UnixNano()))
	task, err := client.New(conn).Task.WorkflowRuntime(ctx, client.WorkflowRuntimeInput{
		Namespace:      client.DefaultNamespace,
		Queue:          runtimeQueue,
		Image:          envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_IMAGE", "SDK_EXAMPLE_RUNTIME_IMAGE"}, defaultRuntimeImage),
		Command:        command,
		Args:           args,
		RuntimeQueue:   childQueue,
		WorkingDir:     envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_WORKING_DIR", "SDK_EXAMPLE_RUNTIME_WORKING_DIR"}, ""),
		PullPolicy:     envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_PULL_POLICY", "SDK_EXAMPLE_RUNTIME_PULL_POLICY"}, ""),
		TimeoutSeconds: envIntAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_TIMEOUT_SECONDS", "SDK_EXAMPLE_RUNTIME_TIMEOUT_SECONDS"}, 900),
		Env: map[string]string{
			"SDK_EXAMPLE_RUN_LABEL":                runLabel,
			"SDK_EXAMPLE_WORKFLOW_RUNS":            fmt.Sprint(envIntAny([]string{"POSTGRIP_EXAMPLE_WORKFLOW_RUNS", "SDK_EXAMPLE_WORKFLOW_RUNS"}, defaultWorkflowRuns)),
			"SDK_EXAMPLE_STEPS":                    fmt.Sprint(envIntAny([]string{"POSTGRIP_EXAMPLE_STEPS", "SDK_EXAMPLE_STEPS"}, defaultStepsPerWorkflow)),
			"SDK_EXAMPLE_STEP_SLEEP_SECONDS":       fmt.Sprint(envIntAny([]string{"POSTGRIP_EXAMPLE_STEP_SLEEP_SECONDS", "SDK_EXAMPLE_STEP_SLEEP_SECONDS"}, defaultStepSleepSeconds)),
			"SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS": fmt.Sprint(envIntAny([]string{"POSTGRIP_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS", "SDK_EXAMPLE_WORKFLOW_TIMEOUT_SECONDS"}, int(defaultRunTimeout/time.Second))),
			"SDK_EXAMPLE_RUNTIME_REF":              envOrAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_REF", "SDK_EXAMPLE_RUNTIME_REF"}, defaultRuntimeRef),
		},
		LeaseTimeoutSeconds: envIntAny([]string{"POSTGRIP_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS", "SDK_EXAMPLE_RUNTIME_LEASE_TIMEOUT_SECONDS"}, 30),
	})
	if err != nil {
		log.Fatalf("submit workflow runtime: %v", err)
	}
	log.Printf("submitted managed workflow runtime task=%s queue=%s child_queue=%s command=%s args=%v", task.ID, runtimeQueue, childQueue, command, args)
}

func envJSONStringsAny(keys []string, fallback []string) []string {
	for _, key := range keys {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		var out []string
		if err := json.Unmarshal([]byte(value), &out); err != nil {
			log.Fatalf("invalid %s: %v", key, err)
		}
		return out
	}
	return fallback
}
