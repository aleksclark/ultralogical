package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/sdk"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// A4.6 — Go SDK and TS SDK observe identical event sequences (payload-level
// comparison of kind+seq) for a scripted multi-run session.
func TestA46_ReplayParityGoVsTS(t *testing.T) {
	// Not parallel: coordinates a TS subprocess against a shared stack.
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not installed")
	}
	tsDir, err := filepath.Abs("../clients/ts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tsDir, "node_modules")); err != nil {
		t.Skip("clients/ts/node_modules missing")
	}

	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()

	// Scripted session on Go SDK.
	sessResp, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID),
		Title:    "parity",
		Labels:   map[string]string{"suite": "parity"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := sessResp.Msg.GetSession().GetId()

	for i := 0; i < 3; i++ {
		if _, err := alice.AppendUserMessage(ctx, sessionID, "parity-msg"); err != nil {
			t.Fatal(err)
		}
	}
	// Start a short run (modelscript).
	if _, _, err := alice.StartRun(ctx, sessionID, "say hi"); err != nil {
		t.Fatal(err)
	}
	// Wait for terminal-ish.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		events, err := alice.GetEvents(ctx, sessionID, 0, 0, 256)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range events {
			k := testclient.Kind(ev)
			if k == "run_completed" || k == "run_failed" || k == "run_awaiting" {
				goto collected
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
collected:
	goEvents, err := alice.GetEvents(ctx, sessionID, 0, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Seq  string `json:"seq"`
		Kind string `json:"kind"`
	}
	goRows := make([]row, 0, len(goEvents))
	for _, ev := range goEvents {
		goRows = append(goRows, row{Seq: itoa(ev.GetSeq()), Kind: sdk.EventKind(ev)})
	}

	// TS client replays the same session via getEvents.
	outFile := filepath.Join(t.TempDir(), "ts-replay.json")
	script := `
import { createClient, eventKind } from "./src/index.js";
import fs from "node:fs";
const client = createClient({ baseUrl: process.env.CORED_URL, apiKey: process.env.CORE_TOKEN });
const events = await client.getEvents(process.env.CORE_SESSION_ID, 0n);
const rows = events.map(e => ({ seq: e.seq.toString(), kind: eventKind(e) }));
fs.writeFileSync(process.env.TS_REPLAY_OUT, JSON.stringify(rows));
`
	scriptPath := filepath.Join(t.TempDir(), "parity.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("npx", "tsx", scriptPath)
	cmd.Dir = tsDir
	cmd.Env = append(os.Environ(),
		"CORED_URL="+stack.BaseURL,
		"CORE_TOKEN="+stack.KeyA,
		"CORE_SESSION_ID="+sessionID,
		"TS_REPLAY_OUT="+outFile,
	)
	// Prefer node --experimental-strip-types or vitest; fall back to writing via vitest inline.
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback: use node with tsx if available, else pure node dynamic import via vitest run of a tiny file.
		t.Logf("tsx path failed (%v): %s; trying node --import tsx", err, out)
		cmd2 := exec.Command("node", "--import", "tsx", scriptPath)
		cmd2.Dir = tsDir
		cmd2.Env = cmd.Env
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			// Last resort: write a temporary vitest file
			vt := filepath.Join(tsDir, ".parity.temp.test.ts")
			body := `
import { it, expect } from "vitest";
import { createClient, eventKind } from "./src/index.js";
import fs from "node:fs";
it("parity dump", async () => {
  const client = createClient({ baseUrl: process.env.CORED_URL!, apiKey: process.env.CORE_TOKEN! });
  const events = await client.getEvents(process.env.CORE_SESSION_ID!, 0n);
  const rows = events.map(e => ({ seq: e.seq.toString(), kind: eventKind(e) }));
  fs.writeFileSync(process.env.TS_REPLAY_OUT!, JSON.stringify(rows));
  expect(rows.length).toBeGreaterThan(0);
});
`
			if err := os.WriteFile(vt, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(vt)
			cmd3 := exec.Command("npx", "vitest", "run", ".parity.temp.test.ts", "--reporter=verbose")
			cmd3.Dir = tsDir
			cmd3.Env = cmd.Env
			if out3, err3 := cmd3.CombinedOutput(); err3 != nil {
				t.Fatalf("ts parity dump failed: %v\n%s\n%s\n%s", err3, out, out2, out3)
			}
		}
	}

	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read ts replay: %v", err)
	}
	var tsRows []row
	if err := json.Unmarshal(raw, &tsRows); err != nil {
		t.Fatal(err)
	}
	if len(tsRows) != len(goRows) {
		t.Fatalf("parity length go=%d ts=%d\ngo=%v\nts=%v", len(goRows), len(tsRows), goRows, tsRows)
	}
	for i := range goRows {
		if goRows[i] != tsRows[i] {
			t.Fatalf("parity mismatch at %d: go=%+v ts=%+v", i, goRows[i], tsRows[i])
		}
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [32]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
