package protocol

import (
	"encoding/json"
	"time"
)

const (
	DefaultNamespace = "default"
	DefaultQueue     = "default"

	AgentPollDirectiveTypeUpgrade  = "upgrade"
	AgentPollDirectiveTypeShutdown = "shutdown"
	AgentPollDirectiveTypeLogLevel = "log_level"
	AgentPollDirectiveTypePollNow  = "poll_now"
	AgentPollDirectiveTypeAttest   = "attest"

	TaskTypeNoop            = "noop"
	TaskTypeShellExec       = "shell.exec"
	TaskTypeContainerExec   = "container.exec"
	TaskTypeWorkflowRuntime = "workflow.runtime"
	TaskTypeTimer           = "timer"

	TaskTypePrefixWorkflow = "workflow:"
	TaskTypePrefixActivity = "activity:"
	TaskTypePrefixQuery    = "query:"
	TaskTypePrefixUpdate   = "update:"
)

type TaskState string

const (
	TaskStateQueued    TaskState = "queued"
	TaskStateLeased    TaskState = "leased"
	TaskStateBlocked   TaskState = "blocked"
	TaskStateSucceeded TaskState = "succeeded"
	TaskStateFailed    TaskState = "failed"
)

type TaskEventKind string

const (
	TaskEventKindLeased    TaskEventKind = "leased"
	TaskEventKindStarted   TaskEventKind = "started"
	TaskEventKindHeartbeat TaskEventKind = "heartbeat"
	TaskEventKindMilestone TaskEventKind = "milestone"
	TaskEventKindProgress  TaskEventKind = "progress"
	TaskEventKindStdout    TaskEventKind = "stdout"
	TaskEventKindStderr    TaskEventKind = "stderr"
	TaskEventKindCompleted TaskEventKind = "completed"
	TaskEventKindFailed    TaskEventKind = "failed"
)

type EnqueueTaskRequest struct {
	TenantID            string          `json:"tenantId,omitempty"`
	Namespace           string          `json:"namespace,omitempty"`
	Queue               string          `json:"queue,omitempty"`
	Type                string          `json:"type"`
	Payload             json.RawMessage `json:"payload,omitempty"`
	LeaseTimeoutSeconds int             `json:"lease_timeout_seconds,omitempty"`
}

type Task struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenantId,omitempty"`
	Namespace           string          `json:"namespace"`
	Queue               string          `json:"queue"`
	Type                string          `json:"type"`
	Payload             json.RawMessage `json:"payload,omitempty"`
	State               TaskState       `json:"state"`
	Attempt             int             `json:"attempt"`
	AgentID             string          `json:"agent_id,omitempty"`
	LeaseTimeoutSeconds int             `json:"lease_timeout_seconds"`
	NotBefore           *time.Time      `json:"not_before,omitempty"`
	LeasedUntil         *time.Time      `json:"leased_until,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	Result              *TaskResult     `json:"result,omitempty"`
	Error               string          `json:"error,omitempty"`
}

func (t *Task) UnmarshalJSON(data []byte) error {
	type rawTask struct {
		ID                  string          `json:"id"`
		TenantID            string          `json:"tenantId,omitempty"`
		Namespace           string          `json:"namespace"`
		Queue               string          `json:"queue"`
		Type                string          `json:"type"`
		Payload             json.RawMessage `json:"payload,omitempty"`
		State               TaskState       `json:"state"`
		Attempt             int             `json:"attempt"`
		AgentID             string          `json:"agent_id,omitempty"`
		LeaseTimeoutSeconds int             `json:"lease_timeout_seconds"`
		NotBefore           *time.Time      `json:"not_before,omitempty"`
		LeasedUntil         *time.Time      `json:"leased_until,omitempty"`
		CreatedAt           time.Time       `json:"created_at"`
		UpdatedAt           time.Time       `json:"updated_at"`
		Result              *TaskResult     `json:"result,omitempty"`
		Error               string          `json:"error,omitempty"`
	}
	var raw rawTask
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.ID = raw.ID
	t.TenantID = raw.TenantID
	t.Namespace = raw.Namespace
	t.Queue = raw.Queue
	t.Type = raw.Type
	t.Payload = raw.Payload
	t.State = raw.State
	t.Attempt = raw.Attempt
	t.AgentID = raw.AgentID
	t.LeaseTimeoutSeconds = raw.LeaseTimeoutSeconds
	t.NotBefore = raw.NotBefore
	t.LeasedUntil = raw.LeasedUntil
	t.CreatedAt = raw.CreatedAt
	t.UpdatedAt = raw.UpdatedAt
	t.Result = raw.Result
	t.Error = raw.Error
	return nil
}

func (t Task) MarshalJSON() ([]byte, error) {
	type rawTask struct {
		ID                  string          `json:"id"`
		TenantID            string          `json:"tenantId,omitempty"`
		Namespace           string          `json:"namespace"`
		Queue               string          `json:"queue"`
		Type                string          `json:"type"`
		Payload             json.RawMessage `json:"payload,omitempty"`
		State               TaskState       `json:"state"`
		Attempt             int             `json:"attempt"`
		AgentID             string          `json:"agent_id,omitempty"`
		LeaseTimeoutSeconds int             `json:"lease_timeout_seconds"`
		NotBefore           *time.Time      `json:"not_before,omitempty"`
		LeasedUntil         *time.Time      `json:"leased_until,omitempty"`
		CreatedAt           time.Time       `json:"created_at"`
		UpdatedAt           time.Time       `json:"updated_at"`
		Result              *TaskResult     `json:"result,omitempty"`
		Error               string          `json:"error,omitempty"`
	}
	return json.Marshal(rawTask{
		ID:                  t.ID,
		TenantID:            t.TenantID,
		Namespace:           t.Namespace,
		Queue:               t.Queue,
		Type:                t.Type,
		Payload:             t.Payload,
		State:               t.State,
		Attempt:             t.Attempt,
		AgentID:             t.AgentID,
		LeaseTimeoutSeconds: t.LeaseTimeoutSeconds,
		NotBefore:           t.NotBefore,
		LeasedUntil:         t.LeasedUntil,
		CreatedAt:           t.CreatedAt,
		UpdatedAt:           t.UpdatedAt,
		Result:              t.Result,
		Error:               t.Error,
	})
}

type PollTaskResponse struct {
	Task      *Task               `json:"task,omitempty"`
	Directive *AgentPollDirective `json:"directive,omitempty"`
}

type AgentPollDirective struct {
	Type            string `json:"type"`
	Image           string `json:"image,omitempty"`
	ExpectedVersion int    `json:"expectedVersion,omitempty"`
	Force           bool   `json:"force,omitempty"`
	LogLevel        string `json:"logLevel,omitempty"`
	Subject         string `json:"subject,omitempty"`
}

type AgentUpgradeRequest struct {
	Image           string `json:"image,omitempty"`
	ExpectedVersion int    `json:"expectedVersion,omitempty"`
}

type AgentMaintenanceWindow struct {
	StartMinute     int    `json:"startMinute"`
	DurationMinutes int    `json:"durationMinutes"`
	Timezone        string `json:"timezone"`
}

type AgentMaintenanceWindowRequest struct {
	Enabled         bool   `json:"enabled"`
	StartMinute     int    `json:"startMinute"`
	DurationMinutes int    `json:"durationMinutes"`
	Timezone        string `json:"timezone"`
}

type UpdateAgentRequest struct {
	Name              *string                        `json:"name,omitempty"`
	MaintenanceWindow *AgentMaintenanceWindowRequest `json:"maintenanceWindow,omitempty"`
	LogLevel          *string                        `json:"logLevel,omitempty"`
}

type CompleteTaskRequest struct {
	Result TaskResult `json:"result"`
}

type FailTaskRequest struct {
	Error  string      `json:"error"`
	Result *TaskResult `json:"result,omitempty"`
}

type BlockTaskRequest struct {
	Reason string `json:"reason,omitempty"`
}

type SignalWorkflowRequest struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type SignalWithStartWorkflowRequest struct {
	Namespace             string                `json:"namespace,omitempty"`
	Queue                 string                `json:"queue,omitempty"`
	WorkflowType          string                `json:"workflowType"`
	WorkflowID            string                `json:"workflowId,omitempty"`
	WorkflowIDReusePolicy string                `json:"workflowIdReusePolicy,omitempty"`
	LeaseTimeoutSeconds   int                   `json:"lease_timeout_seconds,omitempty"`
	RunTimeoutMs          int64                 `json:"runTimeoutMs,omitempty"`
	RetryPolicy           *RetryPolicy          `json:"retry,omitempty"`
	Memo                  map[string]any        `json:"memo,omitempty"`
	SearchAttributes      map[string]any        `json:"searchAttributes,omitempty"`
	Args                  json.RawMessage       `json:"args,omitempty"`
	Signal                SignalWorkflowRequest `json:"signal"`
}

type SignalWithStartWorkflowResponse struct {
	Workflow WorkflowExecution    `json:"workflow"`
	Task     Task                 `json:"task"`
	Signal   WorkflowHistoryEvent `json:"signal"`
}

type CancelWorkflowRequest struct {
	Reason string `json:"reason,omitempty"`
}

type TerminateWorkflowRequest struct {
	Reason string `json:"reason,omitempty"`
}

type HeartbeatTaskRequest struct {
	LeaseTimeoutSeconds int             `json:"lease_timeout_seconds,omitempty"`
	Event               *TaskEventInput `json:"event,omitempty"`
}

type AppendTaskEventRequest struct {
	Event TaskEventInput `json:"event"`
}

type TaskEventInput struct {
	Kind    TaskEventKind  `json:"kind"`
	Stage   string         `json:"stage,omitempty"`
	Message string         `json:"message,omitempty"`
	Stream  string         `json:"stream,omitempty"`
	Data    string         `json:"data,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type AgentEvent struct {
	ID        string         `json:"id"`
	TaskID    string         `json:"task_id"`
	TenantID  string         `json:"tenantId,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Kind      TaskEventKind  `json:"kind"`
	Stage     string         `json:"stage,omitempty"`
	Message   string         `json:"message,omitempty"`
	Stream    string         `json:"stream,omitempty"`
	Data      string         `json:"data,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func (e *AgentEvent) UnmarshalJSON(data []byte) error {
	type rawAgentEvent struct {
		ID        string         `json:"id"`
		TaskID    string         `json:"task_id"`
		TenantID  string         `json:"tenantId,omitempty"`
		AgentID   string         `json:"agent_id,omitempty"`
		Kind      TaskEventKind  `json:"kind"`
		Stage     string         `json:"stage,omitempty"`
		Message   string         `json:"message,omitempty"`
		Stream    string         `json:"stream,omitempty"`
		Data      string         `json:"data,omitempty"`
		Details   map[string]any `json:"details,omitempty"`
		CreatedAt time.Time      `json:"created_at"`
	}
	var raw rawAgentEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	e.ID = raw.ID
	e.TaskID = raw.TaskID
	e.TenantID = raw.TenantID
	e.AgentID = raw.AgentID
	e.Kind = raw.Kind
	e.Stage = raw.Stage
	e.Message = raw.Message
	e.Stream = raw.Stream
	e.Data = raw.Data
	e.Details = raw.Details
	e.CreatedAt = raw.CreatedAt
	return nil
}

func (e AgentEvent) MarshalJSON() ([]byte, error) {
	type rawAgentEvent struct {
		ID        string         `json:"id"`
		TaskID    string         `json:"task_id"`
		TenantID  string         `json:"tenantId,omitempty"`
		AgentID   string         `json:"agent_id,omitempty"`
		Kind      TaskEventKind  `json:"kind"`
		Stage     string         `json:"stage,omitempty"`
		Message   string         `json:"message,omitempty"`
		Stream    string         `json:"stream,omitempty"`
		Data      string         `json:"data,omitempty"`
		Details   map[string]any `json:"details,omitempty"`
		CreatedAt time.Time      `json:"created_at"`
	}
	return json.Marshal(rawAgentEvent{
		ID:        e.ID,
		TaskID:    e.TaskID,
		TenantID:  e.TenantID,
		AgentID:   e.AgentID,
		Kind:      e.Kind,
		Stage:     e.Stage,
		Message:   e.Message,
		Stream:    e.Stream,
		Data:      e.Data,
		Details:   e.Details,
		CreatedAt: e.CreatedAt,
	})
}

type TaskEvent = AgentEvent

type TaskResult struct {
	ExitCode      int                  `json:"exit_code,omitempty"`
	Stdout        string               `json:"stdout,omitempty"`
	Stderr        string               `json:"stderr,omitempty"`
	Message       string               `json:"message,omitempty"`
	Value         any                  `json:"value,omitempty"`
	Failure       *FailureInfo         `json:"failure,omitempty"`
	ContinueAsNew *ContinueAsNewResult `json:"continue_as_new,omitempty"`
	StartedAt     time.Time            `json:"started_at,omitempty"`
	FinishedAt    time.Time            `json:"finished_at,omitempty"`
}

type FailureInfo struct {
	Message      string `json:"message,omitempty"`
	Type         string `json:"type,omitempty"`
	NonRetryable bool   `json:"non_retryable,omitempty"`
	Details      []any  `json:"details,omitempty"`
}

type ContinueAsNewResult struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowType string `json:"workflow_type"`
	TaskQueue    string `json:"task_queue"`
	TaskID       string `json:"task_id"`
}

type WorkflowState string

const (
	WorkflowStateRunning        WorkflowState = "running"
	WorkflowStateSucceeded      WorkflowState = "succeeded"
	WorkflowStateFailed         WorkflowState = "failed"
	WorkflowStateContinuedAsNew WorkflowState = "continued_as_new"
)

type WorkflowIDReusePolicy string

const (
	WorkflowIDReusePolicyAllowDuplicate           WorkflowIDReusePolicy = "allow_duplicate"
	WorkflowIDReusePolicyAllowDuplicateFailedOnly WorkflowIDReusePolicy = "allow_duplicate_failed_only"
	WorkflowIDReusePolicyRejectDuplicate          WorkflowIDReusePolicy = "reject_duplicate"
)

type ScheduleState string

const (
	ScheduleStateActive  ScheduleState = "active"
	ScheduleStatePaused  ScheduleState = "paused"
	ScheduleStateDeleted ScheduleState = "deleted"
)

type ScheduleOverlapPolicy string

const (
	ScheduleOverlapPolicySkip     ScheduleOverlapPolicy = "skip"
	ScheduleOverlapPolicyAllowAll ScheduleOverlapPolicy = "allow_all"
)

type ScheduleMissedRunPolicy string

const (
	ScheduleMissedRunPolicyCatchUp ScheduleMissedRunPolicy = "catch_up"
	ScheduleMissedRunPolicySkip    ScheduleMissedRunPolicy = "skip"
)

type WorkflowExecution struct {
	ID    string `json:"id"`
	RunID string `json:"run_id"`
	// AgentID is the agent currently holding (or last to have held)
	// the lease on a task that belongs to this workflow — workflow
	// task or activity task. The orchestrator stamps it when it
	// hands out the lease, so UIs can attribute "which agent is
	// running this workflow" without an N+1 join through tasks or
	// a walk over workflow_history. Empty until the workflow's
	// first task is leased.
	AgentID          string         `json:"agent_id,omitempty"`
	TenantID         string         `json:"tenantId,omitempty"`
	Namespace        string         `json:"namespace"`
	Type             string         `json:"type"`
	Queue            string         `json:"queue"`
	TaskID           string         `json:"task_id"`
	State            WorkflowState  `json:"state"`
	Attempt          int            `json:"attempt,omitempty"`
	RunTimeoutMs     int64          `json:"run_timeout_ms,omitempty"`
	RetryPolicy      *RetryPolicy   `json:"retry,omitempty"`
	Memo             map[string]any `json:"memo,omitempty"`
	SearchAttributes map[string]any `json:"search_attributes,omitempty"`
	Result           *TaskResult    `json:"result,omitempty"`
	Error            string         `json:"error,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type WorkflowHistoryEvent struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	TenantID   string         `json:"tenantId,omitempty"`
	TaskID     string         `json:"task_id,omitempty"`
	Type       string         `json:"type"`
	Attributes map[string]any `json:"attributes,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type WorkflowCountResponse struct {
	Count int `json:"count"`
}

type Namespace struct {
	TenantID  string    `json:"tenantId,omitempty"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateNamespaceRequest struct {
	TenantID string `json:"tenantId,omitempty"`
	Name     string `json:"name"`
}

type CompactRequest struct {
	RetentionSeconds int64 `json:"retention_seconds,omitempty"`
}

type CompactResponse struct {
	RemovedTasks     int `json:"removed_tasks"`
	RemovedWorkflows int `json:"removed_workflows"`
}

type StoreStats struct {
	Tasks      int `json:"tasks"`
	Workflows  int `json:"workflows"`
	Schedules  int `json:"schedules"`
	Namespaces int `json:"namespaces"`
}

type WorkflowPayload struct {
	Namespace               string          `json:"namespace,omitempty"`
	WorkflowType            string          `json:"workflowType"`
	WorkflowID              string          `json:"workflowId"`
	RunID                   string          `json:"runId,omitempty"`
	WorkflowIDReusePolicy   string          `json:"workflowIdReusePolicy,omitempty"`
	ParentWorkflowID        string          `json:"parentWorkflowId,omitempty"`
	ParentWorkflowRunID     string          `json:"parentWorkflowRunId,omitempty"`
	ParentWorkflowTaskID    string          `json:"parentWorkflowTaskId,omitempty"`
	ParentCancellationType  string          `json:"parentCancellationType,omitempty"`
	ContinuedFromWorkflowID string          `json:"continuedFromWorkflowId,omitempty"`
	RunTimeoutMs            int64           `json:"runTimeoutMs,omitempty"`
	RetryPolicy             *RetryPolicy    `json:"retry,omitempty"`
	Memo                    map[string]any  `json:"memo,omitempty"`
	SearchAttributes        map[string]any  `json:"searchAttributes,omitempty"`
	Args                    json.RawMessage `json:"args,omitempty"`
}

type ScheduleCalendarSpec struct {
	Minute     []int `json:"minute,omitempty"`
	Hour       []int `json:"hour,omitempty"`
	DayOfMonth []int `json:"day_of_month,omitempty"`
	Month      []int `json:"month,omitempty"`
	DayOfWeek  []int `json:"day_of_week,omitempty"`
}

type ScheduleSpec struct {
	IntervalSeconds      int                     `json:"interval_seconds,omitempty"`
	Cron                 string                  `json:"cron,omitempty"`
	Calendar             *ScheduleCalendarSpec   `json:"calendar,omitempty"`
	TimeZone             string                  `json:"timezone,omitempty"`
	JitterSeconds        int                     `json:"jitter_seconds,omitempty"`
	CatchUpWindowSeconds int                     `json:"catch_up_window_seconds,omitempty"`
	MissedRunPolicy      ScheduleMissedRunPolicy `json:"missed_run_policy,omitempty"`
	StartAt              *time.Time              `json:"start_at,omitempty"`
}

type ScheduleAction struct {
	Namespace             string          `json:"namespace,omitempty"`
	Queue                 string          `json:"queue,omitempty"`
	WorkflowType          string          `json:"workflowType"`
	WorkflowID            string          `json:"workflowId,omitempty"`
	WorkflowIDReusePolicy string          `json:"workflowIdReusePolicy,omitempty"`
	RunTimeoutMs          int64           `json:"runTimeoutMs,omitempty"`
	RetryPolicy           *RetryPolicy    `json:"retry,omitempty"`
	Memo                  map[string]any  `json:"memo,omitempty"`
	SearchAttributes      map[string]any  `json:"searchAttributes,omitempty"`
	Args                  json.RawMessage `json:"args,omitempty"`
}

type Schedule struct {
	ID            string                `json:"id"`
	TenantID      string                `json:"tenantId,omitempty"`
	Namespace     string                `json:"namespace"`
	State         ScheduleState         `json:"state"`
	OverlapPolicy ScheduleOverlapPolicy `json:"overlap_policy,omitempty"`
	Spec          ScheduleSpec          `json:"spec"`
	Action        ScheduleAction        `json:"action"`
	LastRunAt     *time.Time            `json:"last_run_at,omitempty"`
	NextRunAt     time.Time             `json:"next_run_at"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type CreateScheduleRequest struct {
	ID            string                `json:"id,omitempty"`
	TenantID      string                `json:"tenantId,omitempty"`
	Namespace     string                `json:"namespace,omitempty"`
	OverlapPolicy ScheduleOverlapPolicy `json:"overlap_policy,omitempty"`
	Spec          ScheduleSpec          `json:"spec"`
	Action        ScheduleAction        `json:"action"`
}

type UpdateScheduleRequest struct {
	OverlapPolicy *ScheduleOverlapPolicy `json:"overlap_policy,omitempty"`
	Spec          *ScheduleSpec          `json:"spec,omitempty"`
	Action        *ScheduleAction        `json:"action,omitempty"`
}

type PauseScheduleRequest struct {
	Reason string `json:"reason,omitempty"`
}

type UnpauseScheduleRequest struct {
	Reason string `json:"reason,omitempty"`
}

type TriggerScheduleRequest struct {
	Reason string `json:"reason,omitempty"`
}

type TriggerScheduleResponse struct {
	Schedule Schedule `json:"schedule"`
	Task     Task     `json:"task"`
}

type BackfillScheduleRequest struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}

type BackfillScheduleResponse struct {
	Schedule Schedule `json:"schedule"`
	Tasks    []Task   `json:"tasks"`
}

type ActivityTaskPayload struct {
	WorkflowID       string          `json:"workflowId,omitempty"`
	WorkflowRunID    string          `json:"workflowRunId,omitempty"`
	WorkflowTaskID   string          `json:"workflowTaskId,omitempty"`
	ActivityType     string          `json:"activityType"`
	Attempt          int             `json:"attempt,omitempty"`
	CancellationType string          `json:"cancellationType,omitempty"`
	RetryPolicy      *RetryPolicy    `json:"retry,omitempty"`
	Args             json.RawMessage `json:"args,omitempty"`
}

type RetryPolicy struct {
	MaximumAttempts        int      `json:"maximumAttempts,omitempty"`
	InitialIntervalMs      int64    `json:"initialIntervalMs,omitempty"`
	BackoffCoefficient     float64  `json:"backoffCoefficient,omitempty"`
	MaximumIntervalMs      int64    `json:"maximumIntervalMs,omitempty"`
	ExpirationIntervalMs   int64    `json:"expirationIntervalMs,omitempty"`
	NonRetryableErrorTypes []string `json:"nonRetryableErrorTypes,omitempty"`
}

type WorkflowQueryPayload struct {
	WorkflowID    string          `json:"workflowId"`
	WorkflowRunID string          `json:"workflowRunId,omitempty"`
	WorkflowType  string          `json:"workflowType"`
	QueryName     string          `json:"queryName"`
	Args          json.RawMessage `json:"args,omitempty"`
}

type WorkflowUpdatePayload struct {
	WorkflowID    string          `json:"workflowId"`
	WorkflowRunID string          `json:"workflowRunId,omitempty"`
	WorkflowType  string          `json:"workflowType"`
	UpdateName    string          `json:"updateName"`
	Args          json.RawMessage `json:"args,omitempty"`
}

type TimerPayload struct {
	WorkflowID     string    `json:"workflowId,omitempty"`
	WorkflowRunID  string    `json:"workflowRunId,omitempty"`
	WorkflowTaskID string    `json:"workflowTaskId,omitempty"`
	TimerID        string    `json:"timerId"`
	DurationMs     int64     `json:"durationMs"`
	FireAt         time.Time `json:"fireAt"`
}

type ShellExecPayload struct {
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// ContainerExecPayload runs a command inside a per-task container the agent
// launches via docker-cli (proxied through the worker stack's docker socket
// proxy). Use this to execute polyglot tasks (Node, Bun, Python, Go, etc.)
// without baking those runtimes into the agent image itself.
//
// Image is required. Command, when set, overrides the image's ENTRYPOINT;
// Args are passed as the container's CMD. PullPolicy mirrors `docker run
// --pull` ("always" | "missing" | "never"); empty means "missing". Env keys
// pass through the same shellExecEnvKeyAllowed allowlist as shell.exec — host
// loader / interpreter / agent-secret prefixes are rejected.
type ContainerExecPayload struct {
	Image          string            `json:"image"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	PullPolicy     string            `json:"pull_policy,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// Isolation tiers a workflow.runtime workload can require, advertised by
// agents through runtime capabilities and requested via
// WorkflowRuntimePayload.Isolation. A requested tier is a floor: container
// work may run in a stronger tier, microvm work must never run in a weaker
// one, and an unrecognized value satisfies nothing.
const (
	IsolationTierContainer = "container"
	IsolationTierMicroVM   = "microvm"

	// RuntimeIsolationCapabilityPrefix namespaces the isolation tiers an
	// agent advertises in its poll capability list, e.g.
	// "runtime_isolation:microvm". The agent emits it and the orchestrator
	// parses it, so it belongs here with the tiers themselves: prefix and
	// tier are two halves of one wire string, and a mismatch on either half
	// is silent — the orchestrator simply finds no tier entries, falls back
	// to the legacy container-only interpretation, and every microvm-capable
	// agent is quietly downgraded.
	RuntimeIsolationCapabilityPrefix = "runtime_isolation:"
)

// WorkflowRuntimePayload starts a supervised SDK workflow runtime under an
// already-enrolled PostGrip agent. The host agent injects delegated agent
// credentials and limits its own polling to operational task types, while the
// SDK runtime polls workflow/activity/query/update task families.
type WorkflowRuntimePayload struct {
	RuntimeID      string            `json:"runtime_id,omitempty"`
	Image          string            `json:"image,omitempty"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Namespace      string            `json:"namespace,omitempty"`
	Queue          string            `json:"queue,omitempty"`
	PullPolicy     string            `json:"pull_policy,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	// Isolation is the isolation floor this workload requires, one of
	// IsolationTierContainer or IsolationTierMicroVM; empty means the
	// container default.
	//
	// Setting it requires Image: a command-only runtime executes directly on
	// the agent host, which honors no isolation floor, so the orchestrator
	// rejects that combination at enqueue rather than running the workload
	// below its requested tier.
	Isolation string `json:"isolation,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

const (
	AgentStatusPending     = "pending"
	AgentStatusOnline      = "online"
	AgentStatusOffline     = "offline"
	AgentStatusQuarantined = "quarantined"
	AgentStatusMaintenance = "maintenance"
	AgentStatusRevoked     = "revoked"

	AgentTrustStateUnknown = "unknown"
	AgentTrustStateTrusted = "trusted"
	AgentTrustStateStale   = "stale"
	AgentTrustStateFailed  = "failed"

	AgentAttestationSubjectAgent  = "agent"
	AgentAttestationSubjectHelper = "agent_helper"
)

type EnrollAgentRequest struct {
	EnrollmentKey string   `json:"enrollmentKey"`
	AgentID       string   `json:"agentId,omitempty"`
	TenantID      string   `json:"tenantId,omitempty"`
	Name          string   `json:"name,omitempty"`
	Host          string   `json:"host,omitempty"`
	Version       int      `json:"version,omitempty"`
	Namespaces    []string `json:"namespaces,omitempty"`
	Queues        []string `json:"queues,omitempty"`
	// SignaturePublicKey is the agent's Ed25519 public key (base64 std-encoded
	// 32 bytes). Sent at enrollment so the orchestrator can verify subsequent
	// task-result POSTs were produced by the same enrolled agent. Optional for
	// backward compatibility with pre-signing agents.
	SignaturePublicKey string `json:"signaturePublicKey,omitempty"`
}

func (r *EnrollAgentRequest) UnmarshalJSON(data []byte) error {
	type rawEnrollAgentRequest struct {
		EnrollmentKey      string   `json:"enrollmentKey"`
		AgentID            string   `json:"agentId,omitempty"`
		TenantID           string   `json:"tenantId,omitempty"`
		Name               string   `json:"name,omitempty"`
		Host               string   `json:"host,omitempty"`
		Version            int      `json:"version,omitempty"`
		Namespaces         []string `json:"namespaces,omitempty"`
		Queues             []string `json:"queues,omitempty"`
		SignaturePublicKey string   `json:"signaturePublicKey,omitempty"`
	}
	var raw rawEnrollAgentRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.EnrollmentKey = raw.EnrollmentKey
	r.AgentID = raw.AgentID
	r.TenantID = raw.TenantID
	r.Name = raw.Name
	r.Host = raw.Host
	r.Version = raw.Version
	r.Namespaces = raw.Namespaces
	r.Queues = raw.Queues
	r.SignaturePublicKey = raw.SignaturePublicKey
	return nil
}

func (r EnrollAgentRequest) MarshalJSON() ([]byte, error) {
	type rawEnrollAgentRequest struct {
		EnrollmentKey      string   `json:"enrollmentKey"`
		AgentID            string   `json:"agentId,omitempty"`
		TenantID           string   `json:"tenantId,omitempty"`
		Name               string   `json:"name,omitempty"`
		Host               string   `json:"host,omitempty"`
		Version            int      `json:"version,omitempty"`
		Namespaces         []string `json:"namespaces,omitempty"`
		Queues             []string `json:"queues,omitempty"`
		SignaturePublicKey string   `json:"signaturePublicKey,omitempty"`
	}
	return json.Marshal(rawEnrollAgentRequest{
		EnrollmentKey:      r.EnrollmentKey,
		AgentID:            r.AgentID,
		TenantID:           r.TenantID,
		Name:               r.Name,
		Host:               r.Host,
		Version:            r.Version,
		Namespaces:         r.Namespaces,
		Queues:             r.Queues,
		SignaturePublicKey: r.SignaturePublicKey,
	})
}

type RefreshAgentSessionRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type AgentAttestationEvidence struct {
	ArtifactDigest      string          `json:"artifactDigest,omitempty"`
	ImageDigest         string          `json:"imageDigest,omitempty"`
	ProvenanceDigest    string          `json:"provenanceDigest,omitempty"`
	SignerIdentity      string          `json:"signerIdentity,omitempty"`
	AttestationProvider string          `json:"attestationProvider,omitempty"`
	IssuedAt            time.Time       `json:"issuedAt,omitempty"`
	Nonce               string          `json:"nonce,omitempty"`
	RawBundle           json.RawMessage `json:"rawBundle,omitempty"`
}

type AgentAttestationChallengeResponse struct {
	Status      string    `json:"status"`
	ChallengeID string    `json:"challengeId"`
	AgentID     string    `json:"agentId"`
	TenantID    string    `json:"tenantId"`
	Nonce       string    `json:"nonce"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type AgentAttestationVerifyRequest struct {
	ChallengeID string                   `json:"challengeId"`
	Evidence    AgentAttestationEvidence `json:"evidence"`
	Subject     string                   `json:"subject,omitempty"`
}

type AgentAttestationVerifyResponse struct {
	Status               string     `json:"status"`
	AgentID              string     `json:"agentId"`
	TenantID             string     `json:"tenantId"`
	Subject              string     `json:"subject,omitempty"`
	TrustState           string     `json:"trustState"`
	Reason               string     `json:"reason,omitempty"`
	WouldTrust           bool       `json:"wouldTrust,omitempty"`
	AttestationExpiresAt *time.Time `json:"attestationExpiresAt,omitempty"`
}

type AgentSessionResponse struct {
	AgentID          string    `json:"agentId"`
	TenantID         string    `json:"tenantId"`
	TokenFamilyID    string    `json:"tokenFamilyId"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
	Status           string    `json:"status"`
	TrustState       string    `json:"trustState"`
	TrustReason      string    `json:"trustReason,omitempty"`
}

func (r *AgentSessionResponse) UnmarshalJSON(data []byte) error {
	type rawAgentSessionResponse struct {
		AgentID          string    `json:"agentId"`
		TenantID         string    `json:"tenantId"`
		TokenFamilyID    string    `json:"tokenFamilyId"`
		AccessToken      string    `json:"accessToken"`
		RefreshToken     string    `json:"refreshToken"`
		AccessExpiresAt  time.Time `json:"accessExpiresAt"`
		RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
		Status           string    `json:"status"`
		TrustState       string    `json:"trustState"`
		TrustReason      string    `json:"trustReason,omitempty"`
	}
	var raw rawAgentSessionResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.AgentID = raw.AgentID
	r.TenantID = raw.TenantID
	r.TokenFamilyID = raw.TokenFamilyID
	r.AccessToken = raw.AccessToken
	r.RefreshToken = raw.RefreshToken
	r.AccessExpiresAt = raw.AccessExpiresAt
	r.RefreshExpiresAt = raw.RefreshExpiresAt
	r.Status = raw.Status
	r.TrustState = raw.TrustState
	r.TrustReason = raw.TrustReason
	return nil
}

func (r AgentSessionResponse) MarshalJSON() ([]byte, error) {
	type rawAgentSessionResponse struct {
		AgentID          string    `json:"agentId"`
		TenantID         string    `json:"tenantId"`
		TokenFamilyID    string    `json:"tokenFamilyId"`
		AccessToken      string    `json:"accessToken"`
		RefreshToken     string    `json:"refreshToken"`
		AccessExpiresAt  time.Time `json:"accessExpiresAt"`
		RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
		Status           string    `json:"status"`
		TrustState       string    `json:"trustState"`
		TrustReason      string    `json:"trustReason,omitempty"`
	}
	return json.Marshal(rawAgentSessionResponse{
		AgentID:          r.AgentID,
		TenantID:         r.TenantID,
		TokenFamilyID:    r.TokenFamilyID,
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		AccessExpiresAt:  r.AccessExpiresAt,
		RefreshExpiresAt: r.RefreshExpiresAt,
		Status:           r.Status,
		TrustState:       r.TrustState,
		TrustReason:      r.TrustReason,
	})
}

type AgentSecurityRecord struct {
	ID                            string                  `json:"id"`
	TenantID                      string                  `json:"tenantId"`
	Name                          string                  `json:"name,omitempty"`
	Host                          string                  `json:"host,omitempty"`
	Status                        string                  `json:"status"`
	TrustState                    string                  `json:"trustState"`
	TrustReason                   string                  `json:"trustReason,omitempty"`
	Version                       int                     `json:"version,omitempty"`
	EnrollmentKeyHash             string                  `json:"enrollmentKeyHash,omitempty"`
	SignaturePublicKey            string                  `json:"signaturePublicKey,omitempty"`
	LastAttestedAt                *time.Time              `json:"lastAttestedAt,omitempty"`
	AttestationExpiresAt          *time.Time              `json:"attestationExpiresAt,omitempty"`
	TrustedArtifactDigest         string                  `json:"trustedArtifactDigest,omitempty"`
	TrustedImageDigest            string                  `json:"trustedImageDigest,omitempty"`
	TrustedSigner                 string                  `json:"trustedSigner,omitempty"`
	TrustedProvenanceDigest       string                  `json:"trustedProvenanceDigest,omitempty"`
	HelperTrustState              string                  `json:"helperTrustState"`
	HelperTrustReason             string                  `json:"helperTrustReason,omitempty"`
	HelperLastAttestedAt          *time.Time              `json:"helperLastAttestedAt,omitempty"`
	HelperAttestationExpiresAt    *time.Time              `json:"helperAttestationExpiresAt,omitempty"`
	HelperTrustedArtifactDigest   string                  `json:"helperTrustedArtifactDigest,omitempty"`
	HelperTrustedImageDigest      string                  `json:"helperTrustedImageDigest,omitempty"`
	HelperTrustedSigner           string                  `json:"helperTrustedSigner,omitempty"`
	HelperTrustedProvenanceDigest string                  `json:"helperTrustedProvenanceDigest,omitempty"`
	HelperMaintenanceMode         bool                    `json:"helperMaintenanceMode"`
	Namespaces                    []string                `json:"namespaces,omitempty"`
	Queues                        []string                `json:"queues,omitempty"`
	MaintenanceWindow             *AgentMaintenanceWindow `json:"maintenanceWindow"`
	LogLevel                      string                  `json:"logLevel"`
	LastSeenAt                    *time.Time              `json:"lastSeenAt,omitempty"`
	EnrolledAt                    *time.Time              `json:"enrolledAt,omitempty"`
	RevokedAt                     *time.Time              `json:"revokedAt,omitempty"`
	RemovedAt                     *time.Time              `json:"removedAt,omitempty"`
	CreatedAt                     time.Time               `json:"createdAt"`
	UpdatedAt                     time.Time               `json:"updatedAt"`
}

type AgentEnrollmentKey struct {
	TenantID    string     `json:"tenantId"`
	KeyHash     string     `json:"keyHash"`
	Label       string     `json:"label,omitempty"`
	UsedByAgent string     `json:"usedByAgent,omitempty"`
	UsedAt      *time.Time `json:"usedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

func (r *AgentEnrollmentKey) UnmarshalJSON(data []byte) error {
	type rawAgentEnrollmentKey struct {
		TenantID    string     `json:"tenantId"`
		KeyHash     string     `json:"keyHash"`
		Label       string     `json:"label,omitempty"`
		UsedByAgent string     `json:"usedByAgent,omitempty"`
		UsedAt      *time.Time `json:"usedAt,omitempty"`
		CreatedAt   time.Time  `json:"createdAt"`
	}
	var raw rawAgentEnrollmentKey
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.TenantID = raw.TenantID
	r.KeyHash = raw.KeyHash
	r.Label = raw.Label
	r.UsedByAgent = raw.UsedByAgent
	r.UsedAt = raw.UsedAt
	r.CreatedAt = raw.CreatedAt
	return nil
}

func (r AgentEnrollmentKey) MarshalJSON() ([]byte, error) {
	type rawAgentEnrollmentKey struct {
		TenantID    string     `json:"tenantId"`
		KeyHash     string     `json:"keyHash"`
		Label       string     `json:"label,omitempty"`
		UsedByAgent string     `json:"usedByAgent,omitempty"`
		UsedAt      *time.Time `json:"usedAt,omitempty"`
		CreatedAt   time.Time  `json:"createdAt"`
	}
	return json.Marshal(rawAgentEnrollmentKey{
		TenantID:    r.TenantID,
		KeyHash:     r.KeyHash,
		Label:       r.Label,
		UsedByAgent: r.UsedByAgent,
		UsedAt:      r.UsedAt,
		CreatedAt:   r.CreatedAt,
	})
}

type AgentAuthSession struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenantId"`
	AgentID          string     `json:"agentId"`
	TokenFamilyID    string     `json:"tokenFamilyId"`
	RefreshTokenHash string     `json:"refreshTokenHash"`
	ExpiresAt        time.Time  `json:"expiresAt"`
	RevokedAt        *time.Time `json:"revokedAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

func (r *AgentAuthSession) UnmarshalJSON(data []byte) error {
	type rawAgentAuthSession struct {
		ID               string     `json:"id"`
		TenantID         string     `json:"tenantId"`
		AgentID          string     `json:"agentId"`
		TokenFamilyID    string     `json:"tokenFamilyId"`
		RefreshTokenHash string     `json:"refreshTokenHash"`
		ExpiresAt        time.Time  `json:"expiresAt"`
		RevokedAt        *time.Time `json:"revokedAt,omitempty"`
		CreatedAt        time.Time  `json:"createdAt"`
	}
	var raw rawAgentAuthSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.ID = raw.ID
	r.TenantID = raw.TenantID
	r.AgentID = raw.AgentID
	r.TokenFamilyID = raw.TokenFamilyID
	r.RefreshTokenHash = raw.RefreshTokenHash
	r.ExpiresAt = raw.ExpiresAt
	r.RevokedAt = raw.RevokedAt
	r.CreatedAt = raw.CreatedAt
	return nil
}

func (r AgentAuthSession) MarshalJSON() ([]byte, error) {
	type rawAgentAuthSession struct {
		ID               string     `json:"id"`
		TenantID         string     `json:"tenantId"`
		AgentID          string     `json:"agentId"`
		TokenFamilyID    string     `json:"tokenFamilyId"`
		RefreshTokenHash string     `json:"refreshTokenHash"`
		ExpiresAt        time.Time  `json:"expiresAt"`
		RevokedAt        *time.Time `json:"revokedAt,omitempty"`
		CreatedAt        time.Time  `json:"createdAt"`
	}
	return json.Marshal(rawAgentAuthSession{
		ID:               r.ID,
		TenantID:         r.TenantID,
		AgentID:          r.AgentID,
		TokenFamilyID:    r.TokenFamilyID,
		RefreshTokenHash: r.RefreshTokenHash,
		ExpiresAt:        r.ExpiresAt,
		RevokedAt:        r.RevokedAt,
		CreatedAt:        r.CreatedAt,
	})
}
