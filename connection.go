package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ConnectionOptions configures the HTTP connection to the agent runtime
// service. Address is the base URL (defaults to http://127.0.0.1:4100).
// AuthToken is sent as `Authorization: Bearer <token>` on management
// endpoints. AgentEnrollmentKey + AgentID + AgentName + AgentHost are used
// only when this connection is also used as a Worker — the SDK exchanges
// the enrollment key for a refresh+access token pair, then refreshes
// transparently.
type ConnectionOptions struct {
	Address        string
	AuthToken      string
	HTTPClient     *http.Client
	Headers        map[string]string
	RequestTimeout time.Duration

	AgentEnrollmentKey string
	AgentID            string
	AgentName          string
	AgentHost          string
	AgentNamespace     string
	AgentQueue         string
}

// Connection is the HTTP transport layer. It is safe for concurrent use.
type Connection struct {
	address    string
	httpClient *http.Client
	authHeader string
	headers    map[string]string

	// Agent (worker-side) auth state. Mutated by ensureAgentSession; reads
	// must hold agentMu. Pre-existing agentAccessToken is reused until 30s
	// before it expires, then refreshed via the refresh token, then
	// re-enrolled if that fails and an enrollment key is available.
	agentMu                sync.Mutex
	agentEnrollmentKey     string
	agentID                string
	agentName              string
	agentHost              string
	agentNamespace         string
	agentQueue             string
	agentAccessToken       string
	agentRefreshToken      string
	agentAccessExpiresUnix int64
}

// NewConnection constructs a Connection. URL validation is deferred to the
// first request — there's no cost to sitting on a configured Connection.
func NewConnection(opts ConnectionOptions) (*Connection, error) {
	address := strings.TrimSpace(opts.Address)
	if address == "" {
		address = "http://127.0.0.1:4100"
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	if _, err := url.Parse(address); err != nil {
		return nil, fmt.Errorf("postgrip-agent: invalid address %q: %w", address, err)
	}
	address = strings.TrimRight(address, "/")
	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.RequestTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	enroll := opts.AgentEnrollmentKey
	if enroll == "" {
		enroll = os.Getenv("POSTGRIP_AGENT_ENROLLMENT_KEY")
	}
	authHeader := ""
	if opts.AuthToken != "" {
		authHeader = "Bearer " + opts.AuthToken
	}
	headers := make(map[string]string, len(opts.Headers))
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return &Connection{
		address:            address,
		httpClient:         httpClient,
		authHeader:         authHeader,
		headers:            headers,
		agentEnrollmentKey: enroll,
		agentID:            opts.AgentID,
		agentName:          opts.AgentName,
		agentHost:          opts.AgentHost,
		agentNamespace:     orDefault(opts.AgentNamespace, DefaultNamespace),
		agentQueue:         orDefault(opts.AgentQueue, DefaultQueue),
	}, nil
}

// Address returns the canonical base URL of the runtime service.
func (c *Connection) Address() string { return c.address }

// Health hits /healthz; returns the parsed JSON body. Useful as a smoke test
// at startup before consuming Client APIs.
func (c *Connection) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// do is the single HTTP entrypoint for the SDK; all the typed helpers above
// funnel through it. agentAuth selects between "use AuthToken" (false) and
// "use the agent's refreshable access token" (true).
func (c *Connection) do(ctx context.Context, method, path string, body any, out any, agentAuth bool) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return &PostGripAgentError{Message: "encode request body", Cause: err}
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, reader)
	if err != nil {
		return &PostGripAgentError{Message: "build request", Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if agentAuth {
		c.agentMu.Lock()
		token := c.agentAccessToken
		c.agentMu.Unlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	} else if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &PostGripAgentError{Message: "http request", Cause: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &PostGripAgentError{Message: "read response body", Cause: err}
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return &PostGripAgentError{Message: fmt.Sprintf("%s %s -> %d %s", method, path, resp.StatusCode, msg)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &PostGripAgentError{Message: fmt.Sprintf("decode response from %s %s", method, path), Cause: err}
	}
	return nil
}

func encodeQuery(params map[string]string) string {
	values := url.Values{}
	for k, v := range params {
		if v == "" {
			continue
		}
		values.Set(k, v)
	}
	return values.Encode()
}

func agentTaskPath(taskID, action, agentID string) string {
	return fmt.Sprintf("/api/v1/agent/tasks/%s/%s?agent_id=%s",
		url.PathEscape(taskID),
		action,
		url.QueryEscape(agentID),
	)
}
