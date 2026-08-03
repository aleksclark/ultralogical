package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

func kinds(events []*corev1.SessionEvent) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = testclient.Kind(ev)
	}
	return out
}

// assertSequence checks that got contains exactly the expected sequence
// where "+" suffixed kinds may repeat 1..n times.
func assertSequence(t *testing.T, got []string, want []string) {
	t.Helper()
	gi := 0
	for wi := 0; wi < len(want); wi++ {
		kind, repeat := strings.CutSuffix(want[wi], "+")
		if gi >= len(got) || got[gi] != kind {
			t.Fatalf("sequence mismatch at want[%d]=%s: got %v", wi, want[wi], got)
		}
		gi++
		if repeat {
			for gi < len(got) && got[gi] == kind {
				gi++
			}
		}
	}
	if gi != len(got) {
		t.Fatalf("unexpected trailing events: %v (full: %v)", got[gi:], got)
	}
}

func isTerminalRunEvent(ev *corev1.SessionEvent) bool {
	switch testclient.Kind(ev) {
	case "run_completed", "run_failed", "run_cancelled":
		return true
	}
	return false
}

// A1.1 — Happy path: exact ordered typed event sequence, run completes,
// step rows audited.
func TestA11_HappyPathSequence(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: "thinking about it", ToolCalls: []modelscript.ToolCallSpec{
			{Name: "post_event", Args: map[string]string{"text": "noted"}},
		}},
		{Text: "all done"},
	}})

	sess := createSession(t, alice, string(stack.OrgA.ID), "happy")
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	run, eventSeq, err := alice.StartRun(ctx, sess.GetId(), "please do the thing")
	if err != nil {
		t.Fatal(err)
	}

	events := sub.CollectUntil(t, 30*time.Second, isTerminalRunEvent)
	assertSequence(t, kinds(events), []string{
		"run_started",
		"step_started", "text_delta+", "tool_call_started", "annotation", "tool_result", "hook_fired", "step_finished",
		"step_started", "text_delta+", "hook_fired", "step_finished",
		"run_completed",
	})

	// StartRun's event_seq matches the RunStarted event.
	if events[0].GetSeq() != eventSeq {
		t.Fatalf("event_seq = %d, RunStarted seq = %d", eventSeq, events[0].GetSeq())
	}

	final := alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 5*time.Second)
	if final.GetFailureReason() != "" {
		t.Fatalf("unexpected failure: %v", final)
	}

	// Step audit rows: indices 0..1, nonzero tokens.
	steps, err := stack.Store.Org(stack.OrgA.ID).Runs().Steps(ctx, uc.RunID(run.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("step count = %d, want 2", len(steps))
	}
	for i, s := range steps {
		if s.StepIndex != i || s.TokensIn == 0 || s.TokensOut == 0 {
			t.Fatalf("step %d malformed: %+v", i, s)
		}
	}
	if errs := stack.Model.Errors(); len(errs) != 0 {
		t.Fatalf("modelscript errors: %v", errs)
	}
}

// A1.2 — Durability under SIGKILL: a fresh worker resumes the run; step
// indices unique; superseded attempt marked.
func TestA12_DurabilityUnderSIGKILL(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	// Step 0 streams slowly (~5s) so the SIGKILL lands mid-step.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: strings.Repeat("chunk ", 50), ChunkSize: 30, ChunkDelay: 500 * time.Millisecond,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]string{"text": "step0"}}}},
		{Text: "resumed and finished"},
	}})

	sess := createSession(t, alice, string(stack.OrgA.ID), "durable")
	run, _, err := alice.StartRun(ctx, sess.GetId(), "long haul")
	if err != nil {
		t.Fatal(err)
	}

	// Kill the worker mid-step-0.
	time.Sleep(1500 * time.Millisecond)
	stack.KillWorker()
	time.Sleep(500 * time.Millisecond)
	stack.StartWorker()

	// The rescued job re-executes step 0 (attempt 2) and the run completes.
	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 120*time.Second)

	// Replay the log: seq gapless, attempt=2 marker present.
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	events := sub.CollectUntil(t, 10*time.Second, isTerminalRunEvent)

	var lastSeq int64
	attempts := map[int32]int32{}
	for _, ev := range events {
		if ev.GetSeq() != lastSeq+1 {
			t.Fatalf("seq gap: %d -> %d", lastSeq, ev.GetSeq())
		}
		lastSeq = ev.GetSeq()
		if ss := ev.GetPayload().GetStepStarted(); ss != nil {
			if ss.GetAttempt() > attempts[ss.GetStepIndex()] {
				attempts[ss.GetStepIndex()] = ss.GetAttempt()
			}
		}
	}
	if attempts[0] < 2 {
		t.Fatalf("expected step 0 attempt >= 2, got %d", attempts[0])
	}

	// Step rows unique 0..1.
	steps, err := stack.Store.Org(stack.OrgA.ID).Runs().Steps(ctx, uc.RunID(run.GetId()))
	if err != nil || len(steps) != 2 {
		t.Fatalf("steps = %v, err %v", steps, err)
	}
}

// A1.3 — Awaiting without parked workers: ask_user parks the run with an
// empty queue; PromptRun resumes it.
func TestA13_AwaitingWithoutParkedWorkers(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: "I need input", ToolCalls: []modelscript.ToolCallSpec{
			{Name: "ask_user", Args: map[string]any{"question": "Which color?", "choices": []string{"red", "blue"}}},
		}},
		{Text: "great choice of blue"},
	}})

	sess := createSession(t, alice, string(stack.OrgA.ID), "awaiting")
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	run, _, err := alice.StartRun(ctx, sess.GetId(), "ask me something")
	if err != nil {
		t.Fatal(err)
	}

	// RunAwaiting event carries the structured question.
	events := sub.CollectUntil(t, 30*time.Second, func(ev *corev1.SessionEvent) bool {
		return testclient.Kind(ev) == "run_awaiting"
	})
	awaiting := events[len(events)-1].GetPayload().GetRunAwaiting()
	if awaiting.GetQuestion().GetText() != "Which color?" ||
		len(awaiting.GetQuestion().GetChoices()) != 2 {
		t.Fatalf("question malformed: %v", awaiting)
	}

	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_AWAITING, 10*time.Second)

	// No job parked while awaiting. (River's completer acks finished jobs
	// asynchronously, so allow it a moment to settle.)
	settleDeadline := time.Now().Add(10 * time.Second)
	for {
		depth, err := stack.QueueDepth(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if depth == 0 {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatalf("queue depth = %d during await, want 0", depth)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Answer resumes the run.
	if _, err := alice.Agents.PromptRun(ctx, connect.NewRequest(&corev1.PromptRunRequest{
		RunId: run.GetId(), Message: "blue",
	})); err != nil {
		t.Fatal(err)
	}
	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 30*time.Second)

	// The answer is in the persisted history as a user message.
	stored, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, uc.RunID(run.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.History), "blue") {
		t.Fatal("answer not found in run history")
	}
	if errs := stack.Model.Errors(); len(errs) != 0 {
		t.Fatalf("modelscript errors: %v", errs)
	}
}

// A1.4 — Cancellation: mid-stream cancel yields a terminal cancelled state,
// no further steps, drained queue, and PromptRun rejection.
func TestA14_Cancellation(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	// A slow multi-step script; cancel lands mid-step-0.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: strings.Repeat("slow ", 100), ChunkSize: 10, ChunkDelay: 300 * time.Millisecond,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]string{"text": "x"}}}},
		{Text: "should never run"},
	}})

	sess := createSession(t, alice, string(stack.OrgA.ID), "cancel")
	run, _, err := alice.StartRun(ctx, sess.GetId(), "take your time")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1 * time.Second)
	if _, err := alice.Agents.CancelRun(ctx, connect.NewRequest(&corev1.CancelRunRequest{RunId: run.GetId()})); err != nil {
		t.Fatal(err)
	}

	alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_CANCELLED, 30*time.Second)

	// Terminal event present, nothing after it.
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	events := sub.CollectUntil(t, 10*time.Second, isTerminalRunEvent)
	last := events[len(events)-1]
	if testclient.Kind(last) != "run_cancelled" {
		t.Fatalf("terminal event = %s", testclient.Kind(last))
	}
	for _, ev := range events {
		if ss := ev.GetPayload().GetStepStarted(); ss != nil && ss.GetStepIndex() > 0 {
			t.Fatalf("step %d started after cancel", ss.GetStepIndex())
		}
	}

	// Queue drains.
	deadline := time.Now().Add(10 * time.Second)
	for {
		depth, err := stack.QueueDepth(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if depth == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue depth still %d after cancel", depth)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// PromptRun on a cancelled run is rejected with a typed error.
	_, err = alice.Agents.PromptRun(ctx, connect.NewRequest(&corev1.PromptRunRequest{
		RunId: run.GetId(), Message: "hello?",
	}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("PromptRun on cancelled run: %v", err)
	}
}

// A1.5 — True streaming: multiple deltas arrive incrementally before the
// run completes.
func TestA15_TrueStreaming(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: strings.Repeat("stream me please ", 40), ChunkSize: 40, ChunkDelay: 150 * time.Millisecond},
	}})

	sess := createSession(t, alice, string(stack.OrgA.ID), "stream")
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if _, _, err := alice.StartRun(ctx, sess.GetId(), "stream it"); err != nil {
		t.Fatal(err)
	}

	type stamped struct {
		kind string
		at   time.Time
		idx  int32
	}
	var seen []stamped
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			ev, err := sub.Next()
			if err != nil {
				return
			}
			s := stamped{kind: testclient.Kind(ev), at: time.Now()}
			if d := ev.GetPayload().GetTextDelta(); d != nil {
				s.idx = d.GetDeltaIndex()
			}
			seen = append(seen, s)
			if isTerminalRunEvent(ev) {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("run never completed")
	}

	var deltas []stamped
	completedAt := time.Time{}
	for _, s := range seen {
		switch s.kind {
		case "text_delta":
			deltas = append(deltas, s)
		case "run_completed":
			completedAt = s.at
		}
	}
	if len(deltas) < 2 {
		t.Fatalf("got %d text deltas, want >= 2", len(deltas))
	}
	for i := 1; i < len(deltas); i++ {
		if deltas[i].idx <= deltas[i-1].idx {
			t.Fatalf("delta_index not increasing: %d then %d", deltas[i-1].idx, deltas[i].idx)
		}
	}
	// Incremental delivery: first delta lands well before completion.
	if !deltas[0].at.Before(completedAt.Add(-500 * time.Millisecond)) {
		t.Fatal("deltas were not delivered incrementally")
	}
}

// A1.7 — BYO credentials: typed fast-fails, real credential use, canary
// never leaks, org isolation.
func TestA17_ByoCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		stack := harness.Up(t, harness.WithoutSeedCredential())
		alice := stack.AliceClient()
		sess := createSession(t, alice, string(stack.OrgA.ID), "nocred")
		run, _, err := alice.StartRun(ctx, sess.GetId(), "hello")
		if err != nil {
			t.Fatal(err)
		}
		final := alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_FAILED, 30*time.Second)
		if final.GetFailureReason() != "credential_missing" {
			t.Fatalf("failure reason = %q", final.GetFailureReason())
		}
		// No step row exists.
		steps, err := stack.Store.Org(stack.OrgA.ID).Runs().Steps(ctx, uc.RunID(run.GetId()))
		if err != nil || len(steps) != 0 {
			t.Fatalf("steps = %v, err = %v", steps, err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		stack := harness.Up(t)
		alice := stack.AliceClient()
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{Status: 401},
			{Status: 401}, // guard: a retry would consume this
		}})
		sess := createSession(t, alice, string(stack.OrgA.ID), "badcred")
		run, _, err := alice.StartRun(ctx, sess.GetId(), "hello")
		if err != nil {
			t.Fatal(err)
		}
		final := alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_FAILED, 30*time.Second)
		if final.GetFailureReason() != "credential_invalid" {
			t.Fatalf("failure reason = %q", final.GetFailureReason())
		}
		if got := len(stack.Model.Requests()); got != 1 {
			t.Fatalf("vendor called %d times, want exactly 1 (no retry burn)", got)
		}
	})

	t.Run("happy path uses org credential and canary never leaks", func(t *testing.T) {
		t.Parallel()
		stack := harness.Up(t)
		alice := stack.AliceClient()
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "done"}}})
		sess := createSession(t, alice, string(stack.OrgA.ID), "cred")
		run, _, err := alice.StartRun(ctx, sess.GetId(), "hello")
		if err != nil {
			t.Fatal(err)
		}
		alice.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 30*time.Second)

		// (c) The org's API key reached the vendor.
		reqs := stack.Model.Requests()
		if len(reqs) == 0 || reqs[0].Authorization != "Bearer "+harness.CanaryAPIKey {
			t.Fatalf("vendor auth header = %q", reqs[0].Authorization)
		}

		// (d) Canary sweep: not in any event payload.
		sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()
		events := sub.CollectUntil(t, 10*time.Second, isTerminalRunEvent)
		for _, ev := range events {
			if strings.Contains(ev.String(), harness.CanaryAPIKey) {
				t.Fatalf("canary leaked into event seq %d", ev.GetSeq())
			}
		}
		// Not in the database (events, runs) either — beyond the encrypted
		// credential row itself.
		var leaks int
		row := queryOne(t, stack.DatabaseURL,
			`SELECT count(*) FROM session_events WHERE payload::text LIKE '%`+harness.CanaryAPIKey+`%'`)
		leaks += row
		row = queryOne(t, stack.DatabaseURL,
			`SELECT count(*) FROM agent_runs WHERE history::text LIKE '%`+harness.CanaryAPIKey+`%'
			   OR failure_message LIKE '%`+harness.CanaryAPIKey+`%'`)
		leaks += row
		if leaks != 0 {
			t.Fatalf("canary found in %d database rows", leaks)
		}
	})

	t.Run("cross-org isolation", func(t *testing.T) {
		t.Parallel()
		stack := harness.Up(t) // credential seeded in org A only
		bob := stack.BobClient()
		sessB := createSession(t, bob, string(stack.OrgB.ID), "bobs")
		run, _, err := bob.StartRun(ctx, sessB.GetId(), "hello")
		if err != nil {
			t.Fatal(err)
		}
		final := bob.AwaitRunState(t, run.GetId(), corev1.RunState_RUN_STATE_FAILED, 30*time.Second)
		if final.GetFailureReason() != "credential_missing" {
			t.Fatalf("org B used org A's credential? reason = %q", final.GetFailureReason())
		}
	})
}

// Credential RPCs: write-only payloads, list/delete, member visibility.
func TestCredentialRPCs(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	put, err := alice.Orgs.PutCredential(ctx, connect.NewRequest(&corev1.PutCredentialRequest{
		OrgId: string(stack.OrgA.ID), Kind: "inference:anthropic",
		ApiKey: "sk-ant-secret-value-12345", BaseUrl: "https://gateway.example.test/anthropic",
		ExtraHeadersJson: `{"cf-aig-collect-log-payload":"false","cf-aig-metadata":"{\"tier\":\"fast\"}"}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if put.Msg.GetCredential().GetName() != "default" {
		t.Fatalf("put returned %v", put.Msg.GetCredential())
	}
	stored, err := stack.Store.Org(stack.OrgA.ID).Credentials().Get(ctx, uc.CredentialKindAnthropic, "default")
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := secrets.NewAESKeyring(stack.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := keyring.Decrypt(stored.EncPayload)
	if err != nil {
		t.Fatal(err)
	}
	var configured uc.InferencePayload
	if err := json.Unmarshal(plaintext, &configured); err != nil {
		t.Fatal(err)
	}
	if configured.BaseURL != "https://gateway.example.test/anthropic" || configured.ExtraHeaders["cf-aig-collect-log-payload"] != "false" {
		t.Fatalf("configuration not persisted: %+v", configured)
	}
	if strings.Contains(put.Msg.String(), "sk-ant-secret") {
		t.Fatal("credential value echoed in response")
	}

	list, err := alice.Orgs.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
		OrgId: string(stack.OrgA.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.GetCredentials()) != 2 { // seeded openai + new anthropic
		t.Fatalf("credential count = %d", len(list.Msg.GetCredentials()))
	}
	if strings.Contains(list.Msg.String(), "sk-") {
		t.Fatal("list leaked secret material")
	}

	if _, err := alice.Orgs.DeleteCredential(ctx, connect.NewRequest(&corev1.DeleteCredentialRequest{
		OrgId: string(stack.OrgA.ID), Kind: "inference:anthropic", Name: "default",
	})); err != nil {
		t.Fatal(err)
	}

	// Bob (not a member of org A) is denied indistinguishably.
	_, err = stack.BobClient().Orgs.ListCredentials(ctx, connect.NewRequest(&corev1.ListCredentialsRequest{
		OrgId: string(stack.OrgA.ID),
	}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeNotFound {
		t.Fatalf("cross-org credential list: %v", err)
	}
}

// queryOne runs a scalar count query against the stack database.
func queryOne(t *testing.T, dbURL, sql string) int {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type toolResult struct {
	Name    string
	Content string
	IsError bool
}

func toolResultsFor(t *testing.T, stack *harness.Stack, session string, run uc.RunID) []toolResult {
	t.Helper()
	var out []toolResult
	var from int64
	for {
		batch, err := stack.Store.Org(stack.OrgA.ID).Events().Range(context.Background(), uc.SessionID(session), from, 512)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, e := range batch {
			from = e.Seq
			if e.Kind != uc.EventKindToolResult {
				continue
			}
			var payload uc.ToolResultPayload
			if json.Unmarshal(e.Payload, &payload) != nil || payload.RunID != run {
				continue
			}
			out = append(out, toolResult{Name: payload.Name, Content: payload.Content, IsError: payload.IsError})
		}
	}
	return out
}

// Flat allowlist denial: a child granted no tools that calls one receives the
// uniform refusal and a PermissionDenied event, with no existence leak.
func TestE1_FlatAllowlistDenialVisibility(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "denial")
	org := stack.OrgA.ID
	prompt := "deny parent " + t.Name()
	childPrompt := "deny child " + t.Name()

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains(prompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": childPrompt, "tools": []string{},
			}}},
		},
		{Match: modelscript.UserContains(prompt), Sticky: true, Text: "parent done"},
		{
			Match: modelscript.UserContains(childPrompt),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "post_event", Args: map[string]any{
				"text": "should be denied",
			}}},
		},
		{Match: modelscript.UserContains(childPrompt), Sticky: true, Text: "child done"},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	kids := childrenOf(t, stack, org, uc.RunID(parent.GetId()), 1, 90*time.Second)
	child := awaitRunOneOf(t, stack, org, kids[0].ID, 3*time.Minute, uc.RunCompleted, uc.RunFailed)
	results := toolResultsFor(t, stack, sess.GetId(), child.ID)
	if len(results) == 0 {
		t.Fatal("expected a tool result for the denied call")
	}
	found := false
	for _, r := range results {
		if r.Name != "post_event" {
			continue
		}
		found = true
		if !r.IsError {
			t.Fatalf("denied tool result is not an error: %+v", r)
		}
		if !strings.Contains(strings.ToLower(r.Content), "permission") && !strings.Contains(strings.ToLower(r.Content), "denied") && !strings.Contains(strings.ToLower(r.Content), "not granted") {
			t.Fatalf("denial message %q is not a uniform denial", r.Content)
		}
		for _, leak := range []string{"canonical", "available tools", "bash", "spawn_agent"} {
			if strings.Contains(strings.ToLower(r.Content), leak) {
				t.Fatalf("denial leaked %q: %s", leak, r.Content)
			}
		}
	}
	if !found {
		t.Fatalf("no post_event tool result among %+v", results)
	}
	denials := collectEvents(t, stack, sess.GetId(), uc.EventKindPermissionDenied, 60*time.Second, 1)
	var denial uc.PermissionDeniedPayload
	if err := json.Unmarshal(denials[len(denials)-1].Payload, &denial); err != nil {
		t.Fatal(err)
	}
	if denial.RunID != child.ID {
		t.Fatalf("denial attributed to %s, want child %s", denial.RunID, child.ID)
	}
	if denial.Tool != "post_event" {
		t.Fatalf("denial tool = %q", denial.Tool)
	}
}
