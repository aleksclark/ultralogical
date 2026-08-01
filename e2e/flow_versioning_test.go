package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
	"github.com/aleksclark/ultralogical/testkit/testclient"
)

// putFlow stores a definition and fails the test if the server rejects it.
func putFlow(t *testing.T, c *testclient.Client, org, name, definition string) *ultrav1.Flow {
	t.Helper()
	resp, err := c.Flows.PutFlow(context.Background(), connect.NewRequest(&ultrav1.PutFlowRequest{
		OrgId: org, Name: name, DefinitionJson: definition,
	}))
	if err != nil {
		t.Fatalf("PutFlow(%s): %v", name, err)
	}
	return resp.Msg.GetFlow()
}

// invokeFlow invokes a flow with JSON parameters.
func invokeFlow(t *testing.T, c *testclient.Client, session, name, params string) *ultrav1.InvokeFlowResponse {
	t.Helper()
	resp, err := c.Flows.InvokeFlow(context.Background(), connect.NewRequest(&ultrav1.InvokeFlowRequest{
		SessionId: session, Name: name, ParamsJson: params,
	}))
	if err != nil {
		t.Fatalf("InvokeFlow(%s): %v", name, err)
	}
	return resp.Msg
}

// getInvocation reads one invocation through the public API.
func getInvocation(t *testing.T, c *testclient.Client, id string) *ultrav1.FlowInvocation {
	t.Helper()
	resp, err := c.Flows.GetFlowInvocation(context.Background(),
		connect.NewRequest(&ultrav1.GetFlowInvocationRequest{InvocationId: id}))
	if err != nil {
		t.Fatalf("GetFlowInvocation(%s): %v", id, err)
	}
	return resp.Msg.GetInvocation()
}

// awaitInvocationState polls until the invocation reaches one of the wanted
// states, then returns it. Waiting on observable API state (not on a sleep) is
// what makes these assertions about the system rather than about timing.
func awaitInvocationState(t *testing.T, c *testclient.Client, id string, timeout time.Duration,
	want ...ultrav1.FlowInvocationState) *ultrav1.FlowInvocation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *ultrav1.FlowInvocation
	for time.Now().Before(deadline) {
		last = getInvocation(t, c, id)
		for _, state := range want {
			if last.GetState() == state {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("invocation %s never reached %v within %s (last %v, reason %q)",
		id, want, timeout, last.GetState(), last.GetTerminalReason())
	return nil
}

// invocationTerminal waits for any terminal invocation state.
func invocationTerminal(t *testing.T, c *testclient.Client, id string, timeout time.Duration) *ultrav1.FlowInvocation {
	t.Helper()
	return awaitInvocationState(t, c, id, timeout,
		ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED,
		ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED,
		ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLED)
}

// flowFieldErrors extracts the structured field errors a rejection carried.
func flowFieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	var connectErr *connect.Error
	if !errorsAs(err, &connectErr) {
		t.Fatalf("expected a connect error, got %T: %v", err, err)
	}
	out := map[string]string{}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if field, ok := value.(*ultrav1.FlowFieldError); ok {
			out[field.GetPath()] = field.GetCode()
		}
	}
	return out
}

// errorsAs is a local alias so this file does not import errors solely for one
// type assertion helper used in assertions.
func errorsAs(err error, target **connect.Error) bool {
	for err != nil {
		if typed, ok := err.(*connect.Error); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// flowScenario labels the flow suite's model turns.
const flowScenario = 9

// flowScript answers every prompt the flow acceptance suite produces. Turns are
// sticky and matcher-selected because a flow starts several agents whose order
// is deliberately not fixed by the test.
func flowScript() modelscript.Script {
	return modelscript.Script{Turns: []modelscript.Turn{
		{Scenario: flowScenario, Match: modelscript.UserContains("flow reviewer"), Sticky: true, Text: "reviewed"},
		{Scenario: flowScenario, Match: modelscript.UserContains("flow summarizer"), Sticky: true, Text: "summarized"},
		{Scenario: flowScenario, Match: modelscript.UserContains("flow worker"), Sticky: true, Text: "worked"},
		{Scenario: flowScenario, Match: modelscript.UserContains("flow slow agent"), Sticky: true,
			Text: "eventually", ChunkDelay: 30 * time.Second},
		{Scenario: flowScenario, Match: modelscript.UserContains("flow env agent"), Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{"command": "ls /work"}}}},
		{Scenario: flowScenario, Match: modelscript.UserContains("flow env agent"), Sticky: true, Text: "environment inspected"},
		{Scenario: flowScenario, Match: modelscript.UserContains("example single agent"), Sticky: true, Text: "example reviewed"},
		{Scenario: flowScenario, Match: modelscript.UserContains("example environment agent"), Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{"command": "ls /work/reports"}}}},
		{Scenario: flowScenario, Match: modelscript.UserContains("example environment agent"), Sticky: true, Text: "example inspected"},
		{Scenario: flowScenario, Match: modelscript.UserContains("example multi agent planner"), Sticky: true, Text: "planned"},
		{Scenario: flowScenario, Match: modelscript.UserContains("example multi agent worker"), Sticky: true, Text: "worked"},
		{Scenario: flowScenario, Match: modelscript.UserContains("example multi agent summarizer"), Sticky: true, Text: "summarized"},
		{Scenario: flowScenario, Match: modelscript.UserContains("catalog supervisor"), Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{"agent_ref": "helper"}}}},
		{Scenario: flowScenario, Match: modelscript.UserContains("catalog supervisor"), Sticky: true, Text: "supervised"},
		{Scenario: flowScenario, Match: modelscript.UserContains("catalog helper"), Sticky: true, Text: "helped"},
		{Scenario: flowScenario, Match: modelscript.UserContains("forbidden supervisor"), Sticky: true,
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{"agent_ref": "not_spawnable"}}}},
		{Scenario: flowScenario, Match: modelscript.UserContains("forbidden supervisor"), Sticky: true, Text: "denied and moved on"},
		{Scenario: flowScenario, Match: modelscript.UserContains("never started"), Sticky: true, Text: "should not happen"},
	}}
}

const singleAgentFlow = `{
  "params": {"subject": {"type": "string", "required": true}},
  "agents": {"reviewer": {"prompt": "flow reviewer: {{.subject}}", "entry": true, "tools": ["post_event"]}}
}`

// A9.1 — versions are immutable, assignment is monotonic, and an explicit
// rewrite of an existing version is refused rather than silently accepted.
func TestA91_VersioningAndValidation(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	v1 := putFlow(t, alice, org, "review", singleAgentFlow)
	if v1.GetVersion() != 1 {
		t.Fatalf("first version = %d, want 1", v1.GetVersion())
	}
	second := strings.Replace(singleAgentFlow, "flow reviewer:", "flow reviewer v2:", 1)
	v2 := putFlow(t, alice, org, "review", second)
	if v2.GetVersion() != 2 {
		t.Fatalf("second version = %d, want 2", v2.GetVersion())
	}

	// Rewriting version 1 must be refused, and version 1 must still read back
	// byte-identical: an invocation that pinned it has to keep replaying it.
	_, err := alice.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{
		OrgId: org, Name: "review", DefinitionJson: second, Version: 1,
	}))
	if err == nil {
		t.Fatal("overwriting an existing flow version was accepted")
	}
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("overwrite error code = %v, want already_exists", connect.CodeOf(err))
	}
	pinned, err := alice.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: org, Name: "review", Version: 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Msg.GetFlow().GetDefinitionJson() != singleAgentFlow {
		t.Fatal("version 1 changed after a rejected overwrite")
	}
	if pinned.Msg.GetFlow().GetId() != v1.GetId() {
		t.Fatal("version 1 identity changed")
	}

	// An unversioned Get resolves the latest.
	latest, err := alice.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{OrgId: org, Name: "review"}))
	if err != nil {
		t.Fatal(err)
	}
	if latest.Msg.GetFlow().GetVersion() != 2 {
		t.Fatalf("latest version = %d, want 2", latest.Msg.GetFlow().GetVersion())
	}

	versions, err := alice.Flows.ListFlowVersions(ctx, connect.NewRequest(&ultrav1.ListFlowVersionsRequest{
		OrgId: org, Name: "review",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions.Msg.GetFlows()) != 2 {
		t.Fatalf("ListFlowVersions returned %d, want 2", len(versions.Msg.GetFlows()))
	}
	if versions.Msg.GetFlows()[0].GetVersion() != 2 {
		t.Fatal("versions are not newest-first")
	}
}

// A9.1 — concurrent auto-assigned writes converge on distinct ascending
// versions with nothing overwritten. Two writers reading max(version) without
// serialization would either collide or silently lose a definition.
func TestA91_ConcurrentVersionConvergence(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	org := string(stack.OrgA.ID)

	const writers = 8
	var wg sync.WaitGroup
	results := make([]int32, writers)
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			definition := fmt.Sprintf(
				`{"agents":{"reviewer":{"prompt":"flow reviewer: writer %d","entry":true,"tools":["post_event"]}}}`, index)
			resp, err := alice.Flows.PutFlow(context.Background(), connect.NewRequest(&ultrav1.PutFlowRequest{
				OrgId: org, Name: "racy", DefinitionJson: definition,
			}))
			if err != nil {
				errs[index] = err
				return
			}
			results[index] = resp.Msg.GetFlow().GetVersion()
		}(i)
	}
	wg.Wait()
	seen := map[int32]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent writer %d failed: %v", i, err)
		}
		if seen[results[i]] {
			t.Fatalf("version %d was assigned twice", results[i])
		}
		seen[results[i]] = true
	}
	for version := int32(1); version <= writers; version++ {
		if !seen[version] {
			t.Fatalf("version %d was never assigned: %v", version, seen)
		}
	}
	// Every version must still be readable and distinct: convergence means
	// nothing was overwritten, not merely that no write errored.
	bodies := map[string]bool{}
	for version := int32(1); version <= writers; version++ {
		got, err := alice.Flows.GetFlow(context.Background(), connect.NewRequest(&ultrav1.GetFlowRequest{
			OrgId: org, Name: "racy", Version: version,
		}))
		if err != nil {
			t.Fatalf("version %d unreadable: %v", version, err)
		}
		body := got.Msg.GetFlow().GetDefinitionJson()
		if bodies[body] {
			t.Fatalf("version %d duplicates another version's definition", version)
		}
		bodies[body] = true
	}
}

// A9.1 — every invalid definition is refused with stable typed field paths,
// through both PutFlow and ValidateFlow, and nothing is persisted.
func TestA91_ValidationWall(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	cases := []struct {
		name       string
		definition string
		path       string
		code       string
	}{
		{"template syntax", `{"agents":{"a":{"prompt":"{{","entry":true}}}`,
			"agents.a.prompt", ultra.FlowErrInvalidTemplate},
		{"undeclared parameter", `{"agents":{"a":{"prompt":"hi {{.who}}","entry":true}}}`,
			"agents.a.prompt", ultra.FlowErrUnknownParam},
		{"unknown tool", `{"agents":{"a":{"prompt":"x","entry":true,"tools":["bsh"]}}}`,
			"agents.a.tools[0]", ultra.FlowErrUnknownTool},
		{"dangling env", `{"agents":{"a":{"prompt":"x","entry":true,"envs":["ghost"]}}}`,
			"agents.a.envs[0]", ultra.FlowErrUnknownEnv},
		{"dangling agent", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","after":["ghost"]}}}`,
			"agents.b.after[0]", ultra.FlowErrUnknownAgent},
		{"cycle", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","after":["c"]},"c":{"prompt":"z","after":["b"]}}}`,
			"agents.b.after", ultra.FlowErrCycle},
		{"no entry agent", `{"agents":{"a":{"prompt":"x","spawnable":true}}}`,
			"agents", ultra.FlowErrNoEntryAgent},
		{"grant exceeds ceiling", `{"agents":{"a":{"prompt":"x","entry":true,"tools":["spawn_agent"],"may_spawn":true,"max_children":999}}}`,
			"agents.a.max_children", ultra.FlowErrInvalidGrant},
		{"param type mismatch", `{"params":{"p":{"type":"number","default":"x"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"params.p.default", ultra.FlowErrTypeMismatch},
		{"duplicate agent", `{"agents":{"a":{"prompt":"x","entry":true},"a":{"prompt":"y","entry":true}}}`,
			"agents.a", ultra.FlowErrDuplicateName},
		{"unknown field", `{"agents":{"a":{"prompt":"x","entry":true}},"surprise":1}`,
			"definition", ultra.FlowErrUnknownField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := alice.Flows.PutFlow(ctx, connect.NewRequest(&ultrav1.PutFlowRequest{
				OrgId: org, Name: "invalid-" + strings.ReplaceAll(tc.name, " ", "-"), DefinitionJson: tc.definition,
			}))
			if err == nil {
				t.Fatal("invalid definition was stored")
			}
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want invalid_argument", connect.CodeOf(err))
			}
			fields := flowFieldErrors(t, err)
			if got := fields[tc.path]; got != tc.code {
				t.Fatalf("PutFlow field errors = %v, want %s at %s", fields, tc.code, tc.path)
			}
			// ValidateFlow must report exactly the same paths and codes, so a
			// client can preview a rejection without attempting a write.
			resp, err := alice.Flows.ValidateFlow(ctx, connect.NewRequest(&ultrav1.ValidateFlowRequest{
				OrgId: org, DefinitionJson: tc.definition,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if resp.Msg.GetValid() {
				t.Fatal("ValidateFlow accepted an invalid definition")
			}
			validated := map[string]string{}
			for _, item := range resp.Msg.GetErrors() {
				validated[item.GetPath()] = item.GetCode()
			}
			if validated[tc.path] != tc.code {
				t.Fatalf("ValidateFlow errors = %v, want %s at %s", validated, tc.code, tc.path)
			}
		})
	}

	// Nothing may have been persisted by any rejection.
	listed, err := alice.Flows.ListFlows(ctx, connect.NewRequest(&ultrav1.ListFlowsRequest{OrgId: org}))
	if err != nil {
		t.Fatal(err)
	}
	for _, flow := range listed.Msg.GetFlows() {
		if strings.HasPrefix(flow.GetName(), "invalid-") {
			t.Fatalf("rejected definition %q was persisted", flow.GetName())
		}
	}

	valid, err := alice.Flows.ValidateFlow(ctx, connect.NewRequest(&ultrav1.ValidateFlowRequest{
		OrgId: org, DefinitionJson: singleAgentFlow,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !valid.Msg.GetValid() || len(valid.Msg.GetErrors()) != 0 {
		t.Fatalf("valid definition reported invalid: %v", valid.Msg.GetErrors())
	}
}

// A9.1 — a flow in another org is indistinguishable from one that does not
// exist, through every read path.
func TestA91_CrossOrgFlowIsNotFound(t *testing.T) {
	stack := harness.Up(t)
	alice, bob := stack.AliceClient(), stack.BobClient()
	ctx := context.Background()
	putFlow(t, alice, string(stack.OrgA.ID), "secret-review", singleAgentFlow)

	// Bob asks his own org for a flow only Alice's org has, and for one that
	// exists nowhere. The two answers must be identical.
	existing, errExisting := bob.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: string(stack.OrgB.ID), Name: "secret-review",
	}))
	missing, errMissing := bob.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: string(stack.OrgB.ID), Name: "no-such-flow",
	}))
	if errExisting == nil || errMissing == nil {
		t.Fatalf("cross-org read succeeded: %v %v", existing, missing)
	}
	if connect.CodeOf(errExisting) != connect.CodeOf(errMissing) ||
		connect.CodeOf(errExisting) != connect.CodeNotFound {
		t.Fatalf("codes differ: %v vs %v", connect.CodeOf(errExisting), connect.CodeOf(errMissing))
	}
	if errExisting.Error() != errMissing.Error() {
		t.Fatalf("messages differ: %q vs %q", errExisting.Error(), errMissing.Error())
	}

	// Bob naming Alice's org is denied identically to naming a bogus org.
	crossOrg, errCross := bob.Flows.GetFlow(ctx, connect.NewRequest(&ultrav1.GetFlowRequest{
		OrgId: string(stack.OrgA.ID), Name: "secret-review",
	}))
	if errCross == nil {
		t.Fatalf("bob read alice's flow: %v", crossOrg)
	}
	if errCross.Error() != errMissing.Error() {
		t.Fatalf("cross-org message %q differs from missing %q", errCross.Error(), errMissing.Error())
	}
}
