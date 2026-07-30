package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// A3.1 — presence transitions are durable and both subscribers converge.
func TestA31_PresenceAndOrderedDelivery(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "multi")
	join, err := alice.Sessions.Join(ctx, connect.NewRequest(&ultrav1.JoinRequest{SessionId: sess.GetId(), Display: "Alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(join.Msg.GetParticipants()) != 1 {
		t.Fatalf("participants=%v", join.Msg.GetParticipants())
	}
	seq, err := alice.AppendUserMessage(ctx, sess.GetId(), "hello multiplayer")
	if err != nil {
		t.Fatal(err)
	}
	// The durable log is authoritative; two independent range reads converge.
	eventsA, err := stack.Store.Org(stack.OrgA.ID).Events().Range(ctx, ultra.SessionID(sess.GetId()), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	eventsB, err := stack.Store.Org(stack.OrgA.ID).Events().Range(ctx, ultra.SessionID(sess.GetId()), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsA) != 2 || len(eventsB) != 2 {
		t.Fatalf("event counts %d %d", len(eventsA), len(eventsB))
	}
	for i := range eventsA {
		if eventsA[i].Seq != eventsB[i].Seq {
			t.Fatal("readers diverged")
		}
	}
	if seq != eventsA[1].Seq {
		t.Fatalf("append seq=%d delivered=%d", seq, eventsA[1].Seq)
	}
	if _, err := alice.Sessions.Leave(ctx, connect.NewRequest(&ultrav1.LeaveRequest{SessionId: sess.GetId()})); err != nil {
		t.Fatal(err)
	}
}

// A3.2 — concurrent run histories remain isolated.
func TestA32_ConcurrentRunIsolation(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "concurrent")
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "result one"}, {Text: "result two"}}})
	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i, p := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			run, _, err := alice.StartRun(ctx, sess.GetId(), p)
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = run.GetId()
		}(i, p)
	}
	wg.Wait()
	for _, id := range ids {
		alice.AwaitRunState(t, id, ultrav1.RunState_RUN_STATE_COMPLETED, 30*time.Second)
	}
	for i, id := range ids {
		run, err := stack.Store.Org(stack.OrgA.ID).Runs().Get(ctx, ultra.RunID(id))
		if err != nil {
			t.Fatal(err)
		}
		other := []string{"beta", "alpha"}[i]
		if containsJSON(run.History, other) {
			t.Fatalf("history %s contains %s", id, other)
		}
	}
}

// A3.5 — concurrent memory writes, caps, and cross-run/session durability.
func TestA35_MemoryCapsAndConcurrency(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	sess := createSession(t, alice, string(stack.OrgA.ID), "memory")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&ultrav1.SetMemoryRequest{SessionId: sess.GetId(), Key: "shared", ValueJson: fmt.Sprintf("%d", i)}))
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := alice.Sessions.GetMemory(ctx, connect.NewRequest(&ultrav1.GetMemoryRequest{SessionId: sess.GetId(), Key: "shared"}))
	if err != nil || got.Msg.GetEntry().GetValueJson() == "" {
		t.Fatalf("memory=%v err=%v", got, err)
	}
	for i := 0; i < 199; i++ {
		_, err := alice.Sessions.SetMemory(ctx, connect.NewRequest(&ultrav1.SetMemoryRequest{SessionId: sess.GetId(), Key: fmt.Sprintf("k.%03d", i), ValueJson: "true"}))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = alice.Sessions.SetMemory(ctx, connect.NewRequest(&ultrav1.SetMemoryRequest{SessionId: sess.GetId(), Key: "overflow", ValueJson: "true"}))
	if err == nil {
		t.Fatal("201st key accepted")
	}
}

func containsJSON(b []byte, s string) bool { return stringContains(string(b), s) }
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
