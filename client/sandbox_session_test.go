package client

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// A relay that vanishes without sending an exit close status must not read as
// a successful run. This is the difference between "the command failed" and
// "the command never reported", and a caller gating on the exit code would act
// on exit 0 as though the work had happened.
func TestExecRejectsAStreamThatEndsWithoutAnExitStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		close func(conn *websocket.Conn)
	}{
		// A normal WebSocket close, no exit status: the previous code read the
		// resulting io.EOF as exit 0.
		{"clean close", func(conn *websocket.Conn) { _ = conn.Close(websocket.StatusNormalClosure, "bye") }},
		// An abrupt drop, which is what a killed relay or recycled proxy does.
		{"abrupt drop", func(conn *websocket.Conn) { _ = conn.CloseNow() }},
		// In range for a close status, but not the exec exit-code range.
		{"unrelated status", func(conn *websocket.Conn) { _ = conn.Close(websocket.StatusInternalError, "boom") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				_ = conn.Write(r.Context(), websocket.MessageBinary, []byte("partial output"))
				tc.close(conn)
			}))
			defer server.Close()

			conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
			if err != nil {
				t.Fatalf("NewConnection: %v", err)
			}
			var stdout bytes.Buffer
			code, err := New(conn).Sandbox.Exec(context.Background(), "sbx_1", []string{"true"}, nil, &stdout)
			if err == nil {
				t.Fatalf("Exec returned (%d, nil) for a session with no exit status", code)
			}
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 alongside the error", code)
			}
		})
	}
}

// Same rule one level down: ExitCode on a raw stream.
func TestExitCodeRejectsEOFWithoutAnExitStatus(t *testing.T) {
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
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}))
	defer server.Close()

	conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	ctx := context.Background()
	stream, err := New(conn).Sandbox.OpenSandboxSession(ctx, "sbx_1", protocol.SandboxSessionKindExec, SandboxSessionOptions{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("OpenSandboxSession: %v", err)
	}
	defer stream.Close()
	if code, err := stream.ExitCode(ctx); err == nil {
		t.Fatalf("ExitCode returned (%d, nil) for a close with no exit status", code)
	}
}

// A stdin reader that fails partway leaves the sandbox with truncated input.
// The process can still exit 0 on what it did receive, so the exit code alone
// would report success for a command that never got its data.
func TestExecReportsAFailedStdinCopy(t *testing.T) {
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
		// Let the stdin copy fail first, then exit 0 as though all was well.
		time.Sleep(100 * time.Millisecond)
		_ = conn.Close(websocket.StatusCode(protocol.SandboxExecCloseStatusBase), "exit:0")
	}))
	defer server.Close()

	conn, err := NewConnection(ConnectionOptions{Address: server.URL, AuthToken: "mgmt"})
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	var stdout bytes.Buffer
	stdin := io.MultiReader(strings.NewReader("some bytes"), &failingReader{})
	code, err := New(conn).Sandbox.Exec(context.Background(), "sbx_1", []string{"cat"}, stdin, &stdout)
	if err == nil {
		t.Fatalf("Exec returned (%d, nil) despite stdin failing mid-copy", code)
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("error does not name stdin as the cause: %v", err)
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errors.New("reader disconnected") }

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
