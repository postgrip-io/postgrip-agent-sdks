package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/postgrip-io/postgrip-agent-sdks/go/failure"
	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

const DefaultAddress = "https://agentorchestrator.postgrip.app"

// ConnectionOptions configures the HTTP connection to the agent runtime
// service. Address is the base URL (defaults to DefaultAddress).
// AuthToken is sent as `Authorization: Bearer <token>` on management and
// global-admin endpoints. Use a dedicated connection configured with the
// service's global admin token for global operations such as compaction.
// AgentID + delegated AgentAccessToken / AgentRefreshToken /
// AgentSigningPrivateKey are used only when this connection is running inside
// a managed workflow runtime launched by a PostGrip host agent.
type ConnectionOptions struct {
	Address                string
	AuthToken              string
	HTTPClient             *http.Client
	Headers                map[string]string
	RequestTimeout         time.Duration
	AgentID                string
	AgentName              string
	AgentHost              string
	AgentNamespace         string
	AgentQueue             string
	AgentAccessToken       string
	AgentRefreshToken      string
	AgentAccessExpiresAt   time.Time
	AgentSigningPrivateKey string
}

// Connection is the HTTP transport layer. It is safe for concurrent use.
type Connection struct {
	address    string
	httpClient *http.Client
	authHeader string
	headers    map[string]string

	// Agent (worker-side) auth state. Mutated by ensureAgentSession; reads
	// must hold agentMu. Agent sessions must be delegated by a host agent and
	// injected into managed workflow runtimes.
	agentMu                sync.Mutex
	agentRefreshMu         sync.Mutex
	agentID                string
	agentName              string
	agentHost              string
	agentNamespace         string
	agentQueue             string
	agentAccessToken       string
	agentRefreshToken      string
	agentAccessExpiresUnix int64

	// Ed25519 keypair the managed runtime uses to sign requests to
	// agent-authed endpoints. It is injected by the host agent.
	agentSignPriv ed25519.PrivateKey
	agentSignPub  ed25519.PublicKey
}

// NewConnection constructs a Connection. URL validation is deferred to the
// first request — there's no cost to sitting on a configured Connection.
func NewConnection(opts ConnectionOptions) (*Connection, error) {
	address := strings.TrimSpace(opts.Address)
	if address == "" {
		address = strings.TrimSpace(os.Getenv("POSTGRIP_AGENTORCHESTRATOR_URL"))
	}
	if address == "" {
		address = DefaultAddress
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
	agentID := firstNonEmpty(opts.AgentID, os.Getenv("POSTGRIP_AGENT_ID"))
	namespace := firstNonEmpty(opts.AgentNamespace, os.Getenv("POSTGRIP_AGENT_NAMESPACE"), DefaultNamespace)
	queue := firstNonEmpty(opts.AgentQueue, os.Getenv("POSTGRIP_AGENT_TASK_QUEUE"), DefaultQueue)
	accessToken := firstNonEmpty(opts.AgentAccessToken, os.Getenv("POSTGRIP_AGENT_ACCESS_TOKEN"))
	refreshToken := firstNonEmpty(opts.AgentRefreshToken, os.Getenv("POSTGRIP_AGENT_REFRESH_TOKEN"))
	accessExpiresAt := opts.AgentAccessExpiresAt
	if accessExpiresAt.IsZero() {
		accessExpiresAt = parseAgentAccessExpiresAt(os.Getenv("POSTGRIP_AGENT_ACCESS_EXPIRES_AT"))
	}
	signingPrivateKey := firstNonEmpty(opts.AgentSigningPrivateKey, os.Getenv("POSTGRIP_AGENT_SIGNING_PRIVATE_KEY"))
	signPriv, signPub, err := decodeAgentSigningPrivateKey(signingPrivateKey)
	if err != nil {
		return nil, err
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
		address:                address,
		httpClient:             httpClient,
		authHeader:             authHeader,
		headers:                headers,
		agentID:                agentID,
		agentName:              opts.AgentName,
		agentHost:              opts.AgentHost,
		agentNamespace:         namespace,
		agentQueue:             queue,
		agentAccessToken:       accessToken,
		agentRefreshToken:      refreshToken,
		agentAccessExpiresUnix: accessExpiresAt.Unix(),
		agentSignPriv:          signPriv,
		agentSignPub:           signPub,
	}, nil
}

// Address returns the canonical base URL of the runtime service.
func (c *Connection) Address() string { return c.address }

// Health hits /healthz; returns the parsed JSON body. Useful as a smoke
// test at startup before consuming Client APIs.
func (c *Connection) Health(ctx context.Context) (map[string]any, error) {
	out, err := c.OpenAPI().Health(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": out.Status}, nil
}

// doOpenAPI resolves the generated method, path, and authentication lane,
// then delegates to the SDK's custom transport. Retry, session refresh, and
// request signing intentionally remain runtime behavior rather than codegen.
func (c *Connection) doOpenAPI(ctx context.Context, id openAPIOperationID, pathParameters map[string]string, query url.Values, body any, out any) error {
	operation, err := resolveOpenAPIOperation(id, pathParameters, query)
	if err != nil {
		return &failure.SDKError{Message: "resolve OpenAPI operation", Cause: err}
	}
	agentAuth := operation.AuthLane == "agent" || (operation.AuthLane == "either" && c.hasAgentRuntimeCredentials())
	if agentAuth {
		if err := c.ensureAgentSession(ctx, "", "", ""); err != nil {
			return err
		}
	}
	return c.do(ctx, operation.Method, operation.Path, body, out, agentAuth, operation.Signing)
}

// do is the single HTTP entrypoint; all the typed helpers funnel through
// it. agentAuth selects between "use AuthToken" (false) and "use the
// agent's refreshable access token" (true). signing is generated from the
// contract and selects the protocol's agent-task-v1 canonical signature for
// the task actions that require it.
func (c *Connection) do(ctx context.Context, method, path string, body any, out any, agentAuth bool, signing string) error {
	var rawBody []byte
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return &failure.SDKError{Message: "encode request body", Cause: err}
		}
		rawBody = encoded
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, reader)
	if err != nil {
		return &failure.SDKError{Message: "build request", Cause: err}
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
		priv := c.agentSignPriv
		pub := c.agentSignPub
		c.agentMu.Unlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if signing == "agent-task-v1" && len(priv) == ed25519.PrivateKeySize {
			ts := time.Now().UTC()
			payload := protocol.AgentRequestSignaturePayload{
				Method:    method,
				Path:      req.URL.Path,
				Query:     req.URL.RawQuery,
				Timestamp: ts,
				Body:      rawBody,
			}
			req.Header.Set(protocol.HeaderAgentSignatureTimestamp, fmt.Sprintf("%d", ts.Unix()))
			req.Header.Set(protocol.HeaderAgentSignatureKeyID, protocol.AgentSigningKeyID(pub))
			req.Header.Set(protocol.HeaderAgentSignature, protocol.SignAgentRequest(priv, payload))
		}
	} else if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &failure.SDKError{Message: "http request", Cause: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &failure.SDKError{Message: "read response body", Cause: err}
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return &failure.SDKError{Message: fmt.Sprintf("%s %s -> %d %s", method, path, resp.StatusCode, msg)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &failure.SDKError{Message: fmt.Sprintf("decode response from %s %s", method, path), Cause: err}
	}
	return nil
}

func queryValues(params map[string]string) url.Values {
	values := url.Values{}
	for k, v := range params {
		if v == "" {
			continue
		}
		values.Set(k, v)
	}
	return values
}

// doStream sends a raw request body rather than JSON-encoding a value, for
// endpoints whose payload is a byte stream — today only the workspace archive
// upload, which takes the gzipped tar directly rather than as multipart.
//
// Always management-authenticated: no streaming endpoint is on the agent lane,
// and the agent request signature covers the body, which would mean buffering
// an archive that can reach 512 MiB just to sign it.
func (c *Connection) doStream(ctx context.Context, method, path string, body io.Reader, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.address+path, body)
	if err != nil {
		return &failure.SDKError{Message: "build request", Cause: err}
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	// Not c.httpClient: its Timeout is a bound on the *whole* exchange, body
	// included, and it defaults to 30 seconds. Applied to an upload that may
	// approach the server's 512 MiB workspace limit, that aborts every archive
	// bigger than the link can move in 30s — so the documented maximum would be
	// unusable at the default configuration, and every caller would have to
	// know to raise RequestTimeout. A streaming upload is bounded by ctx, which
	// the caller controls per call.
	streamClient := *c.httpClient
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return &failure.SDKError{Message: "http request", Cause: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &failure.SDKError{Message: "read response body", Cause: err}
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return &failure.SDKError{Message: fmt.Sprintf("%s %s -> %d %s", method, path, resp.StatusCode, msg)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &failure.SDKError{Message: fmt.Sprintf("decode response from %s %s", method, path), Cause: err}
	}
	return nil
}

func (c *Connection) doStreamOpenAPI(ctx context.Context, id openAPIOperationID, body io.Reader, headers map[string]string, out any) error {
	operation, err := resolveOpenAPIOperation(id, nil, nil)
	if err != nil {
		return &failure.SDKError{Message: "resolve OpenAPI operation", Cause: err}
	}
	if !operation.StreamingRequest {
		return &failure.SDKError{Message: fmt.Sprintf("OpenAPI operation %q is not a streaming request", id)}
	}
	return c.doStream(ctx, operation.Method, operation.Path, body, headers, out)
}

// AuthHeader returns the management Authorization header value, empty when the
// connection has no management token. The sandbox relay dial needs it: the
// session ticket authorizes the session, but the request is still
// authenticated normally.
func (c *Connection) AuthHeader() string { return c.authHeader }

// ConfiguredHeaders returns a copy of the headers set on the connection.
//
// Callers that reach the API over something other than the plain HTTP client
// need these too. The sandbox relay dials a WebSocket, and a gateway that
// authenticates on a header will reject that upgrade without them — while
// every preceding sandbox request succeeds, because those go through the HTTP
// client that does send them.
func (c *Connection) ConfiguredHeaders() map[string]string {
	out := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		out[k] = v
	}
	return out
}
