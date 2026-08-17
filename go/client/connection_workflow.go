package client

import (
	"context"
	"errors"
)

// SignalWorkflow appends a signal to a workflow execution.
func (c *Connection) SignalWorkflow(ctx context.Context, workflowID string, req SignalWorkflowRequest) error {
	_, err := c.OpenAPI().SignalWorkflow(ctx, workflowID, req)
	return err
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
	out, err := c.OpenAPI().SignalWithStartWorkflow(ctx, workflowID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelWorkflow requests cancellation of a running workflow.
func (c *Connection) CancelWorkflow(ctx context.Context, workflowID, reason string) error {
	body := OpenAPICancelWorkflowRequestBody{Reason: reason}
	_, err := c.OpenAPI().CancelWorkflow(ctx, workflowID, &body)
	return err
}

// TerminateWorkflow forcibly fails a running workflow with the given reason.
func (c *Connection) TerminateWorkflow(ctx context.Context, workflowID, reason string) error {
	body := OpenAPITerminateWorkflowRequestBody{Reason: reason}
	_, err := c.OpenAPI().TerminateWorkflow(ctx, workflowID, &body)
	return err
}

// GetWorkflowExecution returns the durable workflow execution row.
func (c *Connection) GetWorkflowExecution(ctx context.Context, workflowID string) (*WorkflowExecution, error) {
	out, err := c.OpenAPI().GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkflowHistory returns the ordered durable history for a workflow.
func (c *Connection) GetWorkflowHistory(ctx context.Context, workflowID string) ([]WorkflowHistoryEvent, error) {
	return c.OpenAPI().ListWorkflowHistory(ctx, workflowID)
}
