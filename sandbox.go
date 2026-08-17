package protocol

import (
	"encoding/json"
	"strings"
	"time"
)

// Sandbox platform wire shapes.
//
// These lived in postgrip-web's private postgrip-shared/sandbox package, which
// no public SDK can import — which is why the sandbox platform had exactly one
// client (the bundled `postgrip` CLI, which is in that repo) and no SDK
// surface at all. They are wire shapes by the same definition as everything in
// types.go, so they belong here.
//
// Names are prefixed for this package's scope: the source package could call a
// sandbox a `Record` because it lived in `sandbox`, but `protocol.Record` says
// nothing. The JSON field names are unchanged and must stay that way.
//
// Note for anyone adding a field: the orchestrator decodes request bodies with
// DisallowUnknownFields, so a client sending a key the server does not model
// gets a 400 rather than having it ignored. New request fields must land
// server-side before any SDK emits them.

// Sandbox lifecycle constants that clients need and would otherwise hardcode.
const (
	// SandboxRelayMaxFrameBytes bounds a single WebSocket frame through the
	// sandbox session relay, in either direction. A peer writing a larger
	// frame has its session closed rather than forwarded, so clients must
	// chunk their writes at or below this size.
	SandboxRelayMaxFrameBytes = 1 << 20

	// SandboxWorkspaceMaxUploadBytes is the server's cap on a workspace
	// archive upload. Larger uploads are rejected with 413.
	SandboxWorkspaceMaxUploadBytes = 512 << 20

	// Workspace upload metadata headers. The upload body is the raw gzipped
	// tar stream — not multipart — and these carry everything else.
	SandboxWorkspaceRepositoryHeader = "X-PostGrip-Repository"
	SandboxWorkspaceRevisionHeader   = "X-PostGrip-Revision"
	// SandboxWorkspaceDigestHeader is returned on workspace download so the
	// agent can verify the archive it extracted.
	SandboxWorkspaceDigestHeader = "X-Content-SHA256"

	// SandboxSessionKindPTY allocates a TTY sized by the session's rows and
	// columns. SandboxSessionKindExec runs a command with stdin attached and
	// no TTY; it requires Command to be set.
	SandboxSessionKindPTY  = "pty"
	SandboxSessionKindExec = "exec"

	// SandboxExecCloseStatusBase encodes a sandbox exec's process exit code
	// in the WebSocket close status: status = base + exitCode, for exit codes
	// 0..255. A close status outside [base, base+255] is a transport failure,
	// not a process exit.
	SandboxExecCloseStatusBase = 4000
	SandboxExecCloseStatusMax  = SandboxExecCloseStatusBase + 255
)

// SandboxExecExitCode reports the process exit code carried by a WebSocket
// close status, and whether the status encoded one at all.
func SandboxExecExitCode(closeStatus int) (code int, ok bool) {
	if closeStatus < SandboxExecCloseStatusBase || closeStatus > SandboxExecCloseStatusMax {
		return 0, false
	}
	return closeStatus - SandboxExecCloseStatusBase, true
}

// SandboxBackend is the runtime a sandbox executes under. SandboxBackendAuto
// lets the control plane pick from what the assigned agent advertises,
// preferring stronger isolation first.
type SandboxBackend string

const (
	SandboxBackendAuto        SandboxBackend = "auto"
	SandboxBackendDocker      SandboxBackend = "docker"
	SandboxBackendPodman      SandboxBackend = "podman"
	SandboxBackendFirecracker SandboxBackend = "firecracker"
)

func (b SandboxBackend) Valid() bool {
	switch b {
	case SandboxBackendAuto, SandboxBackendDocker, SandboxBackendPodman, SandboxBackendFirecracker:
		return true
	default:
		return false
	}
}

// SandboxDesiredState is what the client asked for. Clients set it indirectly
// through the start, stop, and delete endpoints; each transition increments
// the sandbox's Generation.
type SandboxDesiredState string

const (
	SandboxDesiredRunning SandboxDesiredState = "running"
	SandboxDesiredStopped SandboxDesiredState = "stopped"
	SandboxDesiredDeleted SandboxDesiredState = "deleted"
)

// SandboxObservedState is what the assigned agent last reported. A client
// waiting for readiness polls this, but should compare ObservedGeneration
// against Generation as well: a "running" reading can predate a stop or start
// that has not been observed yet.
//
// Only running, stopped, deleted and failed are reported by agents today, and
// scheduling is written by the control plane at placement. The remaining
// values are declared for forward compatibility, so clients must accept any of
// them rather than treating an unexpected value as an error.
type SandboxObservedState string

const (
	SandboxObservedRequested    SandboxObservedState = "requested"
	SandboxObservedScheduling   SandboxObservedState = "scheduling"
	SandboxObservedProvisioning SandboxObservedState = "provisioning"
	SandboxObservedSettingUp    SandboxObservedState = "setting_up"
	SandboxObservedRunning      SandboxObservedState = "running"
	SandboxObservedStopping     SandboxObservedState = "stopping"
	SandboxObservedStopped      SandboxObservedState = "stopped"
	SandboxObservedStarting     SandboxObservedState = "starting"
	SandboxObservedDeleting     SandboxObservedState = "deleting"
	SandboxObservedDeleted      SandboxObservedState = "deleted"
	SandboxObservedFailed       SandboxObservedState = "failed"
)

func (s SandboxObservedState) Valid() bool {
	switch s {
	case SandboxObservedRequested, SandboxObservedScheduling, SandboxObservedProvisioning,
		SandboxObservedSettingUp, SandboxObservedRunning, SandboxObservedStopping,
		SandboxObservedStopped, SandboxObservedStarting, SandboxObservedDeleting,
		SandboxObservedDeleted, SandboxObservedFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether this state will not change again without a new
// client request.
//
// It answers a question about the state alone, so it is not sufficient to bound
// a poll: a sandbox that has just been asked to start still reads `stopped`
// from the previous generation until the agent observes the request. Use
// Sandbox.Settled for that.
func (s SandboxObservedState) Terminal() bool {
	switch s {
	case SandboxObservedRunning, SandboxObservedStopped, SandboxObservedDeleted, SandboxObservedFailed:
		return true
	default:
		return false
	}
}

type SandboxResourceLimits struct {
	CPUs        float64 `json:"cpus,omitempty"`
	MemoryBytes int64   `json:"memoryBytes,omitempty"`
	DiskBytes   int64   `json:"diskBytes,omitempty"`
}

// SandboxNetworkPolicy rejects InternetEgress combined with either
// DenyHostAccess or AllowCIDRs; the server returns 400 for that combination.
type SandboxNetworkPolicy struct {
	InternetEgress bool                 `json:"internetEgress"`
	AllowCIDRs     []string             `json:"allowCidrs,omitempty"`
	DenyHostAccess bool                 `json:"denyHostAccess"`
	Ports          []SandboxPortMapping `json:"ports,omitempty"`
}

type SandboxPortMapping struct {
	HostPort  int    `json:"hostPort"`
	GuestPort int    `json:"guestPort"`
	Protocol  string `json:"protocol,omitempty"`
}

// Sandbox is the full sandbox record, returned by create, get, start, stop and
// delete, and as the element type of SandboxListResponse.
type Sandbox struct {
	TenantID           string                `json:"tenantId"`
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	CreatedBy          string                `json:"createdBy"`
	AgentID            string                `json:"agentId,omitempty"`
	DesiredState       SandboxDesiredState   `json:"desiredState"`
	ObservedState      SandboxObservedState  `json:"observedState"`
	Generation         int64                 `json:"generation"`
	ObservedGeneration int64                 `json:"observedGeneration"`
	Backend            SandboxBackend        `json:"backend"`
	IsolationClass     string                `json:"isolationClass"`
	Image              string                `json:"image"`
	Architecture       string                `json:"architecture,omitempty"`
	WorkspaceID        string                `json:"workspaceId,omitempty"`
	RepositoryName     string                `json:"repositoryName,omitempty"`
	SetupCommand       []string              `json:"setupCommand,omitempty"`
	CredentialRefs     []string              `json:"credentialRefs,omitempty"`
	ResourceLimits     SandboxResourceLimits `json:"resourceLimits"`
	NetworkPolicy      SandboxNetworkPolicy  `json:"networkPolicy"`
	Labels             map[string]string     `json:"labels,omitempty"`
	RuntimeInstanceID  string                `json:"runtimeInstanceId,omitempty"`
	FailureCode        string                `json:"failureCode,omitempty"`
	FailureMessage     string                `json:"failureMessage,omitempty"`
	ExpiresAt          *time.Time            `json:"expiresAt,omitempty"`
	LastActivityAt     *time.Time            `json:"lastActivityAt,omitempty"`
	CreatedAt          time.Time             `json:"createdAt"`
	UpdatedAt          time.Time             `json:"updatedAt"`
	StoppedAt          *time.Time            `json:"stoppedAt,omitempty"`
	DeletedAt          *time.Time            `json:"deletedAt,omitempty"`
}

// Ready reports whether the sandbox is running *and* the agent has observed
// the client's most recent request. ObservedState alone can be stale relative
// to a just-issued start or stop.
func (s Sandbox) Ready() bool {
	return s.ObservedState == SandboxObservedRunning && s.ObservedGeneration >= s.Generation
}

// Settled reports whether the sandbox has reached a state that will not change
// without a new client request, *and* that state reflects the client's most
// recent request. This is the predicate that bounds a poll.
//
// ObservedState.Terminal alone is not enough, and the gap is not theoretical:
// start a stopped sandbox and its record carries the new Generation while
// ObservedState is still `stopped` from the previous one. A poll bounded on the
// state would give up on the start it was waiting for, having observed only the
// state it was trying to leave.
func (s Sandbox) Settled() bool {
	return s.ObservedState.Terminal() && s.ObservedGeneration >= s.Generation
}

// SandboxCreateRequest is the body of POST /api/v1/sandboxes.
//
// Name is required and must match ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$; it is
// unique per tenant among live sandboxes, so a duplicate returns 409. Image is
// required despite being omitempty. CredentialRefs is reserved: any non-empty
// value is rejected with 400 today.
type SandboxCreateRequest struct {
	Name           string                `json:"name"`
	Backend        SandboxBackend        `json:"backend,omitempty"`
	Image          string                `json:"image,omitempty"`
	Architecture   string                `json:"architecture,omitempty"`
	WorkspaceID    string                `json:"workspaceId,omitempty"`
	RepositoryName string                `json:"repositoryName,omitempty"`
	SetupCommand   []string              `json:"setupCommand,omitempty"`
	CredentialRefs []string              `json:"credentialRefs,omitempty"`
	ResourceLimits SandboxResourceLimits `json:"resourceLimits,omitempty"`
	NetworkPolicy  SandboxNetworkPolicy  `json:"networkPolicy,omitempty"`
	Labels         map[string]string     `json:"labels,omitempty"`
	ExpiresAt      *time.Time            `json:"expiresAt,omitempty"`
}

// SandboxListResponse is the envelope returned by GET /api/v1/sandboxes.
// It was an anonymous map server-side, which every client had to rediscover.
// Deleted sandboxes are filtered out of the list but remain fetchable by id.
type SandboxListResponse struct {
	Sandboxes []Sandbox `json:"sandboxes"`
}

// SandboxWorkspace is the record returned by POST /api/v1/workspaces.
//
// ID is not the digest: identical bytes uploaded twice return the *existing*
// record, so a client must not assume a fresh ID per upload.
type SandboxWorkspace struct {
	TenantID  string `json:"tenantId"`
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	// StorageKey is the orchestrator's blob key, derived from SHA256. It is
	// `json:"-"` — it never crosses the wire and means nothing to a client —
	// but it lives here so the server does not need a parallel copy of this
	// struct just to carry one extra field. A fork of a wire type for a
	// non-wire field is how these shapes diverge.
	StorageKey     string    `json:"-"`
	RepositoryName string    `json:"repositoryName"`
	Revision       string    `json:"revision,omitempty"`
	CreatedBy      string    `json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// CreateSandboxSessionRequest is the body of
// POST /api/v1/sandboxes/{id}/sessions. Rows and Columns default to 24x80 and
// are fixed for the life of the session — there is no resize channel. Kind
// defaults to pty; exec requires Command.
//
// The sandbox must be observed running and assigned to an agent, otherwise the
// server returns 400 "sandbox is not running", which is retryable.
type CreateSandboxSessionRequest struct {
	Rows    int      `json:"rows,omitempty"`
	Columns int      `json:"columns,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Command []string `json:"command,omitempty"`
}

// CreateSandboxSessionResponse carries a single-use relay ticket. The ticket
// is returned exactly once and stored only as a hash, and it is short-lived —
// connect promptly rather than holding it.
type CreateSandboxSessionResponse struct {
	ID        string    `json:"id"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// --- agent-plane sandbox shapes -------------------------------------------
//
// These flow between an agent and the orchestrator's /api/v1/agent/ lane. They
// are here so the contract lives in one place, but client SDKs do not use
// them and they are deliberately outside the TS/Python mirror set.

type SandboxEvent struct {
	TenantID  string          `json:"tenantId"`
	SandboxID string          `json:"sandboxId"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Record    json.RawMessage `json:"record"`
	CreatedAt time.Time       `json:"createdAt"`
}

type SandboxObservation struct {
	SandboxID          string               `json:"sandboxId"`
	ObservedGeneration int64                `json:"observedGeneration"`
	State              SandboxObservedState `json:"state"`
	RuntimeInstanceID  string               `json:"runtimeInstanceId,omitempty"`
	FailureCode        string               `json:"failureCode,omitempty"`
	FailureMessage     string               `json:"failureMessage,omitempty"`
}

type SandboxReconcileRequest struct {
	Backends     []SandboxBackend      `json:"backends"`
	Capacity     SandboxResourceLimits `json:"capacity,omitempty"`
	Observations []SandboxObservation  `json:"observations,omitempty"`
}

type SandboxReconcileResponse struct {
	Sandboxes []Sandbox                  `json:"sandboxes"`
	Sessions  []SandboxSessionAssignment `json:"sessions,omitempty"`
}

type SandboxSessionAssignment struct {
	ID                string    `json:"id"`
	SandboxID         string    `json:"sandboxId"`
	RuntimeInstanceID string    `json:"runtimeInstanceId"`
	Kind              string    `json:"kind"`
	Rows              int       `json:"rows"`
	Columns           int       `json:"columns"`
	Command           []string  `json:"command,omitempty"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

// IsolationForSandboxBackend maps a backend onto the isolation tier vocabulary
// shared with workflow.runtime, so both planes describe isolation the same way.
// It reports resolved=false for SandboxBackendAuto, whose tier is not knowable
// until the control plane picks a runtime.
//
// The second return value is not decoration. Auto is allowed to resolve to
// Firecracker, so returning IsolationTierContainer for it — as this did — makes
// a caller advertise the opposite tier from the runtime that actually gets
// chosen. Reporting the empty string instead would be no better, because empty
// already means "the container default" in the workflow.runtime Isolation
// field, so an unresolved tier would read as a resolved one there. A caller
// that must persist something for an auto sandbox has to choose a placeholder
// knowingly, and this signature is what forces that choice to be visible.
func IsolationForSandboxBackend(backend SandboxBackend) (tier string, resolved bool) {
	switch backend {
	case SandboxBackendFirecracker:
		return IsolationTierMicroVM, true
	case SandboxBackendDocker, SandboxBackendPodman:
		return IsolationTierContainer, true
	default:
		return "", false
	}
}

// NormalizeSandboxBackend trims and lowercases a backend, mapping empty onto
// auto. It does not validate: callers wanting rejection use Valid.
func NormalizeSandboxBackend(value string) SandboxBackend {
	b := SandboxBackend(strings.ToLower(strings.TrimSpace(value)))
	if b == "" {
		return SandboxBackendAuto
	}
	return b
}
