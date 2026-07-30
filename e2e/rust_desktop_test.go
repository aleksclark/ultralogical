package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// Phase 3.5 Rust desktop golden: runs the real desktop core and generated
// tonic client against the same real stack as the web suite.
func TestRustDesktopE2E(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo unavailable")
	}
	stack := harness.Up(t)
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{{Text: "rust desktop completed"}}})
	dir, err := filepath.Abs("../ui/desktop")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cargo", "test", "--test", "desktop_e2e", "--", "--nocapture")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ULTRAD_URL="+stack.BaseURL, "ULTRA_TOKEN="+harness.TokenAlice, "ULTRA_ORG_ID="+string(stack.OrgA.ID))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rust desktop e2e: %v\n%s", err, out)
	}
}
