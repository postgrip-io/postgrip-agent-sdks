package client

// orDefault returns v when non-empty, otherwise def. The client uses it to
// fill in DefaultNamespace / DefaultQueue and to backfill agent metadata
// from environment-supplied fallbacks before the first request.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
