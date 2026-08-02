package nomad_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	nomadapi "github.com/hashicorp/nomad/api"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/nomad"
	"github.com/aleksclark/ultralogical/testkit/envconverge"
)

// A10.4 — an allocation stopped outside the platform converges instead of
// sitting stale, and converging leaves no duplicate job or allocation.
//
// The adapter deliberately disables Nomad's own restart and reschedule loops,
// so recovery is the platform's job. That makes this the test that proves the
// platform actually does it: the assertion is on the persisted environment
// state, because an adapter reporting the stop correctly while the environment
// stayed ready forever would still be the defect.
func TestA104_NomadReconcilesExternallyStoppedAllocation(t *testing.T) {
	address := nomadAddress(t)
	client, err := nomadapi.NewClient(&nomadapi.Config{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := nomad.New(nomad.Config{
		Address: address, Image: clusterImage(t), EndpointHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	harness := envconverge.New(t, provider, envconverge.Options{
		Kind: ultra.ProviderKindBYONomad, ReconcileInterval: 500 * time.Millisecond,
	})
	harness.Start(t)

	env := harness.Request(t, ultra.EnvSpec{Name: "reconcile", Workdir: "/work"})
	t.Cleanup(func() {
		current, err := harness.Store.Org(harness.Org).Envs().Get(context.Background(), env.ID)
		if err == nil {
			_ = provider.Terminate(context.Background(), current.Handle)
		}
	})
	harness.Await(t, env.ID, ultra.EnvReady, 3*time.Minute)

	// The allocation really is running before it is stopped, so what follows
	// is a response to the stop rather than to a provisioning that never
	// worked.
	before, err := provider.Resources(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if countPrefixed(before, "allocation/") == 0 {
		t.Fatalf("a ready environment reported no running allocation: %v", before)
	}

	// Stop the allocation the way an operator at the Nomad CLI would: through
	// Nomad's own API, with no involvement from the adapter.
	allocation := runningAllocation(t, ctx, client, env.ID)
	if _, err := client.Allocations().Stop(allocation, nil); err != nil {
		t.Fatalf("stopping the allocation out of band failed: %v", err)
	}

	// Convergence: the environment reaches a terminal state on its own.
	converged := harness.Await(t, env.ID, ultra.EnvFailed, 2*time.Minute)
	if converged.FailureMessage == "" {
		t.Fatal("the environment converged with no diagnosis of why")
	}

	// No duplicate: recovery must not have registered a second job, and Nomad
	// must not be holding a replacement allocation the platform forgot about.
	after, err := provider.Resources(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jobs := countPrefixed(after, "job/"); jobs > 1 {
		t.Fatalf("the environment owns %d jobs after convergence: %v", jobs, after)
	}
	if live := liveJobsFor(t, client, env.ID); live > 1 {
		t.Fatalf("Nomad holds %d live jobs for one environment", live)
	}
}

// A10.4 — provisioning interrupted after the job was registered reuses that
// job instead of registering a second one.
//
// The job name is derived from the environment id, so the property under test
// is that the deterministic identity really is what makes a retry safe. The
// environment is left in `requested` with the job already registered, which is
// what a worker dying between registration and handle persistence leaves.
func TestA104_NomadReusesInterruptedRegistration(t *testing.T) {
	address := nomadAddress(t)
	client, err := nomadapi.NewClient(&nomadapi.Config{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := nomad.New(nomad.Config{
		Address: address, Image: clusterImage(t), EndpointHost: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	harness := envconverge.New(t, provider, envconverge.Options{
		Kind: ultra.ProviderKindBYONomad, ReconcileInterval: 500 * time.Millisecond,
	})
	env := harness.Request(t, ultra.EnvSpec{Name: "adopt", Workdir: "/work"})

	orphan, err := provider.Provision(ctx, env.ID, env.Spec, harness.ClearToken(t, env))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), orphan) })

	harness.Start(t)
	harness.Await(t, env.ID, ultra.EnvReady, 3*time.Minute)

	// Exactly one live job carries this environment's identity.
	if live := liveJobsFor(t, client, env.ID); live != 1 {
		t.Fatalf("Nomad holds %d live jobs for one environment, want 1", live)
	}
}

// liveJobsFor counts the running jobs Nomad holds for an environment.
//
// Every job this adapter registers is enumerated and then read individually,
// because Nomad's list endpoint omits job metadata: filtering the list on meta
// would silently match nothing and report zero duplicates no matter what the
// cluster held. Scoping by metadata rather than by name is what lets this
// notice a second job registered under a name the test did not predict, which
// is the shape a real duplicate would take.
func liveJobsFor(t *testing.T, client *nomadapi.Client, envID ultra.EnvID) int {
	t.Helper()
	listed, _, err := client.Jobs().PrefixList("ultra-env-")
	if err != nil {
		t.Fatal(err)
	}
	live := 0
	for _, stub := range listed {
		if stub.Stop {
			continue
		}
		job, _, err := client.Jobs().Info(stub.ID, nil)
		if err != nil {
			// A job that vanished between the list and the read is not a
			// duplicate, so it is skipped rather than failing the test.
			continue
		}
		if job.Meta["ultralogical.env_id"] == string(envID) {
			live++
		}
	}
	return live
}

// jobIDFor mirrors the deterministic job name the adapter registers.
func jobIDFor(envID ultra.EnvID) string {
	return "ultra-env-" + strings.ToLower(string(envID))
}

// runningAllocation reads the environment's live allocation from Nomad, so the
// test stops the object the cluster actually holds.
func runningAllocation(t *testing.T, ctx context.Context, client *nomadapi.Client, envID ultra.EnvID) *nomadapi.Allocation {
	t.Helper()
	stubs, _, err := client.Jobs().Allocations(jobIDFor(envID), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stub := range stubs {
		if stub.ClientStatus != "running" {
			continue
		}
		allocation, _, err := client.Allocations().Info(stub.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return allocation
	}
	t.Fatalf("no running allocation for %s", envID)
	return nil
}

func countPrefixed(items []string, prefix string) int {
	count := 0
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

// A10.4 — the allocation Nomad scheduled carries the resources the job
// declared. Setting them in the spec proves nothing on its own: a provider
// that quietly dropped the limits would still pass every behavioral step while
// letting one environment starve a cluster.
func TestA104_AllocationCarriesDeclaredResources(t *testing.T) {
	address := nomadAddress(t)
	const (
		declaredCPU    = 700
		declaredMemory = 384
	)
	provider, err := nomad.New(nomad.Config{
		Address: address, Image: clusterImage(t), EndpointHost: "127.0.0.1",
		CPU: declaredCPU, Memory: declaredMemory,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	envID := ultra.EnvID(uuid.NewString())
	handle, err := provider.Provision(ctx, envID, ultra.EnvSpec{Name: "resources", Workdir: "/work"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), handle) })

	client, err := nomadapi.NewClient(&nomadapi.Config{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	jobID := "ultra-env-" + strings.ToLower(string(envID))

	// The registered job is read back from Nomad rather than from the request.
	job, _, err := client.Jobs().Info(jobID, nil)
	if err != nil {
		t.Fatalf("the job is not registered: %v", err)
	}
	if len(job.TaskGroups) != 1 || len(job.TaskGroups[0].Tasks) != 1 {
		t.Fatalf("unexpected job shape: %d groups", len(job.TaskGroups))
	}
	resources := job.TaskGroups[0].Tasks[0].Resources
	if resources == nil || resources.CPU == nil || resources.MemoryMB == nil {
		t.Fatal("the registered task declares no resources, so one environment could starve the cluster")
	}
	if *resources.CPU != declaredCPU {
		t.Fatalf("registered CPU = %d, want %d", *resources.CPU, declaredCPU)
	}
	if *resources.MemoryMB != declaredMemory {
		t.Fatalf("registered memory = %d MiB, want %d", *resources.MemoryMB, declaredMemory)
	}

	// The allocation Nomad actually scheduled carries them too, which is the
	// claim that matters: a job can declare limits the scheduler ignored.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		allocations, _, err := client.Jobs().Allocations(jobID, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, stub := range allocations {
			allocation, _, err := client.Allocations().Info(stub.ID, nil)
			if err != nil || allocation.AllocatedResources == nil {
				continue
			}
			task, ok := allocation.AllocatedResources.Tasks["bezalel"]
			if !ok {
				continue
			}
			if task.Cpu.CpuShares != declaredCPU {
				t.Fatalf("allocated CPU = %d, want %d", task.Cpu.CpuShares, declaredCPU)
			}
			if task.Memory.MemoryMB != declaredMemory {
				t.Fatalf("allocated memory = %d MiB, want %d", task.Memory.MemoryMB, declaredMemory)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("no allocation reported its allocated resources within the deadline")
}
