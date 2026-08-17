// Package protocol — agent task-result signing.
//
// Why: agents run customer code and report task outcomes back to the
// orchestrator. Without signatures, anyone with a stolen access token (or any
// in-network attacker before strict TLS pinning is enforced) can forge
// results — claim a malicious deploy succeeded, hide a failure, etc. Ed25519
// signatures bind every task POST to the agent's enrolled keypair.
//
// What is signed: a canonical request string, not just the body. Signing only
// the body would let an attacker who captured one signed POST replay it
// against a different task ID or action so long as the body shape matched.
// The canonical string includes the HTTP method, path, query, a timestamp,
// and a hash of the body, so a captured signature is bound to one (method,
// resource, body, time) tuple.
//
// Canonical request string layout (newline-separated, trailing newline
// included):
//
//	agent-task-v1
//	{METHOD}                       — uppercase, e.g. POST
//	{PATH}                         — req.URL.Path, percent-encoded as sent
//	{QUERY}                        — req.URL.RawQuery, as sent (may be empty)
//	{TIMESTAMP}                    — unix epoch seconds, decimal string
//	{BODY_SHA256_BASE64}           — base64-std(sha256(body)); empty body -> sha256("")
//
// The "agent-task-v1" prefix is a domain separator so the signature can never
// be repurposed for a different signing context. Bumping the version (e.g.
// "agent-task-v2") lets us evolve the canonical form without ambiguity.
//
// Headers:
//
//	X-Agent-Signature             — base64(std)-encoded 64-byte signature
//	X-Agent-Signature-Key-Id      — first 16 hex chars of sha256(pubkey).
//	                                Today there is one active key per agent
//	                                and the orchestrator looks the key up by
//	                                agent identity, not by key id; the header
//	                                is recorded in audit logs so a future key
//	                                rotation can disambiguate signatures
//	                                across overlapping keys.
//	X-Agent-Signature-Timestamp   — unix epoch seconds, must match the value
//	                                used in the canonical string
//
// Replay window: the orchestrator rejects requests whose timestamp differs
// from server time by more than MaxAgentSignatureSkew (5 minutes by
// default). The window is symmetric, so the longest a captured signed
// request can stay usable is the past-skew (5 min) plus the future-skew
// (5 min) = 10 minutes total. Customer agent hosts must run NTP — drifted
// clocks will see deny audits with `signature timestamp outside accepted
// skew`.
//
// Strict by default. There is no compat path for agents enrolled without a
// signing public key; the orchestrator rejects them and the agent will
// re-enroll automatically when its session lacks a key.
package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	// HeaderAgentSignature carries the base64-encoded Ed25519 signature.
	HeaderAgentSignature = "X-Agent-Signature"
	// HeaderAgentSignatureKeyID identifies which agent key produced the
	// signature (first 16 hex chars of sha256(pubkey)).
	HeaderAgentSignatureKeyID = "X-Agent-Signature-Key-Id"
	// HeaderAgentSignatureTimestamp carries the unix epoch seconds the agent
	// stamped into the canonical string.
	HeaderAgentSignatureTimestamp = "X-Agent-Signature-Timestamp"

	// agentSignatureContext is the fixed domain-separator prefix on every
	// canonical request string. Bumping the suffix evolves the format.
	agentSignatureContext = "agent-task-v1"
)

// MaxAgentSignatureSkew is the default acceptable difference between an
// agent's signed timestamp and the orchestrator's wall clock. Five minutes
// gives generous slack for clock drift while keeping the replay window
// short. Verifiers may pass a tighter bound if they know their fleet.
const MaxAgentSignatureSkew = 5 * time.Minute

// AgentSigningKeyID returns the short identifier used to disambiguate agent
// signing keys. Currently the first 16 hex chars of sha256(pubkey).
func AgentSigningKeyID(pubkey ed25519.PublicKey) string {
	if len(pubkey) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pubkey)
	return hex.EncodeToString(sum[:8])
}

// EncodeAgentPublicKey returns the wire form of the public key (base64 std).
func EncodeAgentPublicKey(pubkey ed25519.PublicKey) string {
	if len(pubkey) != ed25519.PublicKeySize {
		return ""
	}
	return base64.StdEncoding.EncodeToString(pubkey)
}

// DecodeAgentPublicKey parses a wire-form public key into an ed25519.PublicKey.
// Returns an error if the value is not a valid 32-byte Ed25519 public key.
func DecodeAgentPublicKey(encoded string) (ed25519.PublicKey, error) {
	if encoded == "" {
		return nil, errors.New("agent public key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("agent public key has wrong length")
	}
	return ed25519.PublicKey(raw), nil
}

// AgentRequestSignaturePayload describes everything bound into the signature
// for one task POST. Both sides build it from the same fields and feed it to
// CanonicalAgentRequest to produce the byte string that gets signed/verified.
type AgentRequestSignaturePayload struct {
	Method    string
	Path      string
	Query     string
	Timestamp time.Time
	Body      []byte
}

// CanonicalAgentRequest returns the byte string covered by the signature for
// one task POST. The ordering and separators match the package doc; any
// change here is a wire-format break.
func CanonicalAgentRequest(p AgentRequestSignaturePayload) []byte {
	bodyDigest := sha256.Sum256(p.Body)
	var b strings.Builder
	b.Grow(64 + len(p.Path) + len(p.Query))
	b.WriteString(agentSignatureContext)
	b.WriteByte('\n')
	b.WriteString(strings.ToUpper(p.Method))
	b.WriteByte('\n')
	b.WriteString(p.Path)
	b.WriteByte('\n')
	b.WriteString(p.Query)
	b.WriteByte('\n')
	b.WriteString(strconv.FormatInt(p.Timestamp.UTC().Unix(), 10))
	b.WriteByte('\n')
	b.WriteString(base64.StdEncoding.EncodeToString(bodyDigest[:]))
	b.WriteByte('\n')
	return []byte(b.String())
}

// SignAgentRequest computes the wire-form signature for a task POST.
func SignAgentRequest(privkey ed25519.PrivateKey, payload AgentRequestSignaturePayload) string {
	if len(privkey) != ed25519.PrivateKeySize {
		return ""
	}
	canonical := CanonicalAgentRequest(payload)
	sig := ed25519.Sign(privkey, canonical)
	return base64.StdEncoding.EncodeToString(sig)
}

// VerifyAgentRequest reports whether `signature` (base64 std) is a valid
// Ed25519 signature for the given canonical request payload, signed by the
// agent that owns `pubkey`.
func VerifyAgentRequest(pubkey ed25519.PublicKey, payload AgentRequestSignaturePayload, signature string) bool {
	if len(pubkey) != ed25519.PublicKeySize || signature == "" {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	canonical := CanonicalAgentRequest(payload)
	return ed25519.Verify(pubkey, canonical, sig)
}

// ParseAgentSignatureTimestamp parses the X-Agent-Signature-Timestamp header
// value. Returns the parsed time and whether the value was well-formed.
func ParseAgentSignatureTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}
