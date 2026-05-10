// Package client exposes the management surface of the SDK: the HTTP
// Connection, the high-level Client (grouping Task / Workflow / Schedule
// sub-clients), and the input shapes for enqueueing work and managing
// schedules. Customer code that just needs to enqueue tasks or start
// workflows imports this package and nothing else.
package client

import "github.com/postgrip-io/agent-sdk-protocol"

// Re-export the wire types so customer code can stay within the client
// package when reading task / workflow / schedule responses.
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

// WorkflowRuntimePayload starts a supervised SDK workflow runtime under an
// already-enrolled PostGrip agent. The host agent injects delegated agent
// credentials and the SDK runtime polls workflow task families only.
type WorkflowRuntimePayload struct {
	RuntimeID      string            `json:"runtime_id,omitempty"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Namespace      string            `json:"namespace,omitempty"`
	Queue          string            `json:"queue,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

// Re-export task type / event kind / state constants.
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

	TaskTypeNoop            = protocol.TaskTypeNoop
	TaskTypeShellExec       = protocol.TaskTypeShellExec
	TaskTypeContainerExec   = protocol.TaskTypeContainerExec
	TaskTypeWorkflowRuntime = "workflow.runtime"
	TaskTypeTimer           = protocol.TaskTypeTimer

	TaskTypePrefixWorkflow = protocol.TaskTypePrefixWorkflow
	TaskTypePrefixActivity = protocol.TaskTypePrefixActivity
	TaskTypePrefixQuery    = protocol.TaskTypePrefixQuery
	TaskTypePrefixUpdate   = protocol.TaskTypePrefixUpdate

	DefaultNamespace = protocol.DefaultNamespace
	DefaultQueue     = protocol.DefaultQueue
)
