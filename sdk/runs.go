package sdk

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

// StartRun starts an agent run. Model and policy may be nil for defaults.
func (c *Client) StartRun(ctx context.Context, sessionID, prompt string, model *corev1.ModelConfig, policy *corev1.RunPolicy) (*corev1.AgentRun, int64, error) {
	resp, err := c.Runs.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
		SessionId:   sessionID,
		Prompt:      prompt,
		ModelConfig: model,
		Policy:      policy,
	}))
	if err != nil {
		return nil, 0, err
	}
	return resp.Msg.GetRun(), resp.Msg.GetEventSeq(), nil
}

// AnswerRun delivers an answer to an awaiting run (or a new turn on completed).
func (c *Client) AnswerRun(ctx context.Context, runID, message string) (int64, error) {
	resp, err := c.Runs.AnswerRun(ctx, connect.NewRequest(&corev1.AnswerRunRequest{
		RunId:   runID,
		Message: message,
	}))
	if err != nil {
		return 0, err
	}
	return resp.Msg.GetEventSeq(), nil
}

// AwaitRunOptions configure AwaitRun.
type AwaitRunOptions struct {
	// Timeout bounds the wait. Default 2m.
	Timeout time.Duration
	// Terminal states that stop the wait. Default: completed/failed/cancelled/awaiting.
	States []corev1.RunState
	// PollInterval between GetRun calls. Default 50ms.
	PollInterval time.Duration
}

// AwaitRun blocks until the run reaches a terminal (or listed) state.
func (c *Client) AwaitRun(ctx context.Context, runID string, opts AwaitRunOptions) (*corev1.AgentRun, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	states := opts.States
	if len(states) == 0 {
		states = []corev1.RunState{
			corev1.RunState_RUN_STATE_COMPLETED,
			corev1.RunState_RUN_STATE_FAILED,
			corev1.RunState_RUN_STATE_CANCELLED,
			corev1.RunState_RUN_STATE_AWAITING,
		}
	}
	want := map[corev1.RunState]bool{}
	for _, s := range states {
		want[s] = true
	}
	deadline := time.Now().Add(timeout)
	var last *corev1.AgentRun
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		resp, err := c.Runs.GetRun(ctx, connect.NewRequest(&corev1.GetRunRequest{RunId: runID}))
		if err == nil {
			last = resp.Msg.GetRun()
			if want[last.GetState()] {
				return last, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(interval):
		}
	}
	if last == nil {
		return nil, fmt.Errorf("run %s: timed out waiting (no read)", runID)
	}
	return last, fmt.Errorf("run %s: timed out waiting for terminal state (last %v)", runID, last.GetState())
}

// StartAndAwait starts a run and blocks until it reaches a terminal/awaiting state.
func (c *Client) StartAndAwait(ctx context.Context, sessionID, prompt string, model *corev1.ModelConfig, policy *corev1.RunPolicy, opts AwaitRunOptions) (*corev1.AgentRun, error) {
	run, _, err := c.StartRun(ctx, sessionID, prompt, model, policy)
	if err != nil {
		return nil, err
	}
	return c.AwaitRun(ctx, run.GetId(), opts)
}
