// Package worker runs a customer-side polling agent that leases tasks from
// the runtime service, heartbeats them, and dispatches them to registered
// workflow.Func and activity.Func bodies.
//
// Customer code constructs a Worker via New(Options{...}), then calls Run.
// Workflow option types live in /workflow; activity option types live in
// /activity; this package wires them to a /client.Connection and the
// internal replay engine to provide the durable workflow runtime.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/postgrip-io/postgrip-agent-sdks/go/activity"
	"github.com/postgrip-io/postgrip-agent-sdks/go/client"
	"github.com/postgrip-io/postgrip-agent-sdks/go/failure"
	"github.com/postgrip-io/postgrip-agent-sdks/go/workflow"
)

// Options configures a customer-side worker that polls the runtime
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
type Options struct {
	Connection         *client.Connection
	Namespace          string
	Queue              string
	AgentID            string
	Workflows          workflow.Registry
	Activities         activity.Registry
	MaxConcurrentTasks int
	PollInterval       time.Duration
	Logger             *slog.Logger
}

// Worker is a polling agent. Run blocks until context cancellation or
// Shutdown; Shutdown drains in-flight tasks (best-effort up to drain
// timeout).
type Worker struct {
	opts   Options
	logger *slog.Logger

	mu        sync.Mutex
	running   bool
	stopOnce  sync.Once
	stopCh    chan struct{}
	inflight  sync.WaitGroup
	semaphore chan struct{}
}

// New validates options and returns a Worker. It does not connect or poll
// until Run is called.
func New(opts Options) (*Worker, error) {
	if opts.Connection == nil {
		return nil, errors.New("postgrip-agent: worker.Options.Connection is required")
	}
	if opts.AgentID == "" {
		opts.AgentID = os.Getenv("POSTGRIP_AGENT_ID")
	}
	if opts.AgentID == "" {
		return nil, errors.New("postgrip-agent: worker.Options.AgentID is required")
	}
	if opts.Namespace == "" {
		opts.Namespace = client.DefaultNamespace
		if namespace := os.Getenv("POSTGRIP_AGENT_NAMESPACE"); namespace != "" {
			opts.Namespace = namespace
		}
	}
	if opts.Queue == "" {
		opts.Queue = client.DefaultQueue
		if queue := os.Getenv("POSTGRIP_AGENT_TASK_QUEUE"); queue != "" {
			opts.Queue = queue
		}
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
		if resp != nil && resp.Directive != nil && resp.Directive.Type == client.AgentPollDirectiveTypeShutdown {
			<-w.semaphore
			w.logger.Info("worker shutdown requested", "agent_id", w.opts.AgentID)
			w.inflight.Wait()
			return nil
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
		go func(task *client.Task) {
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

func (w *Worker) handleTask(ctx context.Context, task *client.Task) {
	taskCtx, cancelHeartbeat := w.startHeartbeat(ctx, task)
	defer cancelHeartbeat()

	startedAt := time.Now().UTC()
	logger := w.logger.With("task_id", task.ID, "task_type", task.Type)

	if err := w.opts.Connection.EmitTaskEvent(taskCtx, task.ID, w.opts.AgentID, client.TaskEventInput{
		Kind:    client.TaskEventKindStarted,
		Message: "agent picked up task",
		Details: map[string]any{"type": task.Type, "attempt": task.Attempt},
	}); err != nil {
		logger.Warn("emit started event failed", "error", err)
	}

	result, err := w.dispatch(taskCtx, task)
	finishedAt := time.Now().UTC()
	if result == nil {
		result = &client.TaskResult{StartedAt: startedAt, FinishedAt: finishedAt}
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
		var canSentinel *workflow.ContinueAsNewSentinel
		if errors.As(err, &canSentinel) {
			result.ContinueAsNew = &client.ContinueAsNewResult{
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
		result.Failure = failure.ToInfo(err)
		if _, failErr := w.opts.Connection.FailTask(taskCtx, task.ID, w.opts.AgentID, err.Error(), result); failErr != nil {
			logger.Warn("fail task failed", "error", failErr)
		}
		return
	}
	if _, completeErr := w.opts.Connection.CompleteTask(taskCtx, task.ID, w.opts.AgentID, *result); completeErr != nil {
		logger.Warn("complete task failed", "error", completeErr)
	}
}

func (w *Worker) dispatch(ctx context.Context, task *client.Task) (*client.TaskResult, error) {
	switch {
	case task.Type == client.TaskTypeNoop:
		return &client.TaskResult{Message: "noop task completed"}, nil
	case strings.HasPrefix(task.Type, client.TaskTypePrefixActivity):
		return w.runActivity(ctx, task)
	case strings.HasPrefix(task.Type, client.TaskTypePrefixWorkflow):
		return w.runWorkflow(ctx, task)
	case strings.HasPrefix(task.Type, client.TaskTypePrefixQuery), strings.HasPrefix(task.Type, client.TaskTypePrefixUpdate):
		// Query/update tasks attach to a parent workflow execution. The
		// SDK Worker reuses the workflow runtime — but only if the
		// workflow is currently in memory, which we don't model here.
		// Fail with a clear non-retryable error so operators see why.
		return nil, failure.NewNonRetryable(
			"sdk worker does not yet handle "+task.Type+" tasks (workflow runtime is single-run only)",
			"UnsupportedTaskType",
			task.Type,
		)
	default:
		return nil, failure.NewNonRetryable("unsupported task type "+task.Type, "UnsupportedTaskType", task.Type)
	}
}

// errWorkflowAlreadyBlocked tells handleTask that runWorkflow already
// called BlockTask on this workflow task — handleTask should NOT proceed
// to call CompleteTask or FailTask, since the runtime service is now
// waiting for the workflow's dependencies to resolve.
var errWorkflowAlreadyBlocked = errors.New("postgrip-agent: workflow task blocked")

// startHeartbeat fires task heartbeat events on a timer derived from the
// task's lease_timeout_seconds. Returns a derived context that is cancelled
// when the caller invokes the returned cancel func; the heartbeat goroutine
// also exits on cancellation.
func (w *Worker) startHeartbeat(parent context.Context, task *client.Task) (context.Context, func()) {
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
				ev := client.TaskEventInput{
					Kind:    client.TaskEventKindHeartbeat,
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
