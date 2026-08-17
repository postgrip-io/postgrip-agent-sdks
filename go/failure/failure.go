// Package failure carries the structured failure types the runtime service
// and customer code exchange through workflow / activity / task results.
//
// Customer workflows and activities return an *ApplicationFailure (via
// New / NewNonRetryable) to signal a structured error that the runtime
// service understands; the SDK translates it to a wire-format FailureInfo
// on the way out and back to the appropriate failure type on the way in.
// IsCancelled / IsTimeout / IsApplication discriminate without depending
// on internal types.
package failure

import (
	"errors"
	"fmt"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
)

// Info is the wire-format failure record. Re-exported so customer code can
// stay within the failure package when reading task results.
type Info = protocol.FailureInfo

// SDKError is the root error type for SDK-originated failures. Wrap errors
// that escape the SDK boundary so callers can reliably match them with
// errors.As without depending on internal types.
type SDKError struct {
	Message string
	Cause   error
}

func (e *SDKError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("postgrip-agent: %s: %v", e.Message, e.Cause)
	}
	return "postgrip-agent: " + e.Message
}

func (e *SDKError) Unwrap() error { return e.Cause }

// Application mirrors the TS/Python ApplicationFailure: a structured failure
// carrying a message, a stable type tag, an explicit retryability signal,
// and arbitrary details. The Go agent and the runtime service both
// understand the JSON wire shape (Info).
type Application struct {
	Message      string
	Type         string
	NonRetryable bool
	Details      []any
}

func (e *Application) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("%s: %s", e.Type, e.Message)
	}
	return e.Message
}

// NewApplication builds a retryable application failure.
func NewApplication(message, failureType string, details ...any) *Application {
	return &Application{Message: message, Type: failureType, Details: details}
}

// NewNonRetryable builds an application failure the runtime service is
// expected to surface to callers without re-attempting.
func NewNonRetryable(message, failureType string, details ...any) *Application {
	return &Application{Message: message, Type: failureType, NonRetryable: true, Details: details}
}

// Cancelled is raised inside workflow/activity code when the runtime
// service cancels the task. Callers should propagate cancellation rather
// than swallow it unless they have a specific compensation path.
type Cancelled struct {
	Message string
	Details []any
}

func (e *Cancelled) Error() string {
	if e.Message == "" {
		return "task cancelled"
	}
	return e.Message
}

// Timeout is raised when a watched task or polled operation exceeds the
// caller-supplied deadline.
type Timeout struct {
	Message string
}

func (e *Timeout) Error() string {
	if e.Message == "" {
		return "operation timed out"
	}
	return e.Message
}

// TaskFailed is returned from result-waiting helpers when a task terminates
// in the failed state. It carries the task id along with the runtime-service
// error string so callers can distinguish task failures from transport /
// authentication issues.
type TaskFailed struct {
	TaskID  string
	Reason  string
	Failure *Application
}

func (e *TaskFailed) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("task %s failed", e.TaskID)
	}
	return fmt.Sprintf("task %s failed: %s", e.TaskID, e.Reason)
}

func (e *TaskFailed) Unwrap() error {
	if e.Failure == nil {
		return nil
	}
	return e.Failure
}

// IsCancelled reports whether err (or anything it wraps) is a Cancelled.
func IsCancelled(err error) bool {
	var c *Cancelled
	return errors.As(err, &c)
}

// IsTimeout reports whether err (or anything it wraps) is a Timeout.
func IsTimeout(err error) bool {
	var t *Timeout
	return errors.As(err, &t)
}

// IsApplication reports whether err (or anything it wraps) is a structured
// application failure. Use errors.As with *Application to pull the type tag,
// retryability, and details after.
func IsApplication(err error) bool {
	var a *Application
	return errors.As(err, &a)
}

// FromInfo converts a wire-protocol Info into an Application / Cancelled /
// Timeout pointer. Callers should errors.As to extract the typed failure.
func FromInfo(f *Info) error {
	if f == nil {
		return nil
	}
	switch f.Type {
	case "CancelledFailure":
		return &Cancelled{Message: f.Message, Details: f.Details}
	case "TimeoutFailure":
		return &Timeout{Message: f.Message}
	default:
		return InfoToApplication(f)
	}
}

// InfoToApplication is FromInfo without the Cancelled / Timeout discrimination
// — it always returns an *Application. Used by TaskFailed.Failure to retain
// the structured failure even when the wire shape claimed a different type.
func InfoToApplication(f *Info) *Application {
	if f == nil {
		return nil
	}
	return &Application{
		Message:      f.Message,
		Type:         f.Type,
		NonRetryable: f.NonRetryable,
		Details:      f.Details,
	}
}

// ToInfo converts a Go error to a wire-format Info. Recognized SDK failure
// types map cleanly; unknown errors fall back to a plain Message-only Info.
func ToInfo(err error) *Info {
	if err == nil {
		return nil
	}
	var app *Application
	if errors.As(err, &app) {
		return &Info{
			Message:      app.Message,
			Type:         app.Type,
			NonRetryable: app.NonRetryable,
			Details:      app.Details,
		}
	}
	var cancelled *Cancelled
	if errors.As(err, &cancelled) {
		return &Info{Message: cancelled.Message, Type: "CancelledFailure", Details: cancelled.Details}
	}
	var timeout *Timeout
	if errors.As(err, &timeout) {
		return &Info{Message: timeout.Message, Type: "TimeoutFailure"}
	}
	return &Info{Message: err.Error()}
}
