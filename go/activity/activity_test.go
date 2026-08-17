package activity

import (
	"context"
	"testing"

	"github.com/postgrip-io/postgrip-agent-sdks/protocol"
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
	if err := Stdout(context.Background(), "hello"); err == nil {
		t.Fatal("Stdout should error outside an activity")
	}
	if err := Stderr(context.Background(), "warning"); err == nil {
		t.Fatal("Stderr should error outside an activity")
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
	if err := Stdout(ctx, "processed row\n", OutputOptions{Stage: "processStep", Details: map[string]any{"row": 1}}); err != nil {
		t.Fatalf("Stdout: %v", err)
	}
	if err := Stderr(ctx, "retrying row\n"); err != nil {
		t.Fatalf("Stderr: %v", err)
	}
	if len(emitted) != 4 {
		t.Fatalf("emitted = %#v", emitted)
	}
	if emitted[0].Kind != protocol.TaskEventKindHeartbeat || emitted[1].Kind != protocol.TaskEventKindMilestone {
		t.Fatalf("event kinds = %s, %s", emitted[0].Kind, emitted[1].Kind)
	}
	if emitted[1].Details["index"] != 2 || emitted[1].Details["total"] != 10 {
		t.Fatalf("milestone details = %#v", emitted[1].Details)
	}
	if emitted[2].Kind != protocol.TaskEventKindStdout || emitted[2].Stream != "stdout" || emitted[2].Data != "processed row\n" {
		t.Fatalf("stdout event = %#v", emitted[2])
	}
	if emitted[2].Stage != "processStep" || emitted[2].Details["row"] != 1 {
		t.Fatalf("stdout metadata = %#v", emitted[2])
	}
	if emitted[3].Kind != protocol.TaskEventKindStderr || emitted[3].Stream != "stderr" || emitted[3].Data != "retrying row\n" {
		t.Fatalf("stderr event = %#v", emitted[3])
	}
}
