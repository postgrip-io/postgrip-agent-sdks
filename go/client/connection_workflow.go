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
//
// The workflow id is a path segment, not a body field. The orchestrator routes
// this off the `/signal-with-start` suffix on a per-workflow path; a request to
// the collection path is read as a workflow *named* "signal-with-start" and
// 404s. The body's WorkflowID, when set, still wins as the start target — the
// server takes firstNonEmpty(body.WorkflowID, pathID) — so pass the same id in
// both unless you specifically intend to signal one workflow and start another.
func (c *Connection) SignalWithStartWorkflow(ctx context.Context, workflowID string, req SignalWithStartWorkflowRequest) (*SignalWithStartWorkflowResponse, error) {
	if !c.hasAgentRuntimeCredentials() {
		return nil, errors.New("postgrip-agent: signal-with-start can only run from a managed runtime; submit workflow.runtime to an agent pool")
	}
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/signal-with-start"
	var out SignalWithStartWorkflowResponse
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
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
