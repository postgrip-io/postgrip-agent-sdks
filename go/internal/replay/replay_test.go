package replay

import (
	"errors"
	"testing"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

func TestNewSplitsByType(t *testing.T) {
	t.Parallel()
	r := New([]protocol.WorkflowHistoryEvent{
		{ID: "1", Type: EventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		{ID: "2", Type: EventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1000)}},
		{ID: "3", Type: EventTimerFired, TaskID: "tmr-1"},
		{ID: "4", Type: EventChildWorkflowStarted, TaskID: "wf-child-1", Attributes: map[string]any{"workflow_type": "Sub"}},
		{ID: "5", Type: EventWorkflowCancellationRequest},
		{ID: "6", Type: EventWorkflowSignaled, Attributes: map[string]any{"name": "ping", "args": []any{1}}},
		{ID: "7", Type: EventWorkflowSignaled, Attributes: map[string]any{"name": "ping", "args": []any{2}}},
		{ID: "8", Type: EventWorkflowSignaled, Attributes: map[string]any{"name": "other", "args": []any{}}},
	})
	if len(r.activities) != 1 || len(r.timers) != 1 || len(r.children) != 1 {
		t.Fatalf("filtered slices wrong: %d activities, %d timers, %d children", len(r.activities), len(r.timers), len(r.children))
	}
	if !r.cancellationRequested {
		t.Fatal("cancellation flag not set")
	}
	pings := r.SignalsByName("ping")
	if len(pings) != 2 {
		t.Fatalf("ping signals = %d, want 2", len(pings))
	}
	if pings[0][0].(int) != 1 || pings[1][0].(int) != 2 {
		t.Fatalf("signal args order wrong: %#v", pings)
	}
}

func TestNextActivityAdvancesCursorAndDetectsDeterminism(t *testing.T) {
	t.Parallel()
	r := New([]protocol.WorkflowHistoryEvent{
		{Type: EventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		{Type: EventActivityTaskScheduled, TaskID: "act-2", Attributes: map[string]any{"activity_type": "Farewell"}},
	})
	first, err := r.NextActivity("Greet")
	if err != nil || first == nil || first.TaskID != "act-1" {
		t.Fatalf("first NextActivity = %+v, err=%v", first, err)
	}
	// Determinism violation: workflow asks for a different activity than
	// what's recorded next.
	_, err = r.NextActivity("Whoops")
	if err == nil {
		t.Fatal("expected determinism violation on mismatched activity")
	}
	var det *DeterminismError
	if !errors.As(err, &det) {
		t.Fatalf("expected DeterminismError, got %T: %v", err, err)
	}
	// Cursor still advanced past the violation, so the next call returns nil
	// (history exhausted).
	if next, err := r.NextActivity("Anything"); err != nil || next != nil {
		t.Fatalf("NextActivity past end = %+v, err=%v", next, err)
	}
}

func TestNextTimerChecksDuration(t *testing.T) {
	t.Parallel()
	r := New([]protocol.WorkflowHistoryEvent{
		{Type: EventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(2500)}},
	})
	if _, err := r.NextTimer(2000); err == nil {
		t.Fatal("expected determinism violation when timer duration changes")
	}
	r.timerCursor = 0
	if ev, err := r.NextTimer(2500); err != nil || ev == nil || ev.TaskID != "tmr-1" {
		t.Fatalf("NextTimer happy = %+v, err=%v", ev, err)
	}
}

func TestIsTimerFired(t *testing.T) {
	t.Parallel()
	r := New([]protocol.WorkflowHistoryEvent{
		{Type: EventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1000)}},
		{Type: EventTimerFired, TaskID: "tmr-1"},
		{Type: EventTimerStarted, TaskID: "tmr-2", Attributes: map[string]any{"duration_ms": float64(1000)}},
	})
	first, _ := r.NextTimer(1000)
	if !r.IsTimerFired(first) {
		t.Fatal("expected first timer to be fired")
	}
	second, _ := r.NextTimer(1000)
	if r.IsTimerFired(second) {
		t.Fatal("expected second timer to still be pending")
	}
}

func TestCheckCancellationReturnsSentinel(t *testing.T) {
	t.Parallel()
	r := New([]protocol.WorkflowHistoryEvent{
		{Type: EventWorkflowCancellationRequest},
	})
	if err := r.CheckCancellation(); !errors.Is(err, ErrCancellationRequested) {
		t.Fatalf("CheckCancellation = %v, want ErrCancellationRequested", err)
	}
	r2 := New(nil)
	if err := r2.CheckCancellation(); err != nil {
		t.Fatalf("CheckCancellation on empty = %v, want nil", err)
	}
}

func TestNumberAsInt64HandlesJSONShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int(7), 7, true},
		{int64(8), 8, true},
		{float64(9), 9, true},
		{float32(10.7), 10, true},
		{"11", 0, false},
	}
	for _, c := range cases {
		got, ok := numberAsInt64(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("numberAsInt64(%v) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
