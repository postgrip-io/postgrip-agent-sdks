package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/postgrip-io/agent-sdk-go/activity"
	"github.com/postgrip-io/agent-sdk-go/client"
	"github.com/postgrip-io/agent-sdk-go/failure"
	"github.com/postgrip-io/agent-sdk-go/internal/replay"
)

func (w *Worker) runActivity(ctx context.Context, task *client.Task) (*client.TaskResult, error) {
	activityType := strings.TrimPrefix(task.Type, client.TaskTypePrefixActivity)
	fn, ok := w.opts.Activities[activityType]
	if !ok {
		return nil, failure.NewNonRetryable(
			fmt.Sprintf("activity %q is not registered", activityType),
			"ActivityNotRegistered",
			activityType,
		)
	}
	args, err := decodeActivityArgs(task.Payload)
	if err != nil {
		return nil, failure.NewNonRetryable(
			fmt.Sprintf("decode activity args: %v", err),
			"BadActivityPayload",
		)
	}
	runtime := &activity.Runtime{
		Info: activity.Info{
			TaskID:    task.ID,
			AgentID:   w.opts.AgentID,
			Namespace: task.Namespace,
			Queue:     task.Queue,
			Type:      activityType,
			Attempt:   task.Attempt,
			Args:      args,
		},
		Emitter: func(ctx context.Context, ev activity.EventInput) error {
			return w.opts.Connection.EmitTaskEvent(ctx, task.ID, w.opts.AgentID, ev)
		},
	}
	activityCtx := activity.WithRuntime(ctx, runtime)
	value, err := fn(activityCtx, args)
	if err != nil {
		return &client.TaskResult{}, err
	}
	return &client.TaskResult{Value: value}, nil
}

func (w *Worker) runWorkflow(ctx context.Context, task *client.Task) (*client.TaskResult, error) {
	workflowType := strings.TrimPrefix(task.Type, client.TaskTypePrefixWorkflow)
	fn, ok := w.opts.Workflows[workflowType]
	if !ok {
		return nil, failure.NewNonRetryable(
			fmt.Sprintf("workflow %q is not registered", workflowType),
			"WorkflowNotRegistered",
			workflowType,
		)
	}
	args, workflowID, err := decodeWorkflowPayload(task)
	if err != nil {
		return nil, failure.NewNonRetryable(
			fmt.Sprintf("decode workflow payload: %v", err),
			"BadWorkflowPayload",
		)
	}
	// Pull the durable history so the replay returns persisted command
	// results to the workflow body. A failure here is a transport problem,
	// not a workflow failure: proceeding with an empty replay would let the
	// workflow re-enqueue every command it has already completed, which is
	// the duplicate-work bug we exist to prevent. Block the task with a
	// clear reason so the runtime service redelivers and we can try again.
	history, err := w.opts.Connection.GetWorkflowHistory(ctx, workflowID)
	if err != nil {
		w.logger.Warn("workflow history fetch failed; blocking task for redelivery",
			"workflow_id", workflowID,
			"error", err,
		)
		if _, blockErr := w.opts.Connection.BlockTask(ctx, task.ID, w.opts.AgentID, "workflow history fetch failed: "+err.Error()); blockErr != nil {
			w.logger.Warn("block workflow task after history failure", "task_id", task.ID, "error", blockErr)
			return &client.TaskResult{}, blockErr
		}
		return nil, errWorkflowAlreadyBlocked
	}
	rp := replay.New(history)
	wfctx := newWorkflowContext(ctx, w, task, workflowID, workflowType, rp)
	value, err := fn(wfctx, args)
	if err != nil {
		// Workflow yielded on a pending command. Block the task so the
		// runtime service redelivers it after the dependency resolves;
		// returning this from runWorkflow as a regular error would mark
		// the workflow failed, which is wrong — the workflow hasn't
		// failed, it's waiting.
		if isWorkflowSuspended(err) {
			if _, blockErr := w.opts.Connection.BlockTask(ctx, task.ID, w.opts.AgentID, err.Error()); blockErr != nil {
				w.logger.Warn("block workflow task failed", "task_id", task.ID, "error", blockErr)
				return &client.TaskResult{}, blockErr
			}
			return nil, errWorkflowAlreadyBlocked
		}
		return &client.TaskResult{}, err
	}
	return &client.TaskResult{Value: value}, nil
}

// decodeActivityArgs unpacks the wire-format activity payload into the
// []any the customer's activity Func expects. Tolerates missing fields
// (returns an empty arg list) so trivial activities don't have to send a
// dummy payload.
func decodeActivityArgs(raw json.RawMessage) ([]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var envelope struct {
		Args []any `json:"args"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	return envelope.Args, nil
}

func decodeWorkflowPayload(task *client.Task) ([]any, string, error) {
	if len(task.Payload) == 0 {
		return nil, task.ID, nil
	}
	var envelope struct {
		Args       []any  `json:"args"`
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(task.Payload, &envelope); err != nil {
		return nil, "", err
	}
	wfID := envelope.WorkflowID
	if wfID == "" {
		wfID = task.ID
	}
	return envelope.Args, wfID, nil
}

// fmtRFC3339Nano keeps the formatter consistent with the protocol's
// expected timer fire_at format. Defined here (rather than as a constant)
// so the workflow context can call it without importing time uselessly.
func fmtRFC3339Nano(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
