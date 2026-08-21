package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/postgrip-io/postgrip-agent-sdks/go/failure"
	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
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
// The stream carries raw stdio. For a pty session it is terminal traffic; for
// exec it is the process's stdout and stderr *interleaved on the same stream*
// — the relay multiplexes both into one channel, so they cannot be separated
// client-side. WebSocket message boundaries are otherwise opaque, except that
// CloseWrite sends the reserved zero-length binary stdin-EOF message.
type SandboxStream struct {
	conn *websocket.Conn
	// net.Conn rather than io.ReadWriteCloser so ExitCode can unblock a pending
	// read via SetReadDeadline when its context is cancelled. websocket.NetConn
	// already returns one.
	rw          net.Conn
	ctx         context.Context
	writeMu     sync.Mutex
	inputClosed bool
}

// Read implements io.Reader over the session's output.
func (s *SandboxStream) Read(p []byte) (int, error) { return s.rw.Read(p) }

// Write implements io.Writer over the session's input.
//
// A single write must not exceed protocol.SandboxRelayMaxFrameBytes; the relay
// closes the session rather than forwarding a larger frame. io.Copy's default
// 32 KiB buffer is comfortably under that.
func (s *SandboxStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.inputClosed {
		return 0, io.ErrClosedPipe
	}
	// io.Writer permits zero-length writes as no-ops. Keep them that way: an
	// actual empty binary WebSocket message is reserved for CloseWrite.
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > protocol.SandboxRelayMaxFrameBytes {
		return 0, &failure.SDKError{
			Message: "sandbox session write exceeds the relay frame limit; chunk writes at or below protocol.SandboxRelayMaxFrameBytes",
		}
	}
	return s.rw.Write(p)
}

// CloseWrite signals end-of-stdin without closing the session's output side.
//
// The signal is a reserved zero-length binary WebSocket message. It is
// idempotent. Once it succeeds, later writes fail with io.ErrClosedPipe while
// Read and ExitCode continue until the sandbox process exits.
func (s *SandboxStream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.inputClosed {
		return nil
	}
	if err := s.conn.Write(s.ctx, websocket.MessageBinary, nil); err != nil {
		return err
	}
	s.inputClosed = true
	return nil
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
//
// ctx bounds this call specifically. It is honoured separately from the context
// that opened the session: a caller passing a deadline here — the common shape
// being "the command should be done by now" — would otherwise block in Read
// until the session itself ended, because only the opening context could
// interrupt it.
func (s *SandboxStream) ExitCode(ctx context.Context) (int, error) {
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			_, err := s.rw.Read(buf)
			if err == nil {
				continue
			}
			if code, ok := protocol.SandboxExecExitCode(int(websocket.CloseStatus(err))); ok {
				done <- outcome{code: code}
				return
			}
			done <- outcome{err: &failure.SDKError{
				Message: "sandbox session ended without an exit status",
				Cause:   err,
			}}
			return
		}
	}()
	select {
	case o := <-done:
		return o.code, o.err
	case <-ctx.Done():
		// Release the reader rather than leaving it parked on the connection
		// for the life of the session: a deadline in the past makes the
		// pending Read return at once. The stream is finished either way — the
		// caller gave up waiting for the exit status.
		_ = s.rw.SetReadDeadline(time.Now())
		return 0, &failure.SDKError{
			Message: "waiting for the sandbox exit status was cancelled",
			Cause:   ctx.Err(),
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
	// Every header configured on the connection, not just Authorization. A
	// gateway that authenticates on a custom header rejects the upgrade without
	// them, which reads as "sessions are broken" even though sandbox creation
	// and every other call worked — those go through the HTTP client, which
	// does send them.
	header := http.Header{}
	for k, v := range s.conn.ConfiguredHeaders() {
		header.Set(k, v)
	}
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
	return &SandboxStream{conn: conn, rw: websocket.NetConn(ctx, conn, websocket.MessageBinary), ctx: ctx}, nil
}

type sandboxExecStdinOutcome struct {
	copyErr error
	eofErr  error
}

// Exec runs a command in the sandbox, streaming stdin in and stdout out, and
// returns the process exit code.
//
// stdout receives stdout and stderr interleaved — the relay carries one
// stream. Pass a nil stdin for commands that read nothing.
//
// Once stdin is drained (or immediately when it is nil), Exec sends the
// protocol's end-of-stdin message while keeping the output side open. Commands
// that read until EOF, such as cat, sort, and archive imports, can therefore
// finish normally and still report their exit status.
func (s *SandboxClient) Exec(ctx context.Context, sandboxID string, command []string, stdin io.Reader, stdout io.Writer) (int, error) {
	stream, err := s.OpenSandboxSession(ctx, sandboxID, protocol.SandboxSessionKindExec, SandboxSessionOptions{Command: command})
	if err != nil {
		return 0, err
	}
	defer stream.Close()

	// Buffered so the copy never blocks on a receive that no longer happens:
	// when the session ends first, nothing reads this channel.
	stdinDone := make(chan sandboxExecStdinOutcome, 1)
	if stdin == nil {
		stdinDone <- sandboxExecStdinOutcome{eofErr: stream.CloseWrite()}
	} else {
		go func() {
			_, copyErr := io.Copy(stream, stdin)
			outcome := sandboxExecStdinOutcome{copyErr: copyErr}
			if copyErr == nil {
				outcome.eofErr = stream.CloseWrite()
			}
			stdinDone <- outcome
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
	var stdinResult sandboxExecStdinOutcome
	select {
	case stdinResult = <-stdinDone:
	default:
	}

	return sandboxExecResult(copyErr, stdinResult)
}

func sandboxExecResult(outputErr error, stdin sandboxExecStdinOutcome) (int, error) {
	code, ok := protocol.SandboxExecExitCode(int(websocket.CloseStatus(outputErr)))
	if stdin.copyErr != nil {
		return code, &failure.SDKError{Message: "sandbox exec stdin delivery failed; the exit status does not describe a complete run", Cause: stdin.copyErr}
	}
	if ok {
		// A command that does not read stdin can report its valid exit status
		// before the empty EOF message is written. The input bytes were fully
		// delivered (or there were none), so that close wins the race; only a
		// copy failure above means the process saw truncated input.
		return code, nil
	}
	if stdin.eofErr != nil {
		return 0, &failure.SDKError{Message: "sandbox exec stdin delivery failed; the session ended before EOF was signalled", Cause: stdin.eofErr}
	}
	// No exit status: the session ended some other way. A nil copyErr means a
	// clean EOF, which is still the stream vanishing without the sandbox
	// reporting how the process finished.
	return 0, &failure.SDKError{Message: "sandbox exec ended without an exit status", Cause: outputErr}
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
	operation, err := resolveOpenAPIOperation(
		openAPIConnectSandboxSession,
		map[string]string{OpenAPIConnectSandboxSessionPathSessionId: sessionID},
		url.Values{OpenAPIConnectSandboxSessionQueryTicket: []string{ticket}},
	)
	if err != nil {
		return "", &failure.SDKError{Message: "resolve sandbox relay OpenAPI operation", Cause: err}
	}
	return base + operation.Path, nil
}
