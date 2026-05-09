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

func TestNewConnectionNormalizesAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", "http://127.0.0.1:4100"},
		{"127.0.0.1:4100", "http://127.0.0.1:4100"},
		{"http://example.com:1234/", "http://example.com:1234"},
		{"https://agent.test", "https://agent.test"},
	}
	for _, c := range cases {
		conn, err := NewConnection(ConnectionOptions{Address: c.in})
		if err != nil {
			t.Fatalf("NewConnection(%q) err = %v", c.in, err)
		}
		if conn.Address() != c.want {
			t.Fatalf("NewConnection(%q) address = %q, want %q", c.in, conn.Address(), c.want)
		}
	}
}

func TestEnqueueTaskSendsAuthAndJSON(t *testing.T) {
	t.Parallel()
	var seenAuth string
	var seenBody EnqueueTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","namespace":"default","queue":"default","type":"shell.exec","state":"queued","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "secret"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	client := NewClient(conn)
	task, err := client.Task.ShellExec(context.Background(), ShellExecInput{
		Queue:   "default",
		Command: "echo",
		Args:    []string{"hi"},
	})
	if err != nil {
		t.Fatalf("ShellExec: %v", err)
	}
	if task.ID != "task-1" {
		t.Fatalf("task.ID = %q, want task-1", task.ID)
	}
	if seenAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q, want Bearer secret", seenAuth)
	}
	if seenBody.Type != TaskTypeShellExec {
		t.Fatalf("type = %q, want shell.exec", seenBody.Type)
	}
	var payload ShellExecPayload
	if err := json.Unmarshal(seenBody.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Command != "echo" {
		t.Fatalf("command = %q, want echo", payload.Command)
	}
}

func TestContainerExecBuildsExpectedPayload(t *testing.T) {
	t.Parallel()
	var seenBody EnqueueTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seenBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-2","namespace":"default","queue":"default","type":"container.exec","state":"queued","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	client := NewClient(conn)
	_, err := client.Task.ContainerExec(context.Background(), ContainerExecInput{
		Queue:          "default",
		Image:          "node:22-alpine",
		Command:        "node",
		Args:           []string{"-e", "console.log('hi')"},
		PullPolicy:     "missing",
		TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatalf("ContainerExec: %v", err)
	}
	if seenBody.Type != TaskTypeContainerExec {
		t.Fatalf("type = %q, want container.exec", seenBody.Type)
	}
	var p ContainerExecPayload
	if err := json.Unmarshal(seenBody.Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Image != "node:22-alpine" || p.Command != "node" || p.PullPolicy != "missing" || p.TimeoutSeconds != 60 {
		t.Fatalf("payload = %+v, mismatch", p)
	}
	if len(p.Args) != 2 || p.Args[0] != "-e" {
		t.Fatalf("args = %#v, mismatch", p.Args)
	}
}

func TestErrorToFailureRoundTrip(t *testing.T) {
	t.Parallel()
	app := NewNonRetryableApplicationFailure("bad input", "ValidationError", "field=name")
	f := errorToFailure(app)
	if f == nil || f.Type != "ValidationError" || !f.NonRetryable || f.Message != "bad input" {
		t.Fatalf("FailureInfo = %+v, mismatch", f)
	}
	if len(f.Details) != 1 || f.Details[0] != "field=name" {
		t.Fatalf("details = %#v, mismatch", f.Details)
	}

	cancelled := &CancelledFailure{Message: "stopped"}
	if f := errorToFailure(cancelled); f.Type != "CancelledFailure" {
		t.Fatalf("cancelled.Type = %q, want CancelledFailure", f.Type)
	}

	timeout := &TimeoutFailure{Message: "took too long"}
	if f := errorToFailure(timeout); f.Type != "TimeoutFailure" {
		t.Fatalf("timeout.Type = %q, want TimeoutFailure", f.Type)
	}

	plain := errorToFailure(errors.New("boom"))
	if plain.Message != "boom" || plain.Type != "" {
		t.Fatalf("plain failure = %+v, mismatch", plain)
	}
}

func TestFailureToErrorRoundTrip(t *testing.T) {
	t.Parallel()
	err := failureToError(&FailureInfo{Type: "ValidationError", Message: "bad", NonRetryable: true})
	if !IsApplicationFailure(err) {
		t.Fatalf("expected ApplicationFailure, got %T", err)
	}
	var app *ApplicationFailure
	if !errors.As(err, &app) || !app.NonRetryable || app.Message != "bad" {
		t.Fatalf("ApplicationFailure mismatch: %+v", app)
	}

	err = failureToError(&FailureInfo{Type: "CancelledFailure", Message: "stopped"})
	if !IsCancelled(err) {
		t.Fatalf("expected CancelledFailure, got %T", err)
	}

	err = failureToError(&FailureInfo{Type: "TimeoutFailure"})
	if !IsTimeout(err) {
		t.Fatalf("expected TimeoutFailure, got %T", err)
	}
}

func TestHeartbeatIntervalIsAtLeast500ms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lease int
		want  time.Duration
	}{
		{0, 500 * time.Millisecond},
		{1, 500 * time.Millisecond},
		{3, time.Second}, // 3s / 3 = 1s
		{30, 10 * time.Second},
	}
	for _, c := range cases {
		got := heartbeatInterval(c.lease)
		if got != c.want {
			t.Fatalf("heartbeatInterval(%d) = %s, want %s", c.lease, got, c.want)
		}
	}
}

func TestActivityHelpersFailOutsideActivity(t *testing.T) {
	t.Parallel()
	if _, err := GetActivityInfo(context.Background()); err == nil {
		t.Fatal("GetActivityInfo should error outside an activity")
	}
	if err := Heartbeat(context.Background(), nil); err == nil {
		t.Fatal("Heartbeat should error outside an activity")
	}
	if err := ActivityMilestone(context.Background(), "step", MilestoneOptions{}); err == nil {
		t.Fatal("ActivityMilestone should error outside an activity")
	}
}

func TestActivityHelpersInsideActivityCarryInfo(t *testing.T) {
	t.Parallel()
	emitted := []TaskEventInput{}
	runtime := &activityRuntime{
		info: ActivityInfo{TaskID: "t-1", AgentID: "a-1", Type: "Greet", Attempt: 1},
		emitter: func(_ context.Context, ev TaskEventInput) error {
			emitted = append(emitted, ev)
			return nil
		},
	}
	ctx := withActivityRuntime(context.Background(), runtime)
	info, err := GetActivityInfo(ctx)
	if err != nil || info.TaskID != "t-1" || info.Attempt != 1 {
		t.Fatalf("GetActivityInfo = %+v, %v", info, err)
	}
	if err := Heartbeat(ctx, map[string]any{"phase": "halfway"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := ActivityMilestone(ctx, "step-2", MilestoneOptions{Index: 2, Total: 10}); err != nil {
		t.Fatalf("ActivityMilestone: %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted = %#v", emitted)
	}
	if emitted[0].Kind != TaskEventKindHeartbeat || emitted[1].Kind != TaskEventKindMilestone {
		t.Fatalf("event kinds = %s, %s", emitted[0].Kind, emitted[1].Kind)
	}
	if emitted[1].Details["index"] != 2 || emitted[1].Details["total"] != 10 {
		t.Fatalf("milestone details = %#v", emitted[1].Details)
	}
}

func TestContinueAsNewSentinelDetected(t *testing.T) {
	t.Parallel()
	wfctx := &workflowContext{Context: context.Background(), workflowID: "wf-1", workflowType: "Greet", queue: "default"}
	err := wfctx.ContinueAsNew(ContinueAsNewOptions{Args: []any{"world"}})
	if !IsContinueAsNew(err) {
		t.Fatalf("expected continue-as-new sentinel, got %T", err)
	}
	if !strings.Contains(err.Error(), "Greet") {
		t.Fatalf("err = %q, want workflow type", err.Error())
	}
}

func TestNewWorkerValidatesInputs(t *testing.T) {
	t.Parallel()
	if _, err := NewWorker(WorkerOptions{}); err == nil {
		t.Fatal("expected NewWorker to require Connection")
	}
	conn, _ := NewConnection(ConnectionOptions{Address: "http://example.test"})
	if _, err := NewWorker(WorkerOptions{Connection: conn}); err == nil {
		t.Fatal("expected NewWorker to require AgentID")
	}
	w, err := NewWorker(WorkerOptions{Connection: conn, AgentID: "a-1"})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w.opts.Namespace != DefaultNamespace || w.opts.Queue != DefaultQueue {
		t.Fatalf("defaults = %+v", w.opts)
	}
	if w.opts.MaxConcurrentTasks != 4 {
		t.Fatalf("MaxConcurrentTasks default = %d, want 4", w.opts.MaxConcurrentTasks)
	}
}
