package loop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/jobqueue"
)

// waitNamespace derives stable wait ids from spawn keys.
var waitNamespace = uuid.MustParse("2c4d6e80-9a3b-4c5d-8e7f-1a2b3c4d5e6f")

// waitToolName maps a wait's kind back to the tool the model called, so the
// injected result names the same tool the model believes it invoked.
func waitToolName(kind string) string {
	if kind == uc.WaitKindCohort {
		return "run_agent_cohort"
	}
	return "wait_for_agents"
}

// deterministicWaitID derives a wait's id from its originating tool call, so a
// redelivered step recreates the same row rather than a duplicate wait.
func deterministicWaitID(parent uc.RunID, stepIndex int, toolCallID string) string {
	return uuid.NewSHA1(waitNamespace, []byte(spawnKey(parent, stepIndex, toolCallID))).String()
}

// createWait installs a durable wait and resolves the pre-commit race in the
// same transaction.
//
// The race: a child can reach a terminal state between the moment the parent's
// tool call named it and the moment the wait row exists. That child's own
// resolution pass found no wait to close, and nothing would ever close this
// one. Creating the wait and immediately evaluating its members inside one
// transaction removes the window: either the wait is still pending, and a
// later child closes it, or it is already satisfiable and closes now.
//
// It reports whether the parent should park. A wait that closed immediately
// has already resumed the parent, so the parent must not also be marked
// awaiting.
func (w *StepWorker) createWait(ctx context.Context, txs uc.Store, run uc.AgentRun, job StepJob, rec *stepRecorder) (park bool, err error) {
	scope := txs.Org(run.OrgID)
	waitID := deterministicWaitID(run.ID, job.StepIndex, rec.waitToolCallID)
	members := make([]uc.RunWaitMember, 0, len(rec.waitRunIDs))
	for i, id := range rec.waitRunIDs {
		members = append(members, uc.RunWaitMember{WaitID: waitID, RunID: id, Ordinal: i})
	}
	wait := uc.RunWait{
		ID: waitID, ParentRunID: run.ID, StepIndex: job.StepIndex,
		ToolCallID: rec.waitToolCallID, Kind: rec.waitKind, State: uc.WaitOpen,
		TimeoutPolicy: rec.waitPolicy, Deadline: time.Now().Add(rec.waitTimeout),
	}
	if err := scope.Waits().Create(ctx, wait, members); err != nil {
		return false, err
	}
	// Evaluate immediately: children that finished before this row existed are
	// already terminal, and this is the only place that notices.
	resumed, err := w.tryCloseWait(ctx, txs, run.OrgID, waitID, closeReasonChild)
	if err != nil {
		return false, err
	}
	if resumed {
		return false, nil
	}
	// Arm the deadline in the same transaction as the wait, so a wait can
	// never exist without something scheduled to time it out.
	if err := w.Enqueue.EnqueueInTx(ctx, txs, WaitTimeoutJob{OrgID: string(run.OrgID)},
		jobqueue.WithScheduledAt(wait.Deadline.Add(waitTimeoutSlack))); err != nil {
		return false, err
	}
	return true, nil
}

// waitTimeoutSlack delays the sweep slightly past the deadline so a child
// finishing right at the boundary resolves the wait normally instead of
// racing the sweeper into a timeout.
const waitTimeoutSlack = 250 * time.Millisecond

type closeReason int

const (
	// closeReasonChild: a member reached a terminal state.
	closeReasonChild closeReason = iota
	// closeReasonTimeout: the deadline passed before every member finished.
	closeReasonTimeout
)

// tryCloseWait closes a wait and resumes its parent, at most once, when the
// wait is satisfiable. It reports whether this call performed the resumption.
//
// Exactly-once is enforced by two database predicates rather than by process
// state: Close only affects a row still `open`, and MarkResumed only affects a
// row whose resumption is unrecorded. Two concurrent terminal children, or a
// child racing the timeout sweeper, therefore produce exactly one resumption.
func (w *StepWorker) tryCloseWait(ctx context.Context, txs uc.Store, org uc.OrgID, waitID string, reason closeReason) (bool, error) {
	scope := txs.Org(org)
	wait, err := scope.Waits().GetForUpdate(ctx, waitID)
	if errors.Is(err, uc.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	members, err := scope.Waits().Members(ctx, waitID)
	if err != nil {
		return false, err
	}
	outcome := uc.WaitOutcome{WaitID: waitID, Kind: wait.Kind, Members: make([]uc.WaitMemberResult, 0, len(members))}
	pending := 0
	for _, member := range members {
		child, err := scope.Runs().Get(ctx, member.RunID)
		if err != nil {
			return false, err
		}
		if !child.State.Terminal() {
			pending++
			continue
		}
		outcome.Members = append(outcome.Members, uc.WaitMemberResult{
			Ordinal: member.Ordinal, RunID: child.ID, State: child.State, Result: child.Result,
			FailureReason: child.FailureReason, FailureMessage: child.FailureMessage,
		})
		switch child.State {
		case uc.RunCompleted:
			outcome.Completed++
		case uc.RunFailed:
			outcome.Failed++
		case uc.RunCancelled:
			outcome.Cancelled++
		}
	}
	outcome.Pending = pending

	// Still waiting, and no deadline forcing the issue: leave it open.
	if pending > 0 && reason != closeReasonTimeout {
		return false, nil
	}

	state := uc.WaitResolved
	if reason == closeReasonTimeout && pending > 0 {
		state = uc.WaitTimedOut
		outcome.TimedOut = true
	}
	outcome.State = state

	raw, err := json.Marshal(outcome)
	if err != nil {
		return false, err
	}
	if wait.State == uc.WaitOpen {
		closed, err := scope.Waits().Close(ctx, waitID, state, raw)
		if err != nil {
			return false, err
		}
		if !closed {
			// Someone else closed it first; they own the resumption.
			return false, nil
		}
	} else {
		// The timeout sweeper closed this wait by claiming it, which is what
		// makes timing out exactly-once, but the outcome could only be
		// computed afterwards. Record it now.
		if err := scope.Waits().SetResult(ctx, waitID, raw); err != nil {
			return false, err
		}
	}
	return w.resumeParent(ctx, txs, org, wait, outcome, raw)
}

// resumeParent injects the wait's outcome as the result of the parent's
// original tool call and enqueues its next step, at most once.
func (w *StepWorker) resumeParent(ctx context.Context, txs uc.Store, org uc.OrgID, wait uc.RunWait, outcome uc.WaitOutcome, raw json.RawMessage) (bool, error) {
	scope := txs.Org(org)
	// MarkResumed is the at-most-once gate: whoever wins it resumes.
	first, err := scope.Waits().MarkResumed(ctx, wait.ID)
	if err != nil || !first {
		return false, err
	}

	parent, err := scope.Runs().GetForUpdate(ctx, wait.ParentRunID)
	if err != nil {
		return false, err
	}
	// A parent that already went terminal is not resumed; the wait simply
	// closes behind it.
	if parent.State.Terminal() {
		return false, nil
	}

	history, err := appendWaitResult(parent.History, wait, outcome, raw)
	if err != nil {
		return false, err
	}
	if err := scope.Runs().SetHistory(ctx, parent.ID, history); err != nil {
		return false, err
	}
	if err := scope.Runs().SetState(ctx, parent.ID, uc.RunRunning, "", ""); err != nil {
		return false, err
	}
	payload, err := json.Marshal(uc.ToolResultPayload{
		RunID: parent.ID, StepIndex: wait.StepIndex, ToolCallID: wait.ToolCallID,
		Name: waitToolName(wait.Kind), Content: string(raw),
		IsError: outcome.TimedOut && wait.TimeoutPolicy == uc.TimeoutPolicyFail,
	})
	if err != nil {
		return false, err
	}
	if _, err := scope.Events().Append(ctx, parent.SessionID, uc.Event{
		Actor: uc.Actor{Type: uc.ActorSystem}, Kind: uc.EventKindToolResult, Payload: payload,
	}); err != nil {
		return false, err
	}

	steps, err := scope.Runs().Steps(ctx, parent.ID)
	if err != nil {
		return false, err
	}
	next := 0
	for _, s := range steps {
		if s.StepIndex >= next {
			next = s.StepIndex + 1
		}
	}
	if err := w.Enqueue.EnqueueInTx(ctx, txs, StepJob{
		RunID: string(parent.ID), OrgID: string(parent.OrgID),
		SessionID: string(parent.SessionID), StepIndex: next,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// appendWaitResult appends the outcome as a tool result correlated to the
// parent's original tool call. Correlation matters: a model that called
// wait_for_agents with a given call id must receive the answer to that call,
// not a loose user message it has no way to attribute.
func appendWaitResult(raw json.RawMessage, wait uc.RunWait, outcome uc.WaitOutcome, encoded json.RawMessage) (json.RawMessage, error) {
	env, err := DecodeEnvelope(raw)
	if err != nil {
		return nil, err
	}
	var output fantasy.ToolResultOutputContent = fantasy.ToolResultOutputContentText{Text: string(encoded)}
	if outcome.TimedOut && wait.TimeoutPolicy == uc.TimeoutPolicyFail {
		output = fantasy.ToolResultOutputContentError{Error: errors.New(string(encoded))}
	}
	env.Messages = append(env.Messages, fantasy.Message{
		Role: fantasy.MessageRoleTool,
		Content: []fantasy.MessagePart{fantasy.ToolResultPart{
			ToolCallID: wait.ToolCallID,
			Output:     output,
		}},
	})
	return env.Encode()
}

// resolveChildWaits runs in the same transaction that marks a child terminal.
// Every terminal path — completed, failed, cancelled — funnels through it, so
// no child can quietly leave its parent parked forever.
func (w *StepWorker) resolveChildWaits(ctx context.Context, txs uc.Store, child uc.AgentRun) error {
	scope := txs.Org(child.OrgID)
	waits, err := scope.Waits().ListOpenForChild(ctx, child.ID)
	if err != nil {
		return err
	}
	for _, wait := range waits {
		if _, err := w.tryCloseWait(ctx, txs, child.OrgID, wait.ID, closeReasonChild); err != nil {
			return err
		}
	}
	return nil
}

// AbandonWaits closes a terminal run's open waits from outside the loop
// package. The API's cancel path needs it: a cancelled parent must not be
// resumable by a child that finishes afterwards.
func AbandonWaits(ctx context.Context, txs uc.Store, org uc.OrgID, parent uc.RunID) error {
	return (&StepWorker{}).abandonParentWaits(ctx, txs, org, parent)
}

// abandonParentWaits closes a terminal parent's open waits. Without it a
// cancelled parent would leave an open wait the timeout sweeper keeps
// visiting, whose children would try to resume a run that is already finished.
func (w *StepWorker) abandonParentWaits(ctx context.Context, txs uc.Store, org uc.OrgID, parent uc.RunID) error {
	scope := txs.Org(org)
	waits, err := scope.Waits().ListOpenForParent(ctx, parent)
	if err != nil {
		return err
	}
	for _, wait := range waits {
		outcome := uc.WaitOutcome{WaitID: wait.ID, Kind: wait.Kind, State: uc.WaitAbandoned, Members: []uc.WaitMemberResult{}}
		raw, err := json.Marshal(outcome)
		if err != nil {
			return err
		}
		if _, err := scope.Waits().Close(ctx, wait.ID, uc.WaitAbandoned, raw); err != nil {
			return err
		}
		// Consume the resumption slot so a late child cannot resume a parent
		// that has already gone terminal.
		if _, err := scope.Waits().MarkResumed(ctx, wait.ID); err != nil {
			return err
		}
	}
	return nil
}
