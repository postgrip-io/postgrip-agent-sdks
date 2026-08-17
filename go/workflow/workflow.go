// Package workflow exposes the workflow-side surface of the SDK: the
// Context interface customer workflow bodies receive, the option structs
// for activity / child-workflow / continue-as-new dispatch, signal channels,
// and the Func/Registry shapes Worker uses to find workflow implementations.
//
// Customer code reaches into this package both at registration time (worker
// passes a workflow.Registry) and inside workflow bodies (the Context
// argument is a workflow.Context). Implementation lives in the worker
// package — workflow only carries the public types.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

// RetryPolicy is re-exported from protocol so customer code doesn't have to
// import the protocol package alongside workflow.
type RetryPolicy = protocol.RetryPolicy

// IDReusePolicy constrains whether a workflow id can be reused for a new
// run, mirroring the TS/Python WorkflowIdReusePolicy.
type IDReusePolicy string

const (
	IDReusePolicyAllowDuplicate           IDReusePolicy = "allow_duplicate"
	IDReusePolicyAllowDuplicateFailedOnly IDReusePolicy = "allow_duplicate_failed_only"
	IDReusePolicyRejectDuplicate          IDReusePolicy = "reject_duplicate"
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

// MilestoneOptions controls workflow milestone emission so callers can
// render ordered progress.
type MilestoneOptions struct {
	Index   int
	Total   int
	Details map[string]any
}

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
	WorkflowIDReusePolicy IDReusePolicy
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

// ContinueAsNewSentinel is the error returned from Context.ContinueAsNew so
// the Worker dispatch can recognize the intent and translate it into a
// runtime-service ContinueAsNewResult on completion. It's exported so the
// worker package can errors.As it; customer code should not construct one
// directly — return ctx.ContinueAsNew(opts) instead.
type ContinueAsNewSentinel struct {
	Options ContinueAsNewOptions
}

func (c *ContinueAsNewSentinel) Error() string {
	return fmt.Sprintf("continue-as-new to %q", c.Options.WorkflowType)
}

// IsContinueAsNew reports whether err is a ContinueAsNew signal.
func IsContinueAsNew(err error) bool {
	var c *ContinueAsNewSentinel
	return errors.As(err, &c)
}

// Suspended is the sentinel returned by workflow context helpers when the
// workflow body cannot proceed (waiting on an in-flight activity / timer /
// child workflow, or on a signal that hasn't arrived yet). The Worker
// recognizes this via errors.As and translates it into a BlockTask call so
// the runtime service re-leases the workflow when its dependencies resolve.
// Exported so the worker package can construct and recognize it.
type Suspended struct {
	Reason string
}

func (e *Suspended) Error() string { return "workflow suspended: " + e.Reason }

// NewSuspended constructs a Suspended sentinel with a printf-style reason.
// Used by the worker package's Context implementation; customer code should
// not construct one directly.
func NewSuspended(format string, args ...any) error {
	return &Suspended{Reason: fmt.Sprintf(format, args...)}
}

// IsSuspended reports whether err (or anything it wraps) is the workflow-
// suspension sentinel. Customer workflow bodies typically just return errors
// from ctx.ExecuteActivity / ctx.Sleep without handling them — those
// propagations carry the sentinel up to the Worker. IsSuspended is exposed
// for advanced callers that need to discriminate.
func IsSuspended(err error) bool {
	var s *Suspended
	return errors.As(err, &s)
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

// NewSignalChannel constructs a channel pre-seeded with persisted signal
// payloads. Used by the worker package's workflow Context implementation.
// Customer code obtains channels via Context.GetSignalChannel.
func NewSignalChannel(name string, persisted [][]any) *SignalChannel {
	bufSize := len(persisted) + 8
	ch := &SignalChannel{name: name, queue: make(chan []any, bufSize)}
	for _, args := range persisted {
		ch.queue <- args
	}
	return ch
}

// Name returns the signal name this channel routes.
func (s *SignalChannel) Name() string { return s.name }

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
		return nil, NewSuspended("waiting for signal %q", s.name)
	}
}

// Context is the workflow-scoped context handed to every Func. It carries
// cancellation, deadline, and the workflow runtime so workflow code can
// sleep, execute activities, schedule child workflows, register query /
// update / signal handlers, and call ContinueAsNew.
//
// Context implements context.Context so customer code can pass it to any
// stdlib helper that takes a context.Context (e.g. http.NewRequestWithContext).
type Context interface {
	context.Context

	// Now returns the workflow's logical time — durable across replays.
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

	// Milestone emits a milestone event for the workflow task.
	Milestone(name string, opts MilestoneOptions) error

	// ContinueAsNew completes the current run and schedules a new run with
	// the supplied options. The function returns an error that the worker
	// translates into a runtime-service ContinueAsNewResult — workflows
	// should `return ctx.ContinueAsNew(...)` from their body.
	ContinueAsNew(opts ContinueAsNewOptions) error
}

// Func is the customer-supplied workflow body. The SDK invokes it with a
// workflow-scoped Context (sleep / activity / child / signal / query /
// update APIs all dispatch through it) and the deserialized args.
type Func func(ctx Context, args []any) (any, error)

// Registry maps workflow types to their implementations. Worker rejects
// tasks for unregistered workflow types with a non-retryable failure.
type Registry map[string]Func
