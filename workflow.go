package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// CancellationType controls how child workflows / activities react to a
// parent cancellation. Mirrors the TS / Python CancellationType.
type CancellationType string

const (
	CancellationTypeTryCancel                 CancellationType = "try_cancel"
	CancellationTypeWaitCancellationCompleted CancellationType = "wait_cancellation_completed"
	CancellationTypeAbandon                   CancellationType = "abandon"
)

// CancellationScopeType is the workflow scope's behavior under cancellation.
type CancellationScopeType string

const (
	CancellationScopeCancellable    CancellationScopeType = "cancellable"
	CancellationScopeNonCancellable CancellationScopeType = "non_cancellable"
)

// ActivityOptions configures a single activity invocation. Zero values mean
// "use the runtime defaults"; the runtime service applies sensible
// per-namespace defaults if no retry / timeout is supplied.
type ActivityOptions struct {
	TaskQueue           string
	LeaseTimeoutSeconds int
	StartToCloseMs      int
	HeartbeatTimeoutMs  int
	Retry               *RetryPolicy
	CancellationType    CancellationType
}

// ChildWorkflowOptions configures a child workflow invocation.
type ChildWorkflowOptions struct {
	WorkflowID            string
	TaskQueue             string
	LeaseTimeoutSeconds   int
	WorkflowRunTimeoutMs  int
	CancellationType      CancellationType
	CancellationScope     CancellationScopeType
	Retry                 *RetryPolicy
	WorkflowIDReusePolicy WorkflowIDReusePolicy
}

// ContinueAsNewOptions captures the new workflow shape when a workflow opts
// to restart with a fresh history. Returning ContinueAsNew(...) from a
// workflow body completes the current run and atomically schedules the next
// one.
type ContinueAsNewOptions struct {
	WorkflowID           string
	WorkflowType         string
	TaskQueue            string
	Args                 []any
	LeaseTimeoutSeconds  int
	WorkflowRunTimeoutMs int
	Retry                *RetryPolicy
}

// SignalChannel is delivered to workflow code so it can wait for the next
// signal of a given name. Receive() is called inside the workflow body.
//
// Under replay, the channel is pre-seeded with every signal recorded in the
// workflow's durable history at construction time. Each Receive consumes one
// from the buffer; when the buffer drains, Receive returns the suspend
// sentinel so the Worker blocks the workflow task until a new signal arrives
// and the runtime service re-leases.
type SignalChannel struct {
	name  string
	queue chan []any
}

// Receive returns the next buffered signal payload for this channel's name.
// On an empty buffer it returns the suspend sentinel — the Worker
// recognizes it and blocks the workflow task; the runtime service redelivers
// when WorkflowSignaled lands and the next replay re-seeds the buffer.
//
// ctx cancellation short-circuits with ctx.Err() so workflow code that wants
// to bail out of a receive loop on workflow cancellation still can.
func (s *SignalChannel) Receive(ctx Context) ([]any, error) {
	if s == nil {
		return nil, errors.New("postgrip-agent: nil SignalChannel")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case args := <-s.queue:
		return args, nil
	default:
		return nil, newSuspended("waiting for signal %q", s.name)
	}
}

// Context is the workflow-scoped context handed to every WorkflowFunc. It
// carries cancellation, deadline, and the workflow runtime so workflow code
// can sleep, execute activities, schedule child workflows, register query /
// update / signal handlers, and call ContinueAsNew.
//
// Context implements context.Context so customer code can pass it to any
// stdlib helper that takes a context.Context (e.g. http.NewRequestWithContext).
type Context interface {
	context.Context

	// Now returns the workflow's logical time. Mirrors workflow.now() in TS
	// and Python — durable across replays.
	Now() time.Time

	// Logger returns a workflow-scoped slog.Logger pre-tagged with the
	// workflow id and run id.
	Logger() *slog.Logger

	// Sleep blocks until the deadline. Implemented as a durable timer task
	// the runtime service tracks; the workflow can be safely paused and
	// re-scheduled while the timer is pending.
	Sleep(d time.Duration) error

	// ExecuteActivity enqueues an activity task, waits for its completion,
	// and unmarshals the result into target (a pointer). target may be nil
	// if the caller doesn't care about the result.
	ExecuteActivity(activityType string, args []any, target any, opts *ActivityOptions) error

	// ExecuteChildWorkflow enqueues a child workflow execution and waits
	// for it to terminate. target may be nil.
	ExecuteChildWorkflow(workflowType string, args []any, target any, opts *ChildWorkflowOptions) error

	// GetSignalChannel returns a (cached) SignalChannel for the given name.
	// Workflow handlers receive on the channel to react to signals
	// delivered by the client.
	GetSignalChannel(name string) *SignalChannel

	// SetQueryHandler registers a synchronous query handler. The handler
	// runs inside the workflow execution but must not invoke any commands
	// (no Sleep / ExecuteActivity / etc.) — queries are read-only by
	// contract.
	SetQueryHandler(name string, handler func(args []any) (any, error)) error

	// SetUpdateHandler registers an update handler. Update calls block the
	// caller until the handler returns, and they may trigger commands.
	SetUpdateHandler(name string, handler func(args []any) (any, error)) error

	// Milestone emits a TaskEventKindMilestone event for the workflow task.
	Milestone(name string, opts MilestoneOptions) error

	// ContinueAsNew completes the current run and schedules a new run with
	// the supplied options. The function returns an error that the worker
	// translates into a runtime-service ContinueAsNewResult — workflows
	// should `return ctx.ContinueAsNew(...)` from their body.
	ContinueAsNew(opts ContinueAsNewOptions) error
}

// workflowContext is the live Context implementation. The Worker creates one
// per workflow task and passes it to the registered WorkflowFunc.
type workflowContext struct {
	context.Context

	logger *slog.Logger

	conn         *Connection
	agentID      string
	namespace    string
	queue        string
	taskID       string
	workflowID   string
	workflowType string
	now          time.Time

	// replay drives deterministic re-execution against durable history and
	// is REQUIRED. Worker.runWorkflow always builds one before constructing
	// the context. Sleep / ExecuteActivity / ExecuteChildWorkflow do not
	// fall back to in-process polling when it is nil — they would
	// unconditionally suspend, which is a bug at the Worker layer rather
	// than a supported "one-shot" mode. Workflow bodies must run in a
	// single goroutine; signalChannels / queryHandlers / updateHandlers are
	// not synchronized.
	replay *workflowReplay

	signalChannels map[string]*SignalChannel
	queryHandlers  map[string]func(args []any) (any, error)
	updateHandlers map[string]func(args []any) (any, error)
}

func (w *workflowContext) Now() time.Time { return w.now }

func (w *workflowContext) Logger() *slog.Logger { return w.logger }

func (w *workflowContext) Sleep(d time.Duration) error {
	if d <= 0 {
		return nil
	}
	durationMs := d.Milliseconds()

	// Replay path: history may already record this timer. If TimerStarted is
	// recorded and matches the requested duration, either it has fired
	// (return nil) or it hasn't yet (suspend). Cursor advances either way so
	// the next call to Sleep consults the next recorded timer.
	if w.replay != nil {
		if err := w.replay.checkCancellation(); err != nil {
			return err
		}
		started, err := w.replay.nextTimer(durationMs)
		if err != nil {
			return err
		}
		if started != nil {
			if w.replay.isTimerFired(started) {
				return nil
			}
			return newSuspended("waiting for timer (duration=%dms)", durationMs)
		}
	}

	// History didn't record this timer — schedule a fresh one and suspend.
	// The runtime service writes TimerStarted on enqueue and TimerFired when
	// the timer expires; on the next workflow lease, the replay branch above
	// will resolve.
	if _, err := w.conn.EnqueueTask(w.Context, EnqueueTaskRequest{
		Namespace: w.namespace,
		Queue:     w.queue,
		Type:      TaskTypeTimer,
		Payload: mustJSON(map[string]any{
			"workflow_id": w.workflowID,
			"duration_ms": durationMs,
			"fire_at":     w.now.Add(d).UTC().Format(time.RFC3339Nano),
		}),
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule timer: %w", err)
	}
	return newSuspended("scheduled timer (duration=%dms)", durationMs)
}

func (w *workflowContext) ExecuteActivity(activityType string, args []any, target any, opts *ActivityOptions) error {
	if w.replay != nil {
		if err := w.replay.checkCancellation(); err != nil {
			return err
		}
		scheduled, err := w.replay.nextActivity(activityType)
		if err != nil {
			return err
		}
		if scheduled != nil {
			return w.resolveScheduledActivity(activityType, args, target, opts, scheduled)
		}
	}

	taskQueue := w.queue
	leaseTimeout := 0
	var retry *RetryPolicy
	if opts != nil {
		if opts.TaskQueue != "" {
			taskQueue = opts.TaskQueue
		}
		leaseTimeout = opts.LeaseTimeoutSeconds
		retry = opts.Retry
	}
	payload := map[string]any{
		"activityType": activityType,
		"args":         args,
		"workflowId":   w.workflowID,
	}
	if retry != nil {
		payload["retry"] = retry
	}
	if _, err := w.conn.EnqueueTask(w.Context, EnqueueTaskRequest{
		Namespace:           w.namespace,
		Queue:               taskQueue,
		Type:                TaskTypePrefixActivity + activityType,
		Payload:             mustJSON(payload),
		LeaseTimeoutSeconds: leaseTimeout,
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule activity %s: %w", activityType, err)
	}
	return newSuspended("scheduled activity %s", activityType)
}

// resolveScheduledActivity fetches the persisted activity task that the
// workflow's history says was scheduled here and translates its current
// state into the workflow's view: success → decode result; failure → either
// retry (if the runtime queued a retry attempt) or surface the failure;
// cancellation → CancelledFailure; still running → suspend until the next
// workflow lease.
func (w *workflowContext) resolveScheduledActivity(activityType string, args []any, target any, opts *ActivityOptions, scheduled *WorkflowHistoryEvent) error {
	if scheduled.TaskID == "" {
		return newSuspended("waiting for activity %s (no task id recorded)", activityType)
	}
	task, err := w.conn.GetTask(w.Context, scheduled.TaskID)
	if err != nil {
		return err
	}
	switch task.State {
	case TaskStateSucceeded:
		if task.Result == nil {
			return nil
		}
		if task.Result.Failure != nil {
			return failureToError(task.Result.Failure)
		}
		return decodeResultValue(task.Result, target)
	case TaskStateFailed:
		if w.replay.isActivityCanceled(scheduled) {
			return &CancelledFailure{Message: "activity " + activityType + " cancelled"}
		}
		if w.replay.hasActivityRetryScheduled(scheduled) {
			// Runtime requested a retry — recurse so the next nextActivity
			// cursor entry resolves the retry attempt.
			return w.ExecuteActivity(activityType, args, target, opts)
		}
		if task.Result != nil && task.Result.Failure != nil {
			return failureToError(task.Result.Failure)
		}
		return &TaskFailedError{TaskID: task.ID, Reason: task.Error}
	default:
		return newSuspended("waiting for activity %s", activityType)
	}
}

func (w *workflowContext) ExecuteChildWorkflow(workflowType string, args []any, target any, opts *ChildWorkflowOptions) error {
	if w.replay != nil {
		if err := w.replay.checkCancellation(); err != nil {
			return err
		}
		started, err := w.replay.nextChild(workflowType)
		if err != nil {
			return err
		}
		if started != nil {
			return w.resolveChildWorkflow(workflowType, target, started)
		}
	}

	taskQueue := w.queue
	workflowID := ""
	leaseTimeout := 0
	runTimeoutMs := 0
	var retry *RetryPolicy
	reusePolicy := WorkflowIDReusePolicy("")
	if opts != nil {
		if opts.TaskQueue != "" {
			taskQueue = opts.TaskQueue
		}
		workflowID = opts.WorkflowID
		leaseTimeout = opts.LeaseTimeoutSeconds
		runTimeoutMs = opts.WorkflowRunTimeoutMs
		retry = opts.Retry
		reusePolicy = opts.WorkflowIDReusePolicy
	}
	payload := map[string]any{
		"workflowType":    workflowType,
		"args":            args,
		"parent_workflow": w.workflowID,
		"workflow_id":     workflowID,
		"task_queue":      taskQueue,
		"run_timeout_ms":  runTimeoutMs,
		"reuse_policy":    string(reusePolicy),
	}
	if retry != nil {
		payload["retry"] = retry
	}
	if _, err := w.conn.EnqueueTask(w.Context, EnqueueTaskRequest{
		Namespace:           w.namespace,
		Queue:               taskQueue,
		Type:                TaskTypePrefixWorkflow + workflowType,
		Payload:             mustJSON(payload),
		LeaseTimeoutSeconds: leaseTimeout,
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule child workflow %s: %w", workflowType, err)
	}
	return newSuspended("scheduled child workflow %s", workflowType)
}

func (w *workflowContext) resolveChildWorkflow(workflowType string, target any, started *WorkflowHistoryEvent) error {
	if started.TaskID == "" {
		return newSuspended("waiting for child workflow %s (no task id recorded)", workflowType)
	}
	task, err := w.conn.GetTask(w.Context, started.TaskID)
	if err != nil {
		return err
	}
	switch task.State {
	case TaskStateSucceeded:
		if task.Result == nil {
			return nil
		}
		if task.Result.Failure != nil {
			return failureToError(task.Result.Failure)
		}
		return decodeResultValue(task.Result, target)
	case TaskStateFailed:
		if task.Result != nil && task.Result.Failure != nil {
			return failureToError(task.Result.Failure)
		}
		return &TaskFailedError{TaskID: task.ID, Reason: task.Error}
	default:
		return newSuspended("waiting for child workflow %s", workflowType)
	}
}

func (w *workflowContext) GetSignalChannel(name string) *SignalChannel {
	if w.signalChannels == nil {
		w.signalChannels = map[string]*SignalChannel{}
	}
	if ch, ok := w.signalChannels[name]; ok {
		return ch
	}
	// Channel size accommodates the full persisted signal history plus
	// breathing room. Sized lazily so workflows that never receive a signal
	// don't allocate a backlog that fits one.
	persisted := w.persistedSignals(name)
	bufSize := len(persisted) + 8
	ch := &SignalChannel{name: name, queue: make(chan []any, bufSize)}
	for _, args := range persisted {
		ch.queue <- args
	}
	w.signalChannels[name] = ch
	return ch
}

// persistedSignals returns the durable signal stream for this signal name
// (in arrival order). Worker.runWorkflow seeds the workflow context with a
// replay before each invocation so this returns the full backlog the
// runtime service has recorded.
func (w *workflowContext) persistedSignals(name string) [][]any {
	if w.replay == nil {
		return nil
	}
	return w.replay.signalsByName(name)
}

func (w *workflowContext) SetQueryHandler(name string, handler func(args []any) (any, error)) error {
	if w.queryHandlers == nil {
		w.queryHandlers = map[string]func(args []any) (any, error){}
	}
	if _, exists := w.queryHandlers[name]; exists {
		return fmt.Errorf("postgrip-agent: query handler %q already registered", name)
	}
	w.queryHandlers[name] = handler
	return nil
}

func (w *workflowContext) SetUpdateHandler(name string, handler func(args []any) (any, error)) error {
	if w.updateHandlers == nil {
		w.updateHandlers = map[string]func(args []any) (any, error){}
	}
	if _, exists := w.updateHandlers[name]; exists {
		return fmt.Errorf("postgrip-agent: update handler %q already registered", name)
	}
	w.updateHandlers[name] = handler
	return nil
}

func (w *workflowContext) Milestone(name string, opts MilestoneOptions) error {
	details := map[string]any{}
	for k, v := range opts.Details {
		details[k] = v
	}
	if opts.Index > 0 {
		details["index"] = opts.Index
	}
	if opts.Total > 0 {
		details["total"] = opts.Total
	}
	return w.conn.EmitTaskEvent(w.Context, w.taskID, w.agentID, TaskEventInput{
		Kind:    TaskEventKindMilestone,
		Stage:   name,
		Message: name,
		Details: details,
	})
}

// continueAsNewSentinel is returned (as an error) from ContinueAsNew so the
// Worker dispatch can recognize the intent and translate it into a
// runtime-service ContinueAsNewResult on completion. ContinueAsNew is not
// a true error — it's a control-flow signal modeled as one to avoid forcing
// every WorkflowFunc to return a sum type.
type continueAsNewSentinel struct {
	Options ContinueAsNewOptions
}

func (c *continueAsNewSentinel) Error() string {
	return fmt.Sprintf("continue-as-new to %q", c.Options.WorkflowType)
}

// IsContinueAsNew reports whether err is a ContinueAsNew signal.
func IsContinueAsNew(err error) bool {
	var c *continueAsNewSentinel
	return errors.As(err, &c)
}

func (w *workflowContext) ContinueAsNew(opts ContinueAsNewOptions) error {
	if opts.WorkflowType == "" {
		opts.WorkflowType = w.workflowType
	}
	if opts.TaskQueue == "" {
		opts.TaskQueue = w.queue
	}
	if opts.WorkflowID == "" {
		opts.WorkflowID = w.workflowID
	}
	return &continueAsNewSentinel{Options: opts}
}

// waitForTaskCompletion polls the task until it reaches a terminal state.
// Used by ExecuteActivity / ExecuteChildWorkflow / Sleep so the workflow's
// linear control flow can wait on enqueued work without the customer
// hand-rolling a polling loop.
func waitForTaskCompletion(ctx context.Context, conn *Connection, taskID string, target any) error {
	for {
		task, err := conn.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		switch task.State {
		case TaskStateSucceeded:
			if target == nil || task.Result == nil {
				return nil
			}
			if task.Result.Failure != nil {
				return failureToError(task.Result.Failure)
			}
			return decodeResultValue(task.Result, target)
		case TaskStateFailed:
			reason := task.Error
			if task.Result != nil && task.Result.Failure != nil {
				return &TaskFailedError{
					TaskID:  taskID,
					Reason:  reason,
					Failure: failureInfoToApplicationFailure(task.Result.Failure),
				}
			}
			return &TaskFailedError{TaskID: taskID, Reason: reason}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// failureToError converts a wire-protocol FailureInfo into an
// ApplicationFailure / CancelledFailure / TimeoutFailure pointer. Callers
// should errors.As to extract the typed failure.
func failureToError(f *FailureInfo) error {
	if f == nil {
		return nil
	}
	switch f.Type {
	case "CancelledFailure":
		return &CancelledFailure{Message: f.Message, Details: f.Details}
	case "TimeoutFailure":
		return &TimeoutFailure{Message: f.Message}
	default:
		return failureInfoToApplicationFailure(f)
	}
}

func failureInfoToApplicationFailure(f *FailureInfo) *ApplicationFailure {
	if f == nil {
		return nil
	}
	return &ApplicationFailure{
		Message:      f.Message,
		Type:         f.Type,
		NonRetryable: f.NonRetryable,
		Details:      f.Details,
	}
}

func errorToFailure(err error) *FailureInfo {
	if err == nil {
		return nil
	}
	var app *ApplicationFailure
	if errors.As(err, &app) {
		return &FailureInfo{
			Message:      app.Message,
			Type:         app.Type,
			NonRetryable: app.NonRetryable,
			Details:      app.Details,
		}
	}
	var cancelled *CancelledFailure
	if errors.As(err, &cancelled) {
		return &FailureInfo{Message: cancelled.Message, Type: "CancelledFailure", Details: cancelled.Details}
	}
	var timeout *TimeoutFailure
	if errors.As(err, &timeout) {
		return &FailureInfo{Message: timeout.Message, Type: "TimeoutFailure"}
	}
	return &FailureInfo{Message: err.Error()}
}
