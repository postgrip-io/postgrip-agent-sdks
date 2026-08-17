// Package activity exposes the activity-side surface of the SDK: the Func
// type customer activity bodies satisfy, the Info struct describing the
// in-flight task, and the Heartbeat / Milestone / Stdout / Stderr helpers
// customer code calls from inside an activity.
//
// The Worker constructs a runtime per activity task and stashes it on the
// context the activity body receives (via WithRuntime). GetInfo / Heartbeat
// / Milestone / Stdout / Stderr read from that context value, so calling them
// outside an activity invocation returns an error rather than silently
// no-oping.
package activity

import (
	"context"
	"errors"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// EventInput is re-exported from protocol so customer code doesn't have to
// import the protocol package alongside activity.
type EventInput = protocol.TaskEventInput

// Func is the customer-supplied activity body. Standard context.Context is
// honored for cancellation/deadline; the activity's task id and runtime
// metadata can be retrieved via GetInfo(ctx).
type Func func(ctx context.Context, args []any) (any, error)

// Registry maps activity names to their implementations.
type Registry map[string]Func

// Info carries the runtime metadata about the in-flight activity. Customer
// code retrieves it inside an activity body via GetInfo(ctx).
type Info struct {
	TaskID    string
	AgentID   string
	Namespace string
	Queue     string
	Type      string
	Attempt   int
	Args      []any
}

// MilestoneOptions controls activity milestone emission so callers can
// render ordered progress.
type MilestoneOptions struct {
	Index   int
	Total   int
	Details map[string]any
}

// OutputOptions controls stdout/stderr event emission for an activity.
type OutputOptions struct {
	Stage   string
	Message string
	Details map[string]any
}

// Runtime is the per-task scaffolding the worker constructs and attaches to
// the activity context. Customer code never constructs one directly —
// worker calls WithRuntime so GetInfo / Heartbeat / Milestone / Stdout /
// Stderr can read it back out of the context inside the activity body.
type Runtime struct {
	Info    Info
	Emitter func(ctx context.Context, ev EventInput) error
}

type contextKey struct{}

// WithRuntime returns a derived context that GetInfo / Heartbeat / Milestone /
// Stdout / Stderr can read. The worker package calls this before invoking the
// activity Func.
func WithRuntime(parent context.Context, runtime *Runtime) context.Context {
	return context.WithValue(parent, contextKey{}, runtime)
}

func runtimeFromCtx(ctx context.Context) (*Runtime, error) {
	r, ok := ctx.Value(contextKey{}).(*Runtime)
	if !ok || r == nil {
		return nil, errors.New("postgrip-agent: activity helpers called outside an activity invocation")
	}
	return r, nil
}

// GetInfo returns the runtime metadata for the in-flight activity. Returns
// an error when called outside an activity invocation.
func GetInfo(ctx context.Context) (Info, error) {
	r, err := runtimeFromCtx(ctx)
	if err != nil {
		return Info{}, err
	}
	return r.Info, nil
}

// Heartbeat emits a heartbeat event for the in-flight activity. Long-running
// activities should call this periodically (faster than the task's lease
// timeout) so the runtime service does not redeliver the task. Returns an
// error if called outside an activity invocation, or if the underlying emit
// fails.
func Heartbeat(ctx context.Context, details map[string]any) error {
	r, err := runtimeFromCtx(ctx)
	if err != nil {
		return err
	}
	return r.Emitter(ctx, EventInput{
		Kind:    protocol.TaskEventKindHeartbeat,
		Stage:   "running",
		Message: "activity is still executing",
		Details: details,
	})
}

// Milestone emits a milestone event with optional index and total, so
// operators can render ordered progress. Mirrors the TS activity.milestone(...)
// and Python activity.milestone(...) helpers.
func Milestone(ctx context.Context, name string, opts MilestoneOptions) error {
	r, err := runtimeFromCtx(ctx)
	if err != nil {
		return err
	}
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
	return r.Emitter(ctx, EventInput{
		Kind:    protocol.TaskEventKindMilestone,
		Stage:   name,
		Message: name,
		Details: details,
	})
}

// Stdout emits a stdout event for the in-flight activity task. The PostGrip
// console renders these events in the task output panel.
func Stdout(ctx context.Context, data string, opts ...OutputOptions) error {
	return emitOutput(ctx, protocol.TaskEventKindStdout, "stdout", data, opts...)
}

// Stderr emits a stderr event for the in-flight activity task. The PostGrip
// console renders these events in the task output panel.
func Stderr(ctx context.Context, data string, opts ...OutputOptions) error {
	return emitOutput(ctx, protocol.TaskEventKindStderr, "stderr", data, opts...)
}

func emitOutput(ctx context.Context, kind protocol.TaskEventKind, stream, data string, opts ...OutputOptions) error {
	r, err := runtimeFromCtx(ctx)
	if err != nil {
		return err
	}
	event := EventInput{
		Kind:   kind,
		Stage:  "activity",
		Stream: stream,
		Data:   data,
	}
	if len(opts) > 0 {
		opt := opts[0]
		if opt.Stage != "" {
			event.Stage = opt.Stage
		}
		event.Message = opt.Message
		if len(opt.Details) > 0 {
			event.Details = map[string]any{}
			for k, v := range opt.Details {
				event.Details[k] = v
			}
		}
	}
	return r.Emitter(ctx, event)
}
