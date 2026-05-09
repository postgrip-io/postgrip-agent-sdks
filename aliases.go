package sdk

import "github.com/postgrip-io/agent-sdk-protocol"

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
