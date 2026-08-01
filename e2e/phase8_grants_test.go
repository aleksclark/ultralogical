package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
	"github.com/aleksclark/ultralogical/testkit/modelscript"
)

// A8.1 — grant narrowing holds through real environment tools.
//
// Discovery-time filtering is only a convenience. This test proves the
// authority decision is made at dispatch by having a child call environment
// tools it was never offered, against a real Bezalel container, and requiring
// a uniform denial that reveals nothing about what exists.
func TestA81_EnvironmentGrantEnforcement(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "env grants")

	// Two real environments: the child will be granted one and denied the
	// other, so "denied" is about authority rather than about absence.
	granted := provisionEnv(t, stack, sess.GetId())
	forbidden := provisionEnv(t, stack, sess.GetId())

	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		// The parent spawns a child restricted to bash on one environment.
		{
			Match: modelscript.UserContains("env parent"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
				"prompt": "env child", "tools": []string{"bash"},
				"env_ids": []string{granted.GetId()},
			}}},
		},
		{Match: modelscript.UserContains("env parent"), Text: "parent done"},
		// The child uses its granted tool on its granted environment.
		{
			Match: modelscript.UserContains("env child"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "bash", Args: map[string]any{
				"command": "echo granted-work",
			}}},
		},
		// Then it calls a tool it was never granted. The model can emit any
		// name; the server must refuse it.
		{
			Match: modelscript.UserContains("env child"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "write", Args: map[string]any{
				"file_path": "/work/forged.txt", "content": "should never be written",
			}}},
		},
		{Match: modelscript.UserContains("env child"), Sticky: true, Text: "child done"},
	}})

	parent, _, err := alice.StartRun(ctx, sess.GetId(), "env parent")
	if err != nil {
		t.Fatal(err)
	}
	parentID := ultra.RunID(parent.GetId())
	kids := childrenOf(t, stack, org, parentID, 1, 90*time.Second)
	child := kids[0]

	// The child's persisted authority names one environment and one tool.
	if child.Grants.EnvAll {
		t.Fatalf("child was granted every environment: %+v", child.Grants)
	}
	if !child.Grants.AllowsEnv(ultra.EnvID(granted.GetId())) {
		t.Fatalf("child lost the environment it was granted: %+v", child.Grants)
	}
	if child.Grants.AllowsEnv(ultra.EnvID(forbidden.GetId())) {
		t.Fatalf("child was granted an environment it should not see: %+v", child.Grants)
	}

	awaitRunOneOf(t, stack, org, child.ID, 3*time.Minute, ultra.RunCompleted, ultra.RunFailed)

	// The granted call really ran in the real container.
	results := toolResultsFor(t, stack, sess.GetId(), child.ID)
	var sawGranted bool
	for _, r := range results {
		if r.Name == "bash" && strings.Contains(r.Content, "granted-work") && !r.IsError {
			sawGranted = true
		}
	}
	if !sawGranted {
		t.Fatalf("the child's granted bash call did not produce real output: %+v", results)
	}

	// The ungranted tool was refused, with the same opaque message every
	// denial uses, and a PermissionDenied event for visibility.
	var sawDenial bool
	for _, r := range results {
		if r.Name == "write" {
			if !r.IsError {
				t.Fatalf("an ungranted tool call succeeded: %+v", r)
			}
			if !strings.Contains(r.Content, "permission denied") {
				t.Fatalf("denial message %q is not the uniform denial", r.Content)
			}
			// The denial must not disclose whether the tool or environment
			// exists, what it is named, or why precisely it was refused.
			for _, leak := range []string{granted.GetId(), forbidden.GetId(), "/work/forged.txt"} {
				if strings.Contains(r.Content, leak) {
					t.Fatalf("denial leaked %q: %s", leak, r.Content)
				}
			}
			sawDenial = true
		}
	}
	if !sawDenial {
		t.Fatalf("the ungranted tool call was never refused: %+v", results)
	}
	denials := collectEvents(t, stack, sess.GetId(), "permission_denied", 60*time.Second, 1)
	var denial ultra.PermissionDeniedPayload
	if err := json.Unmarshal(denials[len(denials)-1].Payload, &denial); err != nil {
		t.Fatal(err)
	}
	if denial.RunID != child.ID {
		t.Fatalf("denial attributed to %s, want child %s", denial.RunID, child.ID)
	}

	// The forged write never happened: the file does not exist in either
	// environment. This is the substantive check — a denial that still
	// performed the side effect would be worthless.
	for _, env := range []*ultrav1.DevEnv{granted, forbidden} {
		out, err := alice.Envs.ExecPreview(ctx, connect.NewRequest(&ultrav1.ExecPreviewRequest{
			EnvId: env.GetId(), Command: "test -f /work/forged.txt && echo PRESENT || echo ABSENT",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Msg.GetOutput(), "PRESENT") {
			t.Fatalf("the denied write was performed anyway in env %s", env.GetId())
		}
	}
}

// toolResult is one tool result observed in the session log.
type toolResult struct {
	Name    string
	Content string
	IsError bool
}

// toolResultsFor collects the tool results a specific run produced.
func toolResultsFor(t *testing.T, stack *harness.Stack, session string, run ultra.RunID) []toolResult {
	t.Helper()
	var out []toolResult
	var from int64
	for {
		batch, err := stack.Store.Org(stack.OrgA.ID).Events().Range(context.Background(), ultra.SessionID(session), from, 512)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, e := range batch {
			from = e.Seq
			if e.Kind != ultra.EventKindToolResult {
				continue
			}
			var payload ultra.ToolResultPayload
			if json.Unmarshal(e.Payload, &payload) != nil || payload.RunID != run {
				continue
			}
			out = append(out, toolResult{Name: payload.Name, Content: payload.Content, IsError: payload.IsError})
		}
	}
	return out
}

// A8.1 — a child may not wait on a run it did not parent. Waiting on an
// arbitrary run would leak both that run's existence and its result.
func TestA81_WaitAuthorityIsScopedToOwnChildren(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	ctx := context.Background()
	org := stack.OrgA.ID
	sess := createSession(t, alice, string(org), "wait authority")

	// An unrelated run exists in the same session.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{Match: modelscript.UserContains("stranger"), Sticky: true, Text: "stranger done"},
	}})
	stranger, _, err := alice.StartRun(ctx, sess.GetId(), "stranger run")
	if err != nil {
		t.Fatal(err)
	}
	strangerID := ultra.RunID(stranger.GetId())
	awaitRunOneOf(t, stack, org, strangerID, 90*time.Second, ultra.RunCompleted)

	// A second run tries to wait on it.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains("nosy"),
			ToolCalls: []modelscript.ToolCallSpec{{Name: "wait_for_agents", Args: map[string]any{
				"run_ids": []string{string(strangerID)},
			}}},
		},
		{Match: modelscript.UserContains("nosy"), Sticky: true, Text: "nosy finished"},
		{Match: modelscript.UserContains("stranger"), Sticky: true, Text: "stranger done"},
	}})
	nosy, _, err := alice.StartRun(ctx, sess.GetId(), "nosy run")
	if err != nil {
		t.Fatal(err)
	}
	nosyID := ultra.RunID(nosy.GetId())
	awaitRunOneOf(t, stack, org, nosyID, 2*time.Minute, ultra.RunCompleted, ultra.RunFailed)

	// The wait was refused, and no wait row was created for it.
	results := toolResultsFor(t, stack, sess.GetId(), nosyID)
	var refused bool
	for _, r := range results {
		if r.Name == "wait_for_agents" && r.IsError && strings.Contains(r.Content, "permission denied") {
			refused = true
		}
		// The denial must not confirm the other run's state or result.
		if strings.Contains(r.Content, "stranger done") {
			t.Fatalf("a refused wait leaked the other run's result: %+v", r)
		}
	}
	if !refused {
		t.Fatalf("waiting on another agent's run was not refused: %+v", results)
	}
	if waits := waitsOf(t, stack, org, nosyID); len(waits) != 0 {
		t.Fatalf("a refused wait still created %d wait rows", len(waits))
	}
}
