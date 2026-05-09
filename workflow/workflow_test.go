package workflow

import (
	"errors"
	"strings"
	"testing"
)

// fakeContext is just enough Context to let us hand a workflow.Context into
// ContinueAsNew sentinel constructors and IsContinueAsNew assertions
// without dragging in /worker.
type fakeContext struct{}

func TestContinueAsNewSentinelDetected(t *testing.T) {
	t.Parallel()
	err := &ContinueAsNewSentinel{Options: ContinueAsNewOptions{
		WorkflowType: "Greet",
		Args:         []any{"world"},
	}}
	if !IsContinueAsNew(err) {
		t.Fatalf("expected continue-as-new sentinel, got %T", err)
	}
	if !strings.Contains(err.Error(), "Greet") {
		t.Fatalf("err = %q, want workflow type", err.Error())
	}
}

func TestSuspendedSentinelMatchesAndUnwraps(t *testing.T) {
	t.Parallel()
	original := NewSuspended("activity")
	wrapped := errors.New("workflow returned: " + original.Error())
	if IsSuspended(wrapped) {
		t.Fatal("non-wrapped sentinel should not match")
	}
	if !IsSuspended(original) {
		t.Fatal("original sentinel should match")
	}
}

// Ensure fakeContext compiles — referencing Context here would force users
// of this package to satisfy a Context interface, but we don't actually
// need to: workflow tests live in /worker once they touch the impl.
var _ = fakeContext{}
