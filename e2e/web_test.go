package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// webScript is the model script the browser suite runs against. Turns are
// sticky and matcher-selected because the specs execute in an arbitrary order
// and each starts its own run: a prompt must always get the same response.
func webScript() modelscript.Script {
	return modelscript.Script{Turns: []modelscript.Turn{
		{
			Match:     modelscript.UserContains("ask me something"),
			Text:      "I need input",
			Sticky:    true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "ask_user", Args: map[string]any{"question": "Which color?", "choices": []string{"red", "blue"}}}},
		},
		{Match: modelscript.UserContains("blue"), Text: "great choice of blue", ChunkSize: 4, Sticky: true},
		// A7.2: many small chunks so the browser must render intermediate
		// frames rather than a single final flush.
		{
			Match:      modelscript.UserContains("stream to me"),
			Text:       "streaming one two three four five six seven eight",
			ChunkSize:  3,
			ChunkDelay: 60_000_000, // 60ms
			Sticky:     true,
		},
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

	stack := harness.Up(t)
	stack.Model.SetScript(webScript())

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(),
		"ULTRAD_URL="+stack.BaseURL,
		"ULTRA_TOKEN="+harness.TokenAlice,
		"ULTRA_CANARY_KEY="+harness.CanaryAPIKey,
		"WEB_PORT=15317",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Playwright failed: %v\n%s", err, out)
	}
}
