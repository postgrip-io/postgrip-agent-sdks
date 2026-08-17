package client

import (
	"context"
)

// CreateSchedule creates a scheduled workflow trigger.
func (c *Connection) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*Schedule, error) {
	out, err := c.OpenAPI().CreateSchedule(ctx, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSchedules returns all schedules in the namespace.
func (c *Connection) ListSchedules(ctx context.Context, params map[string]string) ([]Schedule, error) {
	var out []Schedule
	if err := c.doOpenAPI(ctx, openAPIListSchedules, nil, queryValues(params), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchedule fetches a single schedule by id.
func (c *Connection) GetSchedule(ctx context.Context, scheduleID string) (*Schedule, error) {
	out, err := c.OpenAPI().GetSchedule(ctx, scheduleID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSchedule patches the schedule's spec / overlap_policy / action.
func (c *Connection) UpdateSchedule(ctx context.Context, scheduleID string, req UpdateScheduleRequest) (*Schedule, error) {
	out, err := c.OpenAPI().UpdateSchedule(ctx, scheduleID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseSchedule pauses scheduled triggers.
func (c *Connection) PauseSchedule(ctx context.Context, scheduleID string, req PauseScheduleRequest) (*Schedule, error) {
	out, err := c.OpenAPI().PauseSchedule(ctx, scheduleID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UnpauseSchedule resumes scheduled triggers.
func (c *Connection) UnpauseSchedule(ctx context.Context, scheduleID string, req UnpauseScheduleRequest) (*Schedule, error) {
	out, err := c.OpenAPI().UnpauseSchedule(ctx, scheduleID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// TriggerSchedule fires the schedule once immediately.
func (c *Connection) TriggerSchedule(ctx context.Context, scheduleID string, req TriggerScheduleRequest) (*TriggerScheduleResponse, error) {
	out, err := c.OpenAPI().TriggerSchedule(ctx, scheduleID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// BackfillSchedule replays missed runs in a window.
func (c *Connection) BackfillSchedule(ctx context.Context, scheduleID string, req BackfillScheduleRequest) (*BackfillScheduleResponse, error) {
	out, err := c.OpenAPI().BackfillSchedule(ctx, scheduleID, req)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSchedule removes a schedule.
func (c *Connection) DeleteSchedule(ctx context.Context, scheduleID string) error {
	_, err := c.OpenAPI().DeleteSchedule(ctx, scheduleID)
	return err
}

// ListNamespaces returns every namespace registered with the runtime service.
func (c *Connection) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	return c.OpenAPI().ListNamespaces(ctx)
}

// CreateNamespace creates a new namespace.
func (c *Connection) CreateNamespace(ctx context.Context, name string) (*Namespace, error) {
	out, err := c.OpenAPI().CreateNamespace(ctx, OpenAPICreateNamespaceRequestBody{Name: name})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
