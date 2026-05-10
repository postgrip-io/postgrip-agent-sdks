package client

import (
	"context"
	"encoding/json"
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
	c := New(conn)
	task, err := c.Task.ShellExec(context.Background(), ShellExecInput{
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
	c := New(conn)
	_, err := c.Task.ContainerExec(context.Background(), ContainerExecInput{
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

func TestWorkflowTaskSubmissionRequiresManagedRuntime(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var seen EnqueueTaskRequest
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","namespace":"default","queue":"default","type":"workflow:Greet","state":"queued","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL})
	c := New(conn)
	_, err := c.Task.Enqueue(context.Background(), EnqueueInput{
		Queue: "default",
		Type:  TaskTypePrefixWorkflow + "Greet",
	})
	if err == nil || !strings.Contains(err.Error(), "workflow.runtime") {
		t.Fatalf("workflow enqueue err = %v, want managed runtime guidance", err)
	}

	conn.SeedAgentSession("agent-1", "tok", time.Now().Add(time.Hour))
	if _, err := c.Task.Enqueue(context.Background(), EnqueueInput{
		Queue: "default",
		Type:  TaskTypePrefixWorkflow + "Greet",
	}); err != nil {
		t.Fatalf("managed workflow enqueue: %v", err)
	}
}

func TestWorkflowRuntimeSubmissionIsExternalPath(t *testing.T) {
	t.Parallel()
	var seen EnqueueTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"runtime-task","namespace":"default","queue":"default","type":"workflow.runtime","state":"queued","attempt":0,"lease_timeout_seconds":0,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "management"})
	c := New(conn)
	task, err := c.Task.WorkflowRuntime(context.Background(), WorkflowRuntimeInput{
		Queue:   "default",
		Command: "sh",
		Args:    []string{"-lc", "echo runtime"},
	})
	if err != nil {
		t.Fatalf("WorkflowRuntime: %v", err)
	}
	if task.ID != "runtime-task" || seen.Type != TaskTypeWorkflowRuntime {
		t.Fatalf("task = %+v seen type = %q", task, seen.Type)
	}
}
