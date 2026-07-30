package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"charm.land/fantasy"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/secrets"
)

// StepJob is one durable step of one agent run. Org and session ride along
// so redelivered jobs need no directory lookups; they are immutable for the
// life of the run.
type StepJob struct {
	RunID     string `json:"run_id"`
	OrgID     string `json:"org_id"`
	SessionID string `json:"session_id"`
	StepIndex int    `json:"step_index"`
}

// Kind implements jobqueue.Job.
func (StepJob) Kind() string { return "agent.step" }

// StepWorker executes StepJobs.
type StepWorker struct {
	Store    ultra.Store
	Keyring  secrets.Keyring
	Enqueue  jobqueue.TxEnqueuer
	Registry *Registry
	Log      *slog.Logger
	// ToolResolver injects dynamic session/environment tools per step.
	ToolResolver ToolResolver

	// CancelPollInterval is how often a running step checks for a cancel
	// request. Defaults to 300ms.
	CancelPollInterval time.Duration

	// DeltaFlushInterval / DeltaFlushBytes bound streaming event batching.
	// Defaults: 100ms / 512 bytes.
	DeltaFlushInterval time.Duration
	DeltaFlushBytes    int
}

func (w *StepWorker) cancelPoll() time.Duration {
	if w.CancelPollInterval > 0 {
		return w.CancelPollInterval
	}
	return 300 * time.Millisecond
}

// errStale signals a redelivered or superseded job: ack silently.
var errStale = errors.New("loop: stale step delivery")

// errCancelledCommit signals that a transaction performed a cancellation
// and must COMMIT, after which the job acks. It is translated to errStale
// (nil ack) by txStale after the commit succeeds.
var errCancelledCommit = errors.New("loop: run cancelled")

// txStale runs fn in a transaction, translating the cancelled-commit
// sentinel into a committed transaction + errStale result.
func (w *StepWorker) txStale(ctx context.Context, fn func(ultra.Store) error) error {
	cancelled := false
	err := w.Store.Tx(ctx, func(txs ultra.Store) error {
		err := fn(txs)
		if errors.Is(err, errCancelledCommit) {
			cancelled = true
			return nil // commit the cancellation
		}
		return err
	})
	if err != nil {
		return err
	}
	if cancelled {
		return errStale
	}
	return nil
}

// Work implements jobqueue.Worker[StepJob].
func (w *StepWorker) Work(ctx context.Context, job StepJob) error {
	scope := w.Store.Org(ultra.OrgID(job.OrgID))
	runID := ultra.RunID(job.RunID)
	sessionID := ultra.SessionID(job.SessionID)

	// Phase 1: claim. Validates state, handles cancellation requested while
	// queued, detects redelivery of already-committed steps, and moves
	// pending → running.
	run, attempt, err := w.claim(ctx, job)
	switch {
	case errors.Is(err, errStale):
		return nil
	case err != nil:
		return err
	}

	events := scope.Events()
	appendEvent := func(kind string, payload any) {
		b, err := json.Marshal(payload)
		if err != nil {
			w.Log.Error("loop: encode event", "kind", kind, "error", err)
			return
		}
		if _, err := events.Append(ctx, sessionID, ultra.Event{
			Actor:   ultra.Actor{Type: ultra.ActorAgent, ID: job.RunID},
			Kind:    kind,
			Payload: b,
		}); err != nil {
			w.Log.Error("loop: append event", "kind", kind, "error", err)
		}
	}

	appendEvent(ultra.EventKindStepStarted, ultra.StepStartedPayload{
		RunID: runID, StepIndex: job.StepIndex, Attempt: attempt,
	})

	// Resolve the model on the org's credentials. Typed failures are
	// terminal and user-actionable.
	model, err := ResolveModel(ctx, scope, w.Keyring, run.ModelConfig)
	if err != nil {
		var cerr *CredentialError
		if errors.As(err, &cerr) {
			return w.failRun(ctx, job, cerr.Reason, cerr.Message)
		}
		return err // infrastructure error: let the queue retry
	}

	def, err := w.Registry.Resolve(run.LoopKind, run.LoopVersion)
	if err != nil {
		return w.failRun(ctx, job, ultra.FailureInternal, "unknown loop version — contact support")
	}

	env, err := DecodeEnvelope(run.History)
	if err != nil {
		return w.failRun(ctx, job, ultra.FailureInternal, "corrupt run history")
	}

	// Cancellation: poll the run row while the step executes; abort the
	// stream promptly when a cancel lands.
	stepCtx, cancelStep := context.WithCancelCause(ctx)
	defer cancelStep(nil)
	go w.pollCancel(stepCtx, scope, runID, cancelStep)

	rec := &stepRecorder{}
	batcher := newDeltaBatcher(runID, job.StepIndex, attempt, w.DeltaFlushInterval, w.DeltaFlushBytes, appendEvent)

	tools := []fantasy.AgentTool{
		newAskUserTool(rec),
		newPostEventTool(events, sessionID, runID),
	}
	tools = append(tools, memoryTools(w.Store, run)...)
	if w.ToolResolver != nil {
		dynamic, err := w.ToolResolver.Tools(ctx, run)
		if err != nil {
			return err
		}
		tools = append(tools, dynamic...)
	}

	agent := fantasy.NewAgent(model,
		fantasy.WithSystemPrompt(def.SystemPrompt),
		fantasy.WithTools(tools...),
	)

	maxRetries := 0
	result, streamErr := agent.Stream(stepCtx, fantasy.AgentStreamCall{
		Messages:   env.Messages,
		StopWhen:   []fantasy.StopCondition{fantasy.StepCountIs(1)},
		MaxRetries: &maxRetries,
		OnTextDelta: func(_ string, delta string) error {
			batcher.addText(delta)
			return nil
		},
		OnReasoningDelta: func(_ string, delta string) error {
			batcher.addReasoning(delta)
			return nil
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			// Flush pending deltas first so the log preserves the order the
			// user perceived: text, then the tool call it led to.
			batcher.flushAll()
			rec.toolsCalled++
			appendEvent(ultra.EventKindToolCallStart, ultra.ToolCallStartedPayload{
				RunID: runID, StepIndex: job.StepIndex,
				ToolCallID: tc.ToolCallID, Name: tc.ToolName, Input: tc.Input,
			})
			return nil
		},
		OnToolResult: func(tr fantasy.ToolResultContent) error {
			content := ""
			isError := false
			switch out := tr.Result.(type) {
			case fantasy.ToolResultOutputContentText:
				content = out.Text
			case fantasy.ToolResultOutputContentError:
				content = out.Error.Error()
				isError = true
			}
			appendEvent(ultra.EventKindToolResult, ultra.ToolResultPayload{
				RunID: runID, StepIndex: job.StepIndex,
				ToolCallID: tr.ToolCallID, Name: tr.ToolName,
				Content: content, IsError: isError,
			})
			return nil
		},
	})
	batcher.flushAll()

	if streamErr != nil {
		return w.handleStreamError(ctx, job, stepCtx, streamErr)
	}
	return w.commitOutcome(ctx, job, attempt, env, result, rec)
}

// claim validates and transitions the run under a row lock.
func (w *StepWorker) claim(ctx context.Context, job StepJob) (ultra.AgentRun, int, error) {
	var run ultra.AgentRun
	var attempt int
	err := w.txStale(ctx, func(txs ultra.Store) error {
		scope := txs.Org(ultra.OrgID(job.OrgID))
		var err error
		run, err = scope.Runs().GetForUpdate(ctx, ultra.RunID(job.RunID))
		if err != nil {
			return err
		}
		if run.State != ultra.RunPending && run.State != ultra.RunRunning {
			return errStale
		}
		if run.CancelRequestedAt != nil {
			return w.markCancelledTx(ctx, txs, job)
		}
		steps, err := scope.Runs().Steps(ctx, run.ID)
		if err != nil {
			return err
		}
		for _, s := range steps {
			if s.StepIndex == job.StepIndex {
				// Already committed: the next-step job was enqueued in the
				// same transaction. Nothing to do.
				return errStale
			}
			if s.StepIndex == job.StepIndex-1 {
				attempt = s.Attempt // informational only
			}
		}
		attempt = w.countAttempts(ctx, scope, job) + 1
		if run.State == ultra.RunPending {
			if err := scope.Runs().SetState(ctx, run.ID, ultra.RunRunning, "", ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ultra.ErrNotFound) {
			return run, 0, errStale
		}
		return run, 0, err
	}
	return run, attempt, nil
}

// countAttempts counts prior StepStarted events for this step index by
// scanning the run's step_started events. Cheap for phase-1 scale.
func (w *StepWorker) countAttempts(ctx context.Context, scope ultra.OrgScope, job StepJob) int {
	count := 0
	var from int64
	for {
		batch, err := scope.Events().Range(ctx, ultra.SessionID(job.SessionID), from, 512)
		if err != nil || len(batch) == 0 {
			return count
		}
		for _, e := range batch {
			from = e.Seq
			if e.Kind != ultra.EventKindStepStarted {
				continue
			}
			var p ultra.StepStartedPayload
			if json.Unmarshal(e.Payload, &p) == nil && string(p.RunID) == job.RunID && p.StepIndex == job.StepIndex {
				count++
			}
		}
	}
}

// pollCancel cancels the step context when a cancel request lands.
func (w *StepWorker) pollCancel(ctx context.Context, scope ultra.OrgScope, runID ultra.RunID, cancel context.CancelCauseFunc) {
	ticker := time.NewTicker(w.cancelPoll())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, err := scope.Runs().Get(ctx, runID)
			if err == nil && run.CancelRequestedAt != nil {
				cancel(errCancelRequested)
				return
			}
		}
	}
}

var errCancelRequested = errors.New("loop: cancel requested")

// handleStreamError classifies a stream failure.
func (w *StepWorker) handleStreamError(ctx context.Context, job StepJob, stepCtx context.Context, streamErr error) error {
	// Cancelled by user request → terminal cancelled.
	if errors.Is(context.Cause(stepCtx), errCancelRequested) {
		err := w.txStale(ctx, func(txs ultra.Store) error {
			return w.markCancelledTx(ctx, txs, job)
		})
		if errors.Is(err, errStale) {
			return nil
		}
		return err
	}
	// Job context cancelled (shutdown / job timeout) → redeliver.
	if ctx.Err() != nil {
		return fmt.Errorf("loop: step interrupted: %w", ctx.Err())
	}
	reason, message := ClassifyProviderError(streamErr)
	w.Log.Warn("loop: step failed", "run", job.RunID, "step", job.StepIndex,
		"reason", reason, "error", secrets.DefaultRedactor.Redact(streamErr.Error()))
	return w.failRun(ctx, job, reason, message)
}

// markCancelledTx transitions to cancelled and emits the terminal event
// inside the given transaction-bound store. It returns errCancelledCommit,
// which txStale translates into a committed transaction + silent ack.
func (w *StepWorker) markCancelledTx(ctx context.Context, txs ultra.Store, job StepJob) error {
	scope := txs.Org(ultra.OrgID(job.OrgID))
	if err := scope.Runs().SetState(ctx, ultra.RunID(job.RunID), ultra.RunCancelled, "", ""); err != nil {
		return err
	}
	payload, _ := json.Marshal(ultra.RunCancelledPayload{RunID: ultra.RunID(job.RunID)})
	_, err := scope.Events().Append(ctx, ultra.SessionID(job.SessionID), ultra.Event{
		Actor:   ultra.Actor{Type: ultra.ActorSystem},
		Kind:    ultra.EventKindRunCancelled,
		Payload: payload,
	})
	if err != nil {
		return err
	}
	return errCancelledCommit
}

// failRun marks a terminal, typed failure.
func (w *StepWorker) failRun(ctx context.Context, job StepJob, reason, message string) error {
	return w.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(ultra.OrgID(job.OrgID))
		run, err := scope.Runs().GetForUpdate(ctx, ultra.RunID(job.RunID))
		if err != nil || run.State.Terminal() {
			return nil
		}
		if err := scope.Runs().SetState(ctx, run.ID, ultra.RunFailed, reason, message); err != nil {
			return err
		}
		payload, _ := json.Marshal(ultra.RunFailedPayload{RunID: run.ID, Reason: reason, Message: message})
		_, err = scope.Events().Append(ctx, ultra.SessionID(job.SessionID), ultra.Event{
			Actor:   ultra.Actor{Type: ultra.ActorSystem},
			Kind:    ultra.EventKindRunFailed,
			Payload: payload,
		})
		return err
	})
}

// commitOutcome persists the step and classifies what happens next, all in
// one transaction with the next-step enqueue.
func (w *StepWorker) commitOutcome(ctx context.Context, job StepJob, attempt int, env Envelope, result *fantasy.AgentResult, rec *stepRecorder) error {
	if len(result.Steps) == 0 {
		return w.failRun(ctx, job, ultra.FailureProviderError, "model returned no step")
	}
	step := result.Steps[0]
	newMessages := step.Messages
	env.Messages = append(env.Messages, newMessages...)
	encoded, err := env.Encode()
	if err != nil {
		return err
	}

	runID := ultra.RunID(job.RunID)
	sessionID := ultra.SessionID(job.SessionID)

	err = w.txStale(ctx, func(txs ultra.Store) error {
		scope := txs.Org(ultra.OrgID(job.OrgID))
		run, err := scope.Runs().GetForUpdate(ctx, runID)
		if err != nil {
			return err
		}
		if run.State != ultra.RunRunning {
			return errStale
		}
		if run.CancelRequestedAt != nil {
			return w.markCancelledTx(ctx, txs, job)
		}
		if err := scope.Runs().SetHistory(ctx, runID, encoded); err != nil {
			return err
		}
		if err := scope.Runs().InsertStep(ctx, ultra.RunStep{
			RunID: runID, StepIndex: job.StepIndex, Attempt: attempt,
			TokensIn: step.Usage.InputTokens, TokensOut: step.Usage.OutputTokens,
			FinishReason: string(step.FinishReason),
		}); err != nil {
			if errors.Is(err, ultra.ErrAlreadyExists) {
				return errStale
			}
			return err
		}

		appendTx := func(kind string, payload any) error {
			b, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			_, err = scope.Events().Append(ctx, sessionID, ultra.Event{
				Actor:   ultra.Actor{Type: ultra.ActorAgent, ID: job.RunID},
				Kind:    kind,
				Payload: b,
			})
			return err
		}

		if err := appendTx(ultra.EventKindStepFinished, ultra.StepFinishedPayload{
			RunID: runID, StepIndex: job.StepIndex,
			TokensIn: step.Usage.InputTokens, TokensOut: step.Usage.OutputTokens,
			FinishReason: string(step.FinishReason),
		}); err != nil {
			return err
		}

		switch {
		case rec.question != nil:
			// Await: no job parked; PromptRun resumes.
			if err := scope.Runs().SetState(ctx, runID, ultra.RunAwaiting, "", ""); err != nil {
				return err
			}
			return appendTx(ultra.EventKindRunAwaiting, ultra.RunAwaitingPayload{
				RunID: runID, Question: *rec.question,
			})
		case step.FinishReason == fantasy.FinishReasonToolCalls:
			// Continue: next step in the same commit.
			return w.Enqueue.EnqueueInTx(ctx, txs, StepJob{
				RunID: job.RunID, OrgID: job.OrgID, SessionID: job.SessionID,
				StepIndex: job.StepIndex + 1,
			})
		default:
			if err := scope.Runs().SetState(ctx, runID, ultra.RunCompleted, "", ""); err != nil {
				return err
			}
			return appendTx(ultra.EventKindRunCompleted, ultra.RunCompletedPayload{
				RunID: runID, FinalText: finalText(result),
			})
		}
	})
	if errors.Is(err, errStale) {
		return nil
	}
	return err
}

// finalText extracts the assistant's final text from the result.
func finalText(result *fantasy.AgentResult) string {
	for _, part := range result.Response.Content {
		if tp, ok := part.(fantasy.TextContent); ok {
			return tp.Text
		}
	}
	return ""
}

// deltaBatcher coalesces streamed deltas into batched events, flushing on
// interval or size so event-log write amplification stays bounded while
// perceived latency stays low.
type deltaBatcher struct {
	runID     ultra.RunID
	stepIndex int
	attempt   int
	interval  time.Duration
	maxBytes  int
	emit      func(kind string, payload any)

	mu         sync.Mutex
	textBuf    []byte
	reasonBuf  []byte
	deltaIndex int
	timer      *time.Timer
}

func newDeltaBatcher(runID ultra.RunID, stepIndex, attempt int, interval time.Duration, maxBytes int, emit func(string, any)) *deltaBatcher {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if maxBytes <= 0 {
		maxBytes = 512
	}
	return &deltaBatcher{
		runID: runID, stepIndex: stepIndex, attempt: attempt,
		interval: interval, maxBytes: maxBytes, emit: emit,
	}
}

func (b *deltaBatcher) addText(delta string) {
	b.add(&b.textBuf, delta)
}

func (b *deltaBatcher) addReasoning(delta string) {
	b.add(&b.reasonBuf, delta)
}

func (b *deltaBatcher) add(buf *[]byte, delta string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*buf = append(*buf, delta...)
	if len(b.textBuf)+len(b.reasonBuf) >= b.maxBytes {
		b.flushLocked()
		return
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.interval, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.flushLocked()
		})
	}
}

func (b *deltaBatcher) flushLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.reasonBuf) > 0 {
		b.emit(ultra.EventKindReasoningDelta, ultra.ReasoningDeltaPayload{
			RunID: b.runID, StepIndex: b.stepIndex, Attempt: b.attempt,
			DeltaIndex: b.deltaIndex, Text: string(b.reasonBuf),
		})
		b.deltaIndex++
		b.reasonBuf = nil
	}
	if len(b.textBuf) > 0 {
		b.emit(ultra.EventKindTextDelta, ultra.TextDeltaPayload{
			RunID: b.runID, StepIndex: b.stepIndex, Attempt: b.attempt,
			DeltaIndex: b.deltaIndex, Text: string(b.textBuf),
		})
		b.deltaIndex++
		b.textBuf = nil
	}
}

func (b *deltaBatcher) flushAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked()
}
