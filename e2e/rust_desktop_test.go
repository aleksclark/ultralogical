package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aleksclark/ultralogical/testkit/harness"
)

// runDesktopSuite launches one Rust integration-test binary against a fresh
// real stack. Each suite gets its own stack so provisioning-heavy environment
// tests cannot destabilize the session and streaming evidence.
func runDesktopSuite(t *testing.T, suite string) {
	t.Helper()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo unavailable")
	}
	dir, err := filepath.Abs("../ui/desktop")
	if err != nil {
		t.Fatal(err)
	}

	stack := harness.Up(t)
	// The desktop suite drives several independent scenarios against one
	// stack, so its turns are sticky and matcher-selected.
	stack.Model.SetScript(webScript())

	cmd := exec.Command("cargo", "test", "--test", suite, "--", "--nocapture", "--test-threads=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"ULTRAD_URL="+stack.BaseURL,
		"ULTRA_TOKEN="+harness.TokenAlice,
		"ULTRA_ORG_ID="+string(stack.OrgA.ID),
		"ULTRA_CANARY_KEY="+harness.CanaryAPIKey,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rust desktop suite %s: %v\n%s", suite, err, out)
	}
	// A suite that silently ran zero tests would otherwise pass. Require the
	// cargo summary to report passing tests.
	summary := string(out)
	if !strings.Contains(summary, "test result: ok.") || strings.Contains(summary, "0 passed") {
		t.Fatalf("rust desktop suite %s reported no passing tests:\n%s", suite, out)
	}
	t.Logf("rust desktop suite %s:\n%s", suite, out)
}

// A7.2/A7.3/A7.8 — the rendered dark GPUI window drives session, streaming,
// replay, awaiting, keystroke, and redaction evidence against the real stack.
func TestRustDesktopE2E(t *testing.T) { runDesktopSuite(t, "desktop_e2e") }

// A7.4/A7.6/A7.8 — the rendered GPUI window drives environment lifecycle,
// restart/epoch rotation, and usage evidence against real Bezalel containers.
func TestRustDesktopEnvironmentE2E(t *testing.T) { runDesktopSuite(t, "environment_e2e") }

// A5.x provider registration evidence through the rendered GPUI window.
func TestRustDesktopProviderE2E(t *testing.T) { runDesktopSuite(t, "provider_e2e") }
