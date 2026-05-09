package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/postgrip-io/agent-sdk-go/client"
	"github.com/postgrip-io/agent-sdk-go/failure"
	"github.com/postgrip-io/agent-sdk-go/internal/replay"
	"github.com/postgrip-io/agent-sdk-go/workflow"
)

// workflowContext is the live workflow.Context implementation. The Worker
// creates one per workflow task and passes it to the registered workflow.Func.
type workflowContext struct {
	context.Context

	logger *slog.Logger

	conn         *client.Connection
	agentID      string
	namespace    string
	queue        string
	taskID       string
	workflowID   string
	workflowType string
	now          time.Time

	// replay drives deterministic re-execution against durable history and
	// is REQUIRED. runWorkflow always builds one before constructing the
	// context. Sleep / ExecuteActivity / ExecuteChildWorkflow do not fall
	// back to in-process polling when it is nil — they would unconditionally
	// suspend, which is a bug at the Worker layer rather than a supported
	// "one-shot" mode. Workflow bodies must run in a single goroutine;
	// signalChannels / queryHandlers / updateHandlers are not synchronized.
	replay *replay.Replay

	signalChannels map[string]*workflow.SignalChannel
	queryHandlers  map[string]func(args []any) (any, error)
	updateHandlers map[string]func(args []any) (any, error)
}

func newWorkflowContext(ctx context.Context, w *Worker, task *client.Task, workflowID, workflowType string, rp *replay.Replay) *workflowContext {
	return &workflowContext{
		Context:      ctx,
		logger:       w.logger.With("workflow_id", workflowID, "workflow_type", workflowType),
		conn:         w.opts.Connection,
		agentID:      w.opts.AgentID,
		namespace:    task.Namespace,
		queue:        task.Queue,
		taskID:       task.ID,
		workflowID:   workflowID,
		workflowType: workflowType,
		now:          time.Now().UTC(),
		replay:       rp,
	}
}

func (w *workflowContext) Now() time.Time { return w.now }

func (w *workflowContext) Logger() *slog.Logger { return w.logger }

// checkReplayCancellation translates the replay package's cancellation
// sentinel into the SDK's public failure.Cancelled. Returns nil when the
// replay reports no cancellation.
func (w *workflowContext) checkReplayCancellation() error {
	if err := w.replay.CheckCancellation(); err != nil {
		return &failure.Cancelled{Message: "workflow cancellation requested"}
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
		return failure.NewNonRetryable(det.Message, "WorkflowDeterminismViolation")
	}
	return err
}

// isWorkflowSuspended reports whether err is the workflow-suspension
// sentinel. Used by runWorkflow to decide whether to block-and-redeliver
// vs. fail.
func isWorkflowSuspended(err error) bool {
	return workflow.IsSuspended(err)
}

func (w *workflowContext) Sleep(d time.Duration) error {
	if d <= 0 {
		return nil
	}
	durationMs := d.Milliseconds()

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
			return workflow.NewSuspended("waiting for timer (duration=%dms)", durationMs)
		}
	}

	if _, err := w.conn.EnqueueTask(w.Context, client.EnqueueTaskRequest{
		Namespace: w.namespace,
		Queue:     w.queue,
		Type:      client.TaskTypeTimer,
		Payload: marshalJSON(map[string]any{
			"workflow_id": w.workflowID,
			"duration_ms": durationMs,
			"fire_at":     w.now.Add(d).UTC().Format(time.RFC3339Nano),
		}),
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule timer: %w", err)
	}
	return workflow.NewSuspended("scheduled timer (duration=%dms)", durationMs)
}

func (w *workflowContext) ExecuteActivity(activityType string, args []any, target any, opts *workflow.ActivityOptions) error {
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
	var retry *client.RetryPolicy
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
	if _, err := w.conn.EnqueueTask(w.Context, client.EnqueueTaskRequest{
		Namespace:           w.namespace,
		Queue:               taskQueue,
		Type:                client.TaskTypePrefixActivity + activityType,
		Payload:             marshalJSON(payload),
		LeaseTimeoutSeconds: leaseTimeout,
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule activity %s: %w", activityType, err)
	}
	return workflow.NewSuspended("scheduled activity %s", activityType)
}

// resolveScheduledActivity fetches the persisted activity task that the
// workflow's history says was scheduled here and translates its current
// state into the workflow's view: success → decode result; failure → either
// retry (if the runtime queued a retry attempt) or surface the failure;
// cancellation → Cancelled; still running → suspend until the next
// workflow lease.
func (w *workflowContext) resolveScheduledActivity(activityType string, args []any, target any, opts *workflow.ActivityOptions, scheduled *client.WorkflowHistoryEvent) error {
	if scheduled.TaskID == "" {
		return workflow.NewSuspended("waiting for activity %s (no task id recorded)", activityType)
	}
	task, err := w.conn.GetTask(w.Context, scheduled.TaskID)
	if err != nil {
		return err
	}
	switch task.State {
	case client.TaskStateSucceeded:
		if task.Result == nil {
			return nil
		}
		if task.Result.Failure != nil {
			return failure.FromInfo(task.Result.Failure)
		}
		return decodeResultValue(task.Result, target)
	case client.TaskStateFailed:
		if w.replay.IsActivityCanceled(scheduled) {
			return &failure.Cancelled{Message: "activity " + activityType + " cancelled"}
		}
		if w.replay.HasActivityRetryScheduled(scheduled) {
			// Runtime requested a retry — recurse so the next NextActivity
			// cursor entry resolves the retry attempt.
			return w.ExecuteActivity(activityType, args, target, opts)
		}
		if task.Result != nil && task.Result.Failure != nil {
			return failure.FromInfo(task.Result.Failure)
		}
		return &failure.TaskFailed{TaskID: task.ID, Reason: task.Error}
	default:
		return workflow.NewSuspended("waiting for activity %s", activityType)
	}
}

func (w *workflowContext) ExecuteChildWorkflow(workflowType string, args []any, target any, opts *workflow.ChildWorkflowOptions) error {
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
	var retry *client.RetryPolicy
	reusePolicy := workflow.IDReusePolicy("")
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
	if _, err := w.conn.EnqueueTask(w.Context, client.EnqueueTaskRequest{
		Namespace:           w.namespace,
		Queue:               taskQueue,
		Type:                client.TaskTypePrefixWorkflow + workflowType,
		Payload:             marshalJSON(payload),
		LeaseTimeoutSeconds: leaseTimeout,
	}); err != nil {
		return fmt.Errorf("postgrip-agent: schedule child workflow %s: %w", workflowType, err)
	}
	return workflow.NewSuspended("scheduled child workflow %s", workflowType)
}

func (w *workflowContext) resolveChildWorkflow(workflowType string, target any, started *client.WorkflowHistoryEvent) error {
	if started.TaskID == "" {
		return workflow.NewSuspended("waiting for child workflow %s (no task id recorded)", workflowType)
	}
	task, err := w.conn.GetTask(w.Context, started.TaskID)
	if err != nil {
		return err
	}
	switch task.State {
	case client.TaskStateSucceeded:
		if task.Result == nil {
			return nil
		}
		if task.Result.Failure != nil {
			return failure.FromInfo(task.Result.Failure)
		}
		return decodeResultValue(task.Result, target)
	case client.TaskStateFailed:
		if task.Result != nil && task.Result.Failure != nil {
			return failure.FromInfo(task.Result.Failure)
		}
		return &failure.TaskFailed{TaskID: task.ID, Reason: task.Error}
	default:
		return workflow.NewSuspended("waiting for child workflow %s", workflowType)
	}
}

func (w *workflowContext) GetSignalChannel(name string) *workflow.SignalChannel {
	if w.signalChannels == nil {
		w.signalChannels = map[string]*workflow.SignalChannel{}
	}
	if ch, ok := w.signalChannels[name]; ok {
		return ch
	}
	persisted := w.persistedSignals(name)
	ch := workflow.NewSignalChannel(name, persisted)
	w.signalChannels[name] = ch
	return ch
}

// persistedSignals returns the durable signal stream for this signal name
// (in arrival order).
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

func (w *workflowContext) Milestone(name string, opts workflow.MilestoneOptions) error {
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
	return w.conn.EmitTaskEvent(w.Context, w.taskID, w.agentID, client.TaskEventInput{
		Kind:    client.TaskEventKindMilestone,
		Stage:   name,
		Message: name,
		Details: details,
	})
}

func (w *workflowContext) ContinueAsNew(opts workflow.ContinueAsNewOptions) error {
	if opts.WorkflowType == "" {
		opts.WorkflowType = w.workflowType
	}
	if opts.TaskQueue == "" {
		opts.TaskQueue = w.queue
	}
	if opts.WorkflowID == "" {
		opts.WorkflowID = w.workflowID
	}
	return &workflow.ContinueAsNewSentinel{Options: opts}
}

// Compile-time assertion that workflowContext implements workflow.Context.
var _ workflow.Context = (*workflowContext)(nil)
