package k8s_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/conformance"
	"github.com/aleksclark/ultralogical/envprovider/k8s"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// kubeconfigFor returns the kubeconfig for the test cluster, skipping when no
// cluster is available. CI always provides one; a developer without kind gets
// a skip rather than a confusing failure.
func kubeconfigFor(t *testing.T) string {
	t.Helper()
	if raw := os.Getenv("ULTRA_TEST_KUBECONFIG"); raw != "" {
		body, err := os.ReadFile(raw)
		if err != nil {
			t.Fatalf("ULTRA_TEST_KUBECONFIG is set but unreadable: %v", err)
		}
		return string(body)
	}
	cluster := os.Getenv("ULTRA_TEST_KIND_CLUSTER")
	if cluster == "" {
		cluster = "ultra-test"
	}
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("kind is not installed")
	}
	out, err := exec.Command("kind", "get", "kubeconfig", "--name", cluster).Output()
	if err != nil {
		t.Skipf("kind cluster %q is not running", cluster)
	}
	return string(out)
}

// clusterImage is the Bezalel image loaded into the test cluster.
func clusterImage(t *testing.T) string {
	t.Helper()
	if image := os.Getenv("ULTRA_BEZALEL_IMAGE"); image != "" {
		return image
	}
	return harness.BezalelImage
}

func clientFor(t *testing.T, kubeconfig string) kubernetes.Interface {
	t.Helper()
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(kubeconfig))
	if err != nil {
		t.Fatal(err)
	}
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// A10.1/A10.2 — the shared provider contract against a real Kubernetes
// cluster, with inspection reading Kubernetes' own objects. An adapter that
// delegated its lifecycle elsewhere would satisfy every behavioral assertion
// and still fail here.
func TestKubernetesConformance(t *testing.T) {
	kubeconfig := kubeconfigFor(t)
	image := clusterImage(t)
	client := clientFor(t, kubeconfig)
	namespace := "ultra-conformance"

	var provider *k8s.Provider
	conformance.RunWith(t, func(t *testing.T) ultra.EnvProvider {
		created, err := k8s.NewWithClient(client, k8s.Config{
			Namespace:    namespace,
			Image:        image,
			EndpointMode: k8s.EndpointModeNodePort,
			EndpointHost: "127.0.0.1",
			// The kind cluster forwards exactly this range to the host, so
			// the suite reaches the environment the same way a worker outside
			// the cluster would.
			NodePortRange: [2]int32{30080, 30082},
		})
		if err != nil {
			t.Fatal(err)
		}
		provider = created
		return created
	}, conformance.Options{
		Capabilities: ultra.ProviderCapabilities{
			Kind: ultra.ProviderKindBYOKubernetes,
			Supported: []ultra.ProviderCapability{
				ultra.CapabilityAdoptsOrphans,
				ultra.CapabilityEnumeratesResources,
				ultra.CapabilityServesToolEndpoint,
			},
			// A pod's workspace is an emptyDir, so restart does not preserve
			// it. The capability is deliberately unclaimed rather than the
			// step being skipped: the contract still requires a restart that
			// rotates the token and comes back serving tools.
			Notes: map[ultra.ProviderCapability]string{
				ultra.CapabilityRestartPreservesWorkspace: "environment workspaces are emptyDir volumes",
			},
		},
		Inspect: func(t *testing.T, ctx context.Context, envID ultra.EnvID) []string {
			t.Helper()
			resources, err := provider.Resources(ctx, envID)
			if err != nil {
				t.Fatalf("kubernetes inspection failed: %v", err)
			}
			// Prove the objects are Kubernetes' own, read back through the
			// API rather than reported by the adapter's bookkeeping.
			for _, item := range resources {
				if !strings.HasPrefix(item, "pod/") {
					continue
				}
				parts := strings.Split(item, "/")
				if len(parts) != 3 {
					t.Fatalf("malformed resource reference %q", item)
				}
				pod, err := client.CoreV1().Pods(parts[1]).Get(ctx, parts[2], metav1.GetOptions{})
				if err != nil {
					t.Fatalf("reported pod %q is not in the cluster: %v", item, err)
				}
				if pod.Labels[k8s.LabelManagedBy] != k8s.ManagedByValue {
					t.Fatalf("pod %q is not labelled as ours", item)
				}
			}
			return resources
		},
	})
}
