package client

import (
	"context"
	"errors"
	"strings"
)

// EnqueueTask enqueues a single task. Use TaskClient.Enqueue / ShellExec /
// ContainerExec / Noop helpers for ergonomic construction; this is the raw
// transport call.
func (c *Connection) EnqueueTask(ctx context.Context, req EnqueueTaskRequest) (*Task, error) {
	if runtimeOnlyTaskType(req.Type) && !c.hasAgentRuntimeCredentials() {
		return nil, errors.New("postgrip-agent: workflow tasks can only be enqueued from a managed runtime; submit workflow.runtime to an agent pool")
	}
	var out Task
	if err := c.doOpenAPI(ctx, openAPIEnqueueTask, nil, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func runtimeOnlyTaskType(taskType string) bool {
	switch strings.TrimSpace(taskType) {
	case TaskTypeTimer:
		return true
	}
	return strings.HasPrefix(taskType, TaskTypePrefixWorkflow) ||
		strings.HasPrefix(taskType, TaskTypePrefixActivity) ||
		strings.HasPrefix(taskType, TaskTypePrefixQuery) ||
		strings.HasPrefix(taskType, TaskTypePrefixUpdate)
}

// ListTasks returns tasks matching the optional filters.
func (c *Connection) ListTasks(ctx context.Context, params map[string]string) ([]Task, error) {
	var out []Task
	if err := c.doOpenAPI(ctx, openAPIListTasks, nil, queryValues(params), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTask fetches a single task by id.
func (c *Connection) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var out Task
	if err := c.doOpenAPI(ctx, openAPIGetTask, map[string]string{"taskId": taskID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTaskEvents returns the full event log for a task.
func (c *Connection) GetTaskEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	var out []TaskEvent
	if err := c.doOpenAPI(ctx, openAPIListTaskEvents, map[string]string{"taskId": taskID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PollTask leases the next task for the given namespace+queue for this
// agent. Returns nil task when the queue is empty (HTTP 204 / empty body
// equivalent). The agent_id is required by the runtime service.
func (c *Connection) PollTask(ctx context.Context, namespace, queue, agentID string) (*PollTaskResponse, error) {
	if err := c.ensureAgentSession(ctx, agentID, namespace, queue); err != nil {
		return nil, err
	}
	query := queryValues(map[string]string{
		"namespace":  namespace,
		"queue":      queue,
		"agent_id":   agentID,
		"task_types": TaskTypePrefixWorkflow + "," + TaskTypePrefixActivity + "," + TaskTypePrefixQuery + "," + TaskTypePrefixUpdate,
	})
	var out PollTaskResponse
	if err := c.doOpenAPI(ctx, openAPIPollAgentTask, nil, query, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HeartbeatTask emits a heartbeat for a leased task. Workers call this on
// a timer derived from the task's lease_timeout_seconds.
func (c *Connection) HeartbeatTask(ctx context.Context, taskID, agentID string, ev *TaskEventInput) error {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return err
	}
	body := map[string]any{}
	if ev != nil {
		body["event"] = ev
	}
	return c.doOpenAPI(ctx, openAPIHeartbeatAgentTask, map[string]string{"taskId": taskID}, queryValues(map[string]string{"agent_id": agentID}), body, nil)
}

// EmitTaskEvent appends an arbitrary task event (progress/stdout/stderr/
// milestone). Used by the Worker dispatch path and exposed for activity
// helpers (Heartbeat, Milestone).
func (c *Connection) EmitTaskEvent(ctx context.Context, taskID, agentID string, ev TaskEventInput) error {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return err
	}
	return c.doOpenAPI(ctx, openAPIAppendAgentTaskEvent, map[string]string{"taskId": taskID}, queryValues(map[string]string{"agent_id": agentID}), map[string]any{"event": ev}, nil)
}

// CompleteTask marks a leased task succeeded with the given result.
func (c *Connection) CompleteTask(ctx context.Context, taskID, agentID string, result TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	var out Task
	if err := c.doOpenAPI(ctx, openAPICompleteAgentTask, map[string]string{"taskId": taskID}, queryValues(map[string]string{"agent_id": agentID}), map[string]any{"result": result}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FailTask marks a leased task failed with the given reason.
func (c *Connection) FailTask(ctx context.Context, taskID, agentID, reason string, result *TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	body := map[string]any{"error": reason}
	if result != nil {
		body["result"] = result
	}
	var out Task
	if err := c.doOpenAPI(ctx, openAPIFailAgentTask, map[string]string{"taskId": taskID}, queryValues(map[string]string{"agent_id": agentID}), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BlockTask marks a leased task blocked (waiting on a signal). Workflow
// runtime uses this when a workflow yields without a terminal result.
func (c *Connection) BlockTask(ctx context.Context, taskID, agentID, reason string) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	var out Task
	if err := c.doOpenAPI(ctx, openAPIBlockAgentTask, map[string]string{"taskId": taskID}, queryValues(map[string]string{"agent_id": agentID}), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
