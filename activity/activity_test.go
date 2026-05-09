package activity

import (
	"context"
	"testing"

	"github.com/postgrip-io/agent-sdk-protocol"
)

func TestHelpersFailOutsideActivity(t *testing.T) {
	t.Parallel()
	if _, err := GetInfo(context.Background()); err == nil {
		t.Fatal("GetInfo should error outside an activity")
	}
	if err := Heartbeat(context.Background(), nil); err == nil {
		t.Fatal("Heartbeat should error outside an activity")
	}
	if err := Milestone(context.Background(), "step", MilestoneOptions{}); err == nil {
		t.Fatal("Milestone should error outside an activity")
	}
}

func TestHelpersInsideActivityCarryInfo(t *testing.T) {
	t.Parallel()
	emitted := []EventInput{}
	runtime := &Runtime{
		Info: Info{TaskID: "t-1", AgentID: "a-1", Type: "Greet", Attempt: 1},
		Emitter: func(_ context.Context, ev EventInput) error {
			emitted = append(emitted, ev)
			return nil
		},
	}
	ctx := WithRuntime(context.Background(), runtime)
	info, err := GetInfo(ctx)
	if err != nil || info.TaskID != "t-1" || info.Attempt != 1 {
		t.Fatalf("GetInfo = %+v, %v", info, err)
	}
	if err := Heartbeat(ctx, map[string]any{"phase": "halfway"}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := Milestone(ctx, "step-2", MilestoneOptions{Index: 2, Total: 10}); err != nil {
		t.Fatalf("Milestone: %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("emitted = %#v", emitted)
	}
	if emitted[0].Kind != protocol.TaskEventKindHeartbeat || emitted[1].Kind != protocol.TaskEventKindMilestone {
		t.Fatalf("event kinds = %s, %s", emitted[0].Kind, emitted[1].Kind)
	}
	if emitted[1].Details["index"] != 2 || emitted[1].Details["total"] != 10 {
		t.Fatalf("milestone details = %#v", emitted[1].Details)
	}
}
