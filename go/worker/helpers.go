package worker

import (
	"encoding/json"

	"github.com/postgrip-io/postgrip-agent-sdks/go/client"
	"github.com/postgrip-io/postgrip-agent-sdks/go/internal/jsonenv"
)

// marshalJSON is a thin private wrapper over jsonenv.Marshal so the rest
// of the worker package keeps its existing signature.
func marshalJSON(v any) json.RawMessage { return jsonenv.Marshal(v) }

// decodeResultValue is a thin private wrapper over jsonenv.DecodeResult.
func decodeResultValue(result *client.TaskResult, target any) error {
	return jsonenv.DecodeResult(result, target)
}
