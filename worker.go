package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/postgrip-io/agent-sdk-go/internal/replay"
)

// WorkerOptions configures a customer-side worker that polls the runtime
// service, leases tasks, and dispatches them to registered workflow /
// activity functions.
//
// Workflow keys must match the workflow `type` (the part after the
// `workflow:` prefix in the wire task type). Activity keys match the
// activity name (after `activity:`).
//
// Concurrency is bounded by MaxConcurrentTasks; default 4. PollInterval is
// the cadence between empty-queue polls (default 1s). HeartbeatInterval is
// derived from each task's lease_timeout_seconds at runtime — leave zero.
type WorkerOptions struct {
	Connection         *Connection
	Namespace          string
	Queue              string
	AgentID            string
	Workflows          WorkflowRegistry
	Activities         ActivityRegistry
	MaxConcurrentTasks int
	PollInterval       time.Duration
	Logger             *slog.Logger

	// EnableSystemTasks routes shell.exec, container.exec, noop, and timer
	// tasks to the same internal handlers the postgrip-agent binary uses.
	// Customer-side workers usually leave this false (the system-side Go
	// agent picks up those task types). Set true to consolidate into one
	// process during local development.
	EnableSystemTasks bool
}

// Worker is a polling agent. Run blocks until context cancellation or
// Shutdown; Shutdown drains in-flight tasks (best-effort up to drain
// timeout).
type Worker struct {
	opts   WorkerOptions
	logger *slog.Logger

	mu        sync.Mutex
	running   bool
	stopOnce  sync.Once
	stopCh    chan struct{}
	inflight  sync.WaitGroup
	semaphore chan struct{}
}

// NewWorker validates options and returns a Worker. It does not connect or
// poll until Run is called.
func NewWorker(opts WorkerOptions) (*Worker, error) {
	if opts.Connection == nil {
		return nil, errors.New("postgrip-agent: WorkerOptions.Connection is required")
	}
	if opts.AgentID == "" {
		return nil, errors.New("postgrip-agent: WorkerOptions.AgentID is required")
	}
	if opts.Namespace == "" {
		opts.Namespace = DefaultNamespace
	}
	if opts.Queue == "" {
		opts.Queue = DefaultQueue
	}
	if opts.MaxConcurrentTasks <= 0 {
		opts.MaxConcurrentTasks = 4
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default().With("component", "postgrip-agent-sdk-worker")
	}
	return &Worker{
		opts:      opts,
		logger:    logger,
		stopCh:    make(chan struct{}),
		semaphore: make(chan struct{}, opts.MaxConcurrentTasks),
	}, nil
}

// Run polls and dispatches tasks until ctx is cancelled or Shutdown is
// called. It returns when in-flight tasks finish; Run is single-shot and
// not safe to call twice.
func (w *Worker) Run(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return errors.New("postgrip-agent: Worker.Run already running")
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("worker started",
		"agent_id", w.opts.AgentID,
		"namespace", w.opts.Namespace,
		"queue", w.opts.Queue,
		"max_concurrent_tasks", w.opts.MaxConcurrentTasks,
	)

	for {
		select {
		case <-ctx.Done():
			w.inflight.Wait()
			return ctx.Err()
		case <-w.stopCh:
			w.inflight.Wait()
			return nil
		default:
		}

		// Reserve a concurrency slot before polling. Without this the
		// worker can lease tasks faster than it can run them and pile up
		// goroutines.
		select {
		case w.semaphore <- struct{}{}:
		case <-ctx.Done():
			w.inflight.Wait()
			return ctx.Err()
		case <-w.stopCh:
			w.inflight.Wait()
			return nil
		}

		resp, err := w.opts.Connection.PollTask(ctx, w.opts.Namespace, w.opts.Queue, w.opts.AgentID)
		if err != nil {
			<-w.semaphore
			w.logger.Warn("poll failed", "error", err)
			if !sleepOrStop(ctx, w.stopCh, w.opts.PollInterval) {
				w.inflight.Wait()
				return ctx.Err()
			}
			continue
		}
		if resp == nil || resp.Task == nil {
			<-w.semaphore
			if !sleepOrStop(ctx, w.stopCh, w.opts.PollInterval) {
				w.inflight.Wait()
				return ctx.Err()
			}
			continue
		}
		task := resp.Task
		w.inflight.Add(1)
		go func(task *Task) {
			defer w.inflight.Done()
			defer func() { <-w.semaphore }()
			w.handleTask(ctx, task)
		}(task)
	}
}

// Shutdown signals the polling loop to stop and waits up to drainTimeout for
// in-flight tasks to finish before returning. drainTimeout=0 means wait
// indefinitely.
func (w *Worker) Shutdown(ctx context.Context, drainTimeout time.Duration) error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	if drainTimeout <= 0 {
		w.inflight.Wait()
		return nil
	}
	done := make(chan struct{})
	go func() { w.inflight.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(drainTimeout):
		return fmt.Errorf("postgrip-agent: Worker.Shutdown timed out after %s", drainTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) handleTask(ctx context.Context, task *Task) {
	taskCtx, cancelHeartbeat := w.startHeartbeat(ctx, task)
	defer cancelHeartbeat()

	startedAt := time.Now().UTC()
	logger := w.logger.With("task_id", task.ID, "task_type", task.Type)

	if err := w.opts.Connection.EmitTaskEvent(taskCtx, task.ID, w.opts.AgentID, TaskEventInput{
		Kind:    TaskEventKindStarted,
		Message: "agent picked up task",
		Details: map[string]any{"type": task.Type, "attempt": task.Attempt},
	}); err != nil {
		logger.Warn("emit started event failed", "error", err)
	}

	result, err := w.dispatch(taskCtx, task)
	finishedAt := time.Now().UTC()
	if result == nil {
		result = &TaskResult{StartedAt: startedAt, FinishedAt: finishedAt}
	} else {
		if result.StartedAt.IsZero() {
			result.StartedAt = startedAt
		}
		if result.FinishedAt.IsZero() {
			result.FinishedAt = finishedAt
		}
	}

	if err != nil {
		// runWorkflow already called BlockTask on the workflow task — the
		// runtime service is now waiting on the workflow's pending
		// dependencies. Don't call CompleteTask or FailTask, that would
		// race with the redelivery cycle.
		if errors.Is(err, errWorkflowAlreadyBlocked) {
			return
		}
		// ContinueAsNew is a control-flow signal, not a real error: the
		// runtime service interprets the result.continue_as_new field as
		// "schedule the next run". Translate before failing.
		var canSentinel *continueAsNewSentinel
		if errors.As(err, &canSentinel) {
			result.ContinueAsNew = &ContinueAsNewResult{
				WorkflowID:   canSentinel.Options.WorkflowID,
				WorkflowType: canSentinel.Options.WorkflowType,
				TaskQueue:    canSentinel.Options.TaskQueue,
				TaskID:       task.ID,
			}
			if _, completeErr := w.opts.Connection.CompleteTask(taskCtx, task.ID, w.opts.AgentID, *result); completeErr != nil {
				logger.Warn("complete (continue-as-new) failed", "error", completeErr)
			}
			return
		}
		result.Failure = errorToFailure(err)
		if _, failErr := w.opts.Connection.FailTask(taskCtx, task.ID, w.opts.AgentID, err.Error(), result); failErr != nil {
			logger.Warn("fail task failed", "error", failErr)
		}
		return
	}
	if _, completeErr := w.opts.Connection.CompleteTask(taskCtx, task.ID, w.opts.AgentID, *result); completeErr != nil {
		logger.Warn("complete task failed", "error", completeErr)
	}
}

func (w *Worker) dispatch(ctx context.Context, task *Task) (*TaskResult, error) {
	switch {
	case task.Type == TaskTypeNoop:
		return &TaskResult{Message: "noop task completed"}, nil
	case strings.HasPrefix(task.Type, TaskTypePrefixActivity):
		return w.runActivity(ctx, task)
	case strings.HasPrefix(task.Type, TaskTypePrefixWorkflow):
		return w.runWorkflow(ctx, task)
	case strings.HasPrefix(task.Type, TaskTypePrefixQuery), strings.HasPrefix(task.Type, TaskTypePrefixUpdate):
		// Query/update tasks attach to a parent workflow execution. The
		// SDK Worker reuses the workflow runtime — but only if the
		// workflow is currently in memory, which we don't model here.
		// Fail with a clear non-retryable error so operators see why.
		return nil, NewNonRetryableApplicationFailure(
			"sdk worker does not yet handle "+task.Type+" tasks (workflow runtime is single-run only)",
			"UnsupportedTaskType",
			task.Type,
		)
	default:
		return nil, NewNonRetryableApplicationFailure("unsupported task type "+task.Type, "UnsupportedTaskType", task.Type)
	}
}

func (w *Worker) runActivity(ctx context.Context, task *Task) (*TaskResult, error) {
	activityType := strings.TrimPrefix(task.Type, TaskTypePrefixActivity)
	fn, ok := w.opts.Activities[activityType]
	if !ok {
		return nil, NewNonRetryableApplicationFailure(
			fmt.Sprintf("activity %q is not registered", activityType),
			"ActivityNotRegistered",
			activityType,
		)
	}
	args, err := decodeActivityArgs(task.Payload)
	if err != nil {
		return nil, NewNonRetryableApplicationFailure(
			fmt.Sprintf("decode activity args: %v", err),
			"BadActivityPayload",
		)
	}
	runtime := &activityRuntime{
		info: ActivityInfo{
			TaskID:    task.ID,
			AgentID:   w.opts.AgentID,
			Namespace: task.Namespace,
			Queue:     task.Queue,
			Type:      activityType,
			Attempt:   task.Attempt,
			Args:      args,
		},
		emitter: func(ctx context.Context, ev TaskEventInput) error {
			return w.opts.Connection.EmitTaskEvent(ctx, task.ID, w.opts.AgentID, ev)
		},
	}
	activityCtx := withActivityRuntime(ctx, runtime)
	value, err := fn(activityCtx, args)
	if err != nil {
		return &TaskResult{}, err
	}
	return &TaskResult{Value: value}, nil
}

func (w *Worker) runWorkflow(ctx context.Context, task *Task) (*TaskResult, error) {
	workflowType := strings.TrimPrefix(task.Type, TaskTypePrefixWorkflow)
	fn, ok := w.opts.Workflows[workflowType]
	if !ok {
		return nil, NewNonRetryableApplicationFailure(
			fmt.Sprintf("workflow %q is not registered", workflowType),
			"WorkflowNotRegistered",
			workflowType,
		)
	}
	args, workflowID, err := decodeWorkflowPayload(task)
	if err != nil {
		return nil, NewNonRetryableApplicationFailure(
			fmt.Sprintf("decode workflow payload: %v", err),
			"BadWorkflowPayload",
		)
	}
	// Pull the durable history so the replay returns persisted command
	// results to the workflow body. A failure here is a transport problem,
	// not a workflow failure: proceeding with an empty replay would let the
	// workflow re-enqueue every command it has already completed, which is
	// the duplicate-work bug we exist to prevent. Block the task with a
	// clear reason so the runtime service redelivers and we can try again.
	history, err := w.opts.Connection.GetWorkflowHistory(ctx, workflowID)
	if err != nil {
		w.logger.Warn("workflow history fetch failed; blocking task for redelivery",
			"workflow_id", workflowID,
			"error", err,
		)
		if _, blockErr := w.opts.Connection.BlockTask(ctx, task.ID, w.opts.AgentID, "workflow history fetch failed: "+err.Error()); blockErr != nil {
			w.logger.Warn("block workflow task after history failure", "task_id", task.ID, "error", blockErr)
			return &TaskResult{}, blockErr
		}
		return nil, errWorkflowAlreadyBlocked
	}
	rp := replay.New(history)
	wfctx := &workflowContext{
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
	value, err := fn(wfctx, args)
	if err != nil {
		// Workflow yielded on a pending command. Block the task so the
		// runtime service redelivers it after the dependency resolves;
		// returning this from runWorkflow as a regular error would mark the
		// workflow failed, which is wrong — the workflow hasn't failed,
		// it's waiting.
		if IsWorkflowSuspended(err) {
			if _, blockErr := w.opts.Connection.BlockTask(ctx, task.ID, w.opts.AgentID, err.Error()); blockErr != nil {
				w.logger.Warn("block workflow task failed", "task_id", task.ID, "error", blockErr)
				return &TaskResult{}, blockErr
			}
			// Returning a non-error result with the special "blocked"
			// signaling: the dispatch path checks for this via an explicit
			// sentinel below.
			return nil, errWorkflowAlreadyBlocked
		}
		return &TaskResult{}, err
	}
	return &TaskResult{Value: value}, nil
}

// errWorkflowAlreadyBlocked tells handleTask that runWorkflow already called
// BlockTask on this workflow task — handleTask should NOT proceed to call
// CompleteTask or FailTask, since the runtime service is now waiting for
// the workflow's dependencies to resolve.
var errWorkflowAlreadyBlocked = errors.New("postgrip-agent: workflow task blocked")

// startHeartbeat fires TaskEventKindHeartbeat on a timer derived from the
// task's lease_timeout_seconds. Returns a derived context that is cancelled
// when the caller invokes the returned cancel func; the heartbeat goroutine
// also exits on cancellation.
func (w *Worker) startHeartbeat(parent context.Context, task *Task) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	interval := heartbeatInterval(task.LeaseTimeoutSeconds)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ev := TaskEventInput{
					Kind:    TaskEventKindHeartbeat,
					Stage:   "running",
					Message: "agent is still executing task",
					Details: map[string]any{"type": task.Type, "attempt": task.Attempt},
				}
				if err := w.opts.Connection.HeartbeatTask(ctx, task.ID, w.opts.AgentID, &ev); err != nil {
					w.logger.Warn("task heartbeat failed", "task_id", task.ID, "error", err)
				}
			}
		}
	}()
	return ctx, cancel
}

func heartbeatInterval(leaseTimeoutSeconds int) time.Duration {
	if leaseTimeoutSeconds <= 1 {
		return 500 * time.Millisecond
	}
	d := time.Duration(leaseTimeoutSeconds) * time.Second / 3
	if d < 500*time.Millisecond {
		return 500 * time.Millisecond
	}
	return d
}

func sleepOrStop(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return true
	case <-time.After(d):
		return true
	}
}

// decodeActivityArgs unpacks the wire-format activity payload into the
// []any the customer's ActivityFunc expects. Tolerates missing fields
// (returns an empty arg list) so trivial activities don't have to send a
// dummy payload.
func decodeActivityArgs(raw json.RawMessage) ([]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var envelope struct {
		Args []any `json:"args"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	return envelope.Args, nil
}

func decodeWorkflowPayload(task *Task) ([]any, string, error) {
	if len(task.Payload) == 0 {
		return nil, task.ID, nil
	}
	var envelope struct {
		Args       []any  `json:"args"`
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(task.Payload, &envelope); err != nil {
		return nil, "", err
	}
	wfID := envelope.WorkflowID
	if wfID == "" {
		wfID = task.ID
	}
	return envelope.Args, wfID, nil
}
