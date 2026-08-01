package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	// A suite that silently ran zero tests, or quietly ignored some, would
	// otherwise pass. Parse the counts rather than matching substrings: "10
	// passed" contains "0 passed", so substring checks are not sound here.
	summary := string(out)
	if !strings.Contains(summary, "test result: ok.") {
		t.Fatalf("rust desktop suite %s did not report a successful result:\n%s", suite, out)
	}
	passed, ignored := cargoCounts(t, summary)
	if passed == 0 {
		t.Fatalf("rust desktop suite %s reported no passing tests:\n%s", suite, out)
	}
	if ignored > 0 {
		t.Fatalf("rust desktop suite %s ignored %d tests; GPUI evidence cannot be skipped:\n%s", suite, ignored, out)
	}
	// Every declared test must have run: a suite silently shrinking is the
	// same failure as evidence that never existed.
	declared := cargoDeclared(t, suite)
	if passed != declared {
		t.Fatalf("rust desktop suite %s passed %d of %d declared tests:\n%s", suite, passed, declared, out)
	}
	t.Logf("rust desktop suite %s passed %d/%d declared GPUI tests", suite, passed, declared)
	t.Logf("rust desktop suite %s:\n%s", suite, out)
}

// cargoResult matches the counts in cargo's summary line.
var cargoResult = regexp.MustCompile(`test result: ok\. (\d+) passed; (\d+) failed; (\d+) ignored`)

// cargoCounts extracts the passed and ignored counts from a cargo summary.
func cargoCounts(t *testing.T, summary string) (passed, ignored int) {
	t.Helper()
	match := cargoResult.FindStringSubmatch(summary)
	if match == nil {
		t.Fatalf("could not parse the cargo summary:\n%s", summary)
	}
	passed, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	ignored, err = strconv.Atoi(match[3])
	if err != nil {
		t.Fatal(err)
	}
	return passed, ignored
}

// cargoTestDecl matches a GPUI test declaration in a suite file.
var cargoTestDecl = regexp.MustCompile(`(?m)^#\[gpui::test\]`)

// cargoDeclared counts the GPUI tests a suite file declares, so a suite cannot
// quietly stop exercising a capability it used to cover.
func cargoDeclared(t *testing.T, suite string) int {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("../ui/desktop/tests", suite+".rs"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	declared := len(cargoTestDecl.FindAllString(string(body), -1))
	if declared == 0 {
		t.Fatalf("%s declares no GPUI tests", path)
	}
	return declared
}

// A7.2/A7.3/A7.8 — the rendered dark GPUI window drives session, streaming,
// replay, awaiting, keystroke, and redaction evidence against the real stack.
func TestRustDesktopE2E(t *testing.T) { runDesktopSuite(t, "desktop_e2e") }

// A7.4/A7.6/A7.8 — the rendered GPUI window drives environment lifecycle,
// restart/epoch rotation, and usage evidence against real Bezalel containers.
func TestRustDesktopEnvironmentE2E(t *testing.T) { runDesktopSuite(t, "environment_e2e") }

// A5.x provider registration evidence through the rendered GPUI window.
func TestRustDesktopProviderE2E(t *testing.T) { runDesktopSuite(t, "provider_e2e") }
