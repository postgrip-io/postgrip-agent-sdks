package sdk

import (
	"encoding/json"
	"fmt"
)

// mustJSON marshals v to a json.RawMessage. It panics on failure because the
// SDK only ever asks json.Marshal to encode types it controls (maps, structs
// with JSON-safe fields, args coming from customer code that callers own).
// The panic surfaces a programming error at the SDK callsite instead of
// silently sending malformed payloads.
func mustJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("postgrip-agent: marshal payload: %v", err))
	}
	return raw
}

// decodeResultValue decodes the TaskResult.Value field into target.
// target must be a non-nil pointer; the SDK uses json.Unmarshal so any
// JSON-compatible Go type works.
func decodeResultValue(result *TaskResult, target any) error {
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
