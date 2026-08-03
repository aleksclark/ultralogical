package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aleksclark/ultracore/testkit/harness"
)

// A0.1 (TS leg) — the generated TypeScript client performs the same
// roundtrip against the same real stack. Requires node + `npm ci` in
// clients/ts (CI does this; locally the test skips when absent).
func TestA01_TSClientSmoke(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not installed")
	}
	tsDir, err := filepath.Abs("../clients/ts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tsDir, "node_modules")); err != nil {
		t.Skip("clients/ts/node_modules missing; run `npm ci` in clients/ts first")
	}

	stack := harness.Up(t)

	cmd := exec.Command("npx", "vitest", "run", "--reporter=verbose")
	cmd.Dir = tsDir
	cmd.Env = append(os.Environ(),
		"CORED_URL="+stack.BaseURL,
		"CORE_TOKEN="+harness.TokenAlice,
		"CORE_ORG_ID="+string(stack.OrgA.ID),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ts smoke failed: %v\n%s", err, out)
	}
}
