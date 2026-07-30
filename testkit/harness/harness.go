// Package harness boots the real stack for functional tests: real Postgres
// (shared container, fresh database per test), migrations, ultrad and worker
// as real child processes on random ports, plus a modelscript server (the
// only substituted component — it replaces the LLM vendor at the network
// boundary). Tests interact only through the public API via testclient.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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
	WorkerEnv []string
}

// Opt mutates Options.
type Opt func(*Options)

// WithoutSeedCredential leaves org A credential-less (for A1.7a).
func WithoutSeedCredential() Opt { return func(o *Options) { o.SeedCredential = false } }

// WithWorkerEnv adds env vars to the worker process.
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

	workerMu  sync.Mutex
	workerCmd *exec.Cmd
	workerEnv []string
	ultradBin string
	workerBin string
	t         *testing.T
}

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
	options := Options{SeedCredential: true}
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
		t:           t,
	}
	t.Cleanup(stack.Model.Close)
	stack.seed(t, store, options)

	port := freePort(t)
	stack.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	ultradCmd := exec.Command(ultradBin)
	ultradCmd.Env = append(os.Environ(),
		"DATABASE_URL="+dbURL,
		fmt.Sprintf("ULTRA_ADDR=127.0.0.1:%d", port),
		fmt.Sprintf("ULTRA_DEV_TOKENS=%s=%s,%s=%s", TokenAlice, EmailAlice, TokenBob, EmailBob),
		"ULTRA_MASTER_KEY="+masterKey,
		"ULTRA_DEFAULT_MODEL=mock-model",
		"ULTRA_MIGRATE=false",
	)
	ultradCmd.Stdout = os.Stderr
	ultradCmd.Stderr = os.Stderr
	if err := ultradCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ultradCmd.Process.Kill()
		_, _ = ultradCmd.Process.Wait()
	})

	stack.StartWorker()
	waitHealthy(t, stack.BaseURL)
	return stack
}

// StartWorker launches a worker process. Call KillWorker first when
// simulating crashes.
func (s *Stack) StartWorker() {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	cmd := exec.Command(s.workerBin)
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+s.DatabaseURL,
		"ULTRA_MASTER_KEY="+s.MasterKey,
		"ULTRA_JOB_TIMEOUT=20s",
		"ULTRA_RESCUE_AFTER=21s",
		"ULTRA_BEZALEL_IMAGE="+BezalelImage,
		"ULTRA_RECONCILE_INTERVAL=1s",
		"ULTRA_PROVISION_TIMEOUT=45s",
	)
	cmd.Env = append(cmd.Env, s.workerEnv...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.workerCmd = cmd
	s.t.Cleanup(func() {
		s.workerMu.Lock()
		defer s.workerMu.Unlock()
		if s.workerCmd != nil {
			_ = s.workerCmd.Process.Kill()
			_, _ = s.workerCmd.Process.Wait()
			s.workerCmd = nil
		}
	})
}

// KillWorker SIGKILLs the worker process (crash simulation).
func (s *Stack) KillWorker() {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workerCmd != nil {
		_ = s.workerCmd.Process.Kill()
		_, _ = s.workerCmd.Process.Wait()
		s.workerCmd = nil
	}
}

// QueueDepth returns the number of queued/running step jobs (harness-side
// inspection of river's table for awaiting-state assertions).
func (s *Stack) QueueDepth(ctx context.Context) (int, error) {
	pool, err := pgxpool.New(ctx, s.DatabaseURL)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	var n int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM river_job WHERE state IN ('available', 'running', 'scheduled', 'retryable')`).
		Scan(&n)
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

// AliceClient returns a client authenticated as Alice (owner of OrgA).
func (s *Stack) AliceClient() *testclient.Client { return testclient.New(s.BaseURL, TokenAlice) }

// BobClient returns a client authenticated as Bob (owner of OrgB).
func (s *Stack) BobClient() *testclient.Client { return testclient.New(s.BaseURL, TokenBob) }
