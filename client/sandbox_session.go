package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/postgrip-io/agent-sdk-protocol"
	"go.postgrip.io/sdk/failure"
)

// SandboxSessionOptions configures an interactive or exec session.
type SandboxSessionOptions struct {
	// Command runs instead of a login shell. Required for Kind exec.
	Command []string
	// Rows and Columns size the PTY. Both default to 24x80 and are fixed for
	// the life of the session — the relay has no resize channel, so a caller
	// that resizes its terminal mid-session cannot inform the sandbox.
	Rows    int
	Columns int
	// RelayAddress overrides where the WebSocket dials. Defaults to the
	// Connection address. Set it when the API is reached through a proxy that
	// does not forward WebSocket upgrades — the relay is deliberately not
	// proxied by postgrip-server, so browser and proxied callers must dial the
	// Agent Orchestrator directly.
	RelayAddress string
}

// SandboxStream is a live sandbox session: a bidirectional byte stream plus
// the process exit code once it closes.
//
// The stream carries raw stdio with no framing. For a pty session it is
// terminal traffic; for exec it is the process's stdout and stderr
// *interleaved on the same stream* — the relay multiplexes both into one
// channel, so they cannot be separated client-side.
type SandboxStream struct {
	conn *websocket.Conn
	rw   io.ReadWriteCloser
}

// Read implements io.Reader over the session's output.
func (s *SandboxStream) Read(p []byte) (int, error) { return s.rw.Read(p) }

// Write implements io.Writer over the session's input.
//
// A single write must not exceed protocol.SandboxRelayMaxFrameBytes; the relay
// closes the session rather than forwarding a larger frame. io.Copy's default
// 32 KiB buffer is comfortably under that.
func (s *SandboxStream) Write(p []byte) (int, error) {
	if len(p) > protocol.SandboxRelayMaxFrameBytes {
		return 0, &failure.SDKError{
			Message: "sandbox session write exceeds the relay frame limit; chunk writes at or below protocol.SandboxRelayMaxFrameBytes",
		}
	}
	return s.rw.Write(p)
}

// Close tears the session down immediately.
//
// Deliberately not a graceful close handshake: the peer that ends a session
// normally is the *sandbox* (it closes with the exit status), so a client
// closing is either abandoning the session or cleaning up after the exit code
// already arrived. Waiting on a handshake there just stalls teardown for the
// handshake timeout — five seconds per session in practice.
func (s *SandboxStream) Close() error { return s.conn.CloseNow() }

// ExitCode blocks until the session ends and reports the process exit code.
//
// The code arrives as the WebSocket close status (4000+code), not in the byte
// stream. Only that close status produces a successful return. Everything else
// is a transport failure and comes back as an error — including a plain
// io.EOF, which is exactly what a dropped relay, a recycled proxy, or a killed
// socket surfaces as. Reading EOF as exit 0, as this did, turns an interrupted
// command into a clean success, which is the worst possible direction for the
// mistake to go: a caller gating on the exit code proceeds as though the
// command ran.
func (s *SandboxStream) ExitCode(ctx context.Context) (int, error) {
	buf := make([]byte, 32<<10)
	for {
		_, err := s.rw.Read(buf)
		if err == nil {
			continue
		}
		if code, ok := protocol.SandboxExecExitCode(int(websocket.CloseStatus(err))); ok {
			return code, nil
		}
		return 0, &failure.SDKError{
			Message: "sandbox session ended without an exit status",
			Cause:   err,
		}
	}
}

// OpenSandboxSession creates a session and dials the relay, returning the live
// stream.
//
// The sandbox must already be running; while it is still coming up the server
// rejects session creation with a retryable 400, so call
// SandboxClient.WaitUntilRunning first. The relay ticket is single-use and
// short-lived, which is why creation and dialling are one call.
func (s *SandboxClient) OpenSandboxSession(ctx context.Context, sandboxID, kind string, opts SandboxSessionOptions) (*SandboxStream, error) {
	if kind != protocol.SandboxSessionKindPTY && kind != protocol.SandboxSessionKindExec {
		return nil, &failure.SDKError{Message: "sandbox session kind must be " + protocol.SandboxSessionKindPTY + " or " + protocol.SandboxSessionKindExec}
	}
	if kind == protocol.SandboxSessionKindExec && len(opts.Command) == 0 {
		return nil, &failure.SDKError{Message: "sandbox exec requires a command"}
	}
	session, err := s.conn.CreateSandboxSession(ctx, sandboxID, CreateSandboxSessionRequest{
		Kind:    kind,
		Command: opts.Command,
		Rows:    opts.Rows,
		Columns: opts.Columns,
	})
	if err != nil {
		return nil, err
	}

	address := opts.RelayAddress
	if address == "" {
		address = s.conn.Address()
	}
	relay, err := sandboxRelayURL(address, session.ID, session.Ticket)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if auth := s.conn.AuthHeader(); auth != "" {
		header.Set("Authorization", auth)
	}
	// The ticket authorizes the session; the management credential still
	// authenticates the request. Both are required.
	conn, _, err := websocket.Dial(ctx, relay, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, &failure.SDKError{Message: "dial sandbox session relay", Cause: err}
	}
	// Frames are opaque binary. NetConn lifts the peer read limit; the relay
	// keeps its own bound, which Write enforces above.
	return &SandboxStream{conn: conn, rw: websocket.NetConn(ctx, conn, websocket.MessageBinary)}, nil
}

// Exec runs a command in the sandbox, streaming stdin in and stdout out, and
// returns the process exit code.
//
// stdout receives stdout and stderr interleaved — the relay carries one
// stream. Pass a nil stdin for commands that read nothing.
//
// # Commands that read stdin to EOF will hang
//
// There is no end-of-input signal on the wire. The agent hands the relay
// connection to the process as its stdin directly, so that stdin reaches EOF
// only when the whole session closes — which is also what carries the exit
// status back. Draining the caller's Reader therefore tells the sandbox
// nothing, and a command that reads until EOF (`cat`, `sort`, an archive
// import, `go test` consuming piped input) keeps waiting while Exec keeps
// waiting for its output, until ctx cancels.
//
// Pass a ctx deadline for such commands. Commands that read a bounded amount
// and commands that read nothing are unaffected. Fixing this properly needs a
// half-close on the wire, which is a protocol change, not an SDK one.
func (s *SandboxClient) Exec(ctx context.Context, sandboxID string, command []string, stdin io.Reader, stdout io.Writer) (int, error) {
	stream, err := s.OpenSandboxSession(ctx, sandboxID, protocol.SandboxSessionKindExec, SandboxSessionOptions{Command: command})
	if err != nil {
		return 0, err
	}
	defer stream.Close()

	// Buffered so the copy never blocks on a receive that no longer happens:
	// when the session ends first, nothing reads this channel.
	stdinDone := make(chan error, 1)
	if stdin != nil {
		go func() {
			_, err := io.Copy(stream, stdin)
			stdinDone <- err
		}()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	_, copyErr := io.Copy(stdout, stream)

	// A failed stdin copy — a network-backed Reader that disconnects, a write
	// the relay rejects — means the sandbox saw truncated input. It can still
	// exit 0 on the bytes it did receive, so the exit code alone would report
	// success for a command that never got its input. Report the code (the
	// caller may still want it) and an error saying not to trust it.
	var stdinErr error
	select {
	case stdinErr = <-stdinDone:
	default:
	}

	code, ok := protocol.SandboxExecExitCode(int(websocket.CloseStatus(copyErr)))
	if stdinErr != nil {
		return code, &failure.SDKError{Message: "sandbox exec stdin delivery failed; the exit status does not describe a complete run", Cause: stdinErr}
	}
	if ok {
		return code, nil
	}
	// No exit status: the session ended some other way. A nil copyErr means a
	// clean EOF, which is still the stream vanishing without the sandbox
	// reporting how the process finished.
	return 0, &failure.SDKError{Message: "sandbox exec ended without an exit status", Cause: copyErr}
}

// sandboxRelayURL builds the ws:// or wss:// session URL from an http(s)
// API address.
func sandboxRelayURL(address, sessionID, ticket string) (string, error) {
	base := strings.TrimSuffix(strings.TrimSpace(address), "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	case strings.HasPrefix(base, "wss://"), strings.HasPrefix(base, "ws://"):
	default:
		return "", &failure.SDKError{Message: "sandbox relay address must be http(s) or ws(s): " + address}
	}
	return base + "/api/v1/sandbox-sessions/" + url.PathEscape(sessionID) +
		"/connect?ticket=" + url.QueryEscape(ticket), nil
}
