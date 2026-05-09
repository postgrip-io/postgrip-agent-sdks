package sdk

import (
	"errors"
	"fmt"
)

// History event type strings the runtime service writes. Mirrors the
// strings the TypeScript SDK matches on (postgrip-agent/typescript/src/agent.ts).
const (
	historyEventActivityTaskScheduled       = "ActivityTaskScheduled"
	historyEventActivityTaskRetryScheduled  = "ActivityTaskRetryScheduled"
	historyEventActivityTaskCanceled        = "ActivityTaskCanceled"
	historyEventTimerStarted                = "TimerStarted"
	historyEventTimerFired                  = "TimerFired"
	historyEventChildWorkflowStarted        = "ChildWorkflowExecutionStarted"
	historyEventWorkflowSignaled            = "WorkflowSignaled"
	historyEventWorkflowCancellationRequest = "WorkflowCancellationRequested"
)

// workflowReplay walks a workflow's durable history and serves persisted
// command results back to the workflow body so it can replay deterministically.
//
// Each command type (activity, timer, child workflow) has its own cursor.
// On each call from the workflow, the cursor advances by one event in that
// type's filtered slice; if the workflow's command doesn't match the recorded
// event, the replay raises a determinism violation. When the cursor is past
// the end of the recorded events for that command type, the workflow is
// considered to be "extending" history — the runtime returns nothing matched,
// which the caller treats as "schedule a new command and suspend".
//
// signalChannels is populated from history at construction time; the workflow
// body's GetSignalChannel returns the same channels seeded with persisted
// signal payloads, so a workflow that does ch.Receive() right at the top
// will replay correctly.
type workflowReplay struct {
	history []WorkflowHistoryEvent

	activities []WorkflowHistoryEvent
	timers     []WorkflowHistoryEvent
	children   []WorkflowHistoryEvent

	activityCursor int
	timerCursor    int
	childCursor    int

	cancellationRequested bool
}

func newWorkflowReplay(history []WorkflowHistoryEvent) *workflowReplay {
	r := &workflowReplay{history: history}
	for i := range history {
		ev := &history[i]
		switch ev.Type {
		case historyEventActivityTaskScheduled:
			r.activities = append(r.activities, *ev)
		case historyEventTimerStarted:
			r.timers = append(r.timers, *ev)
		case historyEventChildWorkflowStarted:
			r.children = append(r.children, *ev)
		case historyEventWorkflowCancellationRequest:
			r.cancellationRequested = true
		}
	}
	return r
}

// nextActivity returns the next ActivityTaskScheduled event whose recorded
// activity_type matches the supplied name. If history exists but disagrees,
// raise a determinism violation; if the cursor is past the recorded events,
// return (nil, nil) to indicate "no recorded scheduling — caller should
// enqueue a fresh command".
func (r *workflowReplay) nextActivity(name string) (*WorkflowHistoryEvent, error) {
	if r.activityCursor >= len(r.activities) {
		return nil, nil
	}
	ev := &r.activities[r.activityCursor]
	r.activityCursor++
	recorded, _ := ev.Attributes["activity_type"].(string)
	if recorded != "" && recorded != name {
		return nil, determinismViolation(fmt.Sprintf("activity command changed at index %d: history=%q replay=%q", r.activityCursor-1, recorded, name))
	}
	return ev, nil
}

// nextTimer mirrors nextActivity for timers. duration_ms in the recorded
// event must match what the workflow asked for (rounded to int) — workflow
// code that varies sleep duration across replays is non-deterministic.
func (r *workflowReplay) nextTimer(durationMs int64) (*WorkflowHistoryEvent, error) {
	if r.timerCursor >= len(r.timers) {
		return nil, nil
	}
	ev := &r.timers[r.timerCursor]
	r.timerCursor++
	if recorded, ok := ev.Attributes["duration_ms"]; ok {
		recordedMs, ok := numberAsInt64(recorded)
		if ok && recordedMs != durationMs {
			return nil, determinismViolation(fmt.Sprintf("timer command changed at index %d: history=%dms replay=%dms", r.timerCursor-1, recordedMs, durationMs))
		}
	}
	return ev, nil
}

// nextChild mirrors nextActivity for child workflows.
func (r *workflowReplay) nextChild(workflowType string) (*WorkflowHistoryEvent, error) {
	if r.childCursor >= len(r.children) {
		return nil, nil
	}
	ev := &r.children[r.childCursor]
	r.childCursor++
	recorded, _ := ev.Attributes["workflow_type"].(string)
	if recorded != "" && recorded != workflowType {
		return nil, determinismViolation(fmt.Sprintf("child workflow command changed at index %d: history=%q replay=%q", r.childCursor-1, recorded, workflowType))
	}
	return ev, nil
}

// isTimerFired reports whether a TimerFired event keyed on the same task_id
// as the supplied TimerStarted event exists in history.
func (r *workflowReplay) isTimerFired(timerStarted *WorkflowHistoryEvent) bool {
	if timerStarted == nil || timerStarted.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type == historyEventTimerFired && ev.TaskID == timerStarted.TaskID {
			return true
		}
	}
	return false
}

// isActivityCanceled reports whether the activity scheduled at this index
// was cancelled before completing.
func (r *workflowReplay) isActivityCanceled(scheduled *WorkflowHistoryEvent) bool {
	if scheduled == nil || scheduled.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type == historyEventActivityTaskCanceled && ev.TaskID == scheduled.TaskID {
			return true
		}
	}
	return false
}

// hasActivityRetryScheduled reports whether the runtime queued a retry for
// the failed activity. Workflow code that wants a clean retry semantic
// recurses into executeActivity when this returns true.
func (r *workflowReplay) hasActivityRetryScheduled(scheduled *WorkflowHistoryEvent) bool {
	if scheduled == nil || scheduled.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type != historyEventActivityTaskRetryScheduled {
			continue
		}
		prev, _ := ev.Attributes["previous_task"].(string)
		if prev == scheduled.TaskID {
			return true
		}
	}
	return false
}

// checkCancellation returns a CancelledFailure if the workflow has a
// recorded WorkflowCancellationRequested event. Workflow context methods
// call this before scheduling new commands so cancellation propagates
// promptly during replay.
func (r *workflowReplay) checkCancellation() error {
	if r == nil || !r.cancellationRequested {
		return nil
	}
	return &CancelledFailure{Message: "workflow cancellation requested"}
}

// signalsByName returns the ordered args lists for every WorkflowSignaled
// event whose recorded name matches. workflowContext.GetSignalChannel uses
// this to seed each replay's signal channel with the durable signal stream.
func (r *workflowReplay) signalsByName(name string) [][]any {
	var out [][]any
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type != historyEventWorkflowSignaled {
			continue
		}
		recordedName, _ := ev.Attributes["name"].(string)
		if recordedName != name {
			continue
		}
		args, _ := ev.Attributes["args"].([]any)
		out = append(out, args)
	}
	return out
}

// numberAsInt64 normalizes JSON's float64 / int / int64 numeric decodings
// down to int64. Necessary because Attributes is map[string]any and JSON
// decodes numeric fields as float64 by default.
func numberAsInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case float64:
		return int64(t), true
	case float32:
		return int64(t), true
	}
	return 0, false
}

// determinismViolation builds a non-retryable ApplicationFailure tagged so
// workflow operators can spot non-deterministic workflow code immediately.
func determinismViolation(message string) error {
	return NewNonRetryableApplicationFailure(message, "WorkflowDeterminismViolation")
}

// errWorkflowSuspended is the sentinel error workflow context methods return
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

// IsWorkflowSuspended reports whether err (or anything it wraps) is the
// workflow-suspension sentinel. Customer workflow bodies typically just
// return errors from ctx.ExecuteActivity / ctx.Sleep without handling them
// — those propagations carry the sentinel up to the Worker. IsWorkflowSuspended
// is exposed for advanced callers that need to discriminate.
func IsWorkflowSuspended(err error) bool {
	var s *errWorkflowSuspended
	return errors.As(err, &s)
}
