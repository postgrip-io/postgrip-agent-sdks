package client

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// ensureAgentSession is a no-op when a non-expired access token is cached.
// Otherwise it tries refresh. Worker calls this implicitly before every
// agent-authenticated request. The SDK does not enroll agents; host agents
// launch managed workflow runtimes and inject delegated session credentials.
func (c *Connection) ensureAgentSession(ctx context.Context, agentID, namespace, queue string) error {
	c.agentMu.Lock()
	if agentID != "" {
		c.agentID = agentID
	}
	if namespace != "" {
		c.agentNamespace = namespace
	}
	if queue != "" {
		c.agentQueue = queue
	}
	if c.agentAccessToken != "" && c.agentAccessExpiresUnix > time.Now().Unix()+30 {
		c.agentMu.Unlock()
		return nil
	}
	c.agentMu.Unlock()

	c.agentRefreshMu.Lock()
	defer c.agentRefreshMu.Unlock()

	c.agentMu.Lock()
	if c.agentAccessToken != "" && c.agentAccessExpiresUnix > time.Now().Unix()+30 {
		c.agentMu.Unlock()
		return nil
	}
	refresh := c.agentRefreshToken
	c.agentMu.Unlock()

	if refresh != "" {
		if err := c.refreshAgentSession(ctx, refresh); err != nil {
			return err
		}
		return nil
	}
	return errors.New("postgrip-agent: managed runtime credentials are required; submit workflow.runtime work to a host agent instead of enrolling SDK agents")
}

func (c *Connection) hasAgentRuntimeCredentials() bool {
	c.agentMu.Lock()
	defer c.agentMu.Unlock()
	return c.agentAccessToken != "" || c.agentRefreshToken != ""
}

func (c *Connection) refreshAgentSession(ctx context.Context, refreshToken string) error {
	body := map[string]string{"refreshToken": refreshToken}
	var out AgentSessionResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent/session/refresh", body, &out, false); err != nil {
		return err
	}
	c.applyAgentSession(out)
	return nil
}

// AgentSessionResponse is the session envelope the orchestrator returns from
// enroll and refresh.
//
// This was a local struct carrying five of the wire type's ten fields:
// TenantID, TokenFamilyID, Status, TrustState and TrustReason were dropped on
// decode, so a runtime could not tell that the session it had just refreshed
// came back quarantined or untrusted. It also missed protocol's custom
// UnmarshalJSON.
type AgentSessionResponse = protocol.AgentSessionResponse

func (c *Connection) applyAgentSession(s AgentSessionResponse) {
	c.agentMu.Lock()
	defer c.agentMu.Unlock()
	if s.AgentID != "" {
		c.agentID = s.AgentID
	}
	c.agentAccessToken = s.AccessToken
	c.agentRefreshToken = s.RefreshToken
	c.agentAccessExpiresUnix = s.AccessExpiresAt.Unix()
}

// SeedAgentSession pre-installs an access token + expiration into the
// connection's agent auth state, bypassing the enroll/refresh HTTP dance.
//
// Intended for tests that exercise agent-authenticated endpoints against
// an httptest server. Production code receives these credentials from a host
// agent when it launches a managed workflow runtime.
func (c *Connection) SeedAgentSession(agentID, accessToken string, accessExpiresAt time.Time) {
	c.applyAgentSession(AgentSessionResponse{
		AgentID:         agentID,
		AccessToken:     accessToken,
		AccessExpiresAt: accessExpiresAt,
	})
}
