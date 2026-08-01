package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/aleksclark/ultralogical/cmd/ultra/cli"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// run executes one CLI invocation against the harness stack and returns its
// exit code, stdout, and stderr. Tests drive exactly the code path a user gets.
func run(t *testing.T, stack *harness.Stack, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	environment := cli.NewEnv(stack.BaseURL, harness.TokenAlice, string(stack.OrgA.ID))
	code, err := cli.RunWithEnv(args, environment, &stdout, &stderr)
	if err != nil {
		stderr.WriteString(err.Error())
	}
	return code, stdout.String(), stderr.String()
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validFlow = `{
  "params": {"subject": {"type": "string", "required": true}},
  "agents": {"reviewer": {"prompt": "cli reviewer: {{.subject}}", "entry": true, "tools": ["post_event"]}}
}`

const invalidFlow = `{"agents":{"reviewer":{"prompt":"{{","entry":true,"tools":["bsh"]}}}`

// cliScript answers the prompts the CLI suite's flows produce.
func cliScript() modelscript.Script {
	return modelscript.Script{Turns: []modelscript.Turn{
		{Match: modelscript.UserContains("cli reviewer"), Sticky: true, Text: "reviewed"},
		{Match: modelscript.UserContains("cli slow"), Sticky: true, Text: "eventually", ChunkDelay: 30 * time.Second},
	}}
}

// A9.7 — validation failures are reported with the server's own field paths and
// exit nonzero, in both human and JSON output.
func TestFlowValidateReportsTypedErrors(t *testing.T) {
	stack := harness.Up(t)
	path := writeTemp(t, invalidFlow)

	code, stdout, _ := run(t, stack, "flow", "validate", "-f", path)
	if code == 0 {
		t.Fatalf("invalid definition exited 0:\n%s", stdout)
	}
	for _, want := range []string{"agents.reviewer.prompt", "invalid_template", "agents.reviewer.tools[0]", "unknown_tool"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = run(t, stack, "flow", "validate", "-f", path, "--json")
	if code == 0 {
		t.Fatalf("invalid definition exited 0 in JSON mode:\n%s", stdout)
	}
	var payload struct {
		Error  string `json:"error"`
		Fields []struct {
			Path string `json:"path"`
			Code string `json:"code"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("JSON output is not machine-readable: %v\n%s", err, stdout)
	}
	byPath := map[string]string{}
	for _, field := range payload.Fields {
		byPath[field.Path] = field.Code
	}
	if byPath["agents.reviewer.prompt"] != "invalid_template" {
		t.Fatalf("field errors = %v", byPath)
	}
	if byPath["agents.reviewer.tools[0]"] != "unknown_tool" {
		t.Fatalf("field errors = %v", byPath)
	}

	// A valid definition validates and exits zero.
	valid := writeTemp(t, validFlow)
	code, stdout, stderr := run(t, stack, "flow", "validate", "-f", valid, "--json")
	if code != 0 {
		t.Fatalf("valid definition exited %d:\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"valid": true`) {
		t.Fatalf("valid output = %s", stdout)
	}

	// A rejected write is also a typed nonzero failure carrying field paths.
	code, stdout, stderr = run(t, stack, "flow", "put", "bad", "-f", path, "--json")
	if code == 0 {
		t.Fatalf("storing an invalid definition exited 0:\n%s", stdout)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "agents.reviewer.prompt") {
		t.Fatalf("put failure lost its field paths:\n%s", combined)
	}
}

// A9.7 — put, get, list, and versions agree with API state.
func TestFlowPutGetListRoundTrip(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	path := writeTemp(t, validFlow)

	code, stdout, stderr := run(t, stack, "flow", "put", "cli-review", "-f", path, "--json")
	if code != 0 {
		t.Fatalf("put exited %d:\n%s\n%s", code, stdout, stderr)
	}
	var stored struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &stored); err != nil {
		t.Fatalf("put output is not JSON: %v\n%s", err, stdout)
	}
	if stored.Version != 1 || stored.Name != "cli-review" {
		t.Fatalf("put returned %+v", stored)
	}

	// The API agrees with what the CLI reported.
	apiFlow, err := alice.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: string(stack.OrgA.ID), Name: "cli-review",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if apiFlow.Msg.GetFlow().GetId() != stored.ID {
		t.Fatalf("CLI reported %s, API has %s", stored.ID, apiFlow.Msg.GetFlow().GetId())
	}

	// A second put assigns version 2; version 1 stays readable and identical.
	if code, stdout, stderr = run(t, stack, "flow", "put", "cli-review", "-f", path, "--json"); code != 0 {
		t.Fatalf("second put exited %d:\n%s\n%s", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, stack, "flow", "get", "cli-review", "--version", "1", "--json")
	if code != 0 {
		t.Fatalf("get exited %d:\n%s\n%s", code, stdout, stderr)
	}
	var fetched struct {
		Version        int    `json:"version"`
		DefinitionJSON string `json:"definition_json"`
	}
	if err := json.Unmarshal([]byte(stdout), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Version != 1 || fetched.DefinitionJSON != validFlow {
		t.Fatalf("version 1 changed: %+v", fetched)
	}

	code, stdout, _ = run(t, stack, "flow", "versions", "cli-review", "--json")
	if code != 0 {
		t.Fatalf("versions exited %d:\n%s", code, stdout)
	}
	var versions struct {
		Versions []struct {
			Version int `json:"version"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(stdout), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions.Versions) != 2 || versions.Versions[0].Version != 2 {
		t.Fatalf("versions = %+v", versions)
	}

	code, stdout, _ = run(t, stack, "flow", "list", "--json")
	if code != 0 {
		t.Fatalf("list exited %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "cli-review") {
		t.Fatalf("list output = %s", stdout)
	}

	// A flow that does not exist is a typed nonzero failure.
	if code, stdout, stderr = run(t, stack, "flow", "get", "no-such-flow", "--json"); code == 0 {
		t.Fatalf("missing flow exited 0:\n%s\n%s", stdout, stderr)
	}
}

// A9.7 — invoke, status, and cancel drive a real invocation and match API state.
func TestFlowInvokeStatusAndCancel(t *testing.T) {
	stack := harness.Up(t)
	stack.Model.SetScript(cliScript())
	alice := stack.AliceClient()
	ctx := context.Background()
	session := createCLISession(t, stack)

	if code, stdout, stderr := run(t, stack, "flow", "put", "cli-invoke", "-f", writeTemp(t, validFlow)); code != 0 {
		t.Fatalf("put exited %d:\n%s\n%s", code, stdout, stderr)
	}
	code, stdout, stderr := run(t, stack, "flow", "invoke", "cli-invoke",
		"--session", session, "--param", "subject=databases", "--wait", "--json")
	if code != 0 {
		t.Fatalf("invoke --wait exited %d:\n%s\n%s", code, stdout, stderr)
	}
	var invocation struct {
		ID             string `json:"id"`
		State          string `json:"state"`
		TerminalReason string `json:"terminal_reason"`
		Runs           []struct {
			AgentName string `json:"agent_name"`
			State     string `json:"state"`
		} `json:"runs"`
		Progress []struct {
			Key string `json:"key"`
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(stdout), &invocation); err != nil {
		t.Fatalf("invoke output is not JSON: %v\n%s", err, stdout)
	}
	if invocation.State != "completed" || invocation.TerminalReason != "completed" {
		t.Fatalf("invocation = %+v", invocation)
	}
	if len(invocation.Runs) != 1 || invocation.Runs[0].AgentName != "reviewer" {
		t.Fatalf("runs = %+v", invocation.Runs)
	}
	if len(invocation.Progress) == 0 {
		t.Fatal("status carried no progress")
	}

	// CLI state matches API state exactly.
	api, err := alice.Flows.GetFlowInvocation(ctx, connect.NewRequest(&ultrav1.GetFlowInvocationRequest{
		InvocationId: invocation.ID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if api.Msg.GetInvocation().GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("API state = %v", api.Msg.GetInvocation().GetState())
	}
	if len(api.Msg.GetInvocation().GetProgress()) != len(invocation.Progress) {
		t.Fatalf("CLI reported %d progress entries, API has %d",
			len(invocation.Progress), len(api.Msg.GetInvocation().GetProgress()))
	}

	code, stdout, _ = run(t, stack, "flow", "status", invocation.ID)
	if code != 0 {
		t.Fatalf("status exited %d:\n%s", code, stdout)
	}
	if !strings.Contains(stdout, invocation.ID) || !strings.Contains(stdout, "completed") {
		t.Fatalf("status output = %s", stdout)
	}

	// Cancelling a slow invocation converges it, and the CLI reports the
	// converged state rather than the state at request time.
	const slowFlow = `{"agents":{"slow":{"prompt":"cli slow: keep going","entry":true,"tools":["post_event"]}}}`
	if code, stdout, stderr = run(t, stack, "flow", "put", "cli-slow", "-f", writeTemp(t, slowFlow)); code != 0 {
		t.Fatalf("put exited %d:\n%s\n%s", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, stack, "flow", "invoke", "cli-slow", "--session", session, "--json")
	if code != 0 {
		t.Fatalf("invoke exited %d:\n%s\n%s", code, stdout, stderr)
	}
	var slow struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &slow); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr = run(t, stack, "flow", "cancel", slow.ID, "--json"); code != 0 {
		t.Fatalf("cancel exited %d:\n%s\n%s", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, stack, "flow", "status", slow.ID, "--wait", "--json")
	if code != 0 {
		t.Fatalf("status --wait exited %d:\n%s\n%s", code, stdout, stderr)
	}
	var cancelled struct {
		State          string `json:"state"`
		TerminalReason string `json:"terminal_reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.State != "cancelled" || cancelled.TerminalReason != "cancelled" {
		t.Fatalf("cancelled invocation = %+v", cancelled)
	}

	// A cancelled invocation makes `invoke --wait` exit nonzero, so a script
	// can branch on the outcome rather than on the text.
	if code, stdout, stderr = run(t, stack, "flow", "invoke", "cli-slow", "--session", session,
		"--wait", "--timeout", "3s", "--json"); code == 0 {
		t.Fatalf("waiting on an unfinished invocation exited 0:\n%s\n%s", stdout, stderr)
	}
}

func createCLISession(t *testing.T, stack *harness.Stack) string {
	t.Helper()
	resp, err := stack.AliceClient().Sessions.CreateSession(context.Background(),
		connect.NewRequest(&ultrav1.CreateSessionRequest{OrgId: string(stack.OrgA.ID), Title: "cli"}))
	if err != nil {
		t.Fatal(err)
	}
	return resp.Msg.GetSession().GetId()
}

// A9.7 — the CLI is a public-API consumer. Importing the store or the queue
// would make it a backdoor, and would stop its passing tests from being
// evidence that the API is complete.
func TestCLIUsesOnlyPublicAPIs(t *testing.T) {
	forbidden := []string{
		"github.com/aleksclark/ultralogical/postgres",
		"github.com/aleksclark/ultralogical/jobqueue",
		"github.com/aleksclark/ultralogical/loop",
		"github.com/aleksclark/ultralogical/flowwork",
		"github.com/aleksclark/ultralogical/envwork",
		"github.com/jackc/pgx/v5",
	}
	root := filepath.Join("..", "..", "..", "cmd", "ultra")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, `"`)
			for _, banned := range forbidden {
				if value == banned || strings.HasPrefix(value, banned+"/") {
					t.Errorf("%s imports %s; the CLI must go through the public API", path, value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
