package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// TaskClient exposes the lower-level enqueue + inspect operations the TS
// SDK calls `client.task.*`.
type TaskClient struct {
	conn *Connection
}

// Enqueue posts an arbitrary task. The Payload is JSON-marshaled before
// transit; pass any JSON-encodable value.
func (t *TaskClient) Enqueue(ctx context.Context, in EnqueueInput) (*Task, error) {
	req := EnqueueTaskRequest{
		Namespace:           in.Namespace,
		Queue:               in.Queue,
		Type:                in.Type,
		LeaseTimeoutSeconds: in.LeaseTimeoutSeconds,
	}
	if in.Payload != nil {
		req.Payload = mustJSON(in.Payload)
	}
	return t.conn.EnqueueTask(ctx, req)
}

// ShellExec enqueues a shell.exec task. The agent runs the command on its
// host using whatever's installed in the agent image.
func (t *TaskClient) ShellExec(ctx context.Context, in ShellExecInput) (*Task, error) {
	payload := ShellExecPayload{
		Command:        in.Command,
		Args:           in.Args,
		Env:            in.Env,
		WorkingDir:     in.WorkingDir,
		TimeoutSeconds: in.TimeoutSeconds,
	}
	return t.Enqueue(ctx, EnqueueInput{
		Queue:   in.Queue,
		Type:    TaskTypeShellExec,
		Payload: payload,
	})
}

// ContainerExec enqueues a container.exec task. The Go agent will launch a
// per-task container from `Image` via its docker CLI; requires the agent
// process to have DOCKER_HOST set on it.
func (t *TaskClient) ContainerExec(ctx context.Context, in ContainerExecInput) (*Task, error) {
	payload := ContainerExecPayload{
		Image:          in.Image,
		Command:        in.Command,
		Args:           in.Args,
		Env:            in.Env,
		WorkingDir:     in.WorkingDir,
		PullPolicy:     in.PullPolicy,
		TimeoutSeconds: in.TimeoutSeconds,
	}
	return t.Enqueue(ctx, EnqueueInput{
		Queue:   in.Queue,
		Type:    TaskTypeContainerExec,
		Payload: payload,
	})
}

// WorkflowRuntime enqueues a managed SDK runtime on a host-agent pool.
//
// Use this from clients that want an existing PostGrip agent to orchestrate
// SDK workflow execution. The launched runtime receives delegated
// POSTGRIP_AGENT_* credentials from the host agent; SDK code should not enroll
// its own agent.
func (t *TaskClient) WorkflowRuntime(ctx context.Context, in WorkflowRuntimeInput) (*Task, error) {
	runtimeQueue := strings.TrimSpace(in.RuntimeQueue)
	if runtimeQueue == "" {
		runtimeQueue = defaultWorkflowRuntimeQueue()
	}
	payload := WorkflowRuntimePayload{
		RuntimeID:      in.RuntimeID,
		Image:          in.Image,
		Command:        in.Command,
		Args:           in.Args,
		Env:            in.Env,
		WorkingDir:     in.WorkingDir,
		Namespace:      in.RuntimeNamespace,
		Queue:          runtimeQueue,
		PullPolicy:     in.PullPolicy,
		TimeoutSeconds: in.TimeoutSeconds,
		Isolation:      in.Isolation,
	}
	return t.Enqueue(ctx, EnqueueInput{
		Namespace:           in.Namespace,
		Queue:               in.Queue,
		Type:                TaskTypeWorkflowRuntime,
		Payload:             payload,
		LeaseTimeoutSeconds: in.LeaseTimeoutSeconds,
	})
}

// Noop enqueues a noop task — useful for smoke-testing agent connectivity.
func (t *TaskClient) Noop(ctx context.Context, queue string) (*Task, error) {
	return t.Enqueue(ctx, EnqueueInput{Queue: queue, Type: TaskTypeNoop})
}

// Get returns a single task by id.
func (t *TaskClient) Get(ctx context.Context, taskID string) (*Task, error) {
	return t.conn.GetTask(ctx, taskID)
}

// List returns tasks matching the optional filters (state=, queue=, etc.).
func (t *TaskClient) List(ctx context.Context, filters map[string]string) ([]Task, error) {
	return t.conn.ListTasks(ctx, filters)
}

// Events returns the full ordered event log for a task.
func (t *TaskClient) Events(ctx context.Context, taskID string) ([]TaskEvent, error) {
	return t.conn.GetTaskEvents(ctx, taskID)
}

func defaultWorkflowRuntimeQueue() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "postgrip-runtime-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("postgrip-runtime-%d", time.Now().UnixNano())
}

// Result blocks until the task reaches a terminal state, then unmarshals
// the result value into target (a pointer; may be nil if you don't need
// the value). Polling cadence is 500ms by default.
//
// For workflow tasks, Result waits for the workflow run to finish — the
// runtime service surfaces the workflow's terminal state through the
// task. Use WorkflowHandle.Result for the same behavior keyed by
// workflow id.
func (t *TaskClient) Result(ctx context.Context, taskID string, target any) error {
	return waitForTaskCompletion(ctx, t.conn, taskID, target)
}

// WatchEvents polls the events endpoint and pushes new events onto the
// returned channel until the context is cancelled or the task reaches a
// terminal state. The channel is closed on shutdown.
func (t *TaskClient) WatchEvents(ctx context.Context, taskID string) (<-chan TaskEvent, error) {
	out := make(chan TaskEvent, 32)
	go func() {
		defer close(out)
		seen := 0
		for {
			events, err := t.conn.GetTaskEvents(ctx, taskID)
			if err == nil {
				for i := seen; i < len(events); i++ {
					select {
					case out <- events[i]:
					case <-ctx.Done():
						return
					}
				}
				seen = len(events)
				task, taskErr := t.conn.GetTask(ctx, taskID)
				if taskErr == nil && (task.State == TaskStateSucceeded || task.State == TaskStateFailed) {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	return out, nil
}
