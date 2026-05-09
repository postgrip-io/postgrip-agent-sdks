package sdk

import (
	"errors"
	"fmt"
)

// PostGripAgentError is the root error type for SDK-originated failures.
// Wrap errors that escape the SDK boundary so callers can reliably match
// them with errors.As without depending on internal types.
type PostGripAgentError struct {
	Message string
	Cause   error
}

func (e *PostGripAgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("postgrip-agent: %s: %v", e.Message, e.Cause)
	}
	return "postgrip-agent: " + e.Message
}

func (e *PostGripAgentError) Unwrap() error { return e.Cause }

// ApplicationFailure mirrors the TS/Python ApplicationFailure: a structured
// failure carrying a message, a stable type tag, an explicit retryability
// signal, and arbitrary details. The Go agent and the runtime service both
// understand the JSON wire shape (FailureInfo).
type ApplicationFailure struct {
	Message      string
	Type         string
	NonRetryable bool
	Details      []any
}

func (e *ApplicationFailure) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("%s: %s", e.Type, e.Message)
	}
	return e.Message
}

// NewApplicationFailure builds a retryable application failure.
func NewApplicationFailure(message, failureType string, details ...any) *ApplicationFailure {
	return &ApplicationFailure{Message: message, Type: failureType, Details: details}
}

// NewNonRetryableApplicationFailure builds an application failure the runtime
// service is expected to surface to callers without re-attempting.
func NewNonRetryableApplicationFailure(message, failureType string, details ...any) *ApplicationFailure {
	return &ApplicationFailure{Message: message, Type: failureType, NonRetryable: true, Details: details}
}

// CancelledFailure is raised inside workflow/activity code when the runtime
// service cancels the task. Callers should propagate cancellation rather than
// swallow it unless they have a specific compensation path.
type CancelledFailure struct {
	Message string
	Details []any
}

func (e *CancelledFailure) Error() string {
	if e.Message == "" {
		return "task cancelled"
	}
	return e.Message
}

// TimeoutFailure is raised when a watched task or polled operation exceeds
// the caller-supplied deadline.
type TimeoutFailure struct {
	Message string
}

func (e *TimeoutFailure) Error() string {
	if e.Message == "" {
		return "operation timed out"
	}
	return e.Message
}

// TaskFailedError is returned from result-waiting helpers when a task
// terminates in the failed state. It carries the task id along with the
// runtime-service error string so callers can distinguish task failures from
// transport / authentication issues.
type TaskFailedError struct {
	TaskID  string
	Reason  string
	Failure *ApplicationFailure
}

func (e *TaskFailedError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("task %s failed", e.TaskID)
	}
	return fmt.Sprintf("task %s failed: %s", e.TaskID, e.Reason)
}

func (e *TaskFailedError) Unwrap() error {
	if e.Failure == nil {
		return nil
	}
	return e.Failure
}

// IsCancelled reports whether err (or anything it wraps) is a CancelledFailure.
func IsCancelled(err error) bool {
	var c *CancelledFailure
	return errors.As(err, &c)
}

// IsTimeout reports whether err (or anything it wraps) is a TimeoutFailure.
func IsTimeout(err error) bool {
	var t *TimeoutFailure
	return errors.As(err, &t)
}

// IsApplicationFailure reports whether err (or anything it wraps) is a
// structured application failure. Use ApplicationFailure to pull the type
// tag, retryability, and details after.
func IsApplicationFailure(err error) bool {
	var a *ApplicationFailure
	return errors.As(err, &a)
}

// failureToError converts a wire-protocol FailureInfo into an
// ApplicationFailure / CancelledFailure / TimeoutFailure pointer. Callers
// should errors.As to extract the typed failure.
func failureToError(f *FailureInfo) error {
	if f == nil {
		return nil
	}
	switch f.Type {
	case "CancelledFailure":
		return &CancelledFailure{Message: f.Message, Details: f.Details}
	case "TimeoutFailure":
		return &TimeoutFailure{Message: f.Message}
	default:
		return failureInfoToApplicationFailure(f)
	}
}

func failureInfoToApplicationFailure(f *FailureInfo) *ApplicationFailure {
	if f == nil {
		return nil
	}
	return &ApplicationFailure{
		Message:      f.Message,
		Type:         f.Type,
		NonRetryable: f.NonRetryable,
		Details:      f.Details,
	}
}

func errorToFailure(err error) *FailureInfo {
	if err == nil {
		return nil
	}
	var app *ApplicationFailure
	if errors.As(err, &app) {
		return &FailureInfo{
			Message:      app.Message,
			Type:         app.Type,
			NonRetryable: app.NonRetryable,
			Details:      app.Details,
		}
	}
	var cancelled *CancelledFailure
	if errors.As(err, &cancelled) {
		return &FailureInfo{Message: cancelled.Message, Type: "CancelledFailure", Details: cancelled.Details}
	}
	var timeout *TimeoutFailure
	if errors.As(err, &timeout) {
		return &FailureInfo{Message: timeout.Message, Type: "TimeoutFailure"}
	}
	return &FailureInfo{Message: err.Error()}
}
