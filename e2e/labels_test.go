package e2e_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
)

// TestA35_LabelsCRUDAndSelectors covers A3.5: labels CRUD, equality and set
// membership selectors, and SessionLabelsChanged in the event log.
func TestA35_LabelsCRUDAndSelectors(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := stack.AliceClient()
	tid := string(stack.TenantA.ID)

	s1, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: tid, Title: "math", Labels: map[string]string{"student": "jacob", "subject": "math"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: tid, Title: "ela", Labels: map[string]string{"student": "maya", "subject": "ela"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: tid, Title: "other", Labels: map[string]string{"student": "jacob", "subject": "ela"},
	})); err != nil {
		t.Fatal(err)
	}

	// Equality selector.
	eq, err := alice.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{
		TenantId: tid,
		LabelSelectors: []*corev1.LabelSelector{{Key: "student", Op: "=", Values: []string{"jacob"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(eq.Msg.GetSessions()) != 2 {
		t.Fatalf("equality: got %d want 2", len(eq.Msg.GetSessions()))
	}

	// Set membership.
	in, err := alice.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{
		TenantId: tid,
		LabelSelectors: []*corev1.LabelSelector{{Key: "subject", Op: "in", Values: []string{"math", "science"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Msg.GetSessions()) != 1 || in.Msg.GetSessions()[0].GetId() != s1.Msg.GetSession().GetId() {
		t.Fatalf("in-selector = %+v", in.Msg.GetSessions())
	}

	// Update labels emits SessionLabelsChanged.
	sub, err := alice.Subscribe(ctx, s2.Msg.GetSession().GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	upd, err := alice.Sessions.UpdateSessionLabels(ctx, connect.NewRequest(&corev1.UpdateSessionLabelsRequest{
		SessionId: s2.Msg.GetSession().GetId(),
		Labels:    map[string]string{"student": "maya", "subject": "writing"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if upd.Msg.GetSession().GetLabels()["subject"] != "writing" {
		t.Fatalf("labels = %+v", upd.Msg.GetSession().GetLabels())
	}
	evs := sub.Collect(t, 1, 5*time.Second)
	if evs[0].GetPayload().GetSessionLabelsChanged() == nil {
		t.Fatalf("want SessionLabelsChanged, got %+v", evs[0].GetPayload())
	}
	if evs[0].GetPayload().GetSessionLabelsChanged().GetLabels()["subject"] != "writing" {
		t.Fatalf("event labels = %+v", evs[0].GetPayload().GetSessionLabelsChanged().GetLabels())
	}
}
