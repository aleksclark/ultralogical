package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/testkit/pgtest"
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

func seedTenant(t *testing.T, s *postgres.Store, name string) (uc.Tenant, string) {
	t.Helper()
	ctx := context.Background()
	tenant := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: name}
	if err := s.Tenants().Create(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	raw, prefix, err := uc.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	// Store without encryption for unit tests that only need hash lookup.
	if err := s.APIKeys().Create(ctx, uc.APIKey{
		ID: uc.APIKeyID(uuid.NewString()), TenantID: tenant.ID, Name: "test",
		Scope: uc.KeyScopeAdmin, Prefix: prefix, KeyHash: uc.HashAPIKey(raw), KeyEnc: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}
	return tenant, raw
}

func TestAPIKeyAuth(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tenant, raw := seedTenant(t, s, "acme")
	auth := uc.NewAPIKeyAuthenticator(s)
	id, err := auth.Authenticate(ctx, raw)
	if err != nil || id.TenantID != tenant.ID || id.Scope != uc.KeyScopeAdmin {
		t.Fatalf("auth = %+v, %v", id, err)
	}
	if _, err := auth.Authenticate(ctx, "uck_nope"); !errors.Is(err, uc.ErrPermissionDenied) {
		t.Fatalf("bad key: %v", err)
	}
	// Revoke fails closed.
	keys, _ := s.APIKeys().List(ctx, tenant.ID)
	if err := s.APIKeys().Revoke(ctx, tenant.ID, keys[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, raw); !errors.Is(err, uc.ErrPermissionDenied) {
		t.Fatalf("revoked key: %v", err)
	}
}

func TestSessionTenantScoping(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	orgA, _ := seedTenant(t, s, "org-a")
	orgB, _ := seedTenant(t, s, "org-b")

	sess := uc.Session{ID: uc.SessionID(uuid.NewString()), Title: "work"}
	if err := s.Tenant(orgA.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// Same-org read works.
	if _, err := s.Tenant(orgA.ID).Sessions().Get(ctx, sess.ID); err != nil {
		t.Fatalf("same-org get: %v", err)
	}
	// Cross-org read is structurally not-found.
	if _, err := s.Tenant(orgB.ID).Sessions().Get(ctx, sess.ID); !errors.Is(err, uc.ErrNotFound) {
		t.Fatalf("cross-org get error = %v, want ErrNotFound", err)
	}
	// Cross-org list is empty.
	list, err := s.Tenant(orgB.ID).Sessions().List(ctx, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-org list = %v, %v", list, err)
	}
	// Cross-org append denied.
	if _, err := s.Tenant(orgB.ID).Events().Append(ctx, sess.ID, uc.Event{
		Actor: uc.Actor{Kind: uc.ActorKindService, ID: "t"}, Kind: uc.EventKindUserMessage,
	}); !errors.Is(err, uc.ErrNotFound) {
		t.Fatalf("cross-org append error = %v, want ErrNotFound", err)
	}
	// Directory lookup.
	owner, err := s.SessionTenant(ctx, sess.ID)
	if err != nil || owner != orgA.ID {
		t.Fatalf("SessionTenant = %v, %v", owner, err)
	}
}

func TestEventAppendGaplessSeq(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org, _ := seedTenant(t, s, "acme")
	sess := uc.Session{ID: uc.SessionID(uuid.NewString())}
	if err := s.Tenant(org.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	events := s.Tenant(org.ID).Events()
	for i := 1; i <= 5; i++ {
		seq, err := events.Append(ctx, sess.ID, uc.Event{
			Actor:   uc.Actor{Kind: uc.ActorKindService, ID: "u1"},
			Kind:    uc.EventKindUserMessage,
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
	org, _ := seedTenant(t, s, "acme")

	sessID := uc.SessionID(uuid.NewString())
	sentinel := errors.New("boom")
	err := s.Tx(ctx, func(txs uc.Store) error {
		if err := txs.Tenant(org.ID).Sessions().Create(ctx, uc.Session{ID: sessID}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Tx error = %v", err)
	}
	if _, err := s.Tenant(org.ID).Sessions().Get(ctx, sessID); !errors.Is(err, uc.ErrNotFound) {
		t.Fatalf("rolled-back session visible: %v", err)
	}
}
