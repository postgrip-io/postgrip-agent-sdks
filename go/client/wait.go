package client

import (
	"context"
	"time"

	"go.postgrip.io/sdk/failure"
)

// waitForTaskCompletion polls the task until it reaches a terminal state.
// Used by TaskClient.Result and WorkflowHandle.Result so callers don't have
// to hand-roll a polling loop.
func waitForTaskCompletion(ctx context.Context, conn *Connection, taskID string, target any) error {
	for {
		task, err := conn.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		switch task.State {
		case TaskStateSucceeded:
			if target == nil || task.Result == nil {
				return nil
			}
			if task.Result.Failure != nil {
				return failure.FromInfo(task.Result.Failure)
			}
			return decodeResultValue(task.Result, target)
		case TaskStateFailed:
			reason := task.Error
			if task.Result != nil && task.Result.Failure != nil {
				return &failure.TaskFailed{
					TaskID:  taskID,
					Reason:  reason,
					Failure: failure.InfoToApplication(task.Result.Failure),
				}
			}
			return &failure.TaskFailed{TaskID: taskID, Reason: reason}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// WaitForTaskCompletion is the exported variant. The /worker package uses
// it to surface the final task result via WorkflowHandle.Result. Same
// behavior as the internal helper.
func WaitForTaskCompletion(ctx context.Context, conn *Connection, taskID string, target any) error {
	return waitForTaskCompletion(ctx, conn, taskID, target)
}
