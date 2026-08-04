// Package harness boots the real stack for functional tests: real Postgres
// (shared container, fresh database per test), migrations, cored and worker
// as real child processes on random ports, plus a modelscript server (the
// only substituted component — it replaces the LLM vendor at the network
// boundary). Tests interact only through the public API via testclient.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/modelscript"
	"github.com/aleksclark/ultracore/testkit/pgtest"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// Seeded identities. The harness provisions two orgs with one user each so
// tenant-isolation is testable out of the box.
const (
	// CanaryAPIKey is the secret embedded in tenant A's seeded inference
	// credential. Tests assert it never leaks into events, logs, or errors.
	CanaryAPIKey = "sk-canary-XyZZy-0451-leak-detector"
)

// Options tune the harness.
type Options struct {
	// SeedCredential controls whether org A gets a default openai
	// credential pointing at the modelscript server. Default true.
	SeedCredential bool
	// WorkerEnv adds environment variables to the worker process.
	WorkerEnv      []string
	UltradReplicas int
	WorkerReplicas int
}

// Opt mutates Options.
type Opt func(*Options)

// WithoutSeedCredential leaves org A credential-less (for A1.7a).
func WithoutSeedCredential() Opt { return func(o *Options) { o.SeedCredential = false } }

// WithWorkerEnv adds env vars to the worker process.
func WithReplicas(cored, workers int) Opt {
	return func(o *Options) { o.UltradReplicas = cored; o.WorkerReplicas = workers }
}

func WithWorkerEnv(kv ...string) Opt {
	return func(o *Options) { o.WorkerEnv = append(o.WorkerEnv, kv...) }
}

// Stack is a running cored + worker + database + modelscript with seeded
// identities.
type Stack struct {
	BaseURL     string
	DatabaseURL string
	MasterKey   string
	TenantA        uc.Tenant
	TenantB        uc.Tenant
	KeyA        string
	KeyB        string
	Store       *postgres.Store
	Model       *modelscript.Server

	workerMu sync.Mutex
	// workers is indexed: tests kill and restart a specific worker, so
	// "worker 0 died and worker 1 finished the work" is a statement about
	// identifiable processes rather than about whichever one was last started.
	workers    []*exec.Cmd
	ultradMu   sync.Mutex
	ultradCmds []*exec.Cmd
	// ReplicaBaseURLs addresses each cored replica directly. BaseURL is the
	// round-robin ingress in front of all of them.
	ReplicaBaseURLs []string
	ingress         *httptest.Server
	workerEnv       []string
	ultradBin       string
	workerBin       string
	logs            *logCapture
	t               *testing.T
}

// logCapture tees a child process's stderr into memory so leak sweeps can
// assert on everything cored and the workers logged, while still forwarding
// output for debugging.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf.Write(p)
	c.mu.Unlock()
	return os.Stderr.Write(p)
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Logs returns everything cored and worker processes have written to stderr
// so far. Used by the redaction sweep.
func (s *Stack) Logs() string { return s.logs.String() }

const BezalelImage = "ultracore/bezalel:phase2-test"

var (
	buildOnce   sync.Once
	buildErr    error
	binDir      string
	bezalelOnce sync.Once
	bezalelErr  error
)

// binaries builds cored and worker once per test process.
func binaries(t *testing.T) (string, string) {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ultra-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		binDir = dir
		for _, target := range []string{"cored", "coreworker"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, target),
				"github.com/aleksclark/ultracore/cmd/"+target)
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %w\n%s", target, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(binDir, "cored"), filepath.Join(binDir, "coreworker")
}

// EnsureBezalelImage builds the pinned real Bezalel image once per test process.
func EnsureBezalelImage(t *testing.T) string {
	t.Helper()
	bezalelOnce.Do(func() {
		cmd := exec.Command("docker", "build", "-t", BezalelImage,
			"https://github.com/aleksclark/bezalel.git#2504ff3152d0ee4e999210641d50ebf5483aa120")
		if out, err := cmd.CombinedOutput(); err != nil {
			bezalelErr = fmt.Errorf("build Bezalel: %w\n%s", err, out)
		}
	})
	if bezalelErr != nil {
		t.Fatal(bezalelErr)
	}
	return BezalelImage
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// Up boots a full stack and registers cleanup on the test.
func Up(t *testing.T, opts ...Opt) *Stack {
	t.Helper()
	options := Options{SeedCredential: true, UltradReplicas: 1, WorkerReplicas: 1}
	for _, opt := range opts {
		opt(&options)
	}
	ctx := context.Background()

	ultradBin, workerBin := binaries(t)
	EnsureBezalelImage(t)
	dbURL := pgtest.NewDB(t)
	if err := postgres.Migrate(ctx, dbURL); err != nil {
		t.Fatal(err)
	}

	store, pool, err := postgres.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	masterKey, err := secrets.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}

	stack := &Stack{
		DatabaseURL: dbURL,
		MasterKey:   masterKey,
		Store:       store,
		Model:       modelscript.New(),
		ultradBin:   ultradBin,
		workerBin:   workerBin,
		workerEnv:   options.WorkerEnv,
		logs:        &logCapture{},
		t:           t,
	}
	t.Cleanup(stack.Model.Close)
	stack.seed(t, store, options)

	port := freePort(t)
	stack.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	stack.ReplicaBaseURLs = append(stack.ReplicaBaseURLs, stack.BaseURL)

	// Each replica owns its address, so killing and restarting one in place
	// is invisible to a client holding that URL — which is what control-plane
	// death actually looks like from outside.
	stack.StartUltrad()
	t.Cleanup(stack.KillUltrad)
	for i := 1; i < options.UltradReplicas; i++ {
		stack.ReplicaBaseURLs = append(stack.ReplicaBaseURLs,
			fmt.Sprintf("http://127.0.0.1:%d", freePort(t)))
		stack.startUltradAt(i)
	}
	for range options.WorkerReplicas {
		stack.StartWorker()
	}
	waitHealthy(t, stack.BaseURL)
	// Every replica must be healthy before the ingress starts fanning out, or
	// the first round-robin request could hit a replica that is still booting.
	for _, base := range stack.ReplicaBaseURLs {
		waitHealthy(t, base)
	}
	stack.startIngress(t)
	return stack
}

// StartUltrad launches the primary cored on its original port and waits for
// it to serve. Restarting in place keeps BaseURL valid, so a client cannot
// tell the difference between a restart and a stall except by observing the
// process.
func (s *Stack) StartUltrad() { s.startUltradAt(0) }

// KillUltrad SIGKILLs the primary cored process (control-plane crash).
func (s *Stack) KillUltrad() {
	s.ultradMu.Lock()
	defer s.ultradMu.Unlock()
	if len(s.ultradCmds) == 0 || s.ultradCmds[0] == nil {
		return
	}
	_ = s.ultradCmds[0].Process.Kill()
	_, _ = s.ultradCmds[0].Process.Wait()
	s.ultradCmds[0] = nil
}

// startUltradAt launches replica index on the address it already owns, so a
// restart is invisible to clients holding that URL.
func (s *Stack) startUltradAt(index int) {
	if index < 0 || index >= len(s.ReplicaBaseURLs) {
		s.t.Fatalf("harness: no cored replica at index %d", index)
	}
	base := s.ReplicaBaseURLs[index]
	parsed, err := url.Parse(base)
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(s.ultradBin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+s.DatabaseURL,
		"CORE_ADDR="+parsed.Host,
		"CORE_MASTER_KEY="+s.MasterKey,
		"CORE_DEFAULT_MODEL=mock-model",
		"CORE_MIGRATE=false",
	)
	cmd.Stdout = s.logs
	cmd.Stderr = s.logs
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.ultradMu.Lock()
	for len(s.ultradCmds) <= index {
		s.ultradCmds = append(s.ultradCmds, nil)
	}
	s.ultradCmds[index] = cmd
	s.ultradMu.Unlock()
	s.t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	waitHealthy(s.t, base)
}

// newWorkerCmd builds a worker process with the harness environment.
func (s *Stack) newWorkerCmd() *exec.Cmd {
	cmd := exec.Command(s.workerBin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+s.DatabaseURL,
		"CORE_MASTER_KEY="+s.MasterKey,
		"CORE_JOB_TIMEOUT=20s",
		"CORE_RESCUE_AFTER=21s",
		"CORE_BEZALEL_IMAGE="+BezalelImage,
		"CORE_RECONCILE_INTERVAL=1s",
		"CORE_PROVISION_TIMEOUT=45s",
	)
	cmd.Env = append(cmd.Env, s.workerEnv...)
	cmd.Stdout = s.logs
	cmd.Stderr = s.logs
	return cmd
}

// StartWorker launches a worker process and returns its index. Every worker
// the harness starts is tracked, so cleanup reaps all of them exactly once.
func (s *Stack) StartWorker() int {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	cmd := s.newWorkerCmd()
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.workers = append(s.workers, cmd)
	index := len(s.workers) - 1
	s.t.Cleanup(func() { s.stopWorker(index) })
	return index
}

// RestartWorker replaces the worker at an index with a fresh process, which is
// how a test proves work survives the death of the process that started it.
func (s *Stack) RestartWorker(index int) {
	s.KillWorkerAt(index)
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if index >= len(s.workers) {
		s.t.Fatalf("harness: no worker at index %d", index)
	}
	cmd := s.newWorkerCmd()
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.workers[index] = cmd
}

// WorkerCount reports how many worker slots exist.
func (s *Stack) WorkerCount() int {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	return len(s.workers)
}

// KillWorker SIGKILLs the most recently started worker (crash simulation).
func (s *Stack) KillWorker() {
	s.workerMu.Lock()
	last := len(s.workers) - 1
	s.workerMu.Unlock()
	if last >= 0 {
		s.KillWorkerAt(last)
	}
}

// KillWorkerAt SIGKILLs one specific worker, leaving its slot empty so a test
// can assert that the remaining workers finish the workload.
func (s *Stack) KillWorkerAt(index int) {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	s.killWorkerLocked(index)
}

func (s *Stack) stopWorker(index int) {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	s.killWorkerLocked(index)
}

func (s *Stack) killWorkerLocked(index int) {
	if index < 0 || index >= len(s.workers) || s.workers[index] == nil {
		return
	}
	cmd := s.workers[index]
	s.workers[index] = nil
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

// runnableJobStates are the states a job can still be worked from. A test
// asking "is anything queued" means these and not, for example, a completed
// row still sitting in the table.
var runnableJobStates = []string{"available", "running", "scheduled", "retryable"}

// QueueDepth counts runnable jobs of the named kinds. Callers must name the
// kinds they mean: a test asserting "the parent holds no worker" is about
// agent.step jobs, and counting unrelated background sweeps instead would make
// the assertion meaningless.
func (s *Stack) QueueDepth(ctx context.Context, kinds ...string) (int, error) {
	if len(kinds) == 0 {
		kinds = []string{"agent.step"}
	}
	pool, err := pgxpool.New(ctx, s.DatabaseURL)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	var n int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job
		  WHERE state = ANY($1) AND args->>'job_kind' = ANY($2)`,
		runnableJobStates, kinds).Scan(&n)
	return n, err
}

// DebugRunnableJobs lists runnable jobs with their state, for diagnosing a
// queue-depth assertion failure.
func (s *Stack) DebugRunnableJobs(t *testing.T, ctx context.Context) []string {
	t.Helper()
	pool, err := pgxpool.New(ctx, s.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `SELECT state, args::text FROM river_job WHERE state = ANY($1)`, runnableJobStates)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var state, args string
		if err := rows.Scan(&state, &args); err != nil {
			t.Fatal(err)
		}
		out = append(out, state+" "+args)
	}
	return out
}

// QueueDepthForRun counts runnable step jobs belonging to one run, which is
// how a test proves a specific parked parent holds no worker slot while other
// runs legitimately keep working.
func (s *Stack) QueueDepthForRun(ctx context.Context, run uc.RunID) (int, error) {
	pool, err := pgxpool.New(ctx, s.DatabaseURL)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	var n int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job
		  WHERE state = ANY($1) AND args->>'job_kind' = 'agent.step'
		    AND args->'payload'->>'run_id' = $2`,
		runnableJobStates, string(run)).Scan(&n)
	return n, err
}

func (s *Stack) seed(t *testing.T, store *postgres.Store, options Options) {
	t.Helper()
	ctx := context.Background()

	s.TenantA = uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "tenant-a"}
	s.TenantB = uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: "tenant-b"}

	for _, tenant := range []uc.Tenant{s.TenantA, s.TenantB} {
		if err := store.Tenants().Create(ctx, tenant); err != nil {
			t.Fatal(err)
		}
	}

	keyring, err := secrets.NewAESKeyring(s.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(tenant uc.TenantID, name string) string {
		raw, prefix, err := uc.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		secrets.DefaultRedactor.Register(raw)
		enc, err := keyring.Encrypt([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.APIKeys().Create(ctx, uc.APIKey{
			ID: uc.APIKeyID(uuid.NewString()), TenantID: tenant, Name: name,
			Scope: uc.KeyScopeAdmin, Prefix: prefix, KeyHash: uc.HashAPIKey(raw), KeyEnc: enc,
		}); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	s.KeyA = mint(s.TenantA.ID, "alice")
	s.KeyB = mint(s.TenantB.ID, "bob")

	// Every test tenant has a default local provider instance.
	for _, tenant := range []uc.Tenant{s.TenantA, s.TenantB} {
		if err := store.Tenant(tenant.ID).Providers().Create(ctx, uc.ProviderInstance{
			ID: uc.ProviderInstanceID(uuid.NewString()), TenantID: tenant.ID,
			Kind: uc.ProviderKindLocalDocker, Name: "default", State: "ready",
			Capabilities: uc.ProviderCapabilities{
				Kind: uc.ProviderKindLocalDocker,
				Supported: []uc.ProviderCapability{
					uc.CapabilityServesToolEndpoint,
					uc.CapabilityRestartPreservesState,
					uc.CapabilityAdoptsOrphans,
					uc.CapabilityEnumeratesResources,
				},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if options.SeedCredential {
		s.SeedCredential(t, s.TenantA.ID, "default", CanaryAPIKey, s.Model.URL())
	}
}

// SeedCredential stores an encrypted openai inference credential for an org.
func (s *Stack) SeedCredential(t *testing.T, tenant uc.TenantID, name, apiKey, baseURL string) {
	t.Helper()
	keyring, err := secrets.NewAESKeyring(s.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(uc.InferencePayload{APIKey: apiKey, BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := keyring.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Tenant(tenant).Credentials().Put(context.Background(), uc.Credential{
		Kind: uc.CredentialKindOpenAI, Name: name, EncPayload: enc,
	}); err != nil {
		t.Fatal(err)
	}
}

// Health reports whether an cored instance answers its health endpoint. Tests
// use it to assert a replica really is serving rather than assuming it is.
func Health(baseURL string) (bool, error) {
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK, nil
}

func waitHealthy(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("cored at %s never became healthy", baseURL)
}

// startIngress puts a round-robin reverse proxy in front of every cored
// replica. A client that talks to it does not know or care which replica
// served a request, which is the point: cross-replica correctness has to hold
// without client affinity.
func (s *Stack) startIngress(t *testing.T) {
	t.Helper()
	targets := make([]*url.URL, 0, len(s.ReplicaBaseURLs))
	for _, base := range s.ReplicaBaseURLs {
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, parsed)
	}
	var next atomic.Uint64
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			target := targets[int(next.Add(1)-1)%len(targets)]
			r.SetURL(target)
			r.Out.Host = target.Host
		},
		// Streaming subscriptions must not be buffered, or a test would see
		// events arrive in one lump and learn nothing about delivery.
		FlushInterval: -1,
	}
	server := httptest.NewUnstartedServer(proxy)
	server.EnableHTTP2 = true
	server.Start()
	s.ingress = server
	t.Cleanup(server.Close)
}

// IngressURL is the round-robin entry point across replicas. With one replica
// it is simply that replica.
func (s *Stack) IngressURL() string {
	if s.ingress != nil {
		return s.ingress.URL
	}
	return s.BaseURL
}

// ReplicaClient returns a client pinned to one cored replica, for tests that
// must act on a named replica rather than wherever the ingress lands.
func (s *Stack) ReplicaClient(index int, token string) *testclient.Client {
	if index < 0 || index >= len(s.ReplicaBaseURLs) {
		s.t.Fatalf("harness: no cored replica at index %d", index)
	}
	return testclient.New(s.ReplicaBaseURLs[index], token)
}

// IngressClient returns a client that round-robins across every replica.
func (s *Stack) IngressClient(token string) *testclient.Client {
	return testclient.New(s.IngressURL(), token)
}

// RestartUltrad replaces one cored replica with a fresh process on the same
// address, so subscribers must reconnect and resume by seq.
func (s *Stack) RestartUltrad(index int) {
	if index < 0 || index >= len(s.ultradCmds) {
		s.t.Fatalf("harness: no cored replica at index %d", index)
	}
	s.ultradMu.Lock()
	if cmd := s.ultradCmds[index]; cmd != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		s.ultradCmds[index] = nil
	}
	s.ultradMu.Unlock()
	s.startUltradAt(index)
}

// AliceClient returns a client authenticated as Alice (owner of TenantA).
func (s *Stack) AliceClient() *testclient.Client { return testclient.New(s.BaseURL, s.KeyA) }

// BobClient returns a client authenticated as Bob (owner of TenantB).
func (s *Stack) BobClient() *testclient.Client { return testclient.New(s.BaseURL, s.KeyB) }
