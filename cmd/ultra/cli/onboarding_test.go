package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/aleksclark/ultralogical/testkit/harness"
)

// documentedCommands extracts the `ultra ...` invocations a guide tells a
// reader to run. Parsing the guide rather than restating its commands is the
// point: a guide that drifts from what works fails here.
func documentedCommands(t *testing.T, guide string) []string {
	t.Helper()
	body, err := os.ReadFile(guide)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	inBlock := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock && strings.HasPrefix(strings.TrimSpace(line), "ultra ") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s documents no ultra commands", guide)
	}
	return out
}

// A10.9 — the onboarding guide is executed, not merely written. Every `ultra`
// command it documents is run against a real cluster in the order the guide
// gives them, so a guide that stopped working fails CI rather than a reader's
// afternoon.
func TestA109_KubernetesOnboardingGuideIsExecutable(t *testing.T) {
	kubeconfig := onboardingKubeconfig(t)
	stack := harness.Up(t)

	commands := documentedCommands(t, "../../../docs/onboarding-kubernetes.md")
	// The guide's register step embeds a kubeconfig through a shell pipeline
	// no test should reproduce; the substitution is what the reader's shell
	// would have produced.
	config, err := json.Marshal(map[string]any{
		"kubeconfig": kubeconfig, "namespace": "ultra-onboarding",
		"endpoint_mode": "nodeport", "endpoint_host": "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}

	ran := 0
	for _, documented := range commands {
		args := onboardingArgs(t, documented, string(config))
		if args == nil {
			continue
		}
		code, stdout, stderr := run(t, stack, args...)
		ran++
		// The removal step is documented as refused while environments exist;
		// here none do, so every documented command must succeed.
		if code != 0 {
			t.Fatalf("documented command %q failed with %d:\n%s\n%s", documented, code, stdout, stderr)
		}
		if strings.HasPrefix(documented, "ultra provider register") {
			if !strings.Contains(stdout, "byo-cluster") {
				t.Fatalf("registration did not report the provider it created:\n%s", stdout)
			}
		}
		if strings.HasPrefix(documented, "ultra provider list") {
			var listed struct {
				Providers []struct {
					Name         string `json:"name"`
					Capabilities []struct {
						Name      string `json:"name"`
						Supported bool   `json:"supported"`
					} `json:"capabilities"`
				} `json:"providers"`
			}
			if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
				t.Fatalf("the guide's list command is not machine-readable: %v\n%s", err, stdout)
			}
			// The guide tells the reader to check for this capability, so the
			// guide is wrong if a real cluster does not report it.
			found := false
			for _, provider := range listed.Providers {
				if provider.Name != "byo-cluster" {
					continue
				}
				for _, capability := range provider.Capabilities {
					if capability.Name == "serves_tool_endpoint" && capability.Supported {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("the guide says to check serves_tool_endpoint, but a real cluster did not report it:\n%s", stdout)
			}
		}
	}
	if ran < 4 {
		t.Fatalf("only %d documented commands ran; the guide's steps are not being executed", ran)
	}
}

// onboardingArgs turns one documented command line into CLI arguments,
// substituting the config the reader's shell would have built.
func onboardingArgs(t *testing.T, documented, config string) []string {
	t.Helper()
	switch {
	case strings.HasPrefix(documented, "ultra provider register"):
		return []string{"provider", "register", "byo-cluster", "--kind", "byo_k8s", "--config", config}
	case strings.HasPrefix(documented, "ultra provider list"):
		return []string{"provider", "list", "--json"}
	case strings.HasPrefix(documented, "ultra provider show"):
		return []string{"provider", "show", "byo-cluster", "--json"}
	case strings.HasPrefix(documented, "ultra provider remove"):
		return []string{"provider", "remove", "byo-cluster"}
	}
	t.Fatalf("the guide documents %q, which this runner does not know how to execute; "+
		"either the guide changed or the runner is stale", documented)
	return nil
}

// onboardingKubeconfig returns the test cluster's configuration.
func onboardingKubeconfig(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("ULTRA_TEST_KUBECONFIG"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ULTRA_TEST_KUBECONFIG is unreadable: %v", err)
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
