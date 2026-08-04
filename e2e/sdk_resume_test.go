package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// A4.5 — SDK subscribe-resume: kill mid-stream and resume from last seq with
// no gaps or duplicates (gapless seq contract).
func TestA45_SDKSubscribeResume(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	sess := createSession(t, alice, string(stack.TenantA.ID), "sdk-resume")

	// Seed events.
	for i := 0; i < 8; i++ {
		if _, err := alice.AppendUserMessage(ctx, sess.GetId(), "m"); err != nil {
			t.Fatal(err)
		}
	}

	// First subscription reads a few events then closes (simulates kill).
	sub1, err := alice.SubscribeResume(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var first []int64
	for len(first) < 3 {
		ev, err := sub1.Next()
		if err != nil {
			t.Fatal(err)
		}
		first = append(first, ev.GetSeq())
	}
	last := sub1.LastSeq()
	sub1.Close()

	// Resume from last seen seq.
	sub2, err := alice.SubscribeResume(ctx, sess.GetId(), last)
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()
	var rest []int64
	for len(first)+len(rest) < 8 {
		ev, err := sub2.Next()
		if err != nil {
			t.Fatal(err)
		}
		rest = append(rest, ev.GetSeq())
	}

	all := append(append([]int64{}, first...), rest...)
	seen := map[int64]bool{}
	for i, s := range all {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
		if i > 0 && s != all[i-1]+1 {
			t.Fatalf("gap: %v then %v", all[i-1], s)
		}
	}
	if len(all) != 8 {
		t.Fatalf("got %d events, want 8", len(all))
	}

	// GetEvents non-streaming range agrees.
	got, err := alice.GetEvents(ctx, sess.GetId(), 0, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 8 {
		t.Fatalf("GetEvents len=%d want 8", len(got))
	}
	for i, ev := range got {
		if ev.GetSeq() != int64(i+1) {
			t.Fatalf("GetEvents[%d].seq=%d", i, ev.GetSeq())
		}
	}
}

// A4.8 helper — ArchiveSession stamps archived_at.
func TestA48_SessionArchive(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	sess := createSession(t, alice, string(stack.TenantA.ID), "to-archive")
	resp, err := alice.Sessions.ArchiveSession(ctx, connect.NewRequest(&corev1.ArchiveSessionRequest{
		SessionId: sess.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.GetSession().GetArchivedAt() == nil {
		t.Fatal("expected archived_at set")
	}
	// Idempotent.
	resp2, err := alice.Sessions.ArchiveSession(ctx, connect.NewRequest(&corev1.ArchiveSessionRequest{
		SessionId: sess.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.Msg.GetSession().GetArchivedAt() == nil {
		t.Fatal("archived still required")
	}
}

// Ensure testclient import used when only helpers referenced.
var _ = testclient.Kind
