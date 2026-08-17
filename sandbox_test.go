package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Exit codes ride in the WebSocket close status. Getting the boundaries wrong
// turns a transport failure into a plausible-looking exit code, or vice versa.
func TestSandboxExecExitCode(t *testing.T) {
	cases := []struct {
		status   int
		wantCode int
		wantOK   bool
	}{
		{SandboxExecCloseStatusBase, 0, true},
		{SandboxExecCloseStatusBase + 1, 1, true},
		{SandboxExecCloseStatusBase + 255, 255, true},
		{SandboxExecCloseStatusBase - 1, 0, false},   // 3999: not ours
		{SandboxExecCloseStatusBase + 256, 0, false}, // past the 8-bit range
		{1000, 0, false}, // normal closure
		{1008, 0, false}, // policy violation (expiry)
	}
	for _, tc := range cases {
		code, ok := SandboxExecExitCode(tc.status)
		if ok != tc.wantOK || code != tc.wantCode {
			t.Fatalf("SandboxExecExitCode(%d) = (%d, %v), want (%d, %v)",
				tc.status, code, ok, tc.wantCode, tc.wantOK)
		}
	}
}

// A "running" reading can predate a stop or start the agent has not observed
// yet, so readiness is state AND generation.
func TestSandboxReadyRequiresObservedGeneration(t *testing.T) {
	stale := Sandbox{ObservedState: SandboxObservedRunning, Generation: 2, ObservedGeneration: 1}
	if stale.Ready() {
		t.Fatal("sandbox with an unobserved generation reported ready")
	}
	fresh := Sandbox{ObservedState: SandboxObservedRunning, Generation: 2, ObservedGeneration: 2}
	if !fresh.Ready() {
		t.Fatal("running sandbox at the observed generation reported not ready")
	}
	stopped := Sandbox{ObservedState: SandboxObservedStopped, Generation: 1, ObservedGeneration: 1}
	if stopped.Ready() {
		t.Fatal("stopped sandbox reported ready")
	}
}

// A poll loop bounds itself on Terminal; treating an in-flight state as
// terminal would abandon a sandbox that was still coming up.
func TestSandboxObservedStateTerminal(t *testing.T) {
	terminal := []SandboxObservedState{
		SandboxObservedRunning, SandboxObservedStopped,
		SandboxObservedDeleted, SandboxObservedFailed,
	}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Fatalf("%q should be terminal", s)
		}
	}
	inFlight := []SandboxObservedState{
		SandboxObservedRequested, SandboxObservedScheduling, SandboxObservedProvisioning,
		SandboxObservedSettingUp, SandboxObservedStopping, SandboxObservedStarting,
		SandboxObservedDeleting,
	}
	for _, s := range inFlight {
		if s.Terminal() {
			t.Fatalf("%q should not be terminal", s)
		}
		if !s.Valid() {
			t.Fatalf("%q should be a valid observed state", s)
		}
	}
	if SandboxObservedState("banana").Valid() {
		t.Fatal("unknown observed state reported valid")
	}
}

func TestNormalizeSandboxBackend(t *testing.T) {
	for input, want := range map[string]SandboxBackend{
		"":             SandboxBackendAuto,
		"   ":          SandboxBackendAuto,
		"DOCKER":       SandboxBackendDocker,
		" Firecracker": SandboxBackendFirecracker,
	} {
		if got := NormalizeSandboxBackend(input); got != want {
			t.Fatalf("NormalizeSandboxBackend(%q) = %q, want %q", input, got, want)
		}
	}
	// Normalize deliberately does not validate; Valid is the gate.
	if got := NormalizeSandboxBackend("qemu"); got.Valid() {
		t.Fatalf("NormalizeSandboxBackend(%q) should not be valid", got)
	}
}

// The sandbox and workflow.runtime planes must describe isolation with one
// vocabulary, or a firecracker sandbox and a microvm runtime disagree.
func TestIsolationForSandboxBackendSharesTheTierVocabulary(t *testing.T) {
	if got := IsolationForSandboxBackend(SandboxBackendFirecracker); got != IsolationTierMicroVM {
		t.Fatalf("firecracker isolation = %q, want %q", got, IsolationTierMicroVM)
	}
	for _, b := range []SandboxBackend{SandboxBackendAuto, SandboxBackendDocker, SandboxBackendPodman} {
		if got := IsolationForSandboxBackend(b); got != IsolationTierContainer {
			t.Fatalf("%q isolation = %q, want %q", b, got, IsolationTierContainer)
		}
	}
}

// The server decodes with DisallowUnknownFields, so an omitempty that fails to
// omit turns into a 400 on any server that predates the field. Also pins the
// JSON names, which are the actual contract.
func TestSandboxCreateRequestOmitsEmptyOptionalFields(t *testing.T) {
	data, err := json.Marshal(SandboxCreateRequest{Name: "task-1", Image: "postgrip/sandbox:1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := string(data)
	for _, key := range []string{
		"workspaceId", "repositoryName", "setupCommand", "credentialRefs",
		"labels", "expiresAt", "architecture", "backend",
	} {
		if strings.Contains(encoded, `"`+key+`"`) {
			t.Fatalf("unset %q was serialized: %s", key, encoded)
		}
	}
	if !strings.Contains(encoded, `"name":"task-1"`) || !strings.Contains(encoded, `"image":"postgrip/sandbox:1"`) {
		t.Fatalf("required fields missing: %s", encoded)
	}
}

// The list endpoint returns an envelope, not a bare array. Every client had to
// rediscover that; pin it.
func TestSandboxListResponseEnvelope(t *testing.T) {
	var out SandboxListResponse
	if err := json.Unmarshal([]byte(`{"sandboxes":[{"id":"sbx_1","name":"a"}]}`), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Sandboxes) != 1 || out.Sandboxes[0].ID != "sbx_1" {
		t.Fatalf("decoded %+v", out.Sandboxes)
	}
}
