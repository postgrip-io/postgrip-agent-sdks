package sdk

import (
	"context"
	"time"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// Client is the high-level entry point. It groups the Task / Workflow /
// Schedule sub-clients sharing a single Connection, mirroring the TS
// Client and Python Client classes.
type Client struct {
	Connection *Connection
	Task       *TaskClient
	Workflow   *WorkflowClient
	Schedule   *ScheduleClient
}

// NewClient wires up the sub-clients around an existing Connection.
func NewClient(conn *Connection) *Client {
	c := &Client{Connection: conn}
	c.Task = &TaskClient{conn: conn}
	c.Workflow = &WorkflowClient{conn: conn}
	c.Schedule = &ScheduleClient{conn: conn}
	return c
}

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

// ShellExec enqueues a shell.exec task. Mirrors TS client.task.shellExec /
// Python client.task.shell_exec — the agent runs the command on its host
// using whatever's installed in the agent image.
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

// ContainerExec enqueues a container.exec task. Mirrors the TS / Python
// containerExec / container_exec helpers added next to ShellExec. The Go
// agent will launch a per-task container from `Image` via its docker CLI;
// requires the agent process to have DOCKER_HOST set on it.
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

// WorkflowClient is the workflow management surface — Start, Signal-with-Start,
// GetHandle. Mirrors TS client.workflow.* and Python client.workflow.*.
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
	if len(opts.Memo) > 0 {
		req.Memo = opts.Memo
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

// ScheduleClient mirrors TS client.schedule.* / Python client.schedule.*.
type ScheduleClient struct {
	conn *Connection
}

// Create registers a new schedule.
func (s *ScheduleClient) Create(ctx context.Context, in CreateScheduleInput) (*Schedule, error) {
	req := CreateScheduleRequest{
		ID:            in.ID,
		Namespace:     orDefault(in.Namespace, DefaultNamespace),
		OverlapPolicy: protocol.ScheduleOverlapPolicy(in.OverlapPolicy),
		Spec:          in.Spec,
		Action:        scheduleActionInputToProtocol(in.Action),
	}
	return s.conn.CreateSchedule(ctx, req)
}

// List returns all schedules, optionally filtered.
func (s *ScheduleClient) List(ctx context.Context, filters map[string]string) ([]Schedule, error) {
	return s.conn.ListSchedules(ctx, filters)
}

// Get fetches a schedule by id.
func (s *ScheduleClient) Get(ctx context.Context, scheduleID string) (*Schedule, error) {
	return s.conn.GetSchedule(ctx, scheduleID)
}

// Update patches a schedule.
func (s *ScheduleClient) Update(ctx context.Context, scheduleID string, in UpdateScheduleInput) (*Schedule, error) {
	req := UpdateScheduleRequest{}
	if in.OverlapPolicy != nil {
		policy := protocol.ScheduleOverlapPolicy(*in.OverlapPolicy)
		req.OverlapPolicy = &policy
	}
	if in.Spec != nil {
		req.Spec = in.Spec
	}
	if in.Action != nil {
		action := scheduleActionInputToProtocol(*in.Action)
		req.Action = &action
	}
	return s.conn.UpdateSchedule(ctx, scheduleID, req)
}

// Pause pauses a schedule with optional reason.
func (s *ScheduleClient) Pause(ctx context.Context, scheduleID, reason string) (*Schedule, error) {
	return s.conn.PauseSchedule(ctx, scheduleID, PauseScheduleRequest{Reason: reason})
}

// Unpause resumes a schedule.
func (s *ScheduleClient) Unpause(ctx context.Context, scheduleID, reason string) (*Schedule, error) {
	return s.conn.UnpauseSchedule(ctx, scheduleID, UnpauseScheduleRequest{Reason: reason})
}

// Trigger fires the schedule once immediately.
func (s *ScheduleClient) Trigger(ctx context.Context, scheduleID, reason string) (*TriggerScheduleResponse, error) {
	return s.conn.TriggerSchedule(ctx, scheduleID, TriggerScheduleRequest{Reason: reason})
}

// Backfill replays missed runs in [start, end].
func (s *ScheduleClient) Backfill(ctx context.Context, scheduleID string, start, end time.Time) (*BackfillScheduleResponse, error) {
	return s.conn.BackfillSchedule(ctx, scheduleID, BackfillScheduleRequest{
		StartAt: start,
		EndAt:   end,
	})
}

// Delete removes a schedule.
func (s *ScheduleClient) Delete(ctx context.Context, scheduleID string) error {
	return s.conn.DeleteSchedule(ctx, scheduleID)
}

// buildWorkflowStartPayload assembles the JSON-encoded payload the runtime
// service expects for a workflow start. Kept centralized so Start and
// SignalWithStart agree on the shape.
func buildWorkflowStartPayload(workflowType string, opts WorkflowStartOptions) map[string]any {
	payload := map[string]any{
		"workflowType": workflowType,
		"workflow_id":  opts.WorkflowID,
		"reuse_policy": string(opts.WorkflowIDReusePolicy),
		"args":         opts.Args,
	}
	if opts.LeaseTimeoutSeconds > 0 {
		payload["lease_timeout_seconds"] = opts.LeaseTimeoutSeconds
	}
	if opts.WorkflowRunTimeoutMs > 0 {
		payload["run_timeout_ms"] = opts.WorkflowRunTimeoutMs
	}
	if opts.Retry != nil {
		payload["retry"] = opts.Retry
	}
	if len(opts.Memo) > 0 {
		payload["memo"] = opts.Memo
	}
	if len(opts.SearchAttributes) > 0 {
		payload["search_attributes"] = opts.SearchAttributes
	}
	return payload
}

func scheduleActionInputToProtocol(in ScheduleActionInput) ScheduleAction {
	out := ScheduleAction{
		Namespace:             orDefault(in.Namespace, DefaultNamespace),
		Queue:                 orDefault(in.Queue, DefaultQueue),
		WorkflowType:          in.WorkflowType,
		WorkflowID:            in.WorkflowID,
		WorkflowIDReusePolicy: string(in.WorkflowIDReusePolicy),
		RunTimeoutMs:          int64(in.RunTimeoutMs),
	}
	if in.Retry != nil {
		out.RetryPolicy = in.Retry
	}
	if len(in.Args) > 0 {
		out.Args = mustJSON(in.Args)
	}
	if len(in.Memo) > 0 {
		out.Memo = in.Memo
	}
	if len(in.SearchAttributes) > 0 {
		out.SearchAttributes = in.SearchAttributes
	}
	return out
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
