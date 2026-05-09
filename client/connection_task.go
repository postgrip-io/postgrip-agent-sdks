package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// EnqueueTask enqueues a single task. Use TaskClient.Enqueue / ShellExec /
// ContainerExec / Noop helpers for ergonomic construction; this is the raw
// transport call.
func (c *Connection) EnqueueTask(ctx context.Context, req EnqueueTaskRequest) (*Task, error) {
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/tasks", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTasks returns tasks matching the optional filters.
func (c *Connection) ListTasks(ctx context.Context, params map[string]string) ([]Task, error) {
	path := "/api/v1/tasks"
	if len(params) > 0 {
		path += "?" + encodeQuery(params)
	}
	var out []Task
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTask fetches a single task by id.
func (c *Connection) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var out Task
	path := "/api/v1/tasks/" + url.PathEscape(taskID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTaskEvents returns the full event log for a task.
func (c *Connection) GetTaskEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	var out []TaskEvent
	path := "/api/v1/tasks/" + url.PathEscape(taskID) + "/events"
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// PollTaskResponse pairs the leased task (if any) with an optional poll
// directive from the runtime service. Exported so /worker can consume it.
type PollTaskResponse struct {
	Task      *Task               `json:"task,omitempty"`
	Directive *AgentPollDirective `json:"directive,omitempty"`
}

// PollTask leases the next task for the given namespace+queue for this
// agent. Returns nil task when the queue is empty (HTTP 204 / empty body
// equivalent). The agent_id is required by the runtime service.
func (c *Connection) PollTask(ctx context.Context, namespace, queue, agentID string) (*PollTaskResponse, error) {
	if err := c.ensureAgentSession(ctx, agentID, namespace, queue); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/agents/%s/poll", url.PathEscape(agentID))
	q := encodeQuery(map[string]string{"namespace": namespace, "queue": queue})
	if q != "" {
		path += "?" + q
	}
	var out PollTaskResponse
	if err := c.do(ctx, http.MethodPost, path, nil, &out, true); err != nil {
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
	path := agentTaskPath(taskID, "heartbeat", agentID)
	body := map[string]any{}
	if ev != nil {
		body["event"] = ev
	}
	return c.do(ctx, http.MethodPost, path, body, nil, true)
}

// EmitTaskEvent appends an arbitrary task event (progress/stdout/stderr/
// milestone). Used by the Worker dispatch path and exposed for activity
// helpers (Heartbeat, Milestone).
func (c *Connection) EmitTaskEvent(ctx context.Context, taskID, agentID string, ev TaskEventInput) error {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return err
	}
	path := agentTaskPath(taskID, "events", agentID)
	return c.do(ctx, http.MethodPost, path, map[string]any{"event": ev}, nil, true)
}

// CompleteTask marks a leased task succeeded with the given result.
func (c *Connection) CompleteTask(ctx context.Context, taskID, agentID string, result TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	path := agentTaskPath(taskID, "complete", agentID)
	var out Task
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"result": result}, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// FailTask marks a leased task failed with the given reason.
func (c *Connection) FailTask(ctx context.Context, taskID, agentID, reason string, result *TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	path := agentTaskPath(taskID, "fail", agentID)
	body := map[string]any{"error": reason}
	if result != nil {
		body["result"] = result
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, path, body, &out, true); err != nil {
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
	path := agentTaskPath(taskID, "block", agentID)
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, path, body, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}
