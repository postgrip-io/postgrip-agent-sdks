package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
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

// EnqueueTask enqueues a single task. Use Client.Task.Enqueue / ShellExec /
// ContainerExec / Noop helpers for ergonomic construction; this is the raw
// transport call.
func (c *Connection) EnqueueTask(ctx context.Context, req EnqueueTaskRequest) (*Task, error) {
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/tasks", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTasks returns tasks matching the optional filters.
func (c *Connection) ListTasks(ctx context.Context, params map[string]string) ([]Task, error) {
	path := "/api/v1/tasks"
	if len(params) > 0 {
		path += "?" + encodeQuery(params)
	}
	var out []Task
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTask fetches a single task by id.
func (c *Connection) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var out Task
	path := "/api/v1/tasks/" + url.PathEscape(taskID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTaskEvents returns the full event log for a task.
func (c *Connection) GetTaskEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	var out []TaskEvent
	path := "/api/v1/tasks/" + url.PathEscape(taskID) + "/events"
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// PollTask leases the next task for the given namespace+queue for this
// agent. Returns nil task when the queue is empty (HTTP 204 / empty body
// equivalent). The agent_id is required by the runtime service.
func (c *Connection) PollTask(ctx context.Context, namespace, queue, agentID string) (*protocolPollTaskResponse, error) {
	if err := c.ensureAgentSession(ctx, agentID, namespace, queue); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/agents/%s/poll", url.PathEscape(agentID))
	q := encodeQuery(map[string]string{"namespace": namespace, "queue": queue})
	if q != "" {
		path += "?" + q
	}
	var out protocolPollTaskResponse
	if err := c.do(ctx, http.MethodPost, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

type protocolPollTaskResponse struct {
	Task      *Task               `json:"task,omitempty"`
	Directive *AgentPollDirective `json:"directive,omitempty"`
}

// HeartbeatTask emits a TaskEventKindHeartbeat for a leased task. Workers
// call this on a timer derived from the task's lease_timeout_seconds.
func (c *Connection) HeartbeatTask(ctx context.Context, taskID, agentID string, ev *TaskEventInput) error {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return err
	}
	path := agentTaskPath(taskID, "heartbeat", agentID)
	body := map[string]any{}
	if ev != nil {
		body["event"] = ev
	}
	return c.do(ctx, http.MethodPost, path, body, nil, true)
}

// EmitTaskEvent appends an arbitrary task event (progress/stdout/stderr/
// milestone). Used by the Worker dispatch path and exposed for activity
// helpers (Heartbeat, Milestone).
func (c *Connection) EmitTaskEvent(ctx context.Context, taskID, agentID string, ev TaskEventInput) error {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return err
	}
	path := agentTaskPath(taskID, "events", agentID)
	return c.do(ctx, http.MethodPost, path, map[string]any{"event": ev}, nil, true)
}

// CompleteTask marks a leased task succeeded with the given result.
func (c *Connection) CompleteTask(ctx context.Context, taskID, agentID string, result TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	path := agentTaskPath(taskID, "complete", agentID)
	var out Task
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"result": result}, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// FailTask marks a leased task failed with the given reason.
func (c *Connection) FailTask(ctx context.Context, taskID, agentID, reason string, result *TaskResult) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	path := agentTaskPath(taskID, "fail", agentID)
	body := map[string]any{"error": reason}
	if result != nil {
		body["result"] = result
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, path, body, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// BlockTask marks a leased task blocked (waiting on a signal). Workflow
// runtime uses this when a workflow yields without a terminal result.
func (c *Connection) BlockTask(ctx context.Context, taskID, agentID, reason string) (*Task, error) {
	if err := c.ensureAgentSession(ctx, agentID, "", ""); err != nil {
		return nil, err
	}
	path := agentTaskPath(taskID, "block", agentID)
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, path, body, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// SignalWorkflow appends a signal to a workflow execution.
func (c *Connection) SignalWorkflow(ctx context.Context, workflowID string, req SignalWorkflowRequest) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/signal"
	return c.do(ctx, http.MethodPost, path, req, nil, false)
}

// SignalWithStartWorkflow starts a workflow if it does not exist, otherwise
// appends a signal to the existing run.
func (c *Connection) SignalWithStartWorkflow(ctx context.Context, req SignalWithStartWorkflowRequest) (*Task, error) {
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/workflows/signal-with-start", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelWorkflow requests cancellation of a running workflow.
func (c *Connection) CancelWorkflow(ctx context.Context, workflowID, reason string) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/cancel"
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.do(ctx, http.MethodPost, path, body, nil, false)
}

// TerminateWorkflow forcibly fails a running workflow with the given reason.
func (c *Connection) TerminateWorkflow(ctx context.Context, workflowID, reason string) error {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/terminate"
	body := map[string]any{}
	if reason != "" {
		body["reason"] = reason
	}
	return c.do(ctx, http.MethodPost, path, body, nil, false)
}

// GetWorkflowExecution returns the durable workflow execution row.
func (c *Connection) GetWorkflowExecution(ctx context.Context, workflowID string) (*WorkflowExecution, error) {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID)
	var out WorkflowExecution
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWorkflowHistory returns the ordered durable history for a workflow.
func (c *Connection) GetWorkflowHistory(ctx context.Context, workflowID string) ([]WorkflowHistoryEvent, error) {
	path := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/history"
	var out []WorkflowHistoryEvent
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateSchedule creates a scheduled workflow trigger.
func (c *Connection) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.do(ctx, http.MethodPost, "/api/v1/schedules", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSchedules returns all schedules in the namespace.
func (c *Connection) ListSchedules(ctx context.Context, params map[string]string) ([]Schedule, error) {
	path := "/api/v1/schedules"
	if len(params) > 0 {
		path += "?" + encodeQuery(params)
	}
	var out []Schedule
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchedule fetches a single schedule by id.
func (c *Connection) GetSchedule(ctx context.Context, scheduleID string) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	var out Schedule
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSchedule patches the schedule's spec / overlap_policy / action.
func (c *Connection) UpdateSchedule(ctx context.Context, scheduleID string, req UpdateScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	var out Schedule
	if err := c.do(ctx, http.MethodPatch, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseSchedule pauses scheduled triggers.
func (c *Connection) PauseSchedule(ctx context.Context, scheduleID string, req PauseScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/pause"
	var out Schedule
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnpauseSchedule resumes scheduled triggers.
func (c *Connection) UnpauseSchedule(ctx context.Context, scheduleID string, req UnpauseScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/unpause"
	var out Schedule
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// TriggerSchedule fires the schedule once immediately.
func (c *Connection) TriggerSchedule(ctx context.Context, scheduleID string, req TriggerScheduleRequest) (*TriggerScheduleResponse, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/trigger"
	var out TriggerScheduleResponse
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// BackfillSchedule replays missed runs in a window.
func (c *Connection) BackfillSchedule(ctx context.Context, scheduleID string, req BackfillScheduleRequest) (*BackfillScheduleResponse, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/backfill"
	var out BackfillScheduleResponse
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSchedule removes a schedule.
func (c *Connection) DeleteSchedule(ctx context.Context, scheduleID string) error {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	return c.do(ctx, http.MethodDelete, path, nil, nil, false)
}

// ListNamespaces returns every namespace registered with the runtime service.
func (c *Connection) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	var out []Namespace
	if err := c.do(ctx, http.MethodGet, "/api/v1/namespaces", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateNamespace creates a new namespace.
func (c *Connection) CreateNamespace(ctx context.Context, name string) (*Namespace, error) {
	var out Namespace
	if err := c.do(ctx, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": name}, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
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

func hostnameOrUnknown() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}
