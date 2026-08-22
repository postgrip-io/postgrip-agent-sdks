package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/postgrip-io/postgrip-agent-sdks/go/client"
)

func TestNewWorkerValidatesInputs(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected New to require Connection")
	}
	conn, _ := client.NewConnection(client.ConnectionOptions{Address: "http://example.test"})
	if _, err := New(Options{Connection: conn}); err == nil {
		t.Fatal("expected New to require AgentID")
	}
	w, err := New(Options{Connection: conn, AgentID: "a-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.opts.Namespace != client.DefaultNamespace || w.opts.Queue != client.DefaultQueue {
		t.Fatalf("defaults = %+v", w.opts)
	}
	if w.opts.MaxConcurrentTasks != 4 {
		t.Fatalf("MaxConcurrentTasks default = %d, want 4", w.opts.MaxConcurrentTasks)
	}
}

func TestWorkerStopsWhenPollReturnsGoneShutdownDirective(t *testing.T) {
	t.Parallel()
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/agent/poll" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		polls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"directive":{"type":"shutdown","subject":"agent"}}`))
	}))
	defer server.Close()

	conn, err := client.NewConnection(client.ConnectionOptions{Address: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	conn.SeedAgentSession("agent-1", "agent-token", time.Now().Add(time.Hour))
	w, err := New(Options{
		Connection:   conn,
		AgentID:      "agent-1",
		PollInterval: time.Millisecond,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := polls.Load(); got != 1 {
		t.Fatalf("poll count = %d, want 1", got)
	}
}

func TestHeartbeatIntervalIsAtLeast500ms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lease int
		want  time.Duration
	}{
		{0, 500 * time.Millisecond},
		{1, 500 * time.Millisecond},
		{3, time.Second}, // 3s / 3 = 1s
		{30, 10 * time.Second},
	}
	for _, c := range cases {
		got := heartbeatInterval(c.lease)
		if got != c.want {
			t.Fatalf("heartbeatInterval(%d) = %s, want %s", c.lease, got, c.want)
		}
	}
}
