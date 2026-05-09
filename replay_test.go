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

	"github.com/postgrip-io/agent-sdk-go/internal/replay"
)

func TestSignalChannelDrainsThenSuspends(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: replay.EventWorkflowSignaled, Attributes: map[string]any{"name": "ready", "args": []any{"a"}}},
		{Type: replay.EventWorkflowSignaled, Attributes: map[string]any{"name": "ready", "args": []any{"b"}}},
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
		{Type: replay.EventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1500)}},
		{Type: replay.EventTimerFired, TaskID: "tmr-1"},
	})
	if err := wfctx.Sleep(1500 * time.Millisecond); err != nil {
		t.Fatalf("Sleep with fired timer = %v, want nil", err)
	}
}

func TestSleepSuspendsWhenTimerStillPending(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: replay.EventTimerStarted, TaskID: "tmr-1", Attributes: map[string]any{"duration_ms": float64(1500)}},
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
		replay:     replay.New(nil),
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"act-1","namespace":"default","queue":"default","type":"activity:Greet","state":"succeeded","attempt":1,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:01Z","result":{"value":"hello, world"}}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	wfctx := &workflowContext{
		Context: context.Background(),
		conn:    conn,
		replay: replay.New([]WorkflowHistoryEvent{
			{Type: replay.EventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
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
		replay: replay.New([]WorkflowHistoryEvent{
			{Type: replay.EventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
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
		replay: replay.New([]WorkflowHistoryEvent{
			{Type: replay.EventActivityTaskScheduled, TaskID: "act-1", Attributes: map[string]any{"activity_type": "Greet"}},
		}),
	}
	err := wfctx.ExecuteActivity("Farewell", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "activity command changed") {
		t.Fatalf("expected determinism violation, got %v", err)
	}
	if !IsApplicationFailure(err) {
		t.Fatalf("determinism violation should surface as ApplicationFailure, got %T", err)
	}
}

func TestCancellationRequestedShortCircuitsCommands(t *testing.T) {
	t.Parallel()
	wfctx := newReplayCtx(t, []WorkflowHistoryEvent{
		{Type: replay.EventWorkflowCancellationRequest},
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
		replay:     replay.New(history),
	}
}
