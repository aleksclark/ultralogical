package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/admin/query"
	adminstore "github.com/aleksclark/ultracore/admin/store"
	"github.com/aleksclark/ultracore/adminhttp"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
	"github.com/aleksclark/ultracore/gen/go/admin/v1/adminv1connect"
	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
	ultrahttp "github.com/aleksclark/ultracore/http"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/pgtest"
)

const adminToken = "test-operator-token-not-a-tenant-key"

func setupAdmin(t *testing.T) (*adminstore.AdminStore, *pgxpool.Pool, adminv1connect.AdminReadServiceClient, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	pool, url := pgtest.NewPool(t)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if _, err := riverqueue.New(ctx, pool, riverqueue.Config{}); err != nil {
		t.Fatal(err)
	}
	signer := &query.Signer{Secret: []byte("admin-test-cursor-secret-0123456")}
	store := adminstore.NewAdminStore(pool, signer, "test")
	srv := httptest.NewServer(adminhttp.NewHandler(adminhttp.Config{
		Store: store, Token: adminToken, Log: nil,
	}))
	t.Cleanup(srv.Close)
	client := adminv1connect.NewAdminReadServiceClient(srv.Client(), srv.URL)
	return store, pool, client, srv
}

func auth() connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+adminToken)
			return next(ctx, req)
		}
	}))
}

func authedClient(t *testing.T, base string) adminv1connect.AdminReadServiceClient {
	t.Helper()
	return adminv1connect.NewAdminReadServiceClient(http.DefaultClient, base, auth())
}

func seedTenants(t *testing.T, pool *pgxpool.Pool, n int) []uc.Tenant {
	t.Helper()
	ctx := context.Background()
	s := postgres.NewStore(pool)
	var out []uc.Tenant
	for i := 0; i < n; i++ {
		ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: fmt.Sprintf("tenant-%03d", i)}
		if err := s.Tenants().Create(ctx, ten); err != nil {
			t.Fatal(err)
		}
		out = append(out, ten)
	}
	return out
}

func TestAdminAuthFailClosed(t *testing.T) {
	_, _, _, srv := setupAdmin(t)
	// missing token
	c := adminv1connect.NewAdminReadServiceClient(srv.Client(), srv.URL)
	_, err := c.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing token: %v", err)
	}
	// wrong token
	bad := adminv1connect.NewAdminReadServiceClient(srv.Client(), srv.URL, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("Authorization", "Bearer wrong-token")
				return next(ctx, req)
			}
		}),
	))
	_, err = bad.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestCoredHasNoAdminRoutes(t *testing.T) {
	// Mount only cored handler and prove admin paths 404.
	ctx := context.Background()
	pool, url := pgtest.NewPool(t)
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(pool)
	h := ultrahttp.NewHandler(ultrahttp.Config{
		Store: store,
		Auth:  uc.NewAPIKeyAuthenticator(store),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	paths := []string{
		"/admin.v1.AdminReadService/ListTenants",
		"/admin.v1.AdminReadService/GetRuntimeHealth",
		"/admin.v1.AdminReadService/DescribeCollection",
	}
	for _, p := range paths {
		resp, err := http.Post(srv.URL+p, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			// Connect may return 415/404 depending on content-type; never 200.
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("cored served admin path %s", p)
			}
		}
	}
	// Public core connect surface is distinct from admin.
	if corev1connect.TenantServiceName == "" {
		t.Fatal("missing core tenant service name")
	}
}

func TestCoredPackageDepsExcludeAdmin(t *testing.T) {
	// A5.1 binary dependency fence: cored must not import admin packages.
	// Mirrors scripts/check-admin-isolation.sh go list -deps check.
	cmd := exec.Command("go", "list", "-deps", "./cmd/cored")
	cmd.Dir = ".."
	// When running as package admin_test under ./admin, module root is parent.
	// Prefer module root via go env.
	if root, err := exec.Command("go", "env", "GOMOD").Output(); err == nil {
		mod := strings.TrimSpace(string(root))
		if strings.HasSuffix(mod, "/go.mod") {
			cmd.Dir = strings.TrimSuffix(mod, "/go.mod")
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ./cmd/cored: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "/admin") || strings.Contains(line, "/gen/go/admin") {
			// Allow nothing under ultracore admin surfaces.
			if strings.Contains(line, "github.com/aleksclark/ultracore/admin") ||
				strings.Contains(line, "github.com/aleksclark/ultracore/gen/go/admin") {
				t.Fatalf("cored depends on admin package: %s", line)
			}
		}
	}
}

func TestAdminListTenantsPaginationAndSearch(t *testing.T) {
	_, pool, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	seedTenants(t, pool, 120)

	// First page.
	resp, err := client.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{
		Search: &adminv1.SearchRequest{Page: &adminv1.PageRequest{Limit: 50}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Items) != 50 || !resp.Msg.Page.HasMore || resp.Msg.Page.NextCursor == "" {
		t.Fatalf("page1: n=%d more=%v cur=%q", len(resp.Msg.Items), resp.Msg.Page.HasMore, resp.Msg.Page.NextCursor)
	}
	seen := map[string]bool{}
	for _, it := range resp.Msg.Items {
		if seen[it.Id] {
			t.Fatalf("duplicate %s", it.Id)
		}
		seen[it.Id] = true
	}
	// Next page.
	resp2, err := client.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{
		Search: &adminv1.SearchRequest{Page: &adminv1.PageRequest{Limit: 50, Cursor: resp.Msg.Page.NextCursor}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range resp2.Msg.Items {
		if seen[it.Id] {
			t.Fatalf("duplicate across pages %s", it.Id)
		}
		seen[it.Id] = true
	}
	// Over-limit rejected.
	_, err = client.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{
		Search: &adminv1.SearchRequest{Page: &adminv1.PageRequest{Limit: 251}},
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("limit 251: %v", err)
	}
	// Invalid filter field rejected.
	_, err = client.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{
		Search: &adminv1.SearchRequest{Filters: []*adminv1.Filter{{Field: "password", Op: "eq", Value: "x"}}},
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad field: %v", err)
	}
	// Text.
	resp3, err := client.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{
		Search: &adminv1.SearchRequest{Query: "tenant-001", Page: &adminv1.PageRequest{Limit: 10}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp3.Msg.Items) == 0 {
		t.Fatal("search returned nothing")
	}
}

func TestAdminCursorConcurrentInserts(t *testing.T) {
	store, pool, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	ctx := context.Background()
	s := postgres.NewStore(pool)

	// Seed base set.
	for i := 0; i < 200; i++ {
		ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: fmt.Sprintf("base-%04d", i)}
		if err := s.Tenants().Create(ctx, ten); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: fmt.Sprintf("conc-%04d", i)}
			_ = s.Tenants().Create(ctx, ten)
			time.Sleep(time.Millisecond)
		}
	}()

	seen := map[string]bool{}
	var cursor string
	for {
		req := &adminv1.ListTenantsRequest{Search: &adminv1.SearchRequest{
			Page: &adminv1.PageRequest{Limit: 40, Cursor: cursor},
		}}
		resp, err := client.ListTenants(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range resp.Msg.Items {
			if seen[it.Id] {
				t.Fatalf("duplicate during concurrent insert: %s", it.Id)
			}
			seen[it.Id] = true
		}
		if !resp.Msg.Page.HasMore {
			break
		}
		cursor = resp.Msg.Page.NextCursor
	}
	wg.Wait()
	if len(seen) < 200 {
		t.Fatalf("traversal too small: %d", len(seen))
	}
	_ = store
}

func TestAdminSecretNonDisclosure(t *testing.T) {
	_, pool, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	ctx := context.Background()
	s := postgres.NewStore(pool)

	ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "sec"}
	if err := s.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	raw := harness.CanaryAPIKey
	// Use a realistic-looking key shape if Generate exists; else store canary as enc payload metadata only.
	prefix := "uck_canary"
	hash := uc.HashAPIKey(raw)
	if err := s.APIKeys().Create(ctx, uc.APIKey{
		ID: uc.APIKeyID(uuid.NewString()), TenantID: ten.ID, Name: "canary",
		Scope: uc.KeyScopeAdmin, Prefix: prefix, KeyHash: hash, KeyEnc: []byte("ciphertext-not-plaintext"),
	}); err != nil {
		t.Fatal(err)
	}
	// Credential with canary as would-be secret — only enc_payload stored.
	if err := s.Tenant(ten.ID).Credentials().Put(ctx, uc.Credential{
		Kind: "openai", Name: "default", EncPayload: []byte("enc:" + raw),
	}); err != nil {
		t.Fatalf("cred seed: %v", err)
	}

	keys, err := client.ListAPIKeys(ctx, connect.NewRequest(&adminv1.ListAPIKeysRequest{
		Search: &adminv1.SearchRequest{Filters: []*adminv1.Filter{{Field: "tenant_id", Op: "eq", Value: string(ten.ID)}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	blob := fmt.Sprintf("%v", keys.Msg)
	if strings.Contains(blob, raw) {
		t.Fatal("raw API key leaked in ListAPIKeys")
	}
	creds, err := client.ListCredentials(ctx, connect.NewRequest(&adminv1.ListCredentialsRequest{
		Search: &adminv1.SearchRequest{Filters: []*adminv1.Filter{{Field: "tenant_id", Op: "eq", Value: string(ten.ID)}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	blob = fmt.Sprintf("%v", creds.Msg)
	if strings.Contains(blob, raw) {
		t.Fatal("credential plaintext leaked")
	}
	if len(creds.Msg.Items) != 1 || !creds.Msg.Items[0].Encrypted || creds.Msg.Items[0].CiphertextBytes == 0 {
		t.Fatalf("credential metadata: %+v", creds.Msg.Items)
	}
}

func TestAdminEventsScalePagination(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	_, pool, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	ctx := context.Background()
	s := postgres.NewStore(pool)

	ten := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "scale"}
	if err := s.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	sess := uc.Session{ID: uc.SessionID(uuid.NewString()), Title: "scale-sess"}
	if err := s.Tenant(ten.ID).Sessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// Bulk insert events (substantial set for CI; document 100k path).
	const n = 5000
	// Use COPY-like multi-value inserts for speed.
	const chunk = 500
	for start := 1; start <= n; start += chunk {
		end := start + chunk - 1
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO session_events (session_id, seq, actor_type, actor_id, kind, payload) VALUES `)
		args := []any{}
		for seq := start; seq <= end; seq++ {
			if seq > start {
				b.WriteByte(',')
			}
			i := len(args)
			fmt.Fprintf(&b, "($%d,$%d,'service','t','user_message',$%d)", i+1, i+2, i+3)
			args = append(args, string(sess.ID), seq, []byte(`{"text":"hello"}`))
		}
		if _, err := pool.Exec(ctx, b.String(), args...); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = pool.Exec(ctx, `UPDATE sessions SET last_seq = $2 WHERE id = $1`, string(sess.ID), n)

	start := time.Now()
	var cursor string
	total := 0
	pages := 0
	for {
		resp, err := client.ListEvents(ctx, connect.NewRequest(&adminv1.ListEventsRequest{
			Search: &adminv1.SearchRequest{
				Filters: []*adminv1.Filter{{Field: "session_id", Op: "eq", Value: string(sess.ID)}},
				Page:    &adminv1.PageRequest{Limit: 250, Cursor: cursor},
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		pages++
		total += len(resp.Msg.Items)
		if pages == 1 && time.Since(start) > 2*time.Second {
			t.Fatalf("first page too slow: %s", time.Since(start))
		}
		if !resp.Msg.Page.HasMore {
			break
		}
		cursor = resp.Msg.Page.NextCursor
	}
	if total != n {
		t.Fatalf("got %d events want %d", total, n)
	}
	t.Logf("paginated %d events in %d pages in %s", total, pages, time.Since(start))
}

func TestAdminDescribeAndHealth(t *testing.T) {
	_, _, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	desc, err := client.DescribeCollection(context.Background(), connect.NewRequest(&adminv1.DescribeCollectionRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.Msg.Collections) < 10 {
		t.Fatalf("collections=%d", len(desc.Msg.Collections))
	}
	h, err := client.GetRuntimeHealth(context.Background(), connect.NewRequest(&adminv1.GetRuntimeHealthRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !h.Msg.Health.RiverSchemaPresent {
		t.Fatal("expected river schema")
	}
	if h.Msg.Health.BuildVersion == "" {
		t.Fatal("missing build version")
	}
}

func TestAdminCrossTenantVisible(t *testing.T) {
	_, pool, _, srv := setupAdmin(t)
	client := authedClient(t, srv.URL)
	tenants := seedTenants(t, pool, 2)
	ctx := context.Background()
	s := postgres.NewStore(pool)
	for _, ten := range tenants {
		sess := uc.Session{ID: uc.SessionID(uuid.NewString()), Title: "s-" + string(ten.ID)[:8]}
		if err := s.Tenant(ten.ID).Sessions().Create(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := client.ListSessions(ctx, connect.NewRequest(&adminv1.ListSessionsRequest{
		Search: &adminv1.SearchRequest{Page: &adminv1.PageRequest{Limit: 50}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Items) < 2 {
		t.Fatalf("expected cross-tenant sessions, got %d", len(resp.Msg.Items))
	}
	// Filter to one tenant.
	resp2, err := client.ListSessions(ctx, connect.NewRequest(&adminv1.ListSessionsRequest{
		Search: &adminv1.SearchRequest{
			Filters: []*adminv1.Filter{{Field: "tenant_id", Op: "eq", Value: string(tenants[0].ID)}},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range resp2.Msg.Items {
		if it.TenantId != string(tenants[0].ID) {
			t.Fatalf("filter leak: %s", it.TenantId)
		}
	}
}
