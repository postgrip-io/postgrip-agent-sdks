package client

import (
	"context"
	"time"

	"github.com/postgrip-io/agent-sdk-protocol"
)

// ScheduleClient mirrors TS client.schedule.* / Python client.schedule.*.
type ScheduleClient struct {
	conn *Connection
}

// Create registers a new schedule.
func (s *ScheduleClient) Create(ctx context.Context, in CreateScheduleInput) (*Schedule, error) {
	req := CreateScheduleRequest{
		ID:            in.ID,
		Namespace:     orDefault(in.Namespace, DefaultNamespace),
		OverlapPolicy: protocol.ScheduleOverlapPolicy(in.OverlapPolicy),
		Spec:          in.Spec,
		Action:        scheduleActionInputToProtocol(in.Action),
	}
	return s.conn.CreateSchedule(ctx, req)
}

// List returns all schedules, optionally filtered.
func (s *ScheduleClient) List(ctx context.Context, filters map[string]string) ([]Schedule, error) {
	return s.conn.ListSchedules(ctx, filters)
}

// Get fetches a schedule by id.
func (s *ScheduleClient) Get(ctx context.Context, scheduleID string) (*Schedule, error) {
	return s.conn.GetSchedule(ctx, scheduleID)
}

// Update patches a schedule.
func (s *ScheduleClient) Update(ctx context.Context, scheduleID string, in UpdateScheduleInput) (*Schedule, error) {
	req := UpdateScheduleRequest{}
	if in.OverlapPolicy != nil {
		policy := protocol.ScheduleOverlapPolicy(*in.OverlapPolicy)
		req.OverlapPolicy = &policy
	}
	if in.Spec != nil {
		req.Spec = in.Spec
	}
	if in.Action != nil {
		action := scheduleActionInputToProtocol(*in.Action)
		req.Action = &action
	}
	return s.conn.UpdateSchedule(ctx, scheduleID, req)
}

// Pause pauses a schedule with optional reason.
func (s *ScheduleClient) Pause(ctx context.Context, scheduleID, reason string) (*Schedule, error) {
	return s.conn.PauseSchedule(ctx, scheduleID, PauseScheduleRequest{Reason: reason})
}

// Unpause resumes a schedule.
func (s *ScheduleClient) Unpause(ctx context.Context, scheduleID, reason string) (*Schedule, error) {
	return s.conn.UnpauseSchedule(ctx, scheduleID, UnpauseScheduleRequest{Reason: reason})
}

// Trigger fires the schedule once immediately.
func (s *ScheduleClient) Trigger(ctx context.Context, scheduleID, reason string) (*TriggerScheduleResponse, error) {
	return s.conn.TriggerSchedule(ctx, scheduleID, TriggerScheduleRequest{Reason: reason})
}

// Backfill replays missed runs in [start, end].
func (s *ScheduleClient) Backfill(ctx context.Context, scheduleID string, start, end time.Time) (*BackfillScheduleResponse, error) {
	return s.conn.BackfillSchedule(ctx, scheduleID, BackfillScheduleRequest{
		StartAt: start,
		EndAt:   end,
	})
}

// Delete removes a schedule.
func (s *ScheduleClient) Delete(ctx context.Context, scheduleID string) error {
	return s.conn.DeleteSchedule(ctx, scheduleID)
}

func scheduleActionInputToProtocol(in ScheduleActionInput) ScheduleAction {
	out := ScheduleAction{
		Namespace:             orDefault(in.Namespace, DefaultNamespace),
		Queue:                 orDefault(in.Queue, DefaultQueue),
		WorkflowType:          in.WorkflowType,
		WorkflowID:            in.WorkflowID,
		WorkflowIDReusePolicy: string(in.WorkflowIDReusePolicy),
		RunTimeoutMs:          int64(in.RunTimeoutMs),
	}
	if in.Retry != nil {
		out.RetryPolicy = in.Retry
	}
	if len(in.Args) > 0 {
		out.Args = mustJSON(in.Args)
	}
	memo := memoWithWorkflowUI(in.Memo, in.UI)
	if len(memo) > 0 {
		out.Memo = memo
	}
	if len(in.SearchAttributes) > 0 {
		out.SearchAttributes = in.SearchAttributes
	}
	return out
}
