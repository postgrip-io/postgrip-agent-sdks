// Package client exposes the management surface of the SDK: the HTTP
// Connection, the high-level Client (grouping Task / Workflow / Schedule
// sub-clients), and the input shapes for enqueueing work and managing
// schedules. Customer code that just needs to enqueue tasks or start
// workflows imports this package and nothing else.
package client

import "github.com/postgrip-io/postgrip-agent-sdks/protocol"

// Re-export the wire types so customer code can stay within the client
// package when reading task / workflow / schedule responses.
type (
	ShellExecPayload              = protocol.ShellExecPayload
	WorkflowPayload               = protocol.WorkflowPayload
	WorkflowQueryPayload          = protocol.WorkflowQueryPayload
	WorkflowUpdatePayload         = protocol.WorkflowUpdatePayload
	ActivityTaskPayload           = protocol.ActivityTaskPayload
	TimerPayload                  = protocol.TimerPayload
	ContainerExecPayload          = protocol.ContainerExecPayload
	AgentMaintenanceWindow        = protocol.AgentMaintenanceWindow
	AgentMaintenanceWindowRequest = protocol.AgentMaintenanceWindowRequest
	UpdateAgentRequest            = protocol.UpdateAgentRequest
	WorkflowRuntimePayload        = protocol.WorkflowRuntimePayload
)

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
	TaskTypeWorkflowRuntime = protocol.TaskTypeWorkflowRuntime
	TaskTypeTimer           = protocol.TaskTypeTimer

	TaskTypePrefixWorkflow = protocol.TaskTypePrefixWorkflow
	TaskTypePrefixActivity = protocol.TaskTypePrefixActivity
	TaskTypePrefixQuery    = protocol.TaskTypePrefixQuery
	TaskTypePrefixUpdate   = protocol.TaskTypePrefixUpdate

	DefaultNamespace = protocol.DefaultNamespace
	DefaultQueue     = protocol.DefaultQueue

	SandboxBackendAuto        = protocol.SandboxBackendAuto
	SandboxBackendDocker      = protocol.SandboxBackendDocker
	SandboxBackendPodman      = protocol.SandboxBackendPodman
	SandboxBackendFirecracker = protocol.SandboxBackendFirecracker

	SandboxDesiredRunning = protocol.SandboxDesiredRunning
	SandboxDesiredStopped = protocol.SandboxDesiredStopped
	SandboxDesiredDeleted = protocol.SandboxDesiredDeleted

	SandboxObservedRunning = protocol.SandboxObservedRunning
	SandboxObservedStopped = protocol.SandboxObservedStopped
	SandboxObservedDeleted = protocol.SandboxObservedDeleted
	SandboxObservedFailed  = protocol.SandboxObservedFailed

	SandboxSessionKindPTY  = protocol.SandboxSessionKindPTY
	SandboxSessionKindExec = protocol.SandboxSessionKindExec

	SandboxRelayMaxFrameBytes = protocol.SandboxRelayMaxFrameBytes

	IsolationTierContainer = protocol.IsolationTierContainer
	IsolationTierMicroVM   = protocol.IsolationTierMicroVM
)
