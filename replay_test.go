package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewWorkflowReplaySplitsByType(t *testing.T) {
	t.Parallel()
	r := newWorkflowReplay([]WorkflowHistoryEvent{
		{ID: "1", Type: historyEventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		{ID: "2", Type: historyEventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1000)}},
		{ID: "3", Type: historyEventTimerFired, TaskID: "tmr-1"},
		{ID: "4", Type: historyEventChildWorkflowStarted, TaskID: "wf-child-1", Attributes: map[string]any{"workflow_type": "Sub"}},
		{ID: "5", Type: historyEventWorkflowCancellationRequest},
		{ID: "6", Type: historyEventWorkflowSignaled, Attributes: map[string]any{"name": "ping", "args": []any{1}}},
		{ID: "7", Type: historyEventWorkflowSignaled, Attributes: map[string]any{"name": "ping", "args": []any{2}}},
		{ID: "8", Type: historyEventWorkflowSignaled, Attributes: map[string]any{"name": "other", "args": []any{}}},
	})
	if len(r.activities) != 1 || len(r.timers) != 1 || len(r.children) != 1 {
		t.Fatalf("filtered slices wrong: %d activities, %d timers, %d children", len(r.activities), len(r.timers), len(r.children))
	}
	if !r.cancellationRequested {
		t.Fatal("cancellation flag not set")
	}
	pings := r.signalsByName("ping")
	if len(pings) != 2 {
		t.Fatalf("ping signals = %d, want 2", len(pings))
	}
	if pings[0][0].(int) != 1 || pings[1][0].(int) != 2 {
		t.Fatalf("signal args order wrong: %#v", pings)
	}
}

func TestNextActivityAdvancesCursorAndDetectsDeterminism(t *testing.T) {
	t.Parallel()
	r := newWorkflowReplay([]WorkflowHistoryEvent{
		{Type: historyEventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		{Type: historyEventActivityTaskScheduled, TaskID: "act-2", Attributes: map[string]any{"activity_type": "Farewell"}},
	})
	first, err := r.nextActivity("Greet")
	if err != nil || first == nil || first.TaskID != "act-1" {
		t.Fatalf("first nextActivity = %+v, err=%v", first, err)
	}
	// Determinism violation: workflow asks for a different activity than
	// what's recorded next.
	if _, err := r.nextActivity("Whoops"); err == nil {
		t.Fatal("expected determinism violation on mismatched activity")
	}
	// Cursor still advanced past the violation, so the next call returns nil
	// (history exhausted).
	if next, err := r.nextActivity("Anything"); err != nil || next != nil {
		t.Fatalf("nextActivity past end = %+v, err=%v", next, err)
	}
}

func TestNextTimerChecksDuration(t *testing.T) {
	t.Parallel()
	r := newWorkflowReplay([]WorkflowHistoryEvent{
		{Type: historyEventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(2500)}},
	})
	// Workflow asks for a different duration -> determinism violation.
	if _, err := r.nextTimer(2000); err == nil {
		t.Fatal("expected determinism violation when timer duration changes")
	}
	// Reset cursor to test happy path.
	r.timerCursor = 0
	if ev, err := r.nextTimer(2500); err != nil || ev == nil || ev.TaskID != "tmr-1" {
		t.Fatalf("nextTimer happy = %+v, err=%v", ev, err)
	}
}

func TestIsTimerFired(t *testing.T) {
	t.Parallel()
	r := newWorkflowReplay([]WorkflowHistoryEvent{
		{Type: historyEventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1000)}},
		{Type: historyEventTimerFired, TaskID: "tmr-1"},
		{Type: historyEventTimerStarted, TaskID: "tmr-2", Attributes: map[string]any{"duration_ms": float64(1000)}},
	})
	first, _ := r.nextTimer(1000)
	if !r.isTimerFired(first) {
		t.Fatal("expected first timer to be fired")
	}
	second, _ := r.nextTimer(1000)
	if r.isTimerFired(second) {
		t.Fatal("expected second timer to still be pending")
	}
}

func TestSignalChannelDrainsThenSuspends(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: historyEventWorkflowSignaled, Attributes: map[string]any{"name": "ready", "args": []any{"a"}}},
		{Type: historyEventWorkflowSignaled, Attributes: map[string]any{"name": "ready", "args": []any{"b"}}},
	})
	ch := wfctx.GetSignalChannel("ready")
	args, err := ch.Receive(wfctx)
	if err != nil || args[0] != "a" {
		t.Fatalf("first receive = %v, err=%v", args, err)
	}
	args, err = ch.Receive(wfctx)
	if err != nil || args[0] != "b" {
		t.Fatalf("second receive = %v, err=%v", args, err)
	}
	if _, err := ch.Receive(wfctx); !IsWorkflowSuspended(err) {
		t.Fatalf("expected suspend after draining buffer, got %v", err)
	}
}

func TestSleepReturnsNilWhenTimerFiredInHistory(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: historyEventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1500)}},
		{Type: historyEventTimerFired, TaskID: "tmr-1"},
	})
	if err := wfctx.Sleep(1500 * time.Millisecond); err != nil {
		t.Fatalf("Sleep with fired timer = %v, want nil", err)
	}
}

func TestSleepSuspendsWhenTimerStillPending(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: historyEventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1500)}},
	})
	err := wfctx.Sleep(1500 * time.Millisecond)
	if !IsWorkflowSuspended(err) {
		t.Fatalf("expected suspend when timer pending, got %v", err)
	}
}

func TestSleepSchedulesAndSuspendsWhenHistoryExhausted(t *testing.T) {
	t.Parallel()
	var seenType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req EnqueueTaskRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		seenType = req.Type
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tmr-new","namespace":"default","queue":"default","type":"timer","state":"queued","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	wfctx := &workflowContext{
		Context:    context.Background(),
		conn:       conn,
		namespace:  "default",
		queue:      "default",
		taskID:     "wf-task",
		workflowID: "wf-1",
		now:        time.Now().UTC(),
		replay:     newWorkflowReplay(nil),
	}
	err := wfctx.Sleep(2 * time.Second)
	if !IsWorkflowSuspended(err) {
		t.Fatalf("expected suspend after scheduling timer, got %v", err)
	}
	if seenType != TaskTypeTimer {
		t.Fatalf("scheduled type = %q, want timer", seenType)
	}
}

func TestExecuteActivityReturnsHistoryResult(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Worker fetched the persisted activity task; respond with success.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"act-1","namespace":"default","queue":"default","type":"activity:Greet","state":"succeeded","attempt":1,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:01Z","result":{"value":"hello, world"}}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	wfctx := &workflowContext{
		Context: context.Background(),
		conn:    conn,
		replay: newWorkflowReplay([]WorkflowHistoryEvent{
			{Type: historyEventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		}),
	}
	var result string
	if err := wfctx.ExecuteActivity("Greet", []any{"world"}, &result, nil); err != nil {
		t.Fatalf("ExecuteActivity = %v", err)
	}
	if result != "hello, world" {
		t.Fatalf("result = %q, want 'hello, world'", result)
	}
}

func TestExecuteActivitySuspendsWhenActivityStillRunning(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"act-1","namespace":"default","queue":"default","type":"activity:Greet","state":"leased","attempt":1,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	wfctx := &workflowContext{
		Context: context.Background(),
		conn:    conn,
		replay: newWorkflowReplay([]WorkflowHistoryEvent{
			{Type: historyEventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		}),
	}
	var ignored string
	err := wfctx.ExecuteActivity("Greet", nil, &ignored, nil)
	if !IsWorkflowSuspended(err) {
		t.Fatalf("expected suspend, got %v", err)
	}
}

func TestExecuteActivityRaisesDeterminismOnNameDrift(t *testing.T) {
	t.Parallel()
	wfctx := &workflowContext{
		Context: context.Background(),
		replay: newWorkflowReplay([]WorkflowHistoryEvent{
			{Type: historyEventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		}),
	}
	err := wfctx.ExecuteActivity("Farewell", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "activity command changed") {
		t.Fatalf("expected determinism violation, got %v", err)
	}
}

func TestCancellationRequestedShortCircuitsCommands(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: historyEventWorkflowCancellationRequest},
	})
	if err := wfctx.Sleep(time.Second); !IsCancelled(err) {
		t.Fatalf("expected CancelledFailure, got %v", err)
	}
	if err := wfctx.ExecuteActivity("Greet", nil, nil, nil); !IsCancelled(err) {
		t.Fatalf("ExecuteActivity expected CancelledFailure, got %v", err)
	}
	if err := wfctx.ExecuteChildWorkflow("Sub", nil, nil, nil); !IsCancelled(err) {
		t.Fatalf("ExecuteChildWorkflow expected CancelledFailure, got %v", err)
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

func TestIsWorkflowSuspendedUnwrapsWrapped(t *testing.T) {
	t.Parallel()
	original := newSuspended("activity")
	wrapped := errors.New("workflow returned: " + original.Error())
	if IsWorkflowSuspended(wrapped) {
		t.Fatal("non-wrapped sentinel should not match")
	}
	if !IsWorkflowSuspended(original) {
		t.Fatal("original sentinel should match")
	}
}

func TestRunWorkflowBlocksWhenHistoryFetchFails(t *testing.T) {
	t.Parallel()
	var blockHits, historyHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/history"):
			historyHits++
			http.Error(w, "history boom", http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/block"):
			blockHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"wf-task","namespace":"default","queue":"default","type":"workflow:Greet","state":"blocked","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
		default:
			http.Error(w, "unexpected request "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "tok"})
	worker, err := NewWorker(WorkerOptions{
		Connection: conn,
		AgentID:    "agent-1",
		Workflows:  WorkflowRegistry{"Greet": func(ctx Context, args []any) (any, error) { return "ok", nil }},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	// Pre-seed the connection's agent session so BlockTask doesn't try to
	// enroll against this stub server (which only serves /history and /block).
	conn.applyAgentSession(agentSessionResponse{AgentID: "agent-1", AccessToken: "tok", AccessExpiresAt: time.Now().Add(time.Hour)})

	res, err := worker.runWorkflow(context.Background(), &Task{
		ID: "wf-task", Namespace: "default", Queue: "default",
		Type: TaskTypePrefixWorkflow + "Greet",
	})
	if !errors.Is(err, errWorkflowAlreadyBlocked) {
		t.Fatalf("err = %v, want errWorkflowAlreadyBlocked", err)
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil", res)
	}
	if historyHits != 1 || blockHits != 1 {
		t.Fatalf("history hits = %d, block hits = %d (want 1/1)", historyHits, blockHits)
	}
}

// newReplayCtx builds a workflowContext suitable for unit tests that don't
// need a live HTTP server. The Connection is a stub that fails any actual
// HTTP call — tests that hit the network path build their own httptest.
func newReplayCtx(t *testing.T, history []WorkflowHistoryEvent) *workflowContext {
	t.Helper()
	conn, err := NewConnection(ConnectionOptions{Address: "http://unused.test"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	return &workflowContext{
		Context:    context.Background(),
		conn:       conn,
		namespace:  "default",
		queue:      "default",
		taskID:     "wf-task",
		workflowID: "wf-1",
		now:        time.Now().UTC(),
		replay:     newWorkflowReplay(history),
	}
}
