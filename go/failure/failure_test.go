package failure

import (
	"errors"
	"testing"
)

func TestToInfoRoundTrip(t *testing.T) {
	t.Parallel()
	app := NewNonRetryable("bad input", "ValidationError", "field=name")
	f := ToInfo(app)
	if f == nil || f.Type != "ValidationError" || !f.NonRetryable || f.Message != "bad input" {
		t.Fatalf("Info = %+v, mismatch", f)
	}
	if len(f.Details) != 1 || f.Details[0] != "field=name" {
		t.Fatalf("details = %#v, mismatch", f.Details)
	}

	cancelled := &Cancelled{Message: "stopped"}
	if f := ToInfo(cancelled); f.Type != "CancelledFailure" {
		t.Fatalf("cancelled.Type = %q, want CancelledFailure", f.Type)
	}

	timeout := &Timeout{Message: "took too long"}
	if f := ToInfo(timeout); f.Type != "TimeoutFailure" {
		t.Fatalf("timeout.Type = %q, want TimeoutFailure", f.Type)
	}

	plain := ToInfo(errors.New("boom"))
	if plain.Message != "boom" || plain.Type != "" {
		t.Fatalf("plain failure = %+v, mismatch", plain)
	}
}

func TestFromInfoRoundTrip(t *testing.T) {
	t.Parallel()
	err := FromInfo(&Info{Type: "ValidationError", Message: "bad", NonRetryable: true})
	if !IsApplication(err) {
		t.Fatalf("expected Application, got %T", err)
	}
	var app *Application
	if !errors.As(err, &app) || !app.NonRetryable || app.Message != "bad" {
		t.Fatalf("Application mismatch: %+v", app)
	}

	err = FromInfo(&Info{Type: "CancelledFailure", Message: "stopped"})
	if !IsCancelled(err) {
		t.Fatalf("expected Cancelled, got %T", err)
	}

	err = FromInfo(&Info{Type: "TimeoutFailure"})
	if !IsTimeout(err) {
		t.Fatalf("expected Timeout, got %T", err)
	}
}
