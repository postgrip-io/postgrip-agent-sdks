package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/postgrip-io/agent-sdk-go/internal/replay"
)

// checkReplayCancellation translates the replay package's cancellation
// sentinel into the SDK's public CancelledFailure. Returns nil when the
// replay reports no cancellation.
func (w *workflowContext) checkReplayCancellation() error {
	if err := w.replay.CheckCancellation(); err != nil {
		return &CancelledFailure{Message: "workflow cancellation requested"}
	}
	return nil
}

// translateReplayError converts replay sentinels (DeterminismError) into
// the SDK's public failure types so workflow bodies and operators see
// consistent errors regardless of where the failure originated.
func translateReplayError(err error) error {
	if err == nil {
		return nil
	}
	var det *replay.DeterminismError
	if errors.As(err, &det) {
		return NewNonRetryableApplicationFailure(det.Message, "WorkflowDeterminismViolation")
	}
	return err
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
	replay *replay.Replay

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
		if err := w.checkReplayCancellation(); err != nil {
			return err
		}
		started, err := w.replay.NextTimer(durationMs)
		if err != nil {
			return translateReplayError(err)
		}
		if started != nil {
			if w.replay.IsTimerFired(started) {
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
		if err := w.checkReplayCancellation(); err != nil {
			return err
		}
		scheduled, err := w.replay.NextActivity(activityType)
		if err != nil {
			return translateReplayError(err)
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
		if w.replay.IsActivityCanceled(scheduled) {
			return &CancelledFailure{Message: "activity " + activityType + " cancelled"}
		}
		if w.replay.HasActivityRetryScheduled(scheduled) {
			// Runtime requested a retry — recurse so the next NextActivity
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
		if err := w.checkReplayCancellation(); err != nil {
			return err
		}
		started, err := w.replay.NextChild(workflowType)
		if err != nil {
			return translateReplayError(err)
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
	return w.replay.SignalsByName(name)
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

// errWorkflowSuspended is the sentinel returned by workflow context helpers
// when the workflow body cannot proceed (waiting on an in-flight activity /
// timer / child workflow, or on a signal that hasn't arrived yet). The
// Worker recognizes this via errors.As and translates it into a BlockTask
// call so the runtime service re-leases the workflow when its dependencies
// resolve.
type errWorkflowSuspended struct {
	reason string
}

func (e *errWorkflowSuspended) Error() string { return "workflow suspended: " + e.reason }

func newSuspended(format string, args ...any) error {
	return &errWorkflowSuspended{reason: fmt.Sprintf(format, args...)}
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
