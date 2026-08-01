// Package harness boots the real stack for functional tests: real Postgres
// (shared container, fresh database per test), migrations, ultrad and worker
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

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/postgres"
	"github.com/aleksclark/ultralogical/secrets"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
	"github.com/aleksclark/ultralogical/testkit/pgtest"
	"github.com/aleksclark/ultralogical/testkit/testclient"
)

// Seeded identities. The harness provisions two orgs with one user each so
// tenant-isolation is testable out of the box.
const (
	TokenAlice = "tok-alice"
	TokenBob   = "tok-bob"
	EmailAlice = "alice@example.com"
	EmailBob   = "bob@example.com"

	// CanaryAPIKey is the secret embedded in org A's seeded inference
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
func WithReplicas(ultrad, workers int) Opt {
	return func(o *Options) { o.UltradReplicas = ultrad; o.WorkerReplicas = workers }
}

func WithWorkerEnv(kv ...string) Opt {
	return func(o *Options) { o.WorkerEnv = append(o.WorkerEnv, kv...) }
}

// Stack is a running ultrad + worker + database + modelscript with seeded
// identities.
type Stack struct {
	BaseURL     string
	DatabaseURL string
	MasterKey   string
	OrgA        ultra.Org
	OrgB        ultra.Org
	Alice       ultra.User
	Bob         ultra.User
	Store       *postgres.Store
	Model       *modelscript.Server

	workerMu sync.Mutex
	// workers is indexed: tests kill and restart a specific worker, so
	// "worker 0 died and worker 1 finished the work" is a statement about
	// identifiable processes rather than about whichever one was last started.
	workers    []*exec.Cmd
	ultradCmds []*exec.Cmd
	// ReplicaBaseURLs addresses each ultrad replica directly. BaseURL is the
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
// assert on everything ultrad and the workers logged, while still forwarding
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

// Logs returns everything ultrad and worker processes have written to stderr
// so far. Used by the redaction sweep.
func (s *Stack) Logs() string { return s.logs.String() }

const BezalelImage = "ultralogical/bezalel:phase2-test"

var (
	buildOnce   sync.Once
	buildErr    error
	binDir      string
	bezalelOnce sync.Once
	bezalelErr  error
)

// binaries builds ultrad and worker once per test process.
func binaries(t *testing.T) (string, string) {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ultra-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		binDir = dir
		for _, target := range []string{"ultrad", "worker"} {
			cmd := exec.Command("go", "build", "-o", filepath.Join(dir, target),
				"github.com/aleksclark/ultralogical/cmd/"+target)
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
	return filepath.Join(binDir, "ultrad"), filepath.Join(binDir, "worker")
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

	ultradCmd := exec.Command(ultradBin)
	ultradCmd.Env = append(os.Environ(),
		"DATABASE_URL="+dbURL,
		fmt.Sprintf("ULTRA_ADDR=127.0.0.1:%d", port),
		fmt.Sprintf("ULTRA_DEV_TOKENS=%s=%s,%s=%s", TokenAlice, EmailAlice, TokenBob, EmailBob),
		"ULTRA_MASTER_KEY="+masterKey,
		"ULTRA_DEFAULT_MODEL=mock-model",
		"ULTRA_MIGRATE=false",
	)
	ultradCmd.Stdout = stack.logs
	ultradCmd.Stderr = stack.logs
	if err := ultradCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ultradCmd.Process.Kill()
		_, _ = ultradCmd.Process.Wait()
	})

	stack.ultradCmds = append(stack.ultradCmds, ultradCmd)
	for i := 1; i < options.UltradReplicas; i++ {
		p := freePort(t)
		base := fmt.Sprintf("http://127.0.0.1:%d", p)
		cmd := exec.Command(ultradBin)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dbURL, fmt.Sprintf("ULTRA_ADDR=127.0.0.1:%d", p), fmt.Sprintf("ULTRA_DEV_TOKENS=%s=%s,%s=%s", TokenAlice, EmailAlice, TokenBob, EmailBob), "ULTRA_MASTER_KEY="+masterKey, "ULTRA_DEFAULT_MODEL=mock-model", "ULTRA_MIGRATE=false")
		cmd.Stdout = stack.logs
		cmd.Stderr = stack.logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stack.ultradCmds = append(stack.ultradCmds, cmd)
		stack.ReplicaBaseURLs = append(stack.ReplicaBaseURLs, base)
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
		waitHealthy(t, base)
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

// newWorkerCmd builds a worker process with the harness environment.
func (s *Stack) newWorkerCmd() *exec.Cmd {
	cmd := exec.Command(s.workerBin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+s.DatabaseURL,
		"ULTRA_MASTER_KEY="+s.MasterKey,
		"ULTRA_JOB_TIMEOUT=20s",
		"ULTRA_RESCUE_AFTER=21s",
		"ULTRA_BEZALEL_IMAGE="+BezalelImage,
		"ULTRA_RECONCILE_INTERVAL=1s",
		"ULTRA_PROVISION_TIMEOUT=45s",
		// Presence expiry is deliberately fast in tests so idle transitions
		// are observable without long sleeps.
		"ULTRA_PRESENCE_AFTER=2s",
		"ULTRA_PRESENCE_INTERVAL=1s",
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
func (s *Stack) QueueDepthForRun(ctx context.Context, run ultra.RunID) (int, error) {
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

	s.OrgA = ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: "org-a"}
	s.OrgB = ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: "org-b"}
	s.Alice = ultra.User{ID: ultra.UserID(uuid.NewString()), Email: EmailAlice, Display: "Alice"}
	s.Bob = ultra.User{ID: ultra.UserID(uuid.NewString()), Email: EmailBob, Display: "Bob"}

	for _, org := range []ultra.Org{s.OrgA, s.OrgB} {
		if err := store.Orgs().Create(ctx, org); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []ultra.User{s.Alice, s.Bob} {
		if err := store.Users().Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	memberships := []ultra.OrgMember{
		{OrgID: s.OrgA.ID, UserID: s.Alice.ID, Role: ultra.OrgRoleOwner},
		{OrgID: s.OrgB.ID, UserID: s.Bob.ID, Role: ultra.OrgRoleOwner},
	}
	for _, m := range memberships {
		if err := store.Orgs().AddMember(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	// Every test org has a default local provider instance.
	for _, org := range []ultra.Org{s.OrgA, s.OrgB} {
		if err := store.Org(org.ID).Providers().Create(ctx, ultra.ProviderInstance{
			ID: ultra.ProviderInstanceID(uuid.NewString()), OrgID: org.ID,
			Kind: ultra.ProviderKindLocalDocker, Name: "default", RateClass: ultra.RateClassBYO, State: "ready",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if options.SeedCredential {
		s.SeedCredential(t, s.OrgA.ID, "default", CanaryAPIKey, s.Model.URL())
	}
}

// SeedCredential stores an encrypted openai inference credential for an org.
func (s *Stack) SeedCredential(t *testing.T, org ultra.OrgID, name, apiKey, baseURL string) {
	t.Helper()
	keyring, err := secrets.NewAESKeyring(s.MasterKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(ultra.InferencePayload{APIKey: apiKey, BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := keyring.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Org(org).Credentials().Put(context.Background(), ultra.Credential{
		Kind: ultra.CredentialKindOpenAI, Name: name, EncPayload: enc,
	}); err != nil {
		t.Fatal(err)
	}
}

// Health reports whether an ultrad instance answers its health endpoint. Tests
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
	t.Fatalf("ultrad at %s never became healthy", baseURL)
}

// startIngress puts a round-robin reverse proxy in front of every ultrad
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

// ReplicaClient returns a client pinned to one ultrad replica, for tests that
// must act on a named replica rather than wherever the ingress lands.
func (s *Stack) ReplicaClient(index int, token string) *testclient.Client {
	if index < 0 || index >= len(s.ReplicaBaseURLs) {
		s.t.Fatalf("harness: no ultrad replica at index %d", index)
	}
	return testclient.New(s.ReplicaBaseURLs[index], token)
}

// IngressClient returns a client that round-robins across every replica.
func (s *Stack) IngressClient(token string) *testclient.Client {
	return testclient.New(s.IngressURL(), token)
}

// RestartUltrad replaces one ultrad replica with a fresh process on the same
// address, so subscribers must reconnect and resume by seq.
func (s *Stack) RestartUltrad(index int) {
	if index < 0 || index >= len(s.ultradCmds) {
		s.t.Fatalf("harness: no ultrad replica at index %d", index)
	}
	base := s.ReplicaBaseURLs[index]
	parsed, err := url.Parse(base)
	if err != nil {
		s.t.Fatal(err)
	}
	if cmd := s.ultradCmds[index]; cmd != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	cmd := exec.Command(s.ultradBin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+s.DatabaseURL,
		"ULTRA_ADDR="+parsed.Host,
		fmt.Sprintf("ULTRA_DEV_TOKENS=%s=%s,%s=%s", TokenAlice, EmailAlice, TokenBob, EmailBob),
		"ULTRA_MASTER_KEY="+s.MasterKey,
		"ULTRA_DEFAULT_MODEL=mock-model",
		"ULTRA_MIGRATE=false",
	)
	cmd.Stdout = s.logs
	cmd.Stderr = s.logs
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.ultradCmds[index] = cmd
	s.t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	waitHealthy(s.t, base)
}

// AliceClient returns a client authenticated as Alice (owner of OrgA).
func (s *Stack) AliceClient() *testclient.Client { return testclient.New(s.BaseURL, TokenAlice) }

// BobClient returns a client authenticated as Bob (owner of OrgB).
func (s *Stack) BobClient() *testclient.Client { return testclient.New(s.BaseURL, TokenBob) }
