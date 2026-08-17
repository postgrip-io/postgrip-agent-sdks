package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sandboxTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	return New(conn), server
}

// The list endpoint returns {"sandboxes":[...]}, not a bare array. Decoding it
// as an array yields an empty list with no error, which reads as "no
// sandboxes" rather than as a bug.
func TestListSandboxesUnwrapsTheEnvelope(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sandboxes" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"sandboxes":[{"id":"sbx_1","name":"a"},{"id":"sbx_2","name":"b"}]}`))
	})
	got, err := c.Sandbox.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].ID != "sbx_1" || got[1].ID != "sbx_2" {
		t.Fatalf("sandboxes = %+v", got)
	}
}

// Sandbox endpoints are management-lane. Sending the agent token would 401.
func TestSandboxRequestsUseTheManagementToken(t *testing.T) {
	t.Parallel()
	var auth string
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"sbx_1"}`))
	})
	// An agent session must not hijack the sandbox lane.
	c.Connection.SeedAgentSession("agent-1", "agent-token", time.Now().Add(time.Hour))
	if _, err := c.Sandbox.Get(context.Background(), "sbx_1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if auth != "Bearer mgmt" {
		t.Fatalf("Authorization = %q, want the management token", auth)
	}
}

func TestSandboxLifecyclePaths(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"id":"sbx_1"}`))
	})
	ctx := context.Background()
	cases := []struct {
		name       string
		call       func() (*Sandbox, error)
		wantMethod string
		wantPath   string
	}{
		{"start", func() (*Sandbox, error) { return c.Sandbox.Start(ctx, "sbx_1") }, http.MethodPost, "/api/v1/sandboxes/sbx_1/start"},
		{"stop", func() (*Sandbox, error) { return c.Sandbox.Stop(ctx, "sbx_1") }, http.MethodPost, "/api/v1/sandboxes/sbx_1/stop"},
		{"delete", func() (*Sandbox, error) { return c.Sandbox.Delete(ctx, "sbx_1") }, http.MethodDelete, "/api/v1/sandboxes/sbx_1"},
		{"get", func() (*Sandbox, error) { return c.Sandbox.Get(ctx, "sbx_1") }, http.MethodGet, "/api/v1/sandboxes/sbx_1"},
	}
	for _, tc := range cases {
		if _, err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if gotMethod != tc.wantMethod || gotPath != tc.wantPath {
			t.Fatalf("%s issued %s %s, want %s %s", tc.name, gotMethod, gotPath, tc.wantMethod, tc.wantPath)
		}
	}
}

// Readiness is state AND generation. A sandbox reported running at a
// generation the agent has not observed is about to change again — treating it
// as ready hands back a sandbox that is, for example, mid-stop.
func TestWaitUntilRunningRequiresTheObservedGeneration(t *testing.T) {
	t.Parallel()
	var calls int
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Running, but the agent is a generation behind.
			_, _ = w.Write([]byte(`{"id":"sbx_1","observedState":"running","generation":2,"observedGeneration":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"sbx_1","observedState":"running","generation":2,"observedGeneration":2}`))
	})
	got, err := c.Sandbox.WaitUntilRunning(context.Background(), "sbx_1", SandboxWaitOptions{
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitUntilRunning: %v", err)
	}
	if !got.Ready() {
		t.Fatalf("returned a sandbox that is not ready: %+v", got)
	}
	if calls < 2 {
		t.Fatalf("returned after %d call(s); the stale generation should not have satisfied the wait", calls)
	}
}

// A failed sandbox must surface its reason immediately rather than burning the
// caller's whole timeout.
func TestWaitUntilRunningFailsFastOnFailure(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sbx_1","observedState":"failed","failureCode":"setup_failed","failureMessage":"setup.sh exited 1"}`))
	})
	start := time.Now()
	_, err := c.Sandbox.WaitUntilRunning(context.Background(), "sbx_1", SandboxWaitOptions{
		Timeout:      30 * time.Second,
		PollInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected an error for a failed sandbox")
	}
	if !strings.Contains(err.Error(), "setup.sh exited 1") {
		t.Fatalf("error lost the failure message: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s; should not have waited out the timeout", elapsed)
	}
}

func TestWaitUntilRunningReportsTheLastObservedState(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sbx_1","observedState":"provisioning","generation":1,"observedGeneration":1}`))
	})
	_, err := c.Sandbox.WaitUntilRunning(context.Background(), "sbx_1", SandboxWaitOptions{
		Timeout:      50 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	// Naming the state it got stuck in is the difference between a useful
	// timeout and "context deadline exceeded".
	if !strings.Contains(err.Error(), "provisioning") {
		t.Fatalf("timeout error did not name the last state: %v", err)
	}
}

// The upload body is the raw archive with metadata in headers — not multipart,
// and not JSON-encoded.
func TestUploadWorkspaceStreamsRawBytesWithMetadataHeaders(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	var gotRepo, gotRev, gotType string
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotRepo = r.Header.Get("X-PostGrip-Repository")
		gotRev = r.Header.Get("X-PostGrip-Revision")
		gotType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"wsp_1","sha256":"abc","sizeBytes":7}`))
	})
	archive := strings.NewReader("GZIPPED")
	out, err := c.Sandbox.UploadWorkspace(context.Background(), archive, "my-repo", "deadbeef")
	if err != nil {
		t.Fatalf("UploadWorkspace: %v", err)
	}
	if string(gotBody) != "GZIPPED" {
		t.Fatalf("body = %q, want the raw archive", gotBody)
	}
	if gotRepo != "my-repo" || gotRev != "deadbeef" {
		t.Fatalf("metadata headers = %q / %q", gotRepo, gotRev)
	}
	if gotType != "application/gzip" {
		t.Fatalf("Content-Type = %q", gotType)
	}
	if out.ID != "wsp_1" {
		t.Fatalf("workspace = %+v", out)
	}
}

// Omitted metadata must not be sent as empty headers.
func TestUploadWorkspaceOmitsBlankMetadata(t *testing.T) {
	t.Parallel()
	var hasRepo, hasRev bool
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, hasRepo = r.Header["X-Postgrip-Repository"]
		_, hasRev = r.Header["X-Postgrip-Revision"]
		_, _ = w.Write([]byte(`{"id":"wsp_1"}`))
	})
	if _, err := c.Sandbox.UploadWorkspace(context.Background(), strings.NewReader("x"), "", ""); err != nil {
		t.Fatalf("UploadWorkspace: %v", err)
	}
	if hasRepo || hasRev {
		t.Fatalf("blank metadata was sent as headers (repo=%v rev=%v)", hasRepo, hasRev)
	}
}

func TestCreateSandboxSessionPostsToTheSandbox(t *testing.T) {
	t.Parallel()
	var gotPath string
	var gotReq CreateSandboxSessionRequest
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ses_1","ticket":"pgss_abc"}`))
	})
	out, err := c.Sandbox.CreateSession(context.Background(), "sbx_1", CreateSandboxSessionRequest{
		Kind:    SandboxSessionKindExec,
		Command: []string{"go", "test", "./..."},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if gotPath != "/api/v1/sandboxes/sbx_1/sessions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotReq.Kind != SandboxSessionKindExec || len(gotReq.Command) != 3 {
		t.Fatalf("request = %+v", gotReq)
	}
	if out.Ticket != "pgss_abc" {
		t.Fatalf("response = %+v", out)
	}
}

// A blank id would otherwise build /api/v1/sandboxes/ and hit the collection
// route, which is a confusing 404 or worse a list.
func TestSandboxCallsRejectABlankID(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request should have been issued, got %s %s", r.Method, r.URL.Path)
	})
	ctx := context.Background()
	if _, err := c.Sandbox.Get(ctx, ""); err == nil {
		t.Fatal("Get accepted a blank sandbox id")
	}
	if _, err := c.Sandbox.Delete(ctx, ""); err == nil {
		t.Fatal("Delete accepted a blank sandbox id")
	}
	if _, err := c.Sandbox.CreateSession(ctx, "", CreateSandboxSessionRequest{}); err == nil {
		t.Fatal("CreateSession accepted a blank sandbox id")
	}
}

// A PollInterval longer than Timeout must still return at the deadline. The
// loop checked the deadline between polls but then slept on the ticker alone,
// so the wait ran for the interval instead — minutes, for a wait configured in
// seconds.
func TestWaitUntilRunningHonoursTheDeadlineBetweenPolls(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sbx_1","observedState":"provisioning","generation":1,"observedGeneration":1}`))
	})
	start := time.Now()
	_, err := c.Sandbox.WaitUntilRunning(context.Background(), "sbx_1", SandboxWaitOptions{
		Timeout:      150 * time.Millisecond,
		PollInterval: time.Minute,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("wait returned success despite never reaching running")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait slept on the poll interval (%s) instead of the deadline", elapsed)
	}
}

// The archive upload must not inherit the connection's whole-request timeout:
// it defaults to 30s and the server accepts archives up to 512 MiB, so the
// documented maximum would be unusable at the default configuration.
func TestUploadWorkspaceIsNotBoundedByTheRequestTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Longer than the connection's request timeout below. A client-level
		// Timeout would abort here; ctx is what should bound this call.
		time.Sleep(300 * time.Millisecond)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"wsp_1"}`))
	}))
	defer server.Close()

	conn, err := NewConnection(ConnectionOptions{
		Address:        server.URL,
		AuthToken:      "mgmt",
		RequestTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	if _, err := conn.UploadWorkspace(context.Background(), strings.NewReader("archive bytes"), "repo", "rev"); err != nil {
		t.Fatalf("UploadWorkspace was cut off by the request timeout: %v", err)
	}
}
