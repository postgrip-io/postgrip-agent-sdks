package client

import (
	"context"
	"errors"
)

// SignalWorkflow appends a signal to a workflow execution.
func (c *Connection) SignalWorkflow(ctx context.Context, workflowID string, req SignalWorkflowRequest) error {
	return c.doOpenAPI(ctx, openAPISignalWorkflow, map[string]string{"workflowId": workflowID}, nil, req, nil)
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
	var out SignalWithStartWorkflowResponse
	if err := c.doOpenAPI(ctx, openAPISignalWithStartWorkflow, map[string]string{"workflowId": workflowID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelWorkflow requests cancellation of a running workflow.
func (c *Connection) CancelWorkflow(ctx context.Context, workflowID, reason string) error {
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.doOpenAPI(ctx, openAPICancelWorkflow, map[string]string{"workflowId": workflowID}, nil, body, nil)
}

// TerminateWorkflow forcibly fails a running workflow with the given reason.
func (c *Connection) TerminateWorkflow(ctx context.Context, workflowID, reason string) error {
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.doOpenAPI(ctx, openAPITerminateWorkflow, map[string]string{"workflowId": workflowID}, nil, body, nil)
}

// GetWorkflowExecution returns the durable workflow execution row.
func (c *Connection) GetWorkflowExecution(ctx context.Context, workflowID string) (*WorkflowExecution, error) {
	var out WorkflowExecution
	if err := c.doOpenAPI(ctx, openAPIGetWorkflow, map[string]string{"workflowId": workflowID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkflowHistory returns the ordered durable history for a workflow.
func (c *Connection) GetWorkflowHistory(ctx context.Context, workflowID string) ([]WorkflowHistoryEvent, error) {
	var out []WorkflowHistoryEvent
	if err := c.doOpenAPI(ctx, openAPIListWorkflowHistory, map[string]string{"workflowId": workflowID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
