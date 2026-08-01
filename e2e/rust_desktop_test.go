package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
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

	// Two ultrad replicas: the desktop suite proves the window can reconnect
	// through a different one and rebuild identical rendered state.
	stack := harness.Up(t, harness.WithReplicas(2, 2))
	// The desktop suite drives several independent scenarios against one
	// stack, so its turns are sticky and matcher-selected.
	stack.Model.SetScript(desktopScript())

	cmd := exec.Command("cargo", "test", "--test", suite, "--", "--nocapture", "--test-threads=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"ULTRAD_URL="+stack.ReplicaBaseURLs[0],
		"ULTRAD_ALT_URL="+stack.ReplicaBaseURLs[1],
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

// A8.7 — the rendered GPUI window drives run trees, lanes, wait transitions,
// memory inspection, and replica reconnection.
func TestRustDesktopRunTreeE2E(t *testing.T) { runDesktopSuite(t, "run_tree_e2e") }

// desktopScenario labels the desktop suite's turns so a prompt that also
// matches a browser turn is reported rather than silently mis-served.
const desktopScenario = 2

// desktopScript extends the browser script with the orchestration scenarios the
// desktop suite drives. Turns are sticky and matcher-selected because the suite
// runs several independent scenarios against one server.
func desktopScript() modelscript.Script {
	script := webScript()
	script.Turns = append(script.Turns,
		// A cohort, so the window has a real run tree to paint.
		modelscript.Turn{
			Scenario: desktopScenario,
			Match:    modelscript.UserContains("desktop cohort fanout"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3m",
				"specs": []map[string]any{
					{"prompt": "desktop member alpha", "tools": []string{"post_event"}},
					{"prompt": "desktop member beta", "tools": []string{"post_event"}},
					{"prompt": "desktop member gamma", "tools": []string{"post_event"}},
				},
			}}},
		},
		modelscript.Turn{Scenario: desktopScenario, Match: modelscript.UserContains("desktop cohort fanout"), Sticky: true, Text: "cohort summarized"},
		modelscript.Turn{Scenario: desktopScenario, Match: modelscript.UserContains("desktop member"), Sticky: true, Text: "member finished"},
		// A cohort whose member never finishes, so a wait visibly times out.
		modelscript.Turn{
			Scenario: desktopScenario,
			Match:    modelscript.UserContains("desktop stalling fanout"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "4s",
				"specs":   []map[string]any{{"prompt": "desktop stalled member", "tools": []string{"post_event"}}},
			}}},
		},
		modelscript.Turn{Scenario: desktopScenario, Match: modelscript.UserContains("desktop stalling fanout"), Sticky: true, Text: "proceeded without the member"},
		modelscript.Turn{
			Match:      modelscript.UserContains("desktop stalled member"),
			Sticky:     true,
			Text:       "far too late",
			ChunkDelay: 30 * time.Second,
		},
		// An agent writes session memory the window then inspects.
		modelscript.Turn{
			Scenario: desktopScenario,
			Match:    modelscript.UserContains("desktop remember note"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "session_memory_set", Args: map[string]any{
				"key": "desktop.note", "value": map[string]any{"detail": "written by an agent"},
			}}},
		},
		modelscript.Turn{Scenario: desktopScenario, Match: modelscript.UserContains("desktop remember note"), Sticky: true, Text: "remembered"},
	)
	return script
}
