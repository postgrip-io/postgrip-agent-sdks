package client

import (
	"encoding/json"

	"go.postgrip.io/sdk/internal/jsonenv"
)

// mustJSON is a thin private wrapper over jsonenv.Marshal so the rest of
// the client package keeps its existing signature.
func mustJSON(v any) json.RawMessage { return jsonenv.Marshal(v) }

// decodeResultValue is a thin private wrapper over jsonenv.DecodeResult.
func decodeResultValue(result *TaskResult, target any) error {
	return jsonenv.DecodeResult(result, target)
}
