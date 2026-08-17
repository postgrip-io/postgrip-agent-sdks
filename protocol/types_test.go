package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnrollAgentRequestJSONUsesAgentFields(t *testing.T) {
	data, err := json.Marshal(EnrollAgentRequest{
		EnrollmentKey: "secret",
		AgentID:       "agent-1",
		TenantID:      "tenant-1",
		Version:       7,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode request payload: %v", err)
	}

	if payload["agentId"] != "agent-1" {
		t.Fatalf("payload = %#v, want agentId", payload)
	}
	if payload["version"] != float64(7) {
		t.Fatalf("payload = %#v, want version", payload)
	}
	if _, ok := payload["workerId"]; ok {
		t.Fatalf("payload = %#v, must not include workerId", payload)
	}
}

func TestAgentSessionResponseJSONUsesAgentFields(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(AgentSessionResponse{
		AgentID:          "agent-1",
		TenantID:         "tenant-1",
		TokenFamilyID:    "family-1",
		AccessToken:      "access",
		RefreshToken:     "refresh",
		AccessExpiresAt:  now,
		RefreshExpiresAt: now.Add(time.Hour),
		Status:           AgentStatusOnline,
		TrustState:       AgentTrustStateTrusted,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode response payload: %v", err)
	}

	if payload["agentId"] != "agent-1" {
		t.Fatalf("payload = %#v, want agentId", payload)
	}
	if _, ok := payload["workerId"]; ok {
		t.Fatalf("payload = %#v, must not include workerId", payload)
	}
}

func TestTaskJSONUsesAgentID(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(Task{
		ID:                  "task-1",
		Namespace:           "default",
		Queue:               "default",
		Type:                TaskTypeNoop,
		State:               TaskStateLeased,
		Attempt:             1,
		AgentID:             "agent-1",
		LeaseTimeoutSeconds: 20,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode task payload: %v", err)
	}

	if payload["agent_id"] != "agent-1" {
		t.Fatalf("payload = %#v, want agent_id", payload)
	}
	if _, ok := payload["worker_id"]; ok {
		t.Fatalf("payload = %#v, must not include worker_id", payload)
	}
}

func TestTaskEventJSONUsesAgentID(t *testing.T) {
	var event TaskEvent
	if err := json.Unmarshal([]byte(`{
		"id":"event-1",
		"task_id":"task-1",
		"agent_id":"agent-1",
		"kind":"stdout",
		"created_at":"2026-04-23T12:00:00Z"
	}`), &event); err != nil {
		t.Fatalf("unmarshal task event: %v", err)
	}

	if event.AgentID != "agent-1" {
		t.Fatalf("agent id = %q, want agent-1", event.AgentID)
	}
}

func TestAgentEventJSONUsesAgentID(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(AgentEvent{
		ID:        "event-1",
		TaskID:    "task-1",
		AgentID:   "agent-1",
		Kind:      TaskEventKindStdout,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("marshal agent event: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode agent event payload: %v", err)
	}

	if payload["agent_id"] != "agent-1" {
		t.Fatalf("payload = %#v, want agent_id", payload)
	}
	if _, ok := payload["worker_id"]; ok {
		t.Fatalf("payload = %#v, must not include worker_id", payload)
	}
}

func TestAgentEnrollmentKeyJSONUsesAgentFields(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(AgentEnrollmentKey{
		TenantID:    "tenant-1",
		KeyHash:     "hash-1",
		Label:       "default",
		UsedByAgent: "agent-1",
		UsedAt:      &now,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("marshal enrollment key: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode enrollment key payload: %v", err)
	}

	if payload["usedByAgent"] != "agent-1" {
		t.Fatalf("payload = %#v, want usedByAgent", payload)
	}
	if _, ok := payload["usedByWorker"]; ok {
		t.Fatalf("payload = %#v, must not include usedByWorker", payload)
	}
}

func TestAgentAuthSessionJSONUsesAgentFields(t *testing.T) {
	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(AgentAuthSession{
		ID:               "session-1",
		TenantID:         "tenant-1",
		AgentID:          "agent-1",
		TokenFamilyID:    "family-1",
		RefreshTokenHash: "refresh-hash",
		ExpiresAt:        now,
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("marshal auth session: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode auth session payload: %v", err)
	}

	if payload["agentId"] != "agent-1" {
		t.Fatalf("payload = %#v, want agentId", payload)
	}
	if _, ok := payload["workerId"]; ok {
		t.Fatalf("payload = %#v, must not include workerId", payload)
	}
}
