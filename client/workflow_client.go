package client

import "context"

// WorkflowHandle is the durable reference to a workflow run. It mirrors
// the TS WorkflowHandle (and Python WorkflowHandle): callers Get it from
// Workflow.Start or Workflow.GetHandle and use it to wait on results,
// signal, query, update, cancel, and terminate.
type WorkflowHandle struct {
	conn       *Connection
	WorkflowID string
	TaskID     string
}

// Result blocks until the workflow reaches a terminal state and unmarshals
// the result into target.
func (h *WorkflowHandle) Result(ctx context.Context, target any) error {
	if h.TaskID == "" {
		exec, err := h.conn.GetWorkflowExecution(ctx, h.WorkflowID)
		if err != nil {
			return err
		}
		h.TaskID = exec.TaskID
	}
	return waitForTaskCompletion(ctx, h.conn, h.TaskID, target)
}

// Describe returns the durable workflow execution metadata.
func (h *WorkflowHandle) Describe(ctx context.Context) (*WorkflowExecutionDescription, error) {
	exec, err := h.conn.GetWorkflowExecution(ctx, h.WorkflowID)
	if err != nil {
		return nil, err
	}
	return workflowExecutionToDescription(exec), nil
}

// Signal appends a signal event to the workflow.
func (h *WorkflowHandle) Signal(ctx context.Context, name string, args ...any) error {
	return h.conn.SignalWorkflow(ctx, h.WorkflowID, SignalWorkflowRequest{
		Name: name,
		Args: mustJSON(args),
	})
}

// Cancel requests cancellation of the workflow.
func (h *WorkflowHandle) Cancel(ctx context.Context, reason string) error {
	return h.conn.CancelWorkflow(ctx, h.WorkflowID, reason)
}

// Terminate forcibly fails the workflow with the given reason.
func (h *WorkflowHandle) Terminate(ctx context.Context, reason string) error {
	return h.conn.TerminateWorkflow(ctx, h.WorkflowID, reason)
}

// History returns the ordered durable history of the workflow run.
func (h *WorkflowHandle) History(ctx context.Context) ([]WorkflowHistoryEvent, error) {
	return h.conn.GetWorkflowHistory(ctx, h.WorkflowID)
}

// WorkflowClient is the workflow management surface — Start,
// SignalWithStart, GetHandle. Mirrors TS client.workflow.* and Python
// client.workflow.*.
type WorkflowClient struct {
	conn *Connection
}

// Start enqueues a new workflow execution.
func (c *WorkflowClient) Start(ctx context.Context, workflowType string, opts WorkflowStartOptions) (*WorkflowHandle, error) {
	queue := orDefault(opts.TaskQueue, DefaultQueue)
	namespace := orDefault(opts.Namespace, DefaultNamespace)
	payload := buildWorkflowStartPayload(workflowType, opts)
	task, err := c.conn.EnqueueTask(ctx, EnqueueTaskRequest{
		Namespace:           namespace,
		Queue:               queue,
		Type:                TaskTypePrefixWorkflow + workflowType,
		Payload:             mustJSON(payload),
		LeaseTimeoutSeconds: opts.LeaseTimeoutSeconds,
	})
	if err != nil {
		return nil, err
	}
	return &WorkflowHandle{
		conn:       c.conn,
		WorkflowID: orDefault(opts.WorkflowID, task.ID),
		TaskID:     task.ID,
	}, nil
}

// SignalWithStart starts a workflow if it does not exist, otherwise sends
// the signal to the existing run. The returned handle is keyed on the
// workflow id either way.
func (c *WorkflowClient) SignalWithStart(ctx context.Context, workflowType, signalName string, opts SignalWithStartOptions) (*WorkflowHandle, error) {
	req := SignalWithStartWorkflowRequest{
		Namespace:             orDefault(opts.Namespace, DefaultNamespace),
		Queue:                 orDefault(opts.TaskQueue, DefaultQueue),
		WorkflowType:          workflowType,
		WorkflowID:            opts.WorkflowID,
		WorkflowIDReusePolicy: string(opts.WorkflowIDReusePolicy),
		LeaseTimeoutSeconds:   opts.LeaseTimeoutSeconds,
		RunTimeoutMs:          int64(opts.WorkflowRunTimeoutMs),
		Args:                  mustJSON(opts.Args),
		Signal: SignalWorkflowRequest{
			Name: signalName,
			Args: mustJSON(opts.SignalArgs),
		},
	}
	if opts.Retry != nil {
		req.RetryPolicy = opts.Retry
	}
	memo := memoWithWorkflowUI(opts.Memo, opts.UI)
	if len(memo) > 0 {
		req.Memo = memo
	}
	if len(opts.SearchAttributes) > 0 {
		req.SearchAttributes = opts.SearchAttributes
	}
	task, err := c.conn.SignalWithStartWorkflow(ctx, req)
	if err != nil {
		return nil, err
	}
	return &WorkflowHandle{
		conn:       c.conn,
		WorkflowID: orDefault(opts.WorkflowID, task.ID),
		TaskID:     task.ID,
	}, nil
}

// GetHandle returns a handle to an existing workflow without enqueueing
// anything. Use this from operations code to wait on / signal / cancel an
// already-running workflow.
func (c *WorkflowClient) GetHandle(workflowID string) *WorkflowHandle {
	return &WorkflowHandle{conn: c.conn, WorkflowID: workflowID}
}

// buildWorkflowStartPayload assembles the JSON-encoded payload the runtime
// service expects for a workflow start. Kept centralized so Start and
// SignalWithStart agree on the shape.
func buildWorkflowStartPayload(workflowType string, opts WorkflowStartOptions) map[string]any {
	payload := map[string]any{
		"workflowType":          workflowType,
		"workflowId":            opts.WorkflowID,
		"workflowIdReusePolicy": string(opts.WorkflowIDReusePolicy),
		"args":                  opts.Args,
	}
	if opts.LeaseTimeoutSeconds > 0 {
		payload["lease_timeout_seconds"] = opts.LeaseTimeoutSeconds
	}
	if opts.WorkflowRunTimeoutMs > 0 {
		payload["runTimeoutMs"] = opts.WorkflowRunTimeoutMs
	}
	if opts.Retry != nil {
		payload["retry"] = opts.Retry
	}
	memo := memoWithWorkflowUI(opts.Memo, opts.UI)
	if len(memo) > 0 {
		payload["memo"] = memo
	}
	if len(opts.SearchAttributes) > 0 {
		payload["searchAttributes"] = opts.SearchAttributes
	}
	return payload
}

func workflowExecutionToDescription(e *WorkflowExecution) *WorkflowExecutionDescription {
	if e == nil {
		return nil
	}
	desc := &WorkflowExecutionDescription{
		WorkflowID:           e.ID,
		RunID:                e.RunID,
		TaskID:               e.TaskID,
		Namespace:            e.Namespace,
		TaskQueue:            e.Queue,
		WorkflowType:         e.Type,
		Status:               string(e.State),
		WorkflowRunTimeoutMs: int(e.RunTimeoutMs),
		StartedAt:            e.CreatedAt,
		UpdatedAt:            e.UpdatedAt,
	}
	if e.Attempt > 0 {
		desc.Attempt = e.Attempt
	}
	if e.RetryPolicy != nil {
		desc.Retry = e.RetryPolicy
	}
	if e.Memo != nil {
		desc.Memo = e.Memo
	}
	if e.SearchAttributes != nil {
		desc.SearchAttributes = e.SearchAttributes
	}
	if e.Result != nil {
		desc.Result = e.Result.Value
	}
	if e.Error != "" {
		desc.Error = e.Error
	}
	return desc
}
