// Package e2e is the functional API suite: it drives a fully real stack
// (Postgres, migrations, cored as a child process) through the generated
// client. Acceptance test IDs refer to plan/phase_0.md.
package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

func createSession(t *testing.T, c *testclient.Client, tenantID, title string) *corev1.Session {
	t.Helper()
	resp, err := c.Sessions.CreateSession(context.Background(), connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: tenantID,
		Title: title,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.GetSession()
}

// A0.1 — CreateSession → GetSession roundtrip via the generated Go client.
func TestA01_SessionRoundtrip(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	created := createSession(t, alice, string(stack.TenantA.ID), "phase zero")
	if created.GetId() == "" || created.GetTenantId() != string(stack.TenantA.ID) || created.GetTitle() != "phase zero" {
		t.Fatalf("created session malformed: %+v", created)
	}

	got, err := alice.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{
		SessionId: created.GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	s := got.Msg.GetSession()
	if s.GetId() != created.GetId() || s.GetTitle() != created.GetTitle() || s.GetTenantId() != created.GetTenantId() {
		t.Fatalf("roundtrip mismatch: created %+v, got %+v", created, s)
	}

	list, err := alice.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{
		TenantId: string(stack.TenantA.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.Msg.GetSessions()); n != 1 {
		t.Fatalf("ListSessions returned %d sessions, want 1", n)
	}
}

// A0.2 — Append fans out to concurrent subscribers with seq 1, and the
// mutation response seq matches the delivered seq.
func TestA02_AppendFanout(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	sess := createSession(t, alice, string(stack.TenantA.ID), "fanout")

	sub1, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub1.Close()
	sub2, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()

	seq, err := alice.AppendUserMessage(ctx, sess.GetId(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("append seq = %d, want 1", seq)
	}

	for i, sub := range []*testclient.Subscription{sub1, sub2} {
		events := sub.Collect(t, 1, 10*time.Second)
		ev := events[0]
		if ev.GetSeq() != seq {
			t.Fatalf("subscriber %d: seq = %d, want %d", i+1, ev.GetSeq(), seq)
		}
		if ev.GetPayload().GetUserMessage().GetText() != "hello world" {
			t.Fatalf("subscriber %d: payload mismatch: %+v", i+1, ev.GetPayload())
		}
		if ev.GetActor().GetKind() == "" {
			t.Fatalf("subscriber %d: empty actor kind: %v", i+1, ev.GetActor())
		}
	}
}

// A0.3 — Resume contract: from_seq delivers exactly the events after it, in
// order, without duplicates; from_seq=0 replays the full log.
func TestA03_ResumeContract(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	sess := createSession(t, alice, string(stack.TenantA.ID), "resume")

	// Event 1 observed live, then the subscriber disconnects.
	sub, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.AppendUserMessage(ctx, sess.GetId(), "one"); err != nil {
		t.Fatal(err)
	}
	sub.Collect(t, 1, 10*time.Second)
	sub.Close()

	// Two more events while disconnected.
	for _, text := range []string{"two", "three"} {
		if _, err := alice.AppendUserMessage(ctx, sess.GetId(), text); err != nil {
			t.Fatal(err)
		}
	}

	// Resume from seq 1: exactly events 2 and 3, in order.
	resumed, err := alice.Subscribe(ctx, sess.GetId(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	events := resumed.Collect(t, 2, 10*time.Second)
	if events[0].GetSeq() != 2 || events[1].GetSeq() != 3 {
		t.Fatalf("resumed seqs = %d,%d want 2,3", events[0].GetSeq(), events[1].GetSeq())
	}
	if events[0].GetPayload().GetUserMessage().GetText() != "two" ||
		events[1].GetPayload().GetUserMessage().GetText() != "three" {
		t.Fatalf("resumed payloads wrong: %v", events)
	}

	// Full replay from zero: 1..3 gapless.
	replay, err := alice.Subscribe(ctx, sess.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	all := replay.Collect(t, 3, 10*time.Second)
	for i, ev := range all {
		if ev.GetSeq() != int64(i+1) {
			t.Fatalf("replay event %d has seq %d", i, ev.GetSeq())
		}
	}
}

// A0.6 — Tenant isolation: cross-org access is denied indistinguishably from
// not-found, with no existence oracle.
func TestA06_TenantIsolation(t *testing.T) {
	t.Parallel()
	stack := harness.Up(t)
	alice := stack.AliceClient()
	bob := stack.BobClient()
	ctx := context.Background()

	sess := createSession(t, alice, string(stack.TenantA.ID), "private")

	assertNotFound := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: cross-tenant access succeeded", name)
		}
		var cerr *connect.Error
		if !errors.As(err, &cerr) || cerr.Code() != connect.CodeNotFound {
			t.Fatalf("%s: error = %v, want not_found", name, err)
		}
	}

	// GetSession on a real (foreign) session vs a nonexistent one must be
	// indistinguishable.
	_, errForeign := bob.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: sess.GetId()}))
	assertNotFound("GetSession(foreign)", errForeign)
	_, errMissing := bob.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: "00000000-0000-0000-0000-000000000000"}))
	assertNotFound("GetSession(missing)", errMissing)
	var cf, cm *connect.Error
	errors.As(errForeign, &cf)
	errors.As(errMissing, &cm)
	if cf.Message() != cm.Message() || cf.Code() != cm.Code() {
		t.Fatalf("existence oracle: foreign=%q missing=%q", cf.Message(), cm.Message())
	}

	// ListSessions across orgs.
	_, err := bob.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{TenantId: string(stack.TenantA.ID)}))
	assertNotFound("ListSessions(org A as bob)", err)

	// Append into a foreign session.
	_, err = bob.AppendUserMessage(ctx, sess.GetId(), "intrusion")
	assertNotFound("Append(foreign)", err)

	// Subscribe to a foreign session: denied at or before first receive.
	sub, err := bob.Subscribe(ctx, sess.GetId(), 0)
	if err == nil {
		_, err = sub.Next()
		sub.Close()
	}
	assertNotFound("Subscribe(foreign)", err)

	// Bob's own org list contains only org B sessions.
	bobSess := createSession(t, bob, string(stack.TenantB.ID), "bobs")
	list, err := bob.Sessions.ListSessions(ctx, connect.NewRequest(&corev1.ListSessionsRequest{TenantId: string(stack.TenantB.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.GetSessions()) != 1 || list.Msg.GetSessions()[0].GetId() != bobSess.GetId() {
		t.Fatalf("bob's session list wrong: %v", list.Msg.GetSessions())
	}

	// Unauthenticated requests are rejected.
	anon := testclient.New(stack.BaseURL, "not-a-token")
	_, err = anon.Sessions.GetSession(ctx, connect.NewRequest(&corev1.GetSessionRequest{SessionId: sess.GetId()}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("anonymous access error = %v, want unauthenticated", err)
	}
}

// Tenant lifecycle: CreateTenant (admin) + API keys.
func TestA03_TenantAndKeys(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := stack.AliceClient()

	created, err := alice.Tenants.CreateTenant(ctx, connect.NewRequest(&corev1.CreateTenantRequest{Name: "newco"}))
	if err != nil {
		t.Fatal(err)
	}
	if created.Msg.GetTenant().GetName() != "newco" {
		t.Fatalf("name = %q", created.Msg.GetTenant().GetName())
	}
	if created.Msg.GetAdminKey() == "" {
		t.Fatal("expected admin key once")
	}
	// New tenant's admin key works; sessions-scope cannot create tenants.
	newAdmin := testclient.New(stack.BaseURL, created.Msg.GetAdminKey())
	sessKey, err := newAdmin.Tenants.CreateAPIKey(ctx, connect.NewRequest(&corev1.CreateAPIKeyRequest{
		TenantId: created.Msg.GetTenant().GetId(), Name: "sess", Scope: corev1.KeyScope_KEY_SCOPE_SESSIONS,
	}))
	if err != nil {
		t.Fatal(err)
	}
	sessClient := testclient.New(stack.BaseURL, sessKey.Msg.GetRawKey())
	if _, err := sessClient.Tenants.CreateTenant(ctx, connect.NewRequest(&corev1.CreateTenantRequest{Name: "nope"})); err == nil {
		t.Fatal("sessions key created a tenant")
	}
	// Cross-tenant get is not-found.
	if _, err := alice.Tenants.GetTenant(ctx, connect.NewRequest(&corev1.GetTenantRequest{TenantId: created.Msg.GetTenant().GetId()})); err == nil {
		t.Fatal("cross-tenant get succeeded")
	}
}


