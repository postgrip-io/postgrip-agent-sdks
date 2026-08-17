package client

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

func TestResolveOpenAPIOperation(t *testing.T) {
	operation, err := resolveOpenAPIOperation(
		openAPICompleteAgentTask,
		map[string]string{"taskId": "task/one?"},
		url.Values{"agent_id": []string{"agent one"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := operation.Method, "POST"; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := operation.Path, "/api/v1/agent/tasks/task%2Fone%3F/complete?agent_id=agent+one"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if operation.AuthLane != "agent" || operation.Signing != "agent-task-v1" {
		t.Fatalf("unexpected operation security: %#v", operation)
	}
	if operation.RequestSchema != "CompleteTaskRequest" || operation.ResponseSchema != "Task" {
		t.Fatalf("unexpected operation schemas: %#v", operation)
	}
	poll, err := resolveOpenAPIOperation(openAPIPollAgentTask, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if poll.AuthLane != "agent" || poll.Signing != "" {
		t.Fatalf("unexpected poll security: %#v", poll)
	}
	compact, err := resolveOpenAPIOperation(openAPICompact, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if compact.AuthLane != "global-admin" {
		t.Fatalf("unexpected compact security: %#v", compact)
	}
}

func TestGeneratedOpenAPIPayloadTypesCompile(t *testing.T) {
	if got, want := OpenAPIOperationCount, 42; got != want {
		t.Fatalf("generated operation count = %d, want %d", got, want)
	}
	if got, want := OpenAPIClientOperationCount, 40; got != want {
		t.Fatalf("generated client operation count = %d, want %d", got, want)
	}
	request := OpenAPIEnqueueTaskRequestBody{Type: "noop"}
	var response OpenAPIEnqueueTaskResponseBody
	if request.Type != "noop" || response.ID != "" {
		t.Fatalf("unexpected generated payloads: request=%#v response=%#v", request, response)
	}
}

func TestGeneratedOpenAPIClientCallsTypedOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"generated","created_at":"2026-08-17T00:00:00Z","updated_at":"2026-08-17T00:00:00Z"}`))
	}))
	defer server.Close()

	connection, err := NewConnection(ConnectionOptions{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := connection.OpenAPI().CreateNamespace(
		context.Background(),
		OpenAPICreateNamespaceRequestBody{Name: "generated"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Name != "generated" {
		t.Fatalf("response = %#v", response)
	}
}

func TestGeneratedOpenAPIListQueryParameters(t *testing.T) {
	orderBy := "-created_at"
	pageToken := "20"
	agentID := "agent-1"
	version := int64(12)
	logLevel := "info"
	if got, want := (OpenAPIListTasksQuery{
		OrderBy:   &orderBy,
		PageToken: &pageToken,
	}).values().Encode(), "order_by=-created_at&page_token=20"; got != want {
		t.Fatalf("task query = %q, want %q", got, want)
	}
	if got, want := (OpenAPIListSchedulesQuery{
		PageToken: &pageToken,
	}).values().Encode(), "page_token=20"; got != want {
		t.Fatalf("schedule query = %q, want %q", got, want)
	}
	if got, want := (OpenAPIListWorkflowsQuery{
		AgentId: &agentID,
	}).values().Encode(), "agent_id=agent-1"; got != want {
		t.Fatalf("workflow query = %q, want %q", got, want)
	}
	if got, want := (OpenAPICountWorkflowsQuery{
		AgentId: &agentID,
	}).values().Encode(), "agent_id=agent-1"; got != want {
		t.Fatalf("workflow count query = %q, want %q", got, want)
	}
	if got, want := (OpenAPIPollAgentTaskQuery{
		Queue:        "default",
		Version:      &version,
		LogLevel:     &logLevel,
		Capabilities: []string{"self_upgrade", "workflow_runtime"},
	}).values().Encode(), "capabilities=self_upgrade&capabilities=workflow_runtime&log_level=info&queue=default&version=12"; got != want {
		t.Fatalf("poll query = %q, want %q", got, want)
	}
}

func TestResolveOpenAPIOperationRequiresPathParameters(t *testing.T) {
	_, err := resolveOpenAPIOperation(openAPIGetTask, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "taskId") {
		t.Fatalf("error = %v, want missing taskId", err)
	}
}

func TestOpenAPIMetadataControlsRequestSigning(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signatures := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signatures[r.URL.Path] = r.Header.Get(protocol.HeaderAgentSignature)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	connection, err := NewConnection(ConnectionOptions{
		Address:                server.URL,
		AgentSigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	connection.SeedAgentSession("agent-1", "access-token", time.Now().Add(time.Hour))
	if err := connection.doOpenAPI(context.Background(), openAPIPollAgentTask, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := connection.doOpenAPI(
		context.Background(),
		openAPIHeartbeatAgentTask,
		map[string]string{"taskId": "task-1"},
		url.Values{"agent_id": []string{"agent-1"}},
		map[string]any{},
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if got := signatures["/api/v1/agent/poll"]; got != "" {
		t.Fatalf("poll signature = %q, want empty", got)
	}
	if got := signatures["/api/v1/agent/tasks/task-1/heartbeat"]; got == "" {
		t.Fatal("heartbeat signature is empty")
	}
}
