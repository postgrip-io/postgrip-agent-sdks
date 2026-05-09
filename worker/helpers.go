package worker

import (
	"encoding/json"

	"go.postgrip.io/sdk/client"
	"go.postgrip.io/sdk/internal/jsonenv"
)

// marshalJSON is a thin private wrapper over jsonenv.Marshal so the rest
// of the worker package keeps its existing signature.
func marshalJSON(v any) json.RawMessage { return jsonenv.Marshal(v) }

// decodeResultValue is a thin private wrapper over jsonenv.DecodeResult.
func decodeResultValue(result *client.TaskResult, target any) error {
	return jsonenv.DecodeResult(result, target)
}
