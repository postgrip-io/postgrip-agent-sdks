package client

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// orDefault returns v when non-empty, otherwise def. The client uses it to
// fill in DefaultNamespace / DefaultQueue and to backfill agent metadata
// from environment-supplied fallbacks before the first request.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseAgentAccessExpiresAt(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	return time.Time{}
}

func decodeAgentSigningPrivateKey(encoded string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil, fmt.Errorf("postgrip-agent: decode POSTGRIP_AGENT_SIGNING_PRIVATE_KEY: %w", err)
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	default:
		return nil, nil, fmt.Errorf("postgrip-agent: signing private key has %d bytes, want %d or %d", len(raw), ed25519.SeedSize, ed25519.PrivateKeySize)
	}
	return priv, priv.Public().(ed25519.PublicKey), nil
}
