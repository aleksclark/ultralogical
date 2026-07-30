package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// A1.6 — Playwright golden against the real stack. Locally skips until
// Playwright Chromium is installed; CI installs it.
func TestA16_WebGolden(t *testing.T) {
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
	if os.Getenv("CI") == "" && os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		t.Skip("Playwright browser not configured locally")
	}

	stack := harness.Up(t)
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Text: "I need input", ChunkSize: 4, ToolCalls: []modelscript.ToolCallSpec{
			{Name: "ask_user", Args: map[string]any{"question": "Which color?", "choices": []string{"red", "blue"}}},
		}},
		{Text: "great choice of blue", ChunkSize: 4},
	}})

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(),
		"ULTRAD_URL="+stack.BaseURL,
		"ULTRA_TOKEN="+harness.TokenAlice,
		"WEB_PORT=15317",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Playwright failed: %v\n%s", err, out)
	}
}
