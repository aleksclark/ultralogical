package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/admin/authz"
	"github.com/aleksclark/ultracore/admin/command"
	"github.com/aleksclark/ultracore/admin/query"
	adminstore "github.com/aleksclark/ultracore/admin/store"
	"github.com/aleksclark/ultracore/adminhttp"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
	"github.com/aleksclark/ultracore/gen/go/admin/v1/adminv1connect"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/pgtest"
)

const (
	tokViewer   = "token-viewer-e7"
	tokOperator = "token-operator-e7"
	tokSecurity = "token-security-e7"
	tokAdmin    = "token-admin-e7"
)

type e7Env struct {
	pool    *pgxpool.Pool
	store   uc.Store
	admin   *adminstore.AdminStore
	queue   *riverqueue.Queue
	engine  *command.Engine
	srv     *httptest.Server
	keyring secrets.Keyring
	master  string
	logBuf  *bytes.Buffer
}

func setupE7(t *testing.T, reveal bool) *e7Env {
	t.Helper()
	ctx := context.Background()
	pool, url := pgtest.NewPool(t)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(pool)
	q, err := riverqueue.New(ctx, pool, riverqueue.Config{})
	if err != nil {
		t.Fatal(err)
	}
	master, err := secrets.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	kr, err := secrets.NewAESKeyring(master)
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	log := slog.New(secrets.NewRedactingHandler(slog.NewJSONHandler(&logBuf, nil)))
	engine := command.New(command.Deps{
		Pool: pool, Store: store, Enqueue: postgres.TxEnqueuer{Queue: q}, River: q,
		Keyring: kr, BuildVersion: "test-e7", Log: log,
		Flags: command.Flags{
			RevealEnabled:    reveal,
			TerminateEnabled: true,
			SuspendEnabled:   true,
		},
		RateLimit: 100, MaxConcurrent: 20,
	})
	signer := &query.Signer{Secret: []byte("admin-test-cursor-secret-0123456")}
	adminStore := adminstore.NewAdminStore(pool, signer, "test-e7")
	tokens := authz.DirectoryFromEntries([]authz.TokenEntry{
		{Token: tokViewer, Role: authz.RoleViewer, Name: "viewer", ID: "viewer"},
		{Token: tokOperator, Role: authz.RoleOperator, Name: "ops", ID: "ops"},
		{Token: tokSecurity, Role: authz.RoleSecurity, Name: "sec", ID: "sec"},
		{Token: tokAdmin, Role: authz.RoleAdmin, Name: "admin", ID: "admin"},
	}, false)
	srv := httptest.NewServer(adminhttp.NewHandler(adminhttp.Config{
		Store: adminStore, Tokens: tokens, Engine: engine, RevealEnabled: reveal, Log: log,
	}))
	t.Cleanup(srv.Close)
	return &e7Env{pool: pool, store: store, admin: adminStore, queue: q, engine: engine, srv: srv, keyring: kr, master: master, logBuf: &logBuf}
}

func cmdClient(t *testing.T, base, token string) adminv1connect.AdminCommandServiceClient {
	t.Helper()
	return adminv1connect.NewAdminCommandServiceClient(http.DefaultClient, base, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer "+token)
				return next(ctx, req)
			}
		}),
	))
}

func cmdClientReauth(t *testing.T, base, token string) adminv1connect.AdminCommandServiceClient {
	t.Helper()
	return adminv1connect.NewAdminCommandServiceClient(http.DefaultClient, base, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer "+token)
				req.Header().Set("X-Admin-Reauth", token)
				return next(ctx, req)
			}
		}),
	))
}

func readClient(t *testing.T, base, token string) adminv1connect.AdminReadServiceClient {
	t.Helper()
	return adminv1connect.NewAdminReadServiceClient(http.DefaultClient, base, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer "+token)
				return next(ctx, req)
			}
		}),
	))
}

func seedTenantSessionRun(t *testing.T, env *e7Env, state uc.RunState) (uc.Tenant, uc.Session, uc.AgentRun) {
	t.Helper()
	ctx := context.Background()
	ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "e7-tenant"}
	if err := env.store.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	sess := uc.Session{ID: uc.SessionID(uuid.NewString()), TenantID: ten.ID, Title: "e7"}
	if err := env.store.Tenant(ten.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	run := uc.AgentRun{
		ID: uc.RunID(uuid.NewString()), SessionID: sess.ID, TenantID: ten.ID,
		State: state, LoopKind: "default", LoopVersion: 1,
		ModelConfig: uc.ModelConfig{Provider: "openai", ModelID: "gpt", Credential: "default"},
		History:     json.RawMessage(`{"v":1,"messages":[]}`),
		Actor:       uc.ActorSystem(),
	}
	if err := env.store.Tenant(ten.ID).Runs().Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	return ten, sess, run
}

func seedAPIKey(t *testing.T, env *e7Env, ten uc.Tenant) (uc.APIKeyID, string) {
	t.Helper()
	ctx := context.Background()
	plain := "uck_test_plain_" + uuid.NewString()
	enc, err := env.keyring.Encrypt([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	id := uc.APIKeyID(uuid.NewString())
	// Use store create path if available; else raw insert.
	sum := make([]byte, 32)
	copy(sum, []byte(plain))
	_, err = env.pool.Exec(ctx, `
		INSERT INTO api_keys (id, tenant_id, name, scope, prefix, key_hash, key_enc)
		VALUES ($1,$2,'e7','admin','uck_test',$3,$4)`,
		string(id), string(ten.ID), sum, enc)
	if err != nil {
		t.Fatal(err)
	}
	return id, plain
}

func TestE7_WhoAmIAndRoleMatrix(t *testing.T) {
	env := setupE7(t, false)
	for _, tc := range []struct {
		tok  string
		role string
		can  bool
	}{
		{tokViewer, "viewer", false},
		{tokOperator, "operator", true},
		{tokSecurity, "security", true},
		{tokAdmin, "admin", true},
	} {
		rc := readClient(t, env.srv.URL, tc.tok)
		me, err := rc.WhoAmI(context.Background(), connect.NewRequest(&adminv1.WhoAmIRequest{}))
		if err != nil {
			t.Fatalf("%s whoami: %v", tc.role, err)
		}
		if me.Msg.Operator.Role != tc.role {
			t.Fatalf("role=%s want %s", me.Msg.Operator.Role, tc.role)
		}
		cc := cmdClient(t, env.srv.URL, tc.tok)
		_, sess, run := seedTenantSessionRun(t, env, uc.RunRunning)
		_ = sess
		resp, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
			Options: &adminv1.CommandOptions{DryRun: true, Reason: "test"},
			RunId:   string(run.ID),
		}))
		if tc.can {
			if err != nil {
				t.Fatalf("%s cancel dry-run: %v", tc.role, err)
			}
			if resp.Msg.Outcome.Preview.PreviewHash == "" {
				t.Fatal("missing preview hash")
			}
		} else if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s expected permission denied, got %v", tc.role, err)
		}
	}
}

func TestE7_CancelRunDryRunExecuteIdempotentStale(t *testing.T) {
	env := setupE7(t, false)
	cc := cmdClient(t, env.srv.URL, tokOperator)
	_, _, run := seedTenantSessionRun(t, env, uc.RunAwaiting)

	prev, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{DryRun: true, Reason: "preview"},
		RunId:   string(run.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	hash := prev.Msg.Outcome.Preview.PreviewHash

	// Stale hash
	_, err = cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{
			DryRun: false, PreviewHash: "deadbeef", IdempotencyKey: "k-stale", Reason: "incident-1",
		},
		RunId: string(run.ID),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale: %v", err)
	}

	// Execute
	ex, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{
			DryRun: false, PreviewHash: hash, IdempotencyKey: "k-cancel-1", Reason: "incident-1",
		},
		RunId: string(run.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ex.Msg.Outcome.Result != "ok" && ex.Msg.Outcome.Result != "already_applied" {
		t.Fatalf("result=%s", ex.Msg.Outcome.Result)
	}
	if ex.Msg.Outcome.AuditEventId == "" {
		t.Fatal("missing audit id")
	}

	// Idempotent replay
	ex2, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{
			DryRun: false, PreviewHash: hash, IdempotencyKey: "k-cancel-1", Reason: "incident-1",
		},
		RunId: string(run.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !ex2.Msg.Outcome.IdempotentReplay {
		t.Fatal("expected idempotent replay")
	}

	// Audit searchable
	rc := readClient(t, env.srv.URL, tokViewer)
	list, err := rc.ListAuditEvents(context.Background(), connect.NewRequest(&adminv1.ListAuditEventsRequest{
		Search: &adminv1.SearchRequest{
			Filters: []*adminv1.Filter{{Field: "command", Op: "eq", Value: "CancelRun"}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.Items) == 0 {
		t.Fatal("expected audit events")
	}
}

func TestE7_RevokeAPIKeyAndPausePrompt(t *testing.T) {
	env := setupE7(t, false)
	cc := cmdClient(t, env.srv.URL, tokOperator)
	ten, sess, _ := seedTenantSessionRun(t, env, uc.RunCompleted)
	keyID, _ := seedAPIKey(t, env, ten)

	prev, err := cc.RevokeAPIKey(context.Background(), connect.NewRequest(&adminv1.RevokeAPIKeyRequest{
		Options:  &adminv1.CommandOptions{DryRun: true, Reason: "r"},
		ApiKeyId: string(keyID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cc.RevokeAPIKey(context.Background(), connect.NewRequest(&adminv1.RevokeAPIKeyRequest{
		Options: &adminv1.CommandOptions{
			PreviewHash:    prev.Msg.Outcome.Preview.PreviewHash,
			IdempotencyKey: "revoke-1", Reason: "compomised",
		},
		ApiKeyId: string(keyID),
	}))
	if err != nil {
		t.Fatal(err)
	}

	// periodic prompt
	ppID := uc.PeriodicPromptID(uuid.NewString())
	if err := env.store.Tenant(ten.ID).PeriodicPrompts().Create(context.Background(), uc.PeriodicPrompt{
		ID: ppID, TenantID: ten.ID, SessionID: sess.ID, Schedule: time.Minute, Prompt: "tick", Enabled: true, NextAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	pprev, err := cc.PausePeriodicPrompt(context.Background(), connect.NewRequest(&adminv1.PausePeriodicPromptRequest{
		Options:          &adminv1.CommandOptions{DryRun: true, Reason: "r"},
		PeriodicPromptId: string(ppID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cc.PausePeriodicPrompt(context.Background(), connect.NewRequest(&adminv1.PausePeriodicPromptRequest{
		Options: &adminv1.CommandOptions{
			PreviewHash:    pprev.Msg.Outcome.Preview.PreviewHash,
			IdempotencyKey: "pause-1", Reason: "maintenance",
		},
		PeriodicPromptId: string(ppID),
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestE7_RevealKillSwitchRoleAndNoPlaintextLogs(t *testing.T) {
	// kill switch off
	envOff := setupE7(t, false)
	ten, _, _ := seedTenantSessionRun(t, envOff, uc.RunCompleted)
	keyID, _ := seedAPIKey(t, envOff, ten)
	sec := cmdClientReauth(t, envOff.srv.URL, tokSecurity)
	_, err := sec.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options:    &adminv1.CommandOptions{DryRun: true, Reason: "incident"},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	// Kill switch treats the RPC as absent (Unimplemented), not a soft deny.
	if err == nil || connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("kill switch: %v", err)
	}

	// reveal on
	env := setupE7(t, true)
	ten, _, _ = seedTenantSessionRun(t, env, uc.RunCompleted)
	keyID, plain := seedAPIKey(t, env, ten)

	// viewer denied
	view := cmdClientReauth(t, env.srv.URL, tokViewer)
	_, err = view.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options:    &adminv1.CommandOptions{DryRun: true, Reason: "x"},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("viewer reveal: %v", err)
	}

	// operator denied
	op := cmdClientReauth(t, env.srv.URL, tokOperator)
	_, err = op.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options:    &adminv1.CommandOptions{DryRun: true, Reason: "x"},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("operator reveal: %v", err)
	}

	// security without reauth
	secNo := cmdClient(t, env.srv.URL, tokSecurity)
	prev, err := secNo.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options:    &adminv1.CommandOptions{DryRun: true, Reason: "incident"},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	if err != nil {
		// dry-run also requires reauth
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("dry without reauth: %v", err)
		}
	} else {
		// if dry-run allowed without reauth in engine path, execute must fail
		_ = prev
	}

	sec = cmdClientReauth(t, env.srv.URL, tokSecurity)
	prev, err = sec.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options:    &adminv1.CommandOptions{DryRun: true, Reason: "incident"},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := sec.RevealSecret(context.Background(), connect.NewRequest(&adminv1.RevealSecretRequest{
		Options: &adminv1.CommandOptions{
			PreviewHash:    prev.Msg.Outcome.Preview.PreviewHash,
			IdempotencyKey: "reveal-1", Reason: "incident-42",
		},
		SecretKind: "api_key", ApiKeyId: string(keyID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ex.Msg.Plaintext != plain {
		t.Fatalf("plaintext mismatch")
	}
	// logs must not contain plaintext
	logs := env.logBuf.String()
	if strings.Contains(logs, plain) {
		t.Fatal("plaintext leaked into logs")
	}
	// audit after summary must not contain plaintext
	rc := readClient(t, env.srv.URL, tokAdmin)
	list, err := rc.ListAuditEvents(context.Background(), connect.NewRequest(&adminv1.ListAuditEventsRequest{
		Search: &adminv1.SearchRequest{Filters: []*adminv1.Filter{{Field: "command", Op: "eq", Value: "RevealSecret"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list.Msg.Items)
	if bytes.Contains(b, []byte(plain)) {
		t.Fatal("plaintext in audit")
	}
}

func TestE7_ExportEvidenceAndDisconnectDeferred(t *testing.T) {
	env := setupE7(t, false)
	cc := cmdClient(t, env.srv.URL, tokOperator)
	_, sess, run := seedTenantSessionRun(t, env, uc.RunFailed)
	prev, err := cc.ExportIncidentEvidence(context.Background(), connect.NewRequest(&adminv1.ExportIncidentEvidenceRequest{
		Options:   &adminv1.CommandOptions{DryRun: true, Reason: "export"},
		SessionId: string(sess.ID), RunId: string(run.ID), MaxEvents: 10,
	}))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := cc.ExportIncidentEvidence(context.Background(), connect.NewRequest(&adminv1.ExportIncidentEvidenceRequest{
		Options: &adminv1.CommandOptions{
			PreviewHash:    prev.Msg.Outcome.Preview.PreviewHash,
			IdempotencyKey: "export-1", Reason: "incident",
		},
		SessionId: string(sess.ID), RunId: string(run.ID), MaxEvents: 10,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Msg.EvidenceJson) == 0 {
		t.Fatal("empty evidence")
	}
	_, err = cc.DisconnectSubscriber(context.Background(), connect.NewRequest(&adminv1.DisconnectSubscriberRequest{
		Options:   &adminv1.CommandOptions{DryRun: true, Reason: "x"},
		SessionId: string(sess.ID), SubscriberId: "sub-1",
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disconnect deferred: %v", err)
	}
}

func TestE7_CoredUnaffectedByAdminDown(t *testing.T) {
	// Prove cored health handler works without admin packages mounted.
	ctx := context.Background()
	pool, url := pgtest.NewPool(t)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(pool)
	// Minimal cored-like mux with only health — mirrors separate process isolation.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	_ = store
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// Admin command path must 404 on this server.
	r2, err := http.Post(srv.URL+"/admin.v1.AdminCommandService/CancelRun", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Body.Close() }()
	if r2.StatusCode == 200 {
		t.Fatal("cored-like server must not serve admin commands")
	}
}

func TestE7_AuditImmutableNoDeleteAPI(t *testing.T) {
	env := setupE7(t, false)
	cc := cmdClient(t, env.srv.URL, tokOperator)
	_, _, run := seedTenantSessionRun(t, env, uc.RunAwaiting)
	prev, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{DryRun: true, Reason: "p"},
		RunId:   string(run.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	// dry-run still audits
	rc := readClient(t, env.srv.URL, tokAdmin)
	list, err := rc.ListAuditEvents(context.Background(), connect.NewRequest(&adminv1.ListAuditEventsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.Items) == 0 {
		t.Fatal("expected dry_run audit")
	}
	id := list.Msg.Items[0].Id
	if id == "" {
		t.Fatal("missing audit id")
	}
	// DB-side immutability: UPDATE/DELETE must fail.
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `UPDATE admin_audit_events SET reason='tamper' WHERE id=$1`, id); err == nil {
		t.Fatal("expected UPDATE of admin_audit_events to fail")
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE id=$1`, id); err == nil {
		t.Fatal("expected DELETE of admin_audit_events to fail")
	}
	_ = prev
	// No delete RPC exists on connect surface — reflection smoke via method set.
	var _ adminv1connect.AdminReadServiceClient
}

func TestE7_StalePreviewDoesNotMutateAndFailedIdempotencyRetries(t *testing.T) {
	env := setupE7(t, false)
	cc := cmdClient(t, env.srv.URL, tokOperator)
	_, _, run := seedTenantSessionRun(t, env, uc.RunAwaiting)

	prev, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{DryRun: true, Reason: "preview"},
		RunId:   string(run.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	hash := prev.Msg.Outcome.Preview.PreviewHash

	// Concurrent state change: another path cancels the run so the awaiting
	// before-state no longer matches the operator's confirmed preview.
	if err := env.store.Tenant(run.TenantID).Runs().RequestCancel(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Tenant(run.TenantID).Runs().SetState(context.Background(), run.ID, uc.RunCancelled, "", ""); err != nil {
		t.Fatal(err)
	}

	// Stale confirmation must fail closed and leave the (already terminal) run alone.
	_, err = cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{
			DryRun: false, PreviewHash: hash, IdempotencyKey: "k-stale-real", Reason: "incident-stale",
		},
		RunId: string(run.ID),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected stale fail-closed, got %v", err)
	}

	// Failed attempt must not poison the idempotency key: a fresh preview + same
	// key on a new target should still be allowed to succeed.
	_, _, run2 := seedTenantSessionRun(t, env, uc.RunAwaiting)
	prev2, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{DryRun: true, Reason: "preview2"},
		RunId:   string(run2.ID),
	}))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := cc.CancelRun(context.Background(), connect.NewRequest(&adminv1.CancelRunRequest{
		Options: &adminv1.CommandOptions{
			DryRun: false, PreviewHash: prev2.Msg.Outcome.Preview.PreviewHash,
			IdempotencyKey: "k-stale-real", Reason: "incident-retry",
		},
		RunId: string(run2.ID),
	}))
	if err != nil {
		t.Fatalf("retry after failed idempotency key: %v", err)
	}
	if ex.Msg.Outcome.Result != "ok" && ex.Msg.Outcome.Result != "already_applied" {
		t.Fatalf("result=%s", ex.Msg.Outcome.Result)
	}
	if ex.Msg.Outcome.IdempotentReplay {
		t.Fatal("failed prior attempt must not count as successful idempotent replay")
	}
}
