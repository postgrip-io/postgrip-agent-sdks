package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func samplePayload() AgentRequestSignaturePayload {
	return AgentRequestSignaturePayload{
		Method:    "POST",
		Path:      "/api/v1/agent/tasks/task-1/heartbeat",
		Query:     "agent_id=agent-1",
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
		Body:      []byte(`{"leaseTimeoutSeconds":30}`),
	}
}

func TestSignAgentRequestRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	payload := samplePayload()
	sig := SignAgentRequest(priv, payload)
	if sig == "" {
		t.Fatalf("sign returned empty signature")
	}
	if !VerifyAgentRequest(pub, payload, sig) {
		t.Fatalf("verify failed for valid signature")
	}
}

// Each binding the canonical string adds is load-bearing — flipping one of
// them in the verifier's payload while keeping the original signature must
// fail. Without these binds, an attacker who captured a signed POST could
// replay it under a different (method, path, query, time, body) combination.
func TestVerifyAgentRequestRejectsTamperedFields(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signed := samplePayload()
	sig := SignAgentRequest(priv, signed)

	cases := []struct {
		name   string
		mutate func(*AgentRequestSignaturePayload)
	}{
		{"method", func(p *AgentRequestSignaturePayload) { p.Method = "PUT" }},
		{"path", func(p *AgentRequestSignaturePayload) {
			p.Path = "/api/v1/agent/tasks/task-2/heartbeat"
		}},
		{"query", func(p *AgentRequestSignaturePayload) { p.Query = "agent_id=other" }},
		{"timestamp", func(p *AgentRequestSignaturePayload) {
			p.Timestamp = signed.Timestamp.Add(time.Second)
		}},
		{"body", func(p *AgentRequestSignaturePayload) {
			p.Body = []byte(`{"leaseTimeoutSeconds":31}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			altered := signed
			tc.mutate(&altered)
			if VerifyAgentRequest(pub, altered, sig) {
				t.Fatalf("verify accepted a request with tampered %s", tc.name)
			}
		})
	}
}

func TestVerifyAgentRequestRejectsWrongKey(t *testing.T) {
	_, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	payload := samplePayload()
	sig := SignAgentRequest(priv1, payload)
	if VerifyAgentRequest(pub2, payload, sig) {
		t.Fatalf("verify accepted signature under a different public key")
	}
}

func TestVerifyAgentRequestRejectsEmptySignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if VerifyAgentRequest(pub, samplePayload(), "") {
		t.Fatalf("verify accepted empty signature")
	}
}

func TestEncodeDecodePublicKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	encoded := EncodeAgentPublicKey(pub)
	decoded, err := DecodeAgentPublicKey(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != string(pub) {
		t.Fatalf("decoded key does not match original")
	}
}

func TestAgentSigningKeyIDStability(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	id1 := AgentSigningKeyID(pub)
	id2 := AgentSigningKeyID(pub)
	if id1 != id2 || len(id1) != 16 {
		t.Fatalf("key id is not stable 16-char hex: %q vs %q", id1, id2)
	}
	if strings.TrimLeft(id1, "0123456789abcdef") != "" {
		t.Fatalf("key id contains non-hex characters: %q", id1)
	}
}

func TestParseAgentSignatureTimestamp(t *testing.T) {
	ts, ok := ParseAgentSignatureTimestamp("1700000000")
	if !ok || ts.Unix() != 1_700_000_000 {
		t.Fatalf("parse valid timestamp = (%v, %v), want (2023-11-14T..., true)", ts, ok)
	}
	if _, ok := ParseAgentSignatureTimestamp(""); ok {
		t.Fatalf("parse empty should fail")
	}
	if _, ok := ParseAgentSignatureTimestamp("not-a-number"); ok {
		t.Fatalf("parse garbage should fail")
	}
}
