package sdk

import (
	"context"
	"net/http"
	"net/url"
)

// CreateSchedule creates a scheduled workflow trigger.
func (c *Connection) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (*Schedule, error) {
	var out Schedule
	if err := c.do(ctx, http.MethodPost, "/api/v1/schedules", req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSchedules returns all schedules in the namespace.
func (c *Connection) ListSchedules(ctx context.Context, params map[string]string) ([]Schedule, error) {
	path := "/api/v1/schedules"
	if len(params) > 0 {
		path += "?" + encodeQuery(params)
	}
	var out []Schedule
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchedule fetches a single schedule by id.
func (c *Connection) GetSchedule(ctx context.Context, scheduleID string) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	var out Schedule
	if err := c.do(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSchedule patches the schedule's spec / overlap_policy / action.
func (c *Connection) UpdateSchedule(ctx context.Context, scheduleID string, req UpdateScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	var out Schedule
	if err := c.do(ctx, http.MethodPatch, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseSchedule pauses scheduled triggers.
func (c *Connection) PauseSchedule(ctx context.Context, scheduleID string, req PauseScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/pause"
	var out Schedule
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnpauseSchedule resumes scheduled triggers.
func (c *Connection) UnpauseSchedule(ctx context.Context, scheduleID string, req UnpauseScheduleRequest) (*Schedule, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/unpause"
	var out Schedule
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// TriggerSchedule fires the schedule once immediately.
func (c *Connection) TriggerSchedule(ctx context.Context, scheduleID string, req TriggerScheduleRequest) (*TriggerScheduleResponse, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/trigger"
	var out TriggerScheduleResponse
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// BackfillSchedule replays missed runs in a window.
func (c *Connection) BackfillSchedule(ctx context.Context, scheduleID string, req BackfillScheduleRequest) (*BackfillScheduleResponse, error) {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID) + "/backfill"
	var out BackfillScheduleResponse
	if err := c.do(ctx, http.MethodPost, path, req, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSchedule removes a schedule.
func (c *Connection) DeleteSchedule(ctx context.Context, scheduleID string) error {
	path := "/api/v1/schedules/" + url.PathEscape(scheduleID)
	return c.do(ctx, http.MethodDelete, path, nil, nil, false)
}

// ListNamespaces returns every namespace registered with the runtime service.
func (c *Connection) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	var out []Namespace
	if err := c.do(ctx, http.MethodGet, "/api/v1/namespaces", nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateNamespace creates a new namespace.
func (c *Connection) CreateNamespace(ctx context.Context, name string) (*Namespace, error) {
	var out Namespace
	if err := c.do(ctx, http.MethodPost, "/api/v1/namespaces", map[string]string{"name": name}, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}
