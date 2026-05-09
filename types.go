package sdk

import (
	"context"
	"time"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// Re-export the wire types so customer code can import everything from one
// package. The SDK and the protocol layer agree on a single JSON shape per
// resource; aliasing keeps that explicit.
type (
	Task                           = protocol.Task
	TaskState                      = protocol.TaskState
	TaskResult                     = protocol.TaskResult
	TaskEvent                      = protocol.TaskEvent
	TaskEventInput                 = protocol.TaskEventInput
	TaskEventKind                  = protocol.TaskEventKind
	EnqueueTaskRequest             = protocol.EnqueueTaskRequest
	FailureInfo                    = protocol.FailureInfo
	ContinueAsNewResult            = protocol.ContinueAsNewResult
	ShellExecPayload               = protocol.ShellExecPayload
	WorkflowPayload                = protocol.WorkflowPayload
	WorkflowQueryPayload           = protocol.WorkflowQueryPayload
	WorkflowUpdatePayload          = protocol.WorkflowUpdatePayload
	WorkflowExecution              = protocol.WorkflowExecution
	WorkflowHistoryEvent           = protocol.WorkflowHistoryEvent
	WorkflowCountResponse          = protocol.WorkflowCountResponse
	Namespace                      = protocol.Namespace
	RetryPolicy                    = protocol.RetryPolicy
	Schedule                       = protocol.Schedule
	ScheduleSpec                   = protocol.ScheduleSpec
	ScheduleCalendarSpec           = protocol.ScheduleCalendarSpec
	ScheduleAction                 = protocol.ScheduleAction
	CreateScheduleRequest          = protocol.CreateScheduleRequest
	UpdateScheduleRequest          = protocol.UpdateScheduleRequest
	PauseScheduleRequest           = protocol.PauseScheduleRequest
	UnpauseScheduleRequest         = protocol.UnpauseScheduleRequest
	TriggerScheduleRequest         = protocol.TriggerScheduleRequest
	TriggerScheduleResponse        = protocol.TriggerScheduleResponse
	BackfillScheduleRequest        = protocol.BackfillScheduleRequest
	BackfillScheduleResponse       = protocol.BackfillScheduleResponse
	ActivityTaskPayload            = protocol.ActivityTaskPayload
	TimerPayload                   = protocol.TimerPayload
	ContainerExecPayload           = protocol.ContainerExecPayload
	SignalWithStartWorkflowRequest = protocol.SignalWithStartWorkflowRequest
	AgentPollDirective             = protocol.AgentPollDirective
	AgentMaintenanceWindow         = protocol.AgentMaintenanceWindow
	AgentMaintenanceWindowRequest  = protocol.AgentMaintenanceWindowRequest
	UpdateAgentRequest             = protocol.UpdateAgentRequest
	CompleteTaskRequest            = protocol.CompleteTaskRequest
	FailTaskRequest                = protocol.FailTaskRequest
	BlockTaskRequest               = protocol.BlockTaskRequest
	SignalWorkflowRequest          = protocol.SignalWorkflowRequest
)

// Re-export task type / event kind / state constants so customer code never
// has to import the protocol package alongside the SDK.
const (
	TaskStateQueued    = protocol.TaskStateQueued
	TaskStateLeased    = protocol.TaskStateLeased
	TaskStateBlocked   = protocol.TaskStateBlocked
	TaskStateSucceeded = protocol.TaskStateSucceeded
	TaskStateFailed    = protocol.TaskStateFailed

	TaskEventKindLeased    = protocol.TaskEventKindLeased
	TaskEventKindStarted   = protocol.TaskEventKindStarted
	TaskEventKindHeartbeat = protocol.TaskEventKindHeartbeat
	TaskEventKindMilestone = protocol.TaskEventKindMilestone
	TaskEventKindProgress  = protocol.TaskEventKindProgress
	TaskEventKindStdout    = protocol.TaskEventKindStdout
	TaskEventKindStderr    = protocol.TaskEventKindStderr
	TaskEventKindCompleted = protocol.TaskEventKindCompleted
	TaskEventKindFailed    = protocol.TaskEventKindFailed

	TaskTypeNoop          = protocol.TaskTypeNoop
	TaskTypeShellExec     = protocol.TaskTypeShellExec
	TaskTypeContainerExec = protocol.TaskTypeContainerExec
	TaskTypeTimer         = protocol.TaskTypeTimer

	TaskTypePrefixWorkflow = protocol.TaskTypePrefixWorkflow
	TaskTypePrefixActivity = protocol.TaskTypePrefixActivity
	TaskTypePrefixQuery    = protocol.TaskTypePrefixQuery
	TaskTypePrefixUpdate   = protocol.TaskTypePrefixUpdate

	DefaultNamespace = protocol.DefaultNamespace
	DefaultQueue     = protocol.DefaultQueue
)

// WorkflowIDReusePolicy constrains whether a workflow id can be reused for a
// new run, mirroring the TS/Python WorkflowIdReusePolicy.
type WorkflowIDReusePolicy string

const (
	WorkflowIDReusePolicyAllowDuplicate           WorkflowIDReusePolicy = "allow_duplicate"
	WorkflowIDReusePolicyAllowDuplicateFailedOnly WorkflowIDReusePolicy = "allow_duplicate_failed_only"
	WorkflowIDReusePolicyRejectDuplicate          WorkflowIDReusePolicy = "reject_duplicate"
)

// WorkflowStartOptions is the SDK-side input to Client.Workflow.Start.
// It carries the durable identity, queue routing, payload args, and the
// retry / memo / search-attribute metadata the runtime service persists.
type WorkflowStartOptions struct {
	Namespace             string
	WorkflowID            string
	WorkflowIDReusePolicy WorkflowIDReusePolicy
	TaskQueue             string
	Args                  []any
	LeaseTimeoutSeconds   int
	WorkflowRunTimeoutMs  int
	Retry                 *RetryPolicy
	Memo                  map[string]any
	SearchAttributes      map[string]any
}

// SignalWithStartOptions is the SDK-side input to Client.Workflow.SignalWithStart.
type SignalWithStartOptions struct {
	WorkflowStartOptions
	SignalName string
	SignalArgs []any
}

// ScheduleClient APIs accept this for create/update; mirrors the TS/Python shape.
type ScheduleActionInput struct {
	Namespace             string
	Queue                 string
	WorkflowType          string
	WorkflowID            string
	WorkflowIDReusePolicy WorkflowIDReusePolicy
	RunTimeoutMs          int
	Retry                 *RetryPolicy
	Memo                  map[string]any
	SearchAttributes      map[string]any
	Args                  []any
}

// CreateScheduleInput is the SDK-side input to Client.Schedule.Create.
type CreateScheduleInput struct {
	ID            string
	Namespace     string
	OverlapPolicy string
	Spec          ScheduleSpec
	Action        ScheduleActionInput
}

// UpdateScheduleInput is the SDK-side input to Client.Schedule.Update; nil
// fields are treated as "no change" by the runtime service.
type UpdateScheduleInput struct {
	OverlapPolicy *string
	Spec          *ScheduleSpec
	Action        *ScheduleActionInput
}

// EnqueueInput is the SDK-side input to Client.Task.Enqueue. Payload may be
// any JSON-serializable value; the SDK marshals it for you.
type EnqueueInput struct {
	Namespace           string
	Queue               string
	Type                string
	Payload             any
	LeaseTimeoutSeconds int
}

// ShellExecInput is the SDK-side input to Client.Task.ShellExec — same as
// the protocol.ShellExecPayload but with a Queue field for routing and
// JSON-tag-free naming.
type ShellExecInput struct {
	Queue          string
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	TimeoutSeconds int
}

// ContainerExecInput is the SDK-side input to Client.Task.ContainerExec.
type ContainerExecInput struct {
	Queue          string
	Image          string
	Command        string
	Args           []string
	Env            map[string]string
	WorkingDir     string
	PullPolicy     string
	TimeoutSeconds int
}

// WorkflowExecutionDescription mirrors the TS WorkflowExecutionDescription
// returned by WorkflowHandle.Describe.
type WorkflowExecutionDescription struct {
	WorkflowID           string         `json:"workflowId"`
	RunID                string         `json:"runId,omitempty"`
	TaskID               string         `json:"taskId"`
	Namespace            string         `json:"namespace"`
	TaskQueue            string         `json:"taskQueue"`
	WorkflowType         string         `json:"workflowType"`
	Status               string         `json:"status"`
	Attempt              int            `json:"attempt,omitempty"`
	LeaseTimeoutSeconds  int            `json:"leaseTimeoutSeconds,omitempty"`
	WorkflowRunTimeoutMs int            `json:"workflowRunTimeoutMs,omitempty"`
	Retry                *RetryPolicy   `json:"retry,omitempty"`
	Memo                 map[string]any `json:"memo,omitempty"`
	SearchAttributes     map[string]any `json:"searchAttributes,omitempty"`
	Result               any            `json:"result,omitempty"`
	Error                string         `json:"error,omitempty"`
	StartedAt            time.Time      `json:"startedAt"`
	UpdatedAt            time.Time      `json:"updatedAt"`
}

// MilestoneOptions controls activity / workflow milestone emission so callers
// can render ordered progress.
type MilestoneOptions struct {
	Index   int
	Total   int
	Details map[string]any
}

// WorkflowFunc is the customer-supplied workflow body. The SDK invokes it
// with a workflow-scoped Context (sleep / activity / child / signal /
// query / update APIs all dispatch through it) and the deserialized args.
type WorkflowFunc func(ctx Context, args []any) (any, error)

// ActivityFunc is the customer-supplied activity body. Standard
// context.Context is honored for cancellation/deadline; the activity's task
// id and runtime metadata can be retrieved via ActivityInfo(ctx).
type ActivityFunc func(ctx context.Context, args []any) (any, error)

// WorkflowRegistry maps workflow types to their implementations. Worker
// rejects tasks for unregistered workflow types with a non-retryable failure.
type WorkflowRegistry map[string]WorkflowFunc

// ActivityRegistry maps activity names to their implementations.
type ActivityRegistry map[string]ActivityFunc
