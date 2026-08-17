from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any, Literal, NotRequired, TypeAlias, TypedDict

TaskState: TypeAlias = Literal["queued", "leased", "blocked", "succeeded", "failed"]
TaskEventKind: TypeAlias = Literal["leased", "started", "heartbeat", "milestone", "progress", "stdout", "stderr", "completed", "failed"]
ScheduleState: TypeAlias = Literal["active", "paused", "deleted"]
ScheduleOverlapPolicy: TypeAlias = Literal["skip", "allow_all"]
ScheduleMissedRunPolicy: TypeAlias = Literal["catch_up", "skip"]
CancellationType: TypeAlias = Literal["try_cancel", "wait_cancellation_completed", "abandon"]
CancellationScopeType: TypeAlias = Literal["cancellable", "non_cancellable"]
WorkflowIdReusePolicy: TypeAlias = Literal["allow_duplicate", "allow_duplicate_failed_only", "reject_duplicate"]
# Isolation tiers a workflow.runtime workload can require. A requested tier is
# a floor: container work may run in a stronger tier, microvm work must never
# run in a weaker one, and an unrecognized value satisfies nothing.
IsolationTier: TypeAlias = Literal["container", "microvm"]
WorkflowFunction: TypeAlias = Callable[..., Awaitable[Any] | Any]
ActivityFunction: TypeAlias = Callable[..., Awaitable[Any] | Any]
WorkflowRegistry: TypeAlias = dict[str, WorkflowFunction]
ActivityRegistry: TypeAlias = dict[str, ActivityFunction]


class FailureInfo(TypedDict, total=False):
    message: str
    type: str
    non_retryable: bool
    details: list[Any]


class ContinueAsNewResult(TypedDict):
    workflow_id: str
    workflow_type: str
    task_queue: str
    task_id: str


class TaskResult(TypedDict, total=False):
    exit_code: int
    stdout: str
    stderr: str
    message: str
    value: Any
    failure: FailureInfo
    continue_as_new: ContinueAsNewResult
    started_at: str
    finished_at: str


class Task(TypedDict, total=False):
    id: str
    tenantId: str
    namespace: str
    queue: str
    type: str
    payload: Any
    state: TaskState
    attempt: int
    agent_id: str
    lease_timeout_seconds: int
    not_before: str
    leased_until: str
    created_at: str
    updated_at: str
    result: TaskResult
    error: str


class TaskEvent(TypedDict, total=False):
    id: str
    tenantId: str
    task_id: str
    agent_id: str
    kind: TaskEventKind
    stage: str
    message: str
    stream: str
    data: str
    details: dict[str, Any]
    created_at: str


class TaskEventInput(TypedDict, total=False):
    kind: TaskEventKind
    stage: str
    message: str
    stream: str
    data: str
    details: dict[str, Any]


class EnqueueTaskRequest(TypedDict, total=False):
    tenantId: str
    namespace: str
    queue: str
    type: str
    payload: Any
    lease_timeout_seconds: int


class ActivityTaskPayload(TypedDict, total=False):
    activityType: str
    args: list[Any]
    workflowId: str
    workflowRunId: str
    workflowTaskId: str
    attempt: int
    cancellationType: str
    retry: "RetryPolicy"


class RetryPolicy(TypedDict, total=False):
    maximumAttempts: int
    initialIntervalMs: int
    backoffCoefficient: float
    maximumIntervalMs: int
    expirationIntervalMs: int
    nonRetryableErrorTypes: list[str]


class ScheduleCalendarSpec(TypedDict, total=False):
    minute: list[int]
    hour: list[int]
    day_of_month: list[int]
    month: list[int]
    day_of_week: list[int]


class ScheduleSpec(TypedDict, total=False):
    interval_seconds: int
    cron: str
    calendar: ScheduleCalendarSpec
    timezone: str
    jitter_seconds: int
    catch_up_window_seconds: int
    missed_run_policy: ScheduleMissedRunPolicy
    start_at: str


class ScheduleAction(TypedDict, total=False):
    namespace: str
    queue: str
    workflowType: str
    workflowId: str
    workflowIdReusePolicy: WorkflowIdReusePolicy
    runTimeoutMs: int
    retry: RetryPolicy
    memo: dict[str, Any]
    searchAttributes: dict[str, Any]
    args: list[Any]


class Schedule(TypedDict, total=False):
    id: str
    tenantId: str
    namespace: str
    state: ScheduleState
    overlap_policy: ScheduleOverlapPolicy
    spec: ScheduleSpec
    action: ScheduleAction
    last_run_at: str
    next_run_at: str
    created_at: str
    updated_at: str


class CreateScheduleRequest(TypedDict):
    spec: ScheduleSpec
    action: ScheduleAction
    id: NotRequired[str]
    namespace: NotRequired[str]
    overlap_policy: NotRequired[ScheduleOverlapPolicy]


class UpdateScheduleRequest(TypedDict, total=False):
    overlap_policy: ScheduleOverlapPolicy
    spec: ScheduleSpec
    action: ScheduleAction


class PauseScheduleRequest(TypedDict, total=False):
    reason: str


class UnpauseScheduleRequest(TypedDict, total=False):
    reason: str


class TriggerScheduleRequest(TypedDict, total=False):
    reason: str


class TriggerScheduleResponse(TypedDict):
    schedule: Schedule
    task: Task


class BackfillScheduleRequest(TypedDict):
    start_at: str
    end_at: str


class BackfillScheduleResponse(TypedDict):
    schedule: Schedule
    tasks: list[Task]


class PollTaskResponse(TypedDict, total=False):
    task: Task
    directive: AgentPollDirective


class AgentPollDirective(TypedDict, total=False):
    type: Literal["upgrade", "shutdown", "log_level", "poll_now", "attest"]
    image: str
    expectedVersion: int
    force: bool
    logLevel: str
    subject: Literal["agent", "agent_helper"]


class AgentUpgradeRequest(TypedDict, total=False):
    image: str
    expectedVersion: int


class ShellExecPayload(TypedDict, total=False):
    command: str
    args: list[str]
    env: dict[str, str]
    working_dir: str
    timeout_seconds: int


class ContainerExecPayload(TypedDict, total=False):
    """Payload for ``container.exec`` tasks.

    The Go agent runs a per-task container from ``image`` via its docker
    CLI (proxied through the worker stack's docker socket proxy). Useful
    for polyglot runtimes (Node, Bun, Python, Go, anything in an image)
    without baking those runtimes into the agent image itself.

    ``image`` is required. ``command``, when set, overrides the image's
    ENTRYPOINT; ``args`` becomes the container's CMD. ``pull_policy``
    mirrors ``docker run --pull`` (``always`` | ``missing`` | ``never``);
    omit for the default ``missing``. Env keys flow through the agent's
    allowlist — ``DOCKER_*``, ``POSTGRIP_*``, and host loader/interpreter
    prefixes are rejected.

    The agent runs the container with ``--rm --network=none`` and never
    mounts host paths; share state via stdin/args/env.
    """

    image: str
    command: str
    args: list[str]
    env: dict[str, str]
    working_dir: str
    pull_policy: str
    timeout_seconds: int


class WorkflowUIMetadata(TypedDict, total=False):
    displayName: str
    description: str
    details: dict[str, str | int | float | bool]
    tags: list[str]


class WorkflowStartOptions(TypedDict, total=False):
    namespace: str
    workflow_id: str
    workflow_id_reuse_policy: WorkflowIdReusePolicy
    task_queue: str
    args: list[Any]
    lease_timeout_seconds: int
    workflow_run_timeout_ms: int
    retry: RetryPolicy
    memo: dict[str, Any]
    search_attributes: dict[str, Any]
    ui: WorkflowUIMetadata


class ContinueAsNewOptions(TypedDict, total=False):
    workflow_id: str
    workflow_type: str
    task_queue: str
    args: list[Any]
    lease_timeout_seconds: int
    workflow_run_timeout_ms: int
    retry: RetryPolicy


class ChildWorkflowOptions(TypedDict, total=False):
    workflow_id: str
    task_queue: str
    args: list[Any]
    lease_timeout_seconds: int
    workflow_run_timeout_ms: int
    cancellation_type: CancellationType
    cancellation_scope: CancellationScopeType
    retry: RetryPolicy


class WorkflowExecutionDescription(TypedDict, total=False):
    workflowId: str
    runId: str
    taskId: str
    namespace: str
    taskQueue: str
    workflowType: str
    status: str
    attempt: int
    leaseTimeoutSeconds: int
    workflowRunTimeoutMs: int
    retry: RetryPolicy
    memo: dict[str, Any]
    searchAttributes: dict[str, Any]
    result: Any
    error: str
    startedAt: str
    updatedAt: str


class WorkflowExecution(TypedDict, total=False):
    id: str
    tenantId: str
    run_id: str
    namespace: str
    type: str
    queue: str
    task_id: str
    agent_id: str
    state: Literal["running", "succeeded", "failed", "continued_as_new"]
    attempt: int
    run_timeout_ms: int
    retry: RetryPolicy
    memo: dict[str, Any]
    search_attributes: dict[str, Any]
    result: TaskResult
    error: str
    created_at: str
    updated_at: str


class WorkflowHistoryEvent(TypedDict, total=False):
    id: str
    workflow_id: str
    tenantId: str
    task_id: str
    type: str
    attributes: dict[str, Any]
    created_at: str


class WorkflowCountResponse(TypedDict):
    count: int


class Namespace(TypedDict):
    name: str
    created_at: str
    updated_at: str


class CompactResponse(TypedDict):
    removed_tasks: int
    removed_workflows: int


class WorkflowSignalDefinition(TypedDict):
    name: str
    type: Literal["signal"]
    args: list[Any]


class WorkflowQueryDefinition(TypedDict):
    name: str
    type: Literal["query"]
    args: list[Any]
    result: Any


class WorkflowUpdateDefinition(TypedDict):
    name: str
    type: Literal["update"]
    args: list[Any]
    result: Any


class SignalWorkflowRequest(TypedDict, total=False):
    name: str
    args: list[Any]


class SignalWithStartWorkflowRequest(TypedDict, total=False):
    namespace: str
    queue: str
    workflowType: str
    workflowId: str
    workflowIdReusePolicy: WorkflowIdReusePolicy
    lease_timeout_seconds: int
    runTimeoutMs: int
    retry: RetryPolicy
    memo: dict[str, Any]
    searchAttributes: dict[str, Any]
    args: list[Any]
    signal: SignalWorkflowRequest


class SignalWithStartWorkflowResponse(TypedDict):
    workflow: WorkflowExecution
    task: Task
    signal: WorkflowHistoryEvent


class CancelWorkflowRequest(TypedDict, total=False):
    reason: str


class TerminateWorkflowRequest(TypedDict, total=False):
    reason: str


class WorkflowQueryPayload(TypedDict):
    workflowId: str
    workflowType: str
    queryName: str
    args: list[Any]
    workflowRunId: NotRequired[str]


class WorkflowUpdatePayload(TypedDict):
    workflowId: str
    workflowType: str
    updateName: str
    args: list[Any]
    workflowRunId: NotRequired[str]


class WorkflowPayload(TypedDict, total=False):
    namespace: str
    workflowType: str
    workflowId: str
    runId: str
    workflowIdReusePolicy: WorkflowIdReusePolicy
    parentWorkflowId: str
    parentWorkflowRunId: str
    parentWorkflowTaskId: str
    parentCancellationType: CancellationType
    continuedFromWorkflowId: str
    runTimeoutMs: int
    retry: RetryPolicy
    memo: dict[str, Any]
    searchAttributes: dict[str, Any]
    args: list[Any]


class ActivityInvocationPayload(TypedDict, total=False):
    activityType: str
    workflowId: str
    workflowRunId: str
    workflowTaskId: str
    attempt: int
    cancellationType: CancellationType
    retry: RetryPolicy
    args: list[Any]


class WorkflowRuntimePayload(TypedDict, total=False):
    runtime_id: str
    image: str
    command: str
    args: list[str]
    env: dict[str, str]
    working_dir: str
    namespace: str
    queue: str
    pull_policy: str
    timeout_seconds: int
    # Isolation floor this workload requires; absent means the container
    # default. Requires ``image`` — a command-only runtime executes directly
    # on the agent host, which honors no isolation floor, and the
    # orchestrator rejects that combination at enqueue.
    isolation: IsolationTier


class TimerPayload(TypedDict):
    timerId: str
    durationMs: int
    fireAt: str
    workflowId: NotRequired[str]
    workflowRunId: NotRequired[str]
    workflowTaskId: NotRequired[str]


class ActivityOptions(TypedDict, total=False):
    start_to_close_timeout_ms: int
    schedule_to_close_timeout_ms: int
    cancellation_type: CancellationType
    cancellation_scope: CancellationScopeType
    retry: RetryPolicy


__all__ = [name for name in globals() if not name.startswith("_")]


# --- sandbox platform ------------------------------------------------------
#
# Mirrors agent-sdk-protocol/sandbox.go. Timestamps are RFC3339 strings, as
# everywhere else in this module.

SandboxBackend: TypeAlias = Literal["auto", "docker", "podman", "firecracker"]
SandboxDesiredState: TypeAlias = Literal["running", "stopped", "deleted"]
# Only "running", "stopped", "deleted" and "failed" are reported by agents
# today, and "scheduling" is written by the control plane at placement. The
# rest exist for forward compatibility — treat an unexpected value as
# in-flight rather than as an error.
SandboxObservedState: TypeAlias = Literal[
    "requested",
    "scheduling",
    "provisioning",
    "setting_up",
    "running",
    "stopping",
    "stopped",
    "starting",
    "deleting",
    "deleted",
    "failed",
]
SandboxSessionKind: TypeAlias = Literal["pty", "exec"]


class SandboxResourceLimits(TypedDict, total=False):
    cpus: float
    memoryBytes: int
    diskBytes: int


class SandboxPortMapping(TypedDict, total=False):
    hostPort: int
    guestPort: int
    protocol: str


class SandboxNetworkPolicy(TypedDict, total=False):
    """``internetEgress`` with ``denyHostAccess`` or ``allowCidrs`` is a 400."""

    internetEgress: bool
    allowCidrs: list[str]
    denyHostAccess: bool
    ports: list[SandboxPortMapping]


class Sandbox(TypedDict, total=False):
    tenantId: str
    id: str
    name: str
    createdBy: str
    agentId: str
    desiredState: SandboxDesiredState
    observedState: SandboxObservedState
    generation: int
    observedGeneration: int
    backend: SandboxBackend
    isolationClass: str
    image: str
    architecture: str
    workspaceId: str
    repositoryName: str
    setupCommand: list[str]
    credentialRefs: list[str]
    resourceLimits: SandboxResourceLimits
    networkPolicy: SandboxNetworkPolicy
    labels: dict[str, str]
    runtimeInstanceId: str
    failureCode: str
    failureMessage: str
    expiresAt: str
    lastActivityAt: str
    createdAt: str
    updatedAt: str
    stoppedAt: str
    deletedAt: str


class SandboxCreateRequest(TypedDict, total=False):
    """Body of ``POST /api/v1/sandboxes``.

    ``name`` must match ``^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`` and is unique per
    tenant among live sandboxes, so a duplicate is a 409. ``image`` is required
    despite being optional here. ``credentialRefs`` is reserved — any non-empty
    value is rejected with 400 today.
    """

    name: str
    backend: SandboxBackend
    image: str
    architecture: str
    workspaceId: str
    repositoryName: str
    setupCommand: list[str]
    credentialRefs: list[str]
    resourceLimits: SandboxResourceLimits
    networkPolicy: SandboxNetworkPolicy
    labels: dict[str, str]
    expiresAt: str


class SandboxListResponse(TypedDict, total=False):
    """The list endpoint returns this envelope, not a bare array."""

    sandboxes: list[Sandbox]


class SandboxWorkspace(TypedDict, total=False):
    """``id`` is not the digest: uploading identical bytes returns the
    pre-existing record, so do not assume a fresh id per upload."""

    tenantId: str
    id: str
    sha256: str
    sizeBytes: int
    repositoryName: str
    revision: str
    createdBy: str
    createdAt: str
    expiresAt: str


class CreateSandboxSessionRequest(TypedDict, total=False):
    """``rows``/``columns`` default to 24x80 and are fixed for the session's
    life — there is no resize channel. ``kind`` defaults to ``pty``; ``exec``
    requires ``command``. The sandbox must be observed running and assigned to
    an agent, else the server returns a retryable 400."""

    rows: int
    columns: int
    kind: SandboxSessionKind
    command: list[str]


class CreateSandboxSessionResponse(TypedDict, total=False):
    """The ticket is returned exactly once and is short-lived."""

    id: str
    ticket: str
    expiresAt: str
