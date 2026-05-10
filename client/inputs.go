package client

import (
	"time"

	"go.postgrip.io/sdk/workflow"
)

// EnqueueInput is the SDK-side input to TaskClient.Enqueue. Payload may be
// any JSON-serializable value; the SDK marshals it for you.
type EnqueueInput struct {
	Namespace           string
	Queue               string
	Type                string
	Payload             any
	LeaseTimeoutSeconds int
}

// ShellExecInput is the SDK-side input to TaskClient.ShellExec — same as
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

// ContainerExecInput is the SDK-side input to TaskClient.ContainerExec.
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

// WorkflowRuntimeInput enqueues a managed SDK workflow runtime onto an
// existing host-agent pool. The host agent leases the workflow.runtime task,
// delegates credentials, then launches Command with Args. The launched process
// should start a SDK Agent using the injected POSTGRIP_AGENT_* environment.
type WorkflowRuntimeInput struct {
	Namespace           string
	Queue               string
	RuntimeID           string
	Command             string
	Args                []string
	Env                 map[string]string
	WorkingDir          string
	RuntimeNamespace    string
	RuntimeQueue        string
	TimeoutSeconds      int
	LeaseTimeoutSeconds int
}

// WorkflowStartOptions is the SDK-side input to WorkflowClient.Start. It
// carries the durable identity, queue routing, payload args, and the retry
// / memo / search-attribute metadata the runtime service persists.
type WorkflowStartOptions struct {
	Namespace             string
	WorkflowID            string
	WorkflowIDReusePolicy workflow.IDReusePolicy
	TaskQueue             string
	Args                  []any
	LeaseTimeoutSeconds   int
	WorkflowRunTimeoutMs  int
	Retry                 *RetryPolicy
	Memo                  map[string]any
	SearchAttributes      map[string]any
}

// SignalWithStartOptions is the SDK-side input to WorkflowClient.SignalWithStart.
type SignalWithStartOptions struct {
	WorkflowStartOptions
	SignalName string
	SignalArgs []any
}

// ScheduleActionInput is accepted by ScheduleClient APIs for create/update;
// mirrors the TS/Python shape.
type ScheduleActionInput struct {
	Namespace             string
	Queue                 string
	WorkflowType          string
	WorkflowID            string
	WorkflowIDReusePolicy workflow.IDReusePolicy
	RunTimeoutMs          int
	Retry                 *RetryPolicy
	Memo                  map[string]any
	SearchAttributes      map[string]any
	Args                  []any
}

// CreateScheduleInput is the SDK-side input to ScheduleClient.Create.
type CreateScheduleInput struct {
	ID            string
	Namespace     string
	OverlapPolicy string
	Spec          ScheduleSpec
	Action        ScheduleActionInput
}

// UpdateScheduleInput is the SDK-side input to ScheduleClient.Update; nil
// fields are treated as "no change" by the runtime service.
type UpdateScheduleInput struct {
	OverlapPolicy *string
	Spec          *ScheduleSpec
	Action        *ScheduleActionInput
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
