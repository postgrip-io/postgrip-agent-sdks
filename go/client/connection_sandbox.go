package client

import (
	"context"
	"io"

	"github.com/postgrip-io/postgrip-agent-sdks/go/failure"
)

// Sandbox endpoints authenticate on the *management* lane, not the agent
// lane: an agent access token is rejected on all of them with 401. Supply a
// management token via ConnectionOptions.AuthToken.

// CreateSandbox provisions a sandbox. It returns as soon as the record
// exists — the sandbox is not yet running. Poll with WaitForSandbox.
//
// Name must match ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$ and is unique per tenant
// among live sandboxes, so a duplicate name is a 409. Image is required.
func (c *Connection) CreateSandbox(ctx context.Context, req SandboxCreateRequest) (*Sandbox, error) {
	out, err := c.OpenAPI().CreateSandbox(ctx, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSandboxes returns the tenant's live sandboxes. Deleted ones are
// filtered out here but stay fetchable by id through GetSandbox.
func (c *Connection) ListSandboxes(ctx context.Context) ([]Sandbox, error) {
	out, err := c.OpenAPI().ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	return out.Sandboxes, nil
}

// GetSandbox fetches one sandbox by id, including sandboxes that have been
// deleted.
func (c *Connection) GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if err := sandboxIDRequired(sandboxID); err != nil {
		return nil, err
	}
	out, err := c.OpenAPI().GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// StartSandbox requests a stopped sandbox be started. The response is
// accepted, not applied: the assigned agent acts on it asynchronously, so the
// returned record still carries the previous observed state.
func (c *Connection) StartSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if err := sandboxIDRequired(sandboxID); err != nil {
		return nil, err
	}
	out, err := c.OpenAPI().StartSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// StopSandbox requests a running sandbox be stopped. Asynchronous, as
// StartSandbox.
func (c *Connection) StopSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if err := sandboxIDRequired(sandboxID); err != nil {
		return nil, err
	}
	out, err := c.OpenAPI().StopSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSandbox requests deletion. If no agent has been assigned yet this
// completes immediately; otherwise it finishes only once the assigned agent
// reports the sandbox deleted.
func (c *Connection) DeleteSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	if err := sandboxIDRequired(sandboxID); err != nil {
		return nil, err
	}
	out, err := c.OpenAPI().DeleteSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSandboxSession mints a single-use relay ticket for an interactive or
// exec session.
//
// The sandbox must be observed running and assigned to an agent; otherwise the
// server returns 400 "sandbox is not running", which is retryable while the
// sandbox comes up. The ticket is short-lived — dial promptly rather than
// holding it.
func (c *Connection) CreateSandboxSession(ctx context.Context, sandboxID string, req CreateSandboxSessionRequest) (*CreateSandboxSessionResponse, error) {
	if err := sandboxIDRequired(sandboxID); err != nil {
		return nil, err
	}
	out, err := c.OpenAPI().CreateSandboxSession(ctx, sandboxID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadWorkspace streams a gzipped tar archive to be materialized inside a
// sandbox, and returns the workspace record whose ID goes in
// SandboxCreateRequest.WorkspaceID.
//
// The body is the raw archive — not multipart. repositoryName and revision are
// optional metadata; both are sent as headers.
//
// Uploading identical bytes twice returns the *pre-existing* record rather
// than creating a second one, so do not assume the returned ID is new.
func (c *Connection) UploadWorkspace(ctx context.Context, archive io.Reader, repositoryName, revision string) (*SandboxWorkspace, error) {
	headers := map[string]string{"Content-Type": "application/gzip"}
	if repositoryName != "" {
		headers[OpenAPIUploadWorkspaceHeaderXPostGripRepository] = repositoryName
	}
	if revision != "" {
		headers[OpenAPIUploadWorkspaceHeaderXPostGripRevision] = revision
	}
	var out SandboxWorkspace
	if err := c.doStreamOpenAPI(ctx, openAPIUploadWorkspace, archive, headers, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func sandboxIDRequired(sandboxID string) error {
	if sandboxID == "" {
		return &failure.SDKError{Message: "sandbox id is required"}
	}
	return nil
}
