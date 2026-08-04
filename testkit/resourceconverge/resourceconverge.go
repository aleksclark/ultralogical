// Package resourceconverge drives the real durable environment lifecycle against
// one provider adapter, so a test can prove what happens when something
// outside the platform destroys a resource.
//
// Reconciliation lives in resourcework, not in the adapters, so asserting an
// adapter's Status in isolation would prove nothing about convergence: the
// question is whether the persisted environment moves. This package therefore
// runs the production Service, the production store, and a real transactional
// queue, and injects only the adapter under test.
package resourceconverge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/jobqueue"
	"github.com/aleksclark/ultracore/jobqueue/inproc"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/resourcework"
	"github.com/aleksclark/ultracore/secrets"
	"github.com/aleksclark/ultracore/testkit/pgtest"
)

// masterKey is a fixed test credential key. Environment tokens are encrypted
// at rest even here, because the lifecycle code decrypts them on the way to
// the provider and a test that skipped that would not exercise the real path.
const masterKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Harness is a running environment control plane over one adapter.
type Harness struct {
	Store     *postgres.Store
	Resources *resourcework.Service
	Keyring   secrets.Keyring
	Tenant    uc.TenantID
	Session   uc.SessionID

	queue *inproc.Queue
	pool  *pgxpool.Pool
}

// fixedAdapter hands the lifecycle service the one adapter under test.
// Deployment wiring picks an adapter by kind; these tests already know which
// adapter they are exercising, and resolving it through the registry would
// only add a way for the test to run against something else.
type fixedAdapter struct{ provider uc.ResourceProvider }

func (f fixedAdapter) Build(context.Context, string, json.RawMessage) (uc.ResourceProvider, error) {
	return f.provider, nil
}

// Options tune the lifecycle timings a test depends on.
type Options struct {
	// ReconcileInterval is how often a ready environment is verified. Tests
	// shorten it so an out-of-band deletion is observed promptly.
	ReconcileInterval time.Duration
	// ProvisionTimeout bounds how long provisioning may take before the
	// watchdog re-drives it.
	ProvisionTimeout time.Duration
	// Kind is the provider kind recorded on the registration, so a failure
	// message names the adapter the test meant to exercise.
	Kind string
}

// New builds a control plane over the given adapter. Jobs are queued but not
// yet worked: a caller that needs to create state before provisioning runs
// (an interrupted provisioning, for instance) can do so, then call Start.
func New(t *testing.T, provider uc.ResourceProvider, options Options) *Harness {
	t.Helper()
	ctx := context.Background()
	if options.ReconcileInterval <= 0 {
		options.ReconcileInterval = 500 * time.Millisecond
	}
	if options.ProvisionTimeout <= 0 {
		options.ProvisionTimeout = 2 * time.Minute
	}
	if options.Kind == "" {
		options.Kind = uc.ProviderKindLocalDocker
	}

	databaseURL := pgtest.NewDB(t)
	if err := postgres.Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, pool, err := postgres.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	keyring, err := secrets.NewAESKeyring(masterKey)
	if err != nil {
		t.Fatal(err)
	}

	queue := inproc.New(pool, inproc.Config{PollInterval: 50 * time.Millisecond})
	envs := &resourcework.Service{
		Store: store, Enqueue: postgres.TxEnqueuer{Queue: queue}, Keyring: keyring,
		Providers:         fixedAdapter{provider: provider},
		ReconcileInterval: options.ReconcileInterval,
		ProvisionTimeout:  options.ProvisionTimeout,
	}
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.ProvisionJob](envs.Provision))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.TerminateJob](envs.Terminate))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.ReconcileJob](envs.Reconcile))
	jobqueue.Register(queue, jobqueue.WorkerFunc[resourcework.RestartJob](envs.Restart))

	h := &Harness{Store: store, Resources: envs, Keyring: keyring, queue: queue, pool: pool}
	// The queue creates its table when it starts, and a caller may need to
	// enqueue work before any job is allowed to run. Starting and immediately
	// stopping is safe here because nothing has been enqueued yet, and it
	// leaves the table in place for the deferred Start.
	if err := queue.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := queue.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	h.seed(t, options.Kind)
	return h
}

// seed creates the org, session, and provider registration the lifecycle
// needs. The registration claims the capabilities these tests rely on rather
// than a blanket set, so a failure reads as the capability it concerns.
func (h *Harness) seed(t *testing.T, kind string) {
	t.Helper()
	ctx := context.Background()
	h.Tenant = uc.TenantID(uuid.NewString())
	if err := h.Store.Tenants().Create(ctx, uc.Tenant{ID: h.Tenant, Name: "converge"}); err != nil {
		t.Fatal(err)
	}
	h.Session = uc.SessionID(uuid.NewString())
	if err := h.Store.Tenant(h.Tenant).Sessions().Create(ctx, uc.Session{
		ID: h.Session, TenantID: h.Tenant, Title: "reconciliation",
	}); err != nil {
		t.Fatal(err)
	}
	err := h.Store.Tenant(h.Tenant).Providers().Create(ctx, uc.ProviderInstance{
		ID: uc.ProviderInstanceID(uuid.NewString()), TenantID: h.Tenant, Kind: kind,
		Name: "default", State: "ready",
		Capabilities: uc.ProviderCapabilities{
			Kind: kind,
			Supported: []uc.ProviderCapability{
				uc.CapabilityServesToolEndpoint,
				uc.CapabilityAdoptsOrphans,
				uc.CapabilityEnumeratesResources,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Start begins working queued jobs.
func (h *Harness) Start(t *testing.T) {
	t.Helper()
	if err := h.queue.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = h.queue.Stop(stopCtx)
	})
}

// Request creates an environment the way the API does, returning the stored
// row. Provisioning happens on the queue.
func (h *Harness) Request(t *testing.T, spec uc.DevEnvSpec) uc.Resource {
	t.Helper()
	b, _ := json.Marshal(spec)
	env, _, err := h.Resources.Request(context.Background(), h.Tenant, h.Session, uc.ResourceKindDevEnv, b, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// Get reads an environment's persisted state.
func (h *Harness) Get(t *testing.T, id uc.ResourceID) uc.Resource {
	t.Helper()
	env, err := h.Store.Tenant(h.Tenant).Resources().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// ClearToken decrypts an environment's bearer token, which is what a caller
// needs to pre-create a resource the way an interrupted provisioning would
// have left it.
func (h *Harness) ClearToken(t *testing.T, env uc.Resource) string {
	t.Helper()
	clear, err := h.Resources.ClearTokenForTools(env)
	if err != nil {
		t.Fatal(err)
	}
	return clear
}

// Await polls until an environment reaches a state, failing with the row's
// own diagnosis rather than a bare timeout.
func (h *Harness) Await(t *testing.T, id uc.ResourceID, want uc.ResourceState, timeout time.Duration) uc.Resource {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last uc.Resource
	for time.Now().Before(deadline) {
		env, err := h.Store.Tenant(h.Tenant).Resources().Get(context.Background(), id)
		if err == nil {
			last = env
			if env.State == want {
				return env
			}
			if want != uc.ResourceFailed && env.State == uc.ResourceFailed {
				t.Fatalf("environment failed while waiting for %s: %s", want, env.FailureMessage)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("environment stayed %s instead of reaching %s (message %q)",
		last.State, want, last.FailureMessage)
	return last
}
