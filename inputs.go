package sdk

import "time"

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
