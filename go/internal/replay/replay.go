// Package replay walks a workflow's durable history and serves persisted
// command results back to the workflow body so it can replay deterministically.
//
// The package is internal: it returns sentinel errors (ErrCancellationRequested,
// DeterminismError) that the SDK translates into the public CancelledFailure
// and WorkflowDeterminismViolation ApplicationFailure at the call boundary.
// This keeps the replay engine free of SDK error types and avoids an import
// cycle with the root sdk package.
package replay

import (
	"errors"
	"fmt"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

// History event type strings the runtime service writes. Mirrors the
// strings the TypeScript SDK matches on (postgrip-agent/typescript/src/agent.ts).
// Exported so SDK-side integration tests can construct realistic histories
// without copy-pasting the wire strings.
const (
	EventActivityTaskScheduled       = "ActivityTaskScheduled"
	EventActivityTaskRetryScheduled  = "ActivityTaskRetryScheduled"
	EventActivityTaskCanceled        = "ActivityTaskCanceled"
	EventTimerStarted                = "TimerStarted"
	EventTimerFired                  = "TimerFired"
	EventChildWorkflowStarted        = "ChildWorkflowExecutionStarted"
	EventWorkflowSignaled            = "WorkflowSignaled"
	EventWorkflowCancellationRequest = "WorkflowCancellationRequested"
)

// ErrCancellationRequested is the sentinel returned by Replay.CheckCancellation
// when the workflow's history contains a WorkflowCancellationRequested event.
// The SDK translates it to a CancelledFailure at the call boundary.
var ErrCancellationRequested = errors.New("workflow cancellation requested")

// DeterminismError is returned by Replay.NextActivity / NextTimer / NextChild
// when the workflow body's next command disagrees with the recorded history.
// The SDK wraps this in a non-retryable ApplicationFailure tagged
// "WorkflowDeterminismViolation" at the call boundary.
type DeterminismError struct {
	Message string
}

func (e *DeterminismError) Error() string { return e.Message }

// Replay walks a workflow's durable history. Each command type (activity,
// timer, child workflow) has its own cursor: every call from the workflow
// advances the cursor by one event in that type's filtered slice. Mismatches
// raise a DeterminismError; cursor exhaustion returns (nil, nil) so the
// caller knows to schedule a fresh command and suspend.
type Replay struct {
	history []protocol.WorkflowHistoryEvent

	activities []protocol.WorkflowHistoryEvent
	timers     []protocol.WorkflowHistoryEvent
	children   []protocol.WorkflowHistoryEvent

	activityCursor int
	timerCursor    int
	childCursor    int

	cancellationRequested bool
}

// New builds a Replay from durable history. Worker.runWorkflow constructs
// one before each workflow body invocation so the body sees the same
// command results across replays.
func New(history []protocol.WorkflowHistoryEvent) *Replay {
	r := &Replay{history: history}
	for i := range history {
		ev := &history[i]
		switch ev.Type {
		case EventActivityTaskScheduled:
			r.activities = append(r.activities, *ev)
		case EventTimerStarted:
			r.timers = append(r.timers, *ev)
		case EventChildWorkflowStarted:
			r.children = append(r.children, *ev)
		case EventWorkflowCancellationRequest:
			r.cancellationRequested = true
		}
	}
	return r
}

// NextActivity returns the next ActivityTaskScheduled event whose recorded
// activity_type matches name. Returns (nil, nil) when the cursor is past
// the recorded events; returns a DeterminismError when history disagrees.
func (r *Replay) NextActivity(name string) (*protocol.WorkflowHistoryEvent, error) {
	if r.activityCursor >= len(r.activities) {
		return nil, nil
	}
	ev := &r.activities[r.activityCursor]
	r.activityCursor++
	recorded, _ := ev.Attributes["activity_type"].(string)
	if recorded != "" && recorded != name {
		return nil, &DeterminismError{Message: fmt.Sprintf("activity command changed at index %d: history=%q replay=%q", r.activityCursor-1, recorded, name)}
	}
	return ev, nil
}

// NextTimer mirrors NextActivity for timers. duration_ms in the recorded
// event must match what the workflow asked for (rounded to int) — workflow
// code that varies sleep duration across replays is non-deterministic.
func (r *Replay) NextTimer(durationMs int64) (*protocol.WorkflowHistoryEvent, error) {
	if r.timerCursor >= len(r.timers) {
		return nil, nil
	}
	ev := &r.timers[r.timerCursor]
	r.timerCursor++
	if recorded, ok := ev.Attributes["duration_ms"]; ok {
		recordedMs, ok := numberAsInt64(recorded)
		if ok && recordedMs != durationMs {
			return nil, &DeterminismError{Message: fmt.Sprintf("timer command changed at index %d: history=%dms replay=%dms", r.timerCursor-1, recordedMs, durationMs)}
		}
	}
	return ev, nil
}

// NextChild mirrors NextActivity for child workflows.
func (r *Replay) NextChild(workflowType string) (*protocol.WorkflowHistoryEvent, error) {
	if r.childCursor >= len(r.children) {
		return nil, nil
	}
	ev := &r.children[r.childCursor]
	r.childCursor++
	recorded, _ := ev.Attributes["workflow_type"].(string)
	if recorded != "" && recorded != workflowType {
		return nil, &DeterminismError{Message: fmt.Sprintf("child workflow command changed at index %d: history=%q replay=%q", r.childCursor-1, recorded, workflowType)}
	}
	return ev, nil
}

// IsTimerFired reports whether a TimerFired event keyed on the same task_id
// as the supplied TimerStarted event exists in history.
func (r *Replay) IsTimerFired(timerStarted *protocol.WorkflowHistoryEvent) bool {
	if timerStarted == nil || timerStarted.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type == EventTimerFired && ev.TaskID == timerStarted.TaskID {
			return true
		}
	}
	return false
}

// IsActivityCanceled reports whether the activity scheduled at this index
// was cancelled before completing.
func (r *Replay) IsActivityCanceled(scheduled *protocol.WorkflowHistoryEvent) bool {
	if scheduled == nil || scheduled.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type == EventActivityTaskCanceled && ev.TaskID == scheduled.TaskID {
			return true
		}
	}
	return false
}

// HasActivityRetryScheduled reports whether the runtime queued a retry for
// the failed activity. Workflow code that wants a clean retry semantic
// recurses into ExecuteActivity when this returns true.
func (r *Replay) HasActivityRetryScheduled(scheduled *protocol.WorkflowHistoryEvent) bool {
	if scheduled == nil || scheduled.TaskID == "" {
		return false
	}
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type != EventActivityTaskRetryScheduled {
			continue
		}
		prev, _ := ev.Attributes["previous_task"].(string)
		if prev == scheduled.TaskID {
			return true
		}
	}
	return false
}

// CheckCancellation returns ErrCancellationRequested if the workflow has a
// recorded WorkflowCancellationRequested event. Workflow context methods
// call this before scheduling new commands so cancellation propagates
// promptly during replay.
func (r *Replay) CheckCancellation() error {
	if r == nil || !r.cancellationRequested {
		return nil
	}
	return ErrCancellationRequested
}

// SignalsByName returns the ordered args lists for every WorkflowSignaled
// event whose recorded name matches. workflowContext.GetSignalChannel uses
// this to seed each replay's signal channel with the durable signal stream.
func (r *Replay) SignalsByName(name string) [][]any {
	var out [][]any
	for i := range r.history {
		ev := &r.history[i]
		if ev.Type != EventWorkflowSignaled {
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
