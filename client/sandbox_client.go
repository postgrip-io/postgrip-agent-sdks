package client

import (
	"context"
	"io"
	"time"

	"go.postgrip.io/sdk/failure"
)

// SandboxClient is the ergonomic surface over the sandbox endpoints: create a
// sandbox from an uploaded workspace, wait for it to come up, run work in it,
// and tear it down.
//
// Sandboxes authenticate on the management lane. Construct the Connection with
// ConnectionOptions.AuthToken set to a management token; an agent access token
// is rejected by every endpoint here.
type SandboxClient struct {
	conn *Connection
}

// SandboxWaitOptions tunes WaitUntilRunning.
type SandboxWaitOptions struct {
	// Timeout bounds the wait. Zero means DefaultSandboxWaitTimeout.
	Timeout time.Duration
	// PollInterval between reads. Zero means DefaultSandboxPollInterval.
	PollInterval time.Duration
}

const (
	DefaultSandboxWaitTimeout  = 2 * time.Minute
	DefaultSandboxPollInterval = time.Second
)

// Create provisions a sandbox. It returns once the record exists; the sandbox
// is not yet running. Follow with WaitUntilRunning.
func (s *SandboxClient) Create(ctx context.Context, req SandboxCreateRequest) (*Sandbox, error) {
	return s.conn.CreateSandbox(ctx, req)
}

// List returns the tenant's live sandboxes.
func (s *SandboxClient) List(ctx context.Context) ([]Sandbox, error) {
	return s.conn.ListSandboxes(ctx)
}

// Get fetches one sandbox by id.
func (s *SandboxClient) Get(ctx context.Context, sandboxID string) (*Sandbox, error) {
	return s.conn.GetSandbox(ctx, sandboxID)
}

// Start requests a stopped sandbox be started. Asynchronous — the returned
// record reflects the request, not the result.
func (s *SandboxClient) Start(ctx context.Context, sandboxID string) (*Sandbox, error) {
	return s.conn.StartSandbox(ctx, sandboxID)
}

// Stop requests a running sandbox be stopped. Asynchronous.
func (s *SandboxClient) Stop(ctx context.Context, sandboxID string) (*Sandbox, error) {
	return s.conn.StopSandbox(ctx, sandboxID)
}

// Delete requests deletion. Asynchronous once an agent has been assigned.
func (s *SandboxClient) Delete(ctx context.Context, sandboxID string) (*Sandbox, error) {
	return s.conn.DeleteSandbox(ctx, sandboxID)
}

// UploadWorkspace streams a gzipped tar archive and returns the workspace
// record. Put its ID in SandboxCreateRequest.WorkspaceID.
func (s *SandboxClient) UploadWorkspace(ctx context.Context, archive io.Reader, repositoryName, revision string) (*SandboxWorkspace, error) {
	return s.conn.UploadWorkspace(ctx, archive, repositoryName, revision)
}

// CreateSession mints a single-use relay ticket. The sandbox must already be
// running; while it is still coming up the server returns a retryable 400, so
// call WaitUntilRunning first.
func (s *SandboxClient) CreateSession(ctx context.Context, sandboxID string, req CreateSandboxSessionRequest) (*CreateSandboxSessionResponse, error) {
	return s.conn.CreateSandboxSession(ctx, sandboxID, req)
}

// WaitUntilRunning polls until the sandbox is ready, fails, or the wait
// expires.
//
// Readiness is observedState==running AND observedGeneration>=generation, not
// state alone: a "running" reading can predate a start or stop the assigned
// agent has not observed yet, so checking only the state can return a sandbox
// that is about to stop.
//
// A sandbox that reaches `failed` returns immediately with its failure
// message rather than burning the whole timeout.
func (s *SandboxClient) WaitUntilRunning(ctx context.Context, sandboxID string, opts SandboxWaitOptions) (*Sandbox, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSandboxWaitTimeout
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = DefaultSandboxPollInterval
	}
	// The deadline is tracked separately rather than by deriving the request
	// context from it. Sharing one context means a deadline that lands *during*
	// a poll surfaces as a bare "context deadline exceeded" from the HTTP call,
	// while a deadline landing *between* polls produces the useful message —
	// the caller's error then depends on timing alone.
	deadline := time.Now().Add(timeout)
	expired := func() bool { return !time.Now().Before(deadline) }

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// The wait deadline has to be selectable, not merely checked between polls.
	// Waiting on the ticker alone meant a PollInterval longer than Timeout slept
	// straight past the deadline: a wait configured for a few seconds with a
	// one-minute interval blocked for a minute. The request context bounds the
	// call, never the sleep that follows it.
	deadlineTimer := time.NewTimer(time.Until(deadline))
	defer deadlineTimer.Stop()
	var last *Sandbox
	for {
		reqCtx, cancelReq := context.WithDeadline(ctx, deadline)
		record, err := s.conn.GetSandbox(reqCtx, sandboxID)
		cancelReq()
		if err != nil {
			// Distinguish "the wait ran out mid-request" from a real
			// transport failure; only the former gets the state summary.
			if expired() && ctx.Err() == nil {
				return last, sandboxWaitTimeout(sandboxID, timeout, last, err)
			}
			return nil, err
		}
		last = record
		if record.Ready() {
			return record, nil
		}
		if record.ObservedState == SandboxObservedFailed {
			msg := record.FailureMessage
			if msg == "" {
				msg = string(record.FailureCode)
			}
			if msg == "" {
				msg = "sandbox failed"
			}
			return record, &failure.SDKError{Message: "sandbox " + sandboxID + " failed: " + msg}
		}
		if expired() {
			return last, sandboxWaitTimeout(sandboxID, timeout, last, nil)
		}
		select {
		case <-ctx.Done():
			return last, &failure.SDKError{
				Message: "waiting for sandbox " + sandboxID + " was cancelled",
				Cause:   ctx.Err(),
			}
		case <-deadlineTimer.C:
			return last, sandboxWaitTimeout(sandboxID, timeout, last, nil)
		case <-ticker.C:
		}
	}
}

// sandboxWaitTimeout names the state the sandbox was stuck in. "context
// deadline exceeded" on its own tells the caller nothing about whether the
// sandbox was still scheduling, mid-setup, or never picked up by an agent.
func sandboxWaitTimeout(sandboxID string, timeout time.Duration, last *Sandbox, cause error) error {
	state := "unknown"
	if last != nil {
		state = string(last.ObservedState)
		if !last.Ready() && last.ObservedGeneration < last.Generation {
			state += " (agent has not observed the latest request)"
		}
	}
	return &failure.SDKError{
		Message: "sandbox " + sandboxID + " was not running within " + timeout.String() +
			" (last observed state: " + state + ")",
		Cause: cause,
	}
}
