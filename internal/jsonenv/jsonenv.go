// Package jsonenv holds the SDK's tiny JSON helpers — payload marshaling
// and TaskResult.Value decoding. Both /client and /worker need them; both
// SDK-internal in nature, so they live behind /internal to keep them off
// the customer-facing API surface.
package jsonenv

import (
	"encoding/json"
	"fmt"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// Marshal renders v as a json.RawMessage. Panics on failure because the
// SDK only ever asks json.Marshal to encode types it controls (maps,
// structs with JSON-safe fields, args coming from customer code that
// callers own); the panic surfaces a programming error at the SDK
// callsite instead of silently sending malformed payloads.
func Marshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("postgrip-agent: marshal payload: %v", err))
	}
	return raw
}

// DecodeResult decodes the TaskResult.Value field into target. target must
// be a non-nil pointer; jsonenv re-marshals + unmarshals so any
// JSON-compatible Go type works.
func DecodeResult(result *protocol.TaskResult, target any) error {
	if result == nil || target == nil {
		return nil
	}
	if result.Value == nil {
		return nil
	}
	raw, err := json.Marshal(result.Value)
	if err != nil {
		return fmt.Errorf("postgrip-agent: re-encode task result for decode: %w", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("postgrip-agent: decode task result: %w", err)
	}
	return nil
}
