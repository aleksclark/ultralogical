package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/postgres"
	"github.com/aleksclark/ultralogical/testkit/pgtest"
)

func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	ctx := context.Background()
	pool, url := pgtest.NewPool(t)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	return postgres.NewStore(pool)
}

func seedOrgUser(t *testing.T, s *postgres.Store, orgName, email string) (ultra.Org, ultra.User) {
	t.Helper()
	ctx := context.Background()
	org := ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: orgName}
	user := ultra.User{ID: ultra.UserID(uuid.NewString()), Email: email}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	if err := s.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := s.Orgs().AddMember(ctx, ultra.OrgMember{OrgID: org.ID, UserID: user.ID, Role: ultra.OrgRoleOwner}); err != nil {
		t.Fatal(err)
	}
	return org, user
}

func TestOrgMembership(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org, user := seedOrgUser(t, s, "acme", "alice@example.com")

	role, err := s.Orgs().MemberRole(ctx, org.ID, user.ID)
	if err != nil || role != ultra.OrgRoleOwner {
		t.Fatalf("MemberRole = %q, %v", role, err)
	}
	if _, err := s.Orgs().MemberRole(ctx, org.ID, ultra.UserID(uuid.NewString())); !errors.Is(err, ultra.ErrNotFound) {
		t.Fatalf("non-member role error = %v, want ErrNotFound", err)
	}
	members, err := s.Orgs().ListMembers(ctx, org.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("ListMembers = %v, %v", members, err)
	}
	got, err := s.Users().GetByEmail(ctx, "alice@example.com")
	if err != nil || got.ID != user.ID {
		t.Fatalf("GetByEmail = %v, %v", got, err)
	}
}

func TestSessionTenantScoping(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	orgA, _ := seedOrgUser(t, s, "org-a", "a@example.com")
	orgB, _ := seedOrgUser(t, s, "org-b", "b@example.com")

	sess := ultra.Session{ID: ultra.SessionID(uuid.NewString()), Title: "work"}
	if err := s.Org(orgA.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// Same-org read works.
	if _, err := s.Org(orgA.ID).Sessions().Get(ctx, sess.ID); err != nil {
		t.Fatalf("same-org get: %v", err)
	}
	// Cross-org read is structurally not-found.
	if _, err := s.Org(orgB.ID).Sessions().Get(ctx, sess.ID); !errors.Is(err, ultra.ErrNotFound) {
		t.Fatalf("cross-org get error = %v, want ErrNotFound", err)
	}
	// Cross-org list is empty.
	list, err := s.Org(orgB.ID).Sessions().List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-org list = %v, %v", list, err)
	}
	// Cross-org append denied.
	if _, err := s.Org(orgB.ID).Events().Append(ctx, sess.ID, ultra.Event{
		Actor: ultra.Actor{Type: ultra.ActorUser}, Kind: ultra.EventKindUserMessage,
	}); !errors.Is(err, ultra.ErrNotFound) {
		t.Fatalf("cross-org append error = %v, want ErrNotFound", err)
	}
	// Directory lookup.
	owner, err := s.SessionOrg(ctx, sess.ID)
	if err != nil || owner != orgA.ID {
		t.Fatalf("SessionOrg = %v, %v", owner, err)
	}
}

func TestEventAppendGaplessSeq(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org, _ := seedOrgUser(t, s, "acme", "alice@example.com")
	sess := ultra.Session{ID: ultra.SessionID(uuid.NewString())}
	if err := s.Org(org.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	events := s.Org(org.ID).Events()
	for i := 1; i <= 5; i++ {
		seq, err := events.Append(ctx, sess.ID, ultra.Event{
			Actor:   ultra.Actor{Type: ultra.ActorUser, ID: "u1"},
			Kind:    ultra.EventKindUserMessage,
			Payload: []byte(`{"text":"hi"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if seq != int64(i) {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}

	got, err := events.Range(ctx, sess.ID, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("Range(from=2) seqs wrong: %+v", got)
	}
}

func TestTxRollback(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org, _ := seedOrgUser(t, s, "acme", "alice@example.com")

	sessID := ultra.SessionID(uuid.NewString())
	sentinel := errors.New("boom")
	err := s.Tx(ctx, func(txs ultra.Store) error {
		if err := txs.Org(org.ID).Sessions().Create(ctx, ultra.Session{ID: sessID}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx error = %v", err)
	}
	if _, err := s.Org(org.ID).Sessions().Get(ctx, sessID); !errors.Is(err, ultra.ErrNotFound) {
		t.Fatalf("rolled-back session visible: %v", err)
	}
}
