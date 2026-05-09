package worker

import (
	"testing"
	"time"

	"github.com/postgrip-io/agent-sdk-go/client"
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
