package client

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"
)

// ensureAgentSession is a no-op when a non-expired access token is cached.
// Otherwise it tries refresh; on refresh failure it falls back to enrollment
// if an enrollment key is configured. Worker calls this implicitly before
// every agent-authenticated request.
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
	refresh := c.agentRefreshToken
	enroll := c.agentEnrollmentKey
	c.agentMu.Unlock()

	if refresh != "" {
		if err := c.refreshAgentSession(ctx, refresh); err == nil {
			return nil
		} else if enroll == "" {
			return err
		}
	}
	if enroll == "" {
		return errors.New("postgrip-agent: no agent session and no enrollment key configured")
	}
	return c.enrollAgent(ctx, enroll)
}

func (c *Connection) refreshAgentSession(ctx context.Context, refreshToken string) error {
	body := map[string]string{"refreshToken": refreshToken}
	var out agentSessionResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent/session/refresh", body, &out, false); err != nil {
		return err
	}
	c.applyAgentSession(out)
	return nil
}

func (c *Connection) enrollAgent(ctx context.Context, enrollmentKey string) error {
	c.agentMu.Lock()
	body := map[string]any{
		"enrollmentKey": enrollmentKey,
		"agentId":       c.agentID,
		"name":          orDefault(c.agentName, c.agentID),
		"host":          orDefault(c.agentHost, hostnameOrUnknown()),
		"namespaces":    []string{c.agentNamespace},
		"queues":        []string{c.agentQueue},
	}
	c.agentMu.Unlock()
	var out agentSessionResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/agent/enroll", body, &out, false); err != nil {
		return err
	}
	c.applyAgentSession(out)
	return nil
}

type agentSessionResponse struct {
	AgentID          string    `json:"agentId"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

func (c *Connection) applyAgentSession(s agentSessionResponse) {
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
// an httptest server that doesn't implement /api/v1/agent/enroll. Production
// code reaches a session via NewConnection's AgentEnrollmentKey + the worker
// poll loop, never via this method.
func (c *Connection) SeedAgentSession(agentID, accessToken string, accessExpiresAt time.Time) {
	c.applyAgentSession(agentSessionResponse{
		AgentID:         agentID,
		AccessToken:     accessToken,
		AccessExpiresAt: accessExpiresAt,
	})
}

func hostnameOrUnknown() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}
