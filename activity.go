package sdk

import (
	"context"
	"errors"
)

// activityContextKey is unexported so ActivityInfo / Heartbeat / Milestone
// only work inside an activity invocation, never across goroutines that
// happen to inherit a customer's context unintentionally.
type activityContextKey struct{}

// ActivityInfo carries the runtime metadata about the in-flight activity.
// Customer code retrieves it inside an activity body via ActivityInfo(ctx).
type ActivityInfo struct {
	TaskID    string
	AgentID   string
	Namespace string
	Queue     string
	Type      string
	Attempt   int
	Args      []any
}

type activityRuntime struct {
	info     ActivityInfo
	emitter  func(ctx context.Context, ev TaskEventInput) error
	complete func()
}

// withActivityRuntime returns a new context that ActivityInfo / Heartbeat /
// Milestone helpers can read out of inside the activity body.
func withActivityRuntime(parent context.Context, runtime *activityRuntime) context.Context {
	return context.WithValue(parent, activityContextKey{}, runtime)
}

func runtimeFromActivityCtx(ctx context.Context) (*activityRuntime, error) {
	r, ok := ctx.Value(activityContextKey{}).(*activityRuntime)
	if !ok || r == nil {
		return nil, errors.New("postgrip-agent: ActivityInfo / Heartbeat / Milestone called outside an activity invocation")
	}
	return r, nil
}

// GetActivityInfo returns the runtime metadata for the in-flight activity.
// Returns an error when called outside an activity invocation.
func GetActivityInfo(ctx context.Context) (ActivityInfo, error) {
	r, err := runtimeFromActivityCtx(ctx)
	if err != nil {
		return ActivityInfo{}, err
	}
	return r.info, nil
}

// Heartbeat emits a TaskEventKindHeartbeat event for the in-flight activity.
// Long-running activities should call this periodically (faster than the
// task's lease timeout) so the runtime service does not redeliver the task.
// Heartbeat returns an error if called outside an activity invocation, or if
// the underlying emit fails. The returned error wraps the runtime-service
// failure for inspection with errors.As.
func Heartbeat(ctx context.Context, details map[string]any) error {
	r, err := runtimeFromActivityCtx(ctx)
	if err != nil {
		return err
	}
	return r.emitter(ctx, TaskEventInput{
		Kind:    TaskEventKindHeartbeat,
		Stage:   "running",
		Message: "activity is still executing",
		Details: details,
	})
}

// ActivityMilestone emits a TaskEventKindMilestone event with optional index
// and total, so operators can render ordered progress. Mirrors the TS
// activity.milestone(...) and Python activity.milestone(...) helpers.
func ActivityMilestone(ctx context.Context, name string, opts MilestoneOptions) error {
	r, err := runtimeFromActivityCtx(ctx)
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
	return r.emitter(ctx, TaskEventInput{
		Kind:    TaskEventKindMilestone,
		Stage:   name,
		Message: name,
		Details: details,
	})
}
