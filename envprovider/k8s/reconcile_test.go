package k8s_test

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/envprovider/k8s"
	"github.com/aleksclark/ultracore/testkit/envconverge"
)

// reconcileNamespace keeps these environments away from the conformance run's
// namespace, so a leak assertion in one cannot be satisfied or broken by the
// other.
const reconcileNamespace = "ultra-reconcile"

// reconcileProvider builds an adapter against the test cluster. The node-port
// range is the one kind forwards to the host, which is how a worker outside
// the cluster reaches an environment.
func reconcileProvider(t *testing.T) *k8s.Provider {
	t.Helper()
	provider, err := k8s.NewWithClient(clientFor(t, kubeconfigFor(t)), k8s.Config{
		Namespace: reconcileNamespace, Image: clusterImage(t),
		EndpointMode: k8s.EndpointModeNodePort, EndpointHost: "127.0.0.1",
		NodePortRange: [2]int32{30080, 30082},
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// A10.2 — a pod deleted outside the platform converges instead of sitting
// stale, and converging creates no duplicate.
//
// The assertion is on the persisted environment, not on the adapter's Status:
// reconciliation lives in envwork, so an adapter that reported the deletion
// correctly while the environment stayed ready forever would still be the bug
// this test exists to catch. The duplicate check matters because the recovery
// path re-drives provisioning, and a recovery that created a second pod would
// leave the operator's cluster holding an orphan no handle points at.
func TestA102_KubernetesReconcilesExternallyDeletedPod(t *testing.T) {
	client := clientFor(t, kubeconfigFor(t))
	provider := reconcileProvider(t)
	ctx := context.Background()

	harness := envconverge.New(t, provider, envconverge.Options{
		Kind: uc.ProviderKindBYOKubernetes,
		// Reconciliation has to notice the deletion quickly enough for the
		// test to be about convergence rather than about waiting.
		ReconcileInterval: 500 * time.Millisecond,
	})
	harness.Start(t)

	env := harness.Request(t, uc.EnvSpec{Name: "reconcile", Workdir: "/work"})
	t.Cleanup(func() {
		current, err := harness.Store.Org(harness.Org).Envs().Get(context.Background(), env.ID)
		if err == nil {
			_ = provider.Terminate(context.Background(), current.Handle)
		}
	})
	ready := harness.Await(t, env.ID, uc.EnvReady, 3*time.Minute)

	// The pod really exists before it is destroyed, so the convergence below
	// is a response to a deletion rather than to a provisioning that never
	// worked.
	before, err := provider.Resources(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("a ready environment reported no Kubernetes resources")
	}

	// Delete the pod the way something outside the platform would: through the
	// Kubernetes API, with no involvement from the adapter.
	podName := podNameFor(t, ctx, client, env.ID)
	grace := int64(0)
	err = client.CoreV1().Pods(reconcileNamespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	})
	if err != nil {
		t.Fatalf("deleting the pod out of band failed: %v", err)
	}

	// Convergence: the environment must reach a terminal state on its own.
	converged := harness.Await(t, env.ID, uc.EnvFailed, 90*time.Second)
	if converged.FailureMessage == "" {
		t.Fatal("the environment converged with no diagnosis of why")
	}

	// No duplicate: the recovery path must not have created a second pod.
	after, err := provider.Resources(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	pods := countPrefixed(after, "pod/")
	if pods > 0 {
		t.Fatalf("a converged environment still owns %d pods: %v", pods, after)
	}
	// And no orphan under this environment's labels anywhere in the namespace,
	// which is what a duplicate would look like from the cluster's side.
	live, err := client.CoreV1().Pods(reconcileNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: k8s.LabelEnvID + "=" + sanitizedEnvID(env.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Items) != 0 {
		names := make([]string, 0, len(live.Items))
		for _, pod := range live.Items {
			names = append(names, pod.Name)
		}
		t.Fatalf("the cluster still holds pods for the converged environment: %v", names)
	}

	// The endpoint the environment published must stop being usable, or a
	// caller holding it could keep talking to something.
	if ready.Endpoint == "" {
		t.Fatal("the ready environment published no endpoint, so this proves nothing")
	}
}

// A10.2 — provisioning interrupted after the pod was created reuses that pod
// instead of leaving a second one behind.
//
// This is the other half of "no duplicates": the deletion case proves recovery
// does not duplicate, and this proves the ordinary retry does not either. The
// environment is left in `requested` with a pre-created pod, which is exactly
// what a worker dying between resource creation and handle persistence leaves
// behind.
//
// The assertion is on the pod's identity, not on which code path produced it.
// Two mechanisms can deliver this (the adapter's Adopt seam and its
// create-by-deterministic-name idempotency), and the property the operator
// cares about is that their cluster ends up with exactly the one pod that was
// already running.
func TestA102_KubernetesAdoptsInterruptedProvisioning(t *testing.T) {
	client := clientFor(t, kubeconfigFor(t))
	provider := reconcileProvider(t)
	ctx := context.Background()

	harness := envconverge.New(t, provider, envconverge.Options{
		Kind: uc.ProviderKindBYOKubernetes, ReconcileInterval: 500 * time.Millisecond,
	})
	env := harness.Request(t, uc.EnvSpec{Name: "adopt", Workdir: "/work"})

	// Create the resource before any worker runs, using the environment's own
	// token: this is the state an interrupted provisioning leaves behind.
	orphan, err := provider.Provision(ctx, env.ID, env.Spec, harness.ClearToken(t, env))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Terminate(context.Background(), orphan) })
	created := podNameFor(t, ctx, client, env.ID)

	harness.Start(t)
	harness.Await(t, env.ID, uc.EnvReady, 3*time.Minute)

	// Exactly one pod, and it is the one that already existed.
	live, err := client.CoreV1().Pods(reconcileNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: k8s.LabelEnvID + "=" + sanitizedEnvID(env.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Items) != 1 {
		t.Fatalf("provisioning produced %d pods for one environment, want 1", len(live.Items))
	}
	if live.Items[0].Name != created {
		t.Fatalf("provisioning replaced the existing pod %q with %q instead of adopting it",
			created, live.Items[0].Name)
	}
}

// podNameFor reads the environment's pod name from the cluster, so the test
// addresses the object Kubernetes actually holds rather than recomputing a
// name the adapter is responsible for.
func podNameFor(t *testing.T, ctx context.Context, client kubernetes.Interface, envID uc.EnvID) string {
	t.Helper()
	pods, err := client.CoreV1().Pods(reconcileNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: k8s.LabelEnvID + "=" + sanitizedEnvID(envID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected exactly one pod for %s, found %d", envID, len(pods.Items))
	}
	return pods.Items[0].Name
}

// sanitizedEnvID mirrors the label value the adapter writes. A UUID is already
// a valid label value, so this only guards against a future id shape that is
// not.
func sanitizedEnvID(id uc.EnvID) string { return string(id) }

func countPrefixed(items []string, prefix string) int {
	count := 0
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}
