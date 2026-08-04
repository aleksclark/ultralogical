package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
)

// jsonObject encodes a provider configuration, so a kubeconfig with newlines
// is escaped correctly rather than hand-quoted.
func jsonObject(value map[string]any) (string, error) {
	body, err := json.Marshal(value)
	return string(body), err
}

// kindKubeconfig returns a kubeconfig for the local test cluster, skipping
// when none is running.
func kindKubeconfig(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CORE_TEST_KUBECONFIG"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("CORE_TEST_KUBECONFIG is set but unreadable: %v", err)
		}
		return string(body)
	}
	cluster := os.Getenv("CORE_TEST_KIND_CLUSTER")
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

// A10.6 — registration probes the real control plane before persisting, and
// records what that control plane reported it can do.
//
// The previous version of this test registered every kind with a placeholder
// config, which only proved a string was accepted. Registration now reaches a
// cluster, so the test does too.
func TestA51_A106_ProviderRegistrationProbesTheControlPlane(t *testing.T) {
	kubeconfig := kindKubeconfig(t)
	stack := harness.Up(t)
	client := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.TenantA.ID)

	config, err := jsonObject(map[string]any{
		"kubeconfig": kubeconfig, "namespace": "ultra-registration",
		"endpoint_mode": "nodeport", "endpoint_host": "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := client.Providers.RegisterProvider(ctx, connect.NewRequest(&corev1.RegisterProviderRequest{
		TenantId: org, Kind: uc.ProviderKindBYOKubernetes, Name: "cluster", ConfigJson: config,
	}))
	if err != nil {
		t.Fatalf("registering a reachable cluster failed: %v", err)
	}
	if registered.Msg.GetProvider().GetKind() != uc.ProviderKindBYOKubernetes {
		t.Fatalf("kind = %q", registered.Msg.GetProvider().GetKind())
	}
	got, err := client.Providers.GetProvider(ctx, connect.NewRequest(&corev1.GetProviderRequest{
		TenantId: org, ProviderId: registered.Msg.GetProvider().GetId(),
	}))
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Msg.GetProvider().GetId() != registered.Msg.GetProvider().GetId() {
		t.Fatalf("GetProvider id mismatch")
	}

	// The probe's answer is stored with the registration, so a later decision
	// about what this provider can do never depends on reaching it again.
	stored, err := stack.Store.Tenant(stack.TenantA.ID).Providers().GetByName(ctx, "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Capabilities.Has(uc.CapabilityServesToolEndpoint) {
		t.Fatalf("a reachable cluster reported no tool endpoint capability: %+v", stored.Capabilities)
	}
	if stored.Capabilities.Kind != uc.ProviderKindBYOKubernetes {
		t.Fatalf("probed capabilities name kind %q", stored.Capabilities.Kind)
	}
}

// A10.6 — a registration that cannot reach its control plane, or whose
// configuration is wrong, is refused and persists nothing.
func TestA54_A106_UnreachableAndInvalidRegistrationsPersistNothing(t *testing.T) {
	stack := harness.Up(t)
	client := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.TenantA.ID)

	cases := []struct {
		name   string
		kind   string
		config string
		expect string
	}{
		{
			name: "unreachable cluster", kind: uc.ProviderKindBYOKubernetes,
			config: `{"kubeconfig":"apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: http://127.0.0.1:1\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: u\ncurrent-context: c\nusers:\n- name: u\n  user: {}\n"}`,
			expect: "unreachable",
		},
		{
			name: "unreachable nomad", kind: uc.ProviderKindBYONomad,
			config: `{"address":"http://127.0.0.1:1"}`,
			expect: "unreachable",
		},
		{
			name: "misspelled field", kind: uc.ProviderKindBYOKubernetes,
			config: `{"namespcae":"typo"}`,
			expect: "namespcae",
		},
		{
			name: "tunnel without a signing secret", kind: uc.ProviderKindTunnelLocal,
			config: `{"control_url":"http://127.0.0.1:1","token":"t"}`,
			expect: "signing secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Providers.RegisterProvider(ctx, connect.NewRequest(&corev1.RegisterProviderRequest{
				TenantId: org, Kind: tc.kind, Name: "refused-" + strings.ReplaceAll(tc.name, " ", "-"),
				ConfigJson: tc.config,
			}))
			if err == nil {
				t.Fatal("an unusable registration was accepted")
			}
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want invalid_argument", connect.CodeOf(err))
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.expect)) {
				t.Fatalf("the refusal does not explain the problem (want %q): %v", tc.expect, err)
			}
		})
	}

	// Nothing was persisted by any refusal.
	listed, err := client.Providers.ListProviders(ctx, connect.NewRequest(&corev1.ListProvidersRequest{TenantId: org}))
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range listed.Msg.GetProviders() {
		if strings.HasPrefix(provider.GetName(), "refused-") {
			t.Fatalf("a refused registration was persisted: %s", provider.GetName())
		}
	}

	// A kind the deployment does not host is refused by name, not silently
	// substituted with one that happens to be available.
	_, err = client.Providers.RegisterProvider(ctx, connect.NewRequest(&corev1.RegisterProviderRequest{
		TenantId: org, Kind: "not_a_provider", Name: "bogus", ConfigJson: `{}`,
	}))
	if err == nil {
		t.Fatal("an unknown provider kind was registered")
	}
}
