package client

import (
	"context"
)

// CreateSchedule creates a scheduled workflow trigger.
func (c *Connection) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.doOpenAPI(ctx, openAPICreateSchedule, nil, nil, req, &out); err != nil {
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
	var out Schedule
	if err := c.doOpenAPI(ctx, openAPIGetSchedule, map[string]string{"scheduleId": scheduleID}, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSchedule patches the schedule's spec / overlap_policy / action.
func (c *Connection) UpdateSchedule(ctx context.Context, scheduleID string, req UpdateScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.doOpenAPI(ctx, openAPIUpdateSchedule, map[string]string{"scheduleId": scheduleID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseSchedule pauses scheduled triggers.
func (c *Connection) PauseSchedule(ctx context.Context, scheduleID string, req PauseScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.doOpenAPI(ctx, openAPIPauseSchedule, map[string]string{"scheduleId": scheduleID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnpauseSchedule resumes scheduled triggers.
func (c *Connection) UnpauseSchedule(ctx context.Context, scheduleID string, req UnpauseScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.doOpenAPI(ctx, openAPIUnpauseSchedule, map[string]string{"scheduleId": scheduleID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TriggerSchedule fires the schedule once immediately.
func (c *Connection) TriggerSchedule(ctx context.Context, scheduleID string, req TriggerScheduleRequest) (*TriggerScheduleResponse, error) {
	var out TriggerScheduleResponse
	if err := c.doOpenAPI(ctx, openAPITriggerSchedule, map[string]string{"scheduleId": scheduleID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BackfillSchedule replays missed runs in a window.
func (c *Connection) BackfillSchedule(ctx context.Context, scheduleID string, req BackfillScheduleRequest) (*BackfillScheduleResponse, error) {
	var out BackfillScheduleResponse
	if err := c.doOpenAPI(ctx, openAPIBackfillSchedule, map[string]string{"scheduleId": scheduleID}, nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSchedule removes a schedule.
func (c *Connection) DeleteSchedule(ctx context.Context, scheduleID string) error {
	return c.doOpenAPI(ctx, openAPIDeleteSchedule, map[string]string{"scheduleId": scheduleID}, nil, nil, nil)
}

// ListNamespaces returns every namespace registered with the runtime service.
func (c *Connection) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	var out []Namespace
	if err := c.doOpenAPI(ctx, openAPIListNamespaces, nil, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateNamespace creates a new namespace.
func (c *Connection) CreateNamespace(ctx context.Context, name string) (*Namespace, error) {
	var out Namespace
	if err := c.doOpenAPI(ctx, openAPICreateNamespace, nil, nil, map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
