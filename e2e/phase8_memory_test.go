package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// A8.6 — session memory across concurrency, replay, expiry, and tenancy.
func TestA86_SessionMemory(t *testing.T) {
	stack := harness.Up(t, harness.WithReplicas(2, 2))
	ctx := context.Background()
	org := stack.TenantA.ID
	alice := stack.IngressClient(stack.KeyA)
	bob := stack.BobClient()
	sess := createSession(t, alice, string(org), "session memory")

	t.Run("concurrent_writes_are_atomic", func(t *testing.T) {
		// Ten writers race one key. The winner must be one of the written
		// values (no torn or interleaved write), and every accepted write must
		// have produced exactly one event.
		const writers = 10
		var wg sync.WaitGroup
		accepted := make([]bool, writers)
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
					SessionId: sess.GetId(), Key: "contended.key",
					ValueJson: fmt.Sprintf(`{"writer":%d}`, i),
				}))
				accepted[i] = err == nil
			}(i)
		}
		wg.Wait()

		got, err := alice.Sessions.GetMemory(ctx, connect.NewRequest(&corev1.GetMemoryRequest{
			SessionId: sess.GetId(), Key: "contended.key",
		}))
		if err != nil {
			t.Fatal(err)
		}
		// Compare semantically: the database stores JSON canonically, so the
		// stored text differs from the submitted text in whitespace only.
		var stored struct {
			Writer *int `json:"writer"`
		}
		value := got.Msg.GetEntry().GetValueJson()
		if err := json.Unmarshal([]byte(value), &stored); err != nil {
			t.Fatalf("stored value %q is not the shape any writer wrote: %v", value, err)
		}
		if stored.Writer == nil || *stored.Writer < 0 || *stored.Writer >= writers {
			t.Fatalf("final value %q is not any single writer's value; the write was not atomic", value)
		}

		wantEvents := 0
		for _, ok := range accepted {
			if ok {
				wantEvents++
			}
		}
		events := collectEvents(t, stack, sess.GetId(), uc.EventKindMemorySet, 60*time.Second, wantEvents)
		setsForKey := 0
		for _, e := range events {
			if strings.Contains(string(e.Payload), "contended.key") {
				setsForKey++
			}
		}
		if setsForKey != wantEvents {
			t.Fatalf("%d writes were accepted but %d events recorded; updates were lost", wantEvents, setsForKey)
		}
	})

	t.Run("key_namespace_is_enforced", func(t *testing.T) {
		// Dotted namespaces are the documented convention, so they must work.
		for _, key := range []string{"investigation.findings.db", "plain", "a-b_c.9"} {
			if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
				SessionId: sess.GetId(), Key: key, ValueJson: "true",
			})); err != nil {
				t.Fatalf("valid key %q was rejected: %v", key, err)
			}
		}
		// Malformed keys are refused rather than silently creating an
		// unreadable namespace shared by every agent in the session.
		for _, key := range []string{"", ".leading", "trailing.", "double..dot", "has space", "has/slash", "tab\tkey"} {
			if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
				SessionId: sess.GetId(), Key: key, ValueJson: "true",
			})); err == nil {
				t.Fatalf("malformed key %q was accepted", key)
			}
		}
	})

	t.Run("caps_are_enforced", func(t *testing.T) {
		// An oversized value is refused.
		big := `"` + strings.Repeat("x", uc.MaxMemoryValue+1) + `"`
		if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: sess.GetId(), Key: "oversized.value", ValueJson: big,
		})); err == nil {
			t.Fatal("a value larger than the documented limit was accepted")
		}

		// Fill to the key cap in its own session so the count is exact.
		capped := createSession(t, alice, string(org), "memory caps")
		for i := range uc.MaxMemoryKeys {
			if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
				SessionId: capped.GetId(), Key: fmt.Sprintf("filler.%03d", i), ValueJson: "true",
			})); err != nil {
				t.Fatalf("key %d of the allowed %d was rejected: %v", i, uc.MaxMemoryKeys, err)
			}
		}
		if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: capped.GetId(), Key: "one.too.many", ValueJson: "true",
		})); err == nil {
			t.Fatalf("key %d was accepted past the documented cap", uc.MaxMemoryKeys+1)
		}
		// Overwriting an existing key still works at the cap: the limit is on
		// distinct keys, not on writes.
		if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: capped.GetId(), Key: "filler.000", ValueJson: "false",
		})); err != nil {
			t.Fatalf("overwriting an existing key at the cap was rejected: %v", err)
		}
	})

	t.Run("agent_written_memory_survives_run_boundaries", func(t *testing.T) {
		// One run writes memory through the native tool; a later run and a
		// human client both read it back. Memory outliving its writer is the
		// point of it being session-scoped.
		shared := createSession(t, alice, string(org), "memory across runs")
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains("writer run"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "session_memory_set", Args: map[string]any{
					"key": "handoff.note", "value": map[string]any{"from": "writer"},
				}}},
			},
			{Match: modelscript.UserContains("writer run"), Text: "wrote the note"},
			{
				Match:     modelscript.UserContains("reader run"),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "session_memory_get", Args: map[string]any{"key": "handoff.note"}}},
			},
			{Match: modelscript.UserContains("reader run"), Sticky: true, Text: "read the note"},
		}})

		writer, _, err := alice.StartRun(ctx, shared.GetId(), "writer run")
		if err != nil {
			t.Fatal(err)
		}
		alice.AwaitRunState(t, writer.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 90*time.Second)

		reader, _, err := alice.StartRun(ctx, shared.GetId(), "reader run")
		if err != nil {
			t.Fatal(err)
		}
		alice.AwaitRunState(t, reader.GetId(), corev1.RunState_RUN_STATE_COMPLETED, 90*time.Second)

		// The second run really read the value the first wrote.
		results := toolResultsFor(t, stack, shared.GetId(), uc.RunID(reader.GetId()))
		var readBack bool
		for _, r := range results {
			if r.Name == "session_memory_get" && strings.Contains(r.Content, `"from"`) && strings.Contains(r.Content, "writer") {
				readBack = true
			}
		}
		if !readBack {
			t.Fatalf("the second run did not read the first run's memory: %+v", results)
		}
		// And a human sees the same value through the API.
		got, err := alice.Sessions.GetMemory(ctx, connect.NewRequest(&corev1.GetMemoryRequest{
			SessionId: shared.GetId(), Key: "handoff.note",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Msg.GetEntry().GetValueJson(), "writer") {
			t.Fatalf("human view of memory is %q", got.Msg.GetEntry().GetValueJson())
		}
		if got.Msg.GetEntry().GetUpdatedByType() != "agent" {
			t.Fatalf("memory attributed to %q, want the agent that wrote it", got.Msg.GetEntry().GetUpdatedByType())
		}
	})

	t.Run("cross_tenant_access_is_indistinguishable", func(t *testing.T) {
		// Bob is in another org. Reading Alice's session memory must look
		// exactly like reading something that does not exist.
		if _, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: sess.GetId(), Key: "tenant.secret", ValueJson: `{"secret":true}`,
		})); err != nil {
			t.Fatal(err)
		}
		missing := "00000000-0000-0000-0000-000000000000"

		_, foreignMem := bob.Sessions.GetMemory(ctx, connect.NewRequest(&corev1.GetMemoryRequest{
			SessionId: sess.GetId(), Key: "tenant.secret",
		}))
		_, missingMem := bob.Sessions.GetMemory(ctx, connect.NewRequest(&corev1.GetMemoryRequest{
			SessionId: missing, Key: "tenant.secret",
		}))
		assertSameDenial(t, "GetMemory", foreignMem, missingMem)

		_, foreignList := bob.Sessions.ListMemory(ctx, connect.NewRequest(&corev1.ListMemoryRequest{SessionId: sess.GetId()}))
		_, missingList := bob.Sessions.ListMemory(ctx, connect.NewRequest(&corev1.ListMemoryRequest{SessionId: missing}))
		assertSameDenial(t, "ListMemory", foreignList, missingList)

		_, foreignWrite := bob.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: sess.GetId(), Key: "tenant.intrusion", ValueJson: "true",
		}))
		_, missingWrite := bob.Sessions.SetMemory(ctx, connect.NewRequest(&corev1.SetMemoryRequest{
			SessionId: missing, Key: "tenant.intrusion", ValueJson: "true",
		}))
		assertSameDenial(t, "SetMemory", foreignWrite, missingWrite)

		// And the intrusion did not land.
		if _, err := alice.Sessions.GetMemory(ctx, connect.NewRequest(&corev1.GetMemoryRequest{
			SessionId: sess.GetId(), Key: "tenant.intrusion",
		})); err == nil {
			t.Fatal("a cross-tenant write was actually applied")
		}
	})
}

// collectEventsIn reads a named session's log until it sees n events of a kind.
// It differs from collectEvents only in taking the session explicitly.
func collectEventsIn(t *testing.T, stack *harness.Stack, session, kind string, timeout time.Duration, n int) []uc.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var found []uc.Event
		var from int64
		for {
			batch, err := stack.Store.Tenant(stack.TenantA.ID).Events().Range(context.Background(), uc.SessionID(session), from, 512)
			if err != nil || len(batch) == 0 {
				break
			}
			for _, e := range batch {
				from = e.Seq
				if e.Kind == kind {
					found = append(found, e)
				}
			}
		}
		if len(found) >= n {
			return found
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("session %s never recorded %d %q events within %s", session, n, kind, timeout)
	return nil
}
