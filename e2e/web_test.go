package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// webScenario labels the browser suite's turns. Two labelled scenarios must
// never match one prompt; the script server refuses rather than guessing.
const webScenario = 1

// webScript is the model script the browser suite runs against. Turns are
// sticky and matcher-selected because the specs execute in an arbitrary order
// and each starts its own run: a prompt must always get the same response.
func webScript() modelscript.Script {
	return modelscript.Script{Turns: []modelscript.Turn{
		{
			Scenario:  webScenario,
			Match:     modelscript.UserContains("ask me something"),
			Text:      "I need input",
			Sticky:    true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "ask_user", Args: map[string]any{"question": "Which color?", "choices": []string{"red", "blue"}}}},
		},
		{Scenario: webScenario, Match: modelscript.UserContains("blue"), Text: "great choice of blue", ChunkSize: 4, Sticky: true},
		// A7.2: many small chunks so the browser must render intermediate
		// frames rather than a single final flush.
		{
			Scenario:   webScenario,
			Match:      modelscript.UserContains("stream to me"),
			Text:       "streaming one two three four five six seven eight",
			ChunkSize:  3,
			ChunkDelay: 60 * time.Millisecond,
			Sticky:     true,
		},
		// A8.7: a cohort, so the browser has a real run tree to render.
		{
			Scenario: webScenario,
			Match:    modelscript.UserContains("cohort work"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3m",
				"specs": []map[string]any{
					{"prompt": "browser member alpha", "tools": []string{"post_event"}},
					{"prompt": "browser member beta", "tools": []string{"post_event"}},
					{"prompt": "browser member gamma", "tools": []string{"post_event"}},
				},
			}}},
		},
		{Scenario: webScenario, Match: modelscript.UserContains("cohort work"), Sticky: true, Text: "cohort summarized"},
		{Scenario: webScenario, Match: modelscript.UserContains("browser member"), Sticky: true, Text: "member finished"},
		// A8.7: a cohort whose member never finishes, so the browser can show
		// a wait timing out.
		{
			Scenario: webScenario,
			Match:    modelscript.UserContains("stalling cohort"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "run_agent_cohort", Args: map[string]any{
				"timeout": "3s",
				"specs":   []map[string]any{{"prompt": "browser stalled member", "tools": []string{"post_event"}}},
			}}},
		},
		{Scenario: webScenario, Match: modelscript.UserContains("stalling cohort"), Sticky: true, Text: "proceeded without the member"},
		{
			Scenario:   webScenario,
			Match:      modelscript.UserContains("browser stalled member"),
			Sticky:     true,
			Text:       "far too late",
			ChunkDelay: 30 * time.Second,
		},
		// A8.7: an agent writes session memory the browser then inspects.
		{
			Scenario: webScenario,
			Match:    modelscript.UserContains("remember something"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "session_memory_set", Args: map[string]any{
				"key": "browser.note", "value": map[string]any{"detail": "written by an agent"},
			}}},
		},
		{Scenario: webScenario, Match: modelscript.UserContains("remember something"), Sticky: true, Text: "remembered"},
	}}
}

func webSuiteEnabled(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not installed")
	}
	webDir, err := filepath.Abs("../ui/web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(webDir, "node_modules")); err != nil {
		t.Skip("ui/web/node_modules missing; run npm ci")
	}
	// Browsers may not be installed locally. CI sets PLAYWRIGHT_BROWSERS_PATH.
	if os.Getenv("CI") == "" && os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" &&
		os.Getenv("ULTRA_WEB_TESTS") == "" {
		t.Skip("Playwright browser not configured locally")
	}
	return webDir
}

// A1.6/A7.2/A7.4/A7.6/A7.8 — Playwright golden against the real stack: the
// shipped dark shadcn application drives sessions, streaming, environments,
// restart/epoch rotation, and usage.
func TestA16_WebGolden(t *testing.T) {
	webDir := webSuiteEnabled(t)

	// Two ultrad replicas: the browser suite proves a client can reconnect
	// through a different one and rebuild identical state.
	stack := harness.Up(t, harness.WithReplicas(2, 2))
	stack.Model.SetScript(webScript())

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(),
		"ULTRAD_URL="+stack.ReplicaBaseURLs[0],
		"ULTRAD_ALT_URL="+stack.ReplicaBaseURLs[1],
		"ULTRA_TOKEN="+harness.TokenAlice,
		"ULTRA_CANARY_KEY="+harness.CanaryAPIKey,
		"WEB_PORT=15317",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Playwright failed: %v\n%s", err, out)
	}
	// A run that silently executed nothing, or quietly skipped a spec, would
	// otherwise pass. Require every declared spec to have actually passed.
	declared := countWebSpecs(t, webDir)
	if skipped := webCount(string(out), "skipped"); skipped > 0 {
		t.Fatalf("Playwright skipped %d browser tests; browser evidence must run:\n%s", skipped, out)
	}
	passed := webCount(string(out), "passed")
	if passed != declared {
		t.Fatalf("Playwright reported %d passing tests but %d are declared:\n%s", passed, declared, out)
	}
	t.Logf("Playwright passed %d/%d declared browser tests", passed, declared)
}

// countWebSpecs counts declared Playwright tests across the spec directory.
func countWebSpecs(t *testing.T, webDir string) int {
	t.Helper()
	specs, err := filepath.Glob(filepath.Join(webDir, "e2e", "*.spec.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatal("no Playwright spec files found")
	}
	declared := 0
	for _, spec := range specs {
		body, err := os.ReadFile(spec)
		if err != nil {
			t.Fatal(err)
		}
		declared += len(webTestDecl.FindAllString(string(body), -1))
		// A disabled spec is not evidence: fail loudly rather than quietly
		// shrinking the expected count.
		if disabled := webTestDisabled.FindAllString(string(body), -1); len(disabled) > 0 {
			t.Fatalf("%s disables %d test(s) with %v; browser evidence cannot be skipped",
				filepath.Base(spec), len(disabled), disabled)
		}
	}
	if declared == 0 {
		t.Fatal("Playwright spec files declare no tests")
	}
	return declared
}

var (
	webTestDecl     = regexp.MustCompile(`(?m)^test\s*\(`)
	webTestDisabled = regexp.MustCompile(`(?m)^test\.(skip|fixme|only)\s*\(`)
)

// webCount extracts a labelled count from Playwright's summary line, for
// example the 14 in "14 passed".
func webCount(out, label string) int {
	match := regexp.MustCompile(`(\d+) ` + regexp.QuoteMeta(label)).FindStringSubmatch(out)
	if match == nil {
		return 0
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return n
}
