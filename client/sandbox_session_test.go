package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/postgrip-io/agent-sdk-protocol"
)

func TestSandboxRelayURL(t *testing.T) {
	t.Parallel()
	cases := []struct{ address, want string }{
		{"https://agents.example.com", "wss://agents.example.com/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t"},
		{"http://127.0.0.1:4100", "ws://127.0.0.1:4100/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t"},
		{"https://agents.example.com/", "wss://agents.example.com/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t"},
		{"wss://agents.example.com", "wss://agents.example.com/api/v1/sandbox-sessions/ses_1/connect?ticket=pgss_t"},
	}
	for _, tc := range cases {
		got, err := sandboxRelayURL(tc.address, "ses_1", "pgss_t")
		if err != nil {
			t.Fatalf("sandboxRelayURL(%q): %v", tc.address, err)
		}
		if got != tc.want {
			t.Fatalf("sandboxRelayURL(%q) = %q, want %q", tc.address, got, tc.want)
		}
	}
	if _, err := sandboxRelayURL("ftp://nope", "ses_1", "t"); err == nil {
		t.Fatal("accepted a non-http(s) relay address")
	}
}

// The ticket must be escaped into the query, not concatenated raw.
func TestSandboxRelayURLEscapesTheTicket(t *testing.T) {
	t.Parallel()
	got, err := sandboxRelayURL("https://x.example", "ses/1", "a b&c=d")
	if err != nil {
		t.Fatalf("sandboxRelayURL: %v", err)
	}
	if strings.Contains(got, "a b&c=d") {
		t.Fatalf("ticket was not escaped: %s", got)
	}
	if !strings.Contains(got, "ses%2F1") {
		t.Fatalf("session id was not escaped: %s", got)
	}
}

// A pty session with no command is legal; an exec session without one is not,
// and the server would reject it after a round trip.
func TestOpenSandboxSessionValidatesKindAndCommand(t *testing.T) {
	t.Parallel()
	c, _ := sandboxTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no request should have been issued, got %s %s", r.Method, r.URL.Path)
	})
	ctx := context.Background()
	if _, err := c.Sandbox.OpenSandboxSession(ctx, "sbx_1", "port", SandboxSessionOptions{}); err == nil {
		t.Fatal("accepted an unsupported session kind")
	}
	if _, err := c.Sandbox.OpenSandboxSession(ctx, "sbx_1", protocol.SandboxSessionKindExec, SandboxSessionOptions{}); err == nil {
		t.Fatal("accepted an exec session with no command")
	}
}

// End-to-end over a real WebSocket: the session is created over HTTP, the
// relay is dialled, bytes flow, and the exit code arrives as a close status.
func TestExecStreamsAndReportsTheExitCode(t *testing.T) {
	t.Parallel()
	var gotStdin bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sessions") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"ses_1","ticket":"pgss_t"}`))
			return
		}
		if !strings.Contains(r.URL.Path, "/sandbox-sessions/") {
			t.Errorf("unexpected path %s", r.URL.Path)
			return
		}
		if r.URL.Query().Get("ticket") != "pgss_t" {
			t.Errorf("relay dialled without the ticket: %s", r.URL.RawQuery)
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		ctx := r.Context()
		_ = conn.Write(ctx, websocket.MessageBinary, []byte("hello from sandbox"))
		// Drain whatever stdin arrives, then close with exit code 3.
		go func() {
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				gotStdin.Write(data)
			}
		}()
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close(websocket.StatusCode(protocol.SandboxExecCloseStatusBase+3), "exit:3")
	}))
	defer server.Close()

	conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	c := New(conn)

	var stdout bytes.Buffer
	code, err := c.Sandbox.Exec(context.Background(), "sbx_1", []string{"false"}, strings.NewReader("stdin bytes"), &stdout)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (close status %d)", code, protocol.SandboxExecCloseStatusBase+3)
	}
	if !strings.Contains(stdout.String(), "hello from sandbox") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// A write past the relay's frame bound is refused locally. The relay would
// otherwise close the session, which surfaces as an unexplained disconnect.
func TestSandboxStreamRejectsOversizedWrites(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sessions") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"ses_1","ticket":"pgss_t"}`))
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-r.Context().Done()
	}))
	defer server.Close()

	conn, _ := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
	c := New(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := c.Sandbox.OpenSandboxSession(ctx, "sbx_1", protocol.SandboxSessionKindPTY, SandboxSessionOptions{})
	if err != nil {
		t.Fatalf("OpenSandboxSession: %v", err)
	}
	defer stream.Close()

	oversized := make([]byte, protocol.SandboxRelayMaxFrameBytes+1)
	if _, err := stream.Write(oversized); err == nil {
		t.Fatal("oversized write was accepted; the relay would have closed the session")
	}
	if _, err := stream.Write([]byte("small")); err != nil {
		t.Fatalf("a normal write failed: %v", err)
	}
}
