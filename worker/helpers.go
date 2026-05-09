package worker

import (
	"encoding/json"

	"github.com/postgrip-io/agent-sdk-go/client"
)

// marshalJSON delegates to client.MarshalJSON so the worker payloads use
// the same panic-on-encode-failure semantics as the rest of the SDK.
func marshalJSON(v any) json.RawMessage { return client.MarshalJSON(v) }

// decodeResultValue delegates to client.DecodeResultValue so workflow
// resolution paths share the same target-decode behavior as the client
// result-waiting helper.
func decodeResultValue(result *client.TaskResult, target any) error {
	return client.DecodeResultValue(result, target)
}
