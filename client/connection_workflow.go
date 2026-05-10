package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// SignalWorkflow appends a signal to a workflow execution.
func (c *Connection) SignalWorkflow(ctx context.Context, workflowID string, req SignalWorkflowRequest) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/signal"
	return c.do(ctx, http.MethodPost, path, req, nil, false)
}

// SignalWithStartWorkflow starts a workflow if it does not exist, otherwise
// appends a signal to the existing run.
func (c *Connection) SignalWithStartWorkflow(ctx context.Context, req SignalWithStartWorkflowRequest) (*Task, error) {
	if !c.hasAgentRuntimeCredentials() {
		return nil, errors.New("postgrip-agent: signal-with-start can only run from a managed runtime; submit workflow.runtime to an agent pool")
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/workflows/signal-with-start", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelWorkflow requests cancellation of a running workflow.
func (c *Connection) CancelWorkflow(ctx context.Context, workflowID, reason string) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/cancel"
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.do(ctx, http.MethodPost, path, body, nil, false)
}

// TerminateWorkflow forcibly fails a running workflow with the given reason.
func (c *Connection) TerminateWorkflow(ctx context.Context, workflowID, reason string) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/terminate"
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.do(ctx, http.MethodPost, path, body, nil, false)
}

// GetWorkflowExecution returns the durable workflow execution row.
func (c *Connection) GetWorkflowExecution(ctx context.Context, workflowID string) (*WorkflowExecution, error) {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID)
	var out WorkflowExecution
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkflowHistory returns the ordered durable history for a workflow.
func (c *Connection) GetWorkflowHistory(ctx context.Context, workflowID string) ([]WorkflowHistoryEvent, error) {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/history"
	var out []WorkflowHistoryEvent
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}
