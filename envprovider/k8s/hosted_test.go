package k8s_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/k8s"
	"github.com/aleksclark/ultralogical/mcp"
)

// hostedProvider builds a hosted provider for one org.
func hostedProvider(t *testing.T, org string, maxEnvironments int) *k8s.Provider {
	t.Helper()
	provider, err := k8s.NewWithClient(clientFor(t, kubeconfigFor(t)), k8s.Config{
		Hosted: true, OrgID: org, Image: clusterImage(t),
		EndpointMode: k8s.EndpointModeNodePort, EndpointHost: "127.0.0.1",
		NodePortRange: [2]int32{30080, 30082}, MaxEnvironments: maxEnvironments,
		// The suite reaches environments through the node's forwarded ports,
		// the same path a worker outside the cluster uses, so the policy must
		// admit that traffic while still excluding other orgs.
		PlatformIngressCIDRs: []string{"0.0.0.0/0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func awaitReadyEnv(t *testing.T, ctx context.Context, provider *k8s.Provider, handle ultra.ProviderHandle) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, handle)
		if err == nil && status.State == ultra.EnvReady {
			endpoint, err := provider.Endpoint(ctx, handle)
			if err == nil && mcp.Healthy(ctx, endpoint) == nil {
				return endpoint
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("hosted environment never became ready")
	return ""
}

// A10.3 — two hosted orgs share a cluster but not a boundary. Isolation is
// asserted against the live objects and against real network behavior, not
// against the fact that a policy was submitted.
func TestA103_HostedIsolationAndQuota(t *testing.T) {
	client := clientFor(t, kubeconfigFor(t))
	ctx := context.Background()
	orgA, orgB := "isoa"+uuid.NewString()[:8], "isob"+uuid.NewString()[:8]
	providerA, providerB := hostedProvider(t, orgA, 2), hostedProvider(t, orgB, 2)

	namespaceA, namespaceB := providerA.Namespace(), providerB.Namespace()
	if namespaceA == namespaceB {
		t.Fatal("two orgs resolved to the same namespace")
	}
	t.Cleanup(func() {
		for _, namespace := range []string{namespaceA, namespaceB} {
			_ = client.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		}
	})

	envA := ultra.EnvID(uuid.NewString())
	handleA, err := providerA.Provision(ctx, envA, ultra.EnvSpec{Name: "iso", Workdir: "/work"}, "token-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = providerA.Terminate(context.Background(), handleA) })
	endpointA := awaitReadyEnv(t, ctx, providerA, handleA)

	envB := ultra.EnvID(uuid.NewString())
	handleB, err := providerB.Provision(ctx, envB, ultra.EnvSpec{Name: "iso", Workdir: "/work"}, "token-b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = providerB.Terminate(context.Background(), handleB) })
	awaitReadyEnv(t, ctx, providerB, handleB)

	t.Run("pods land in per-org namespaces", func(t *testing.T) {
		podsA, err := client.CoreV1().Pods(namespaceA).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(podsA.Items) != 1 {
			t.Fatalf("org A namespace holds %d pods, want 1", len(podsA.Items))
		}
		podsB, err := client.CoreV1().Pods(namespaceB).List(ctx, metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(podsB.Items) != 1 {
			t.Fatalf("org B namespace holds %d pods, want 1", len(podsB.Items))
		}
		if podsA.Items[0].Name == podsB.Items[0].Name && namespaceA == namespaceB {
			t.Fatal("the two orgs' environments are the same object")
		}
	})

	t.Run("RBAC and policy objects exist per org", func(t *testing.T) {
		for _, namespace := range []string{namespaceA, namespaceB} {
			if _, err := client.CoreV1().ServiceAccounts(namespace).Get(ctx, "ultra-env", metav1.GetOptions{}); err != nil {
				t.Fatalf("%s has no service account: %v", namespace, err)
			}
			role, err := client.RbacV1().Roles(namespace).Get(ctx, "ultra-env", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("%s has no role: %v", namespace, err)
			}
			if len(role.Rules) != 0 {
				t.Fatalf("%s grants environments Kubernetes API rules: %v", namespace, role.Rules)
			}
			if _, err := client.RbacV1().RoleBindings(namespace).Get(ctx, "ultra-env", metav1.GetOptions{}); err != nil {
				t.Fatalf("%s has no role binding: %v", namespace, err)
			}
			policy, err := client.NetworkingV1().NetworkPolicies(namespace).Get(ctx, "ultra-env-isolation", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("%s has no network policy: %v", namespace, err)
			}
			if len(policy.Spec.Ingress) != 1 || len(policy.Spec.Ingress[0].From) == 0 {
				t.Fatalf("%s network policy does not restrict ingress: %+v", namespace, policy.Spec)
			}
			// Every platform range must exclude the cluster's pod network, or
			// the range would silently re-admit the other orgs.
			for _, peer := range policy.Spec.Ingress[0].From {
				if peer.IPBlock == nil {
					continue
				}
				if len(peer.IPBlock.Except) == 0 {
					t.Fatalf("%s admits %s without excluding pod traffic", namespace, peer.IPBlock.CIDR)
				}
			}
			quota, err := client.CoreV1().ResourceQuotas(namespace).Get(ctx, "ultra-env-quota", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("%s has no resource quota: %v", namespace, err)
			}
			if quota.Spec.Hard.Pods().IsZero() {
				t.Fatalf("%s quota sets no pod ceiling", namespace)
			}
		}
	})

	t.Run("one org cannot reach the other's environment", func(t *testing.T) {
		// A real request from inside org B's namespace to org A's service.
		// Asserting the policy object exists would not prove the cluster
		// enforces it, which is the only thing that matters here.
		podsA, err := client.CoreV1().Pods(namespaceA).List(ctx, metav1.ListOptions{})
		if err != nil || len(podsA.Items) == 0 {
			t.Fatalf("org A pod missing: %v", err)
		}
		// The Service shares the pod's name, so the target is read from the
		// cluster rather than reconstructed from the environment id.
		serviceA := podsA.Items[0].Name
		target := "http://" + serviceA + "." + namespaceA + ".svc.cluster.local:8080/health"
		blocked := runProbePod(t, ctx, namespaceB, target)
		if !blocked {
			t.Fatal("org B reached org A's environment across the namespace boundary")
		}
		// The same request from inside org A's own namespace must succeed, so
		// the previous result is isolation rather than a broken service.
		if reachable := runProbePod(t, ctx, namespaceA, target); reachable {
			t.Fatal("org A could not reach its own environment; the block above proves nothing")
		}
	})

	t.Run("exceeding the ceiling is a typed refusal that creates nothing", func(t *testing.T) {
		second := ultra.EnvID(uuid.NewString())
		handle, err := providerA.Provision(ctx, second, ultra.EnvSpec{Name: "iso2", Workdir: "/work"}, "token-a2")
		if err != nil {
			t.Fatalf("the second environment should fit under a ceiling of 2: %v", err)
		}
		t.Cleanup(func() { _ = providerA.Terminate(context.Background(), handle) })
		awaitReadyEnv(t, ctx, providerA, handle)

		third := ultra.EnvID(uuid.NewString())
		_, err = providerA.Provision(ctx, third, ultra.EnvSpec{Name: "iso3", Workdir: "/work"}, "token-a3")
		if err == nil {
			t.Fatal("provisioning past the ceiling succeeded")
		}
		var quotaErr *k8s.QuotaError
		if !errors.As(err, &quotaErr) {
			t.Fatalf("ceiling error is not typed: %T %v", err, err)
		}
		if quotaErr.Limit != 2 {
			t.Fatalf("quota error reports limit %d", quotaErr.Limit)
		}
		// Nothing may have been created for the refused environment.
		leftover, err := providerA.Resources(ctx, third)
		if err != nil {
			t.Fatal(err)
		}
		if len(leftover) != 0 {
			t.Fatalf("a refused provision left resources behind: %v", leftover)
		}
	})

	t.Run("credentials do not cross the boundary", func(t *testing.T) {
		// Org B's token must not authenticate against org A's environment.
		if err := mcp.NewClient(endpointA, "token-b").Initialize(ctx); err == nil {
			t.Fatal("org B's token authenticated against org A's environment")
		}
		if err := mcp.NewClient(endpointA, "token-a").Initialize(ctx); err != nil {
			t.Fatalf("org A's own token was rejected: %v", err)
		}
	})
}

// runProbePod issues one HTTP request from inside a namespace and reports
// whether it was blocked. It runs a real pod because the question is what the
// cluster's network actually permits.
func runProbePod(t *testing.T, ctx context.Context, namespace, target string) bool {
	t.Helper()
	client := clientFor(t, kubeconfigFor(t))
	name := "probe-" + uuid.NewString()[:8]
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   "busybox:1.36",
				Command: []string{"sh", "-c", "wget -q -T 6 -O- " + target + " >/dev/null 2>&1 && echo REACHED || echo BLOCKED"},
			}},
		},
	}
	if _, err := client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create probe pod: %v", err)
	}
	t.Cleanup(func() {
		grace := int64(0)
		_ = client.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	})
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		current, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && (current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed) {
			logs, err := client.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(ctx)
			if err != nil {
				t.Fatalf("read probe logs: %v", err)
			}
			return strings.Contains(string(logs), "BLOCKED")
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("the probe pod never finished")
	return false
}
