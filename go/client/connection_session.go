package client

import (
	"context"
	"errors"
	"time"
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
	out, err := c.OpenAPI().RefreshAgentSession(ctx, OpenAPIRefreshAgentSessionRequestBody{RefreshToken: refreshToken})
	if err != nil {
		return err
	}
	c.applyAgentSession(out)
	return nil
}

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
