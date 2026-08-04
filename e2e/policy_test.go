package e2e_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
	"github.com/aleksclark/ultracore/testkit/modelscript"
)

// TestA36_PolicyEnforcement covers A3.6: denied tools produce uniform refusal
// events; empty ResourceKinds blocks provision; ChildInherit / IsSubset hold;
// MaxChildren is enforced; E1 Grants dual path is gone.
func TestA36_PolicyEnforcement(t *testing.T) {
	stack := harness.Up(t)
	ctx := context.Background()
	alice := stack.AliceClient()

	// Model tries bash (denied) then finishes.
	stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
		{
			Match: modelscript.UserContains("try bash"),
			ToolCalls: []modelscript.ToolCallSpec{
				{Name: "bash", Args: map[string]string{"command": "echo hi"}},
			},
		},
		{Match: modelscript.UserContains("try bash"), Sticky: true, Text: "done"},
	}})

	sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
		TenantId: string(stack.TenantA.ID), Title: "policy",
	}))
	if err != nil {
		t.Fatal(err)
	}
	run, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
		SessionId: sess.Msg.GetSession().GetId(),
		Prompt:    "try bash",
		Policy: &corev1.RunPolicy{
			AllowTools:   []string{"post_event"},
			ChildInherit: true,
		},
	}))
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(45 * time.Second)
	var stored *corev1.AgentRun
	for time.Now().Before(deadline) {
		got, err := alice.Agents.GetRun(ctx, connect.NewRequest(&corev1.GetRunRequest{RunId: run.Msg.GetRun().GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		st := got.Msg.GetRun().GetState()
		if st == corev1.RunState_RUN_STATE_COMPLETED || st == corev1.RunState_RUN_STATE_FAILED {
			stored = got.Msg.GetRun()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if stored == nil {
		t.Fatal("run did not finish")
	}
	if len(stored.GetPolicy().GetAllowTools()) != 1 || stored.GetPolicy().GetAllowTools()[0] != "post_event" {
		t.Fatalf("stored policy = %+v", stored.GetPolicy())
	}
	// Proto3 unset max_children defaults to DefaultRunPolicy's spawn cap when
	// allow_tools is non-empty (cannot distinguish 0 from unset on the wire).
	if stored.GetPolicy().GetMaxChildren() != 16 {
		t.Fatalf("max_children = %d, want default 16", stored.GetPolicy().GetMaxChildren())
	}
	// Empty resource_kinds must stay empty (none), not be widened to "*".
	if len(stored.GetPolicy().GetResourceKinds()) != 0 {
		t.Fatalf("resource_kinds should stay empty for partial policy, got %+v", stored.GetPolicy().GetResourceKinds())
	}
	if stored.GetPolicy().GetChildInherit() != true {
		t.Fatal("child_inherit must round-trip")
	}

	// Replay for permission_denied.
	sub, err := alice.Subscribe(ctx, sess.Msg.GetSession().GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	found := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !found {
		ev, err := sub.Next()
		if err != nil {
			break
		}
		if pd := ev.GetPayload().GetPermissionDenied(); pd != nil {
			found = true
			if pd.GetTool() != "bash" {
				t.Fatalf("denied tool = %q", pd.GetTool())
			}
		}
	}
	if !found {
		t.Fatal("expected permission_denied for bash")
	}

	// Resource kind empty blocks provision at the type level.
	p := uc.RunPolicy{AllowTools: []string{"provision_resource"}}
	if p.AllowsResourceKind(uc.ResourceKindDevEnv) {
		t.Fatal("empty ResourceKinds must deny provision")
	}

	// Subset refuse.
	parent := uc.DefaultRunPolicy()
	parent.MaxChildren = 2
	escape := uc.DefaultRunPolicy()
	escape.MaxChildren = 8
	if escape.IsSubset(parent) {
		t.Fatal("child MaxChildren > parent must fail IsSubset")
	}

	// ChildInherit: children receive parent policy verbatim even when spawn
	// asks for a narrower tools list.
	t.Run("child_inherit_verbatim", func(t *testing.T) {
		prompt := "inherit parent " + t.Name()
		childPrompt := "inherit child " + t.Name()
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains(prompt),
				ToolCalls: []modelscript.ToolCallSpec{{Name: "spawn_agent", Args: map[string]any{
					"prompt": childPrompt, "tools": []string{"post_event"},
				}}},
			},
			{Match: modelscript.UserContains(prompt), Sticky: true, Text: "parent done"},
			{Match: modelscript.UserContains(childPrompt), Sticky: true, Text: "child done"},
		}})
		sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
			TenantId: string(stack.TenantA.ID), Title: "inherit",
		}))
		if err != nil {
			t.Fatal(err)
		}
		parentRun, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
			SessionId: sess.Msg.GetSession().GetId(),
			Prompt:    prompt,
			Policy: &corev1.RunPolicy{
				AllowTools:    []string{"spawn_agent", "post_event", "wait_for_agents"},
				ResourceKinds: []string{"*"},
				MaxChildren:   4,
				ChildInherit:  true,
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		// Wait for a child to appear.
		var child uc.AgentRun
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			kids, err := stack.Store.Tenant(stack.TenantA.ID).Runs().Children(ctx, uc.RunID(parentRun.Msg.GetRun().GetId()))
			if err != nil {
				t.Fatal(err)
			}
			if len(kids) > 0 {
				child = kids[0]
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if child.ID == "" {
			t.Fatal("no child spawned")
		}
		// Under ChildInherit the tools shorthand is ignored: child == parent.
		if !child.Policy.AllowsTool("spawn_agent") {
			t.Fatalf("ChildInherit lost spawn_agent: %+v", child.Policy)
		}
		if !child.Policy.ChildInherit {
			t.Fatalf("ChildInherit flag not propagated: %+v", child.Policy)
		}
		if child.Policy.MaxChildren != 4 {
			t.Fatalf("MaxChildren = %d, want 4", child.Policy.MaxChildren)
		}
	})

	// MaxChildren enforced across spawn attempts.
	t.Run("max_children_cap", func(t *testing.T) {
		prompt := "cap parent " + t.Name()
		stack.Model.SetScript(modelscript.Script{Turns: []modelscript.Turn{
			{
				Match: modelscript.UserContains(prompt),
				ToolCalls: []modelscript.ToolCallSpec{
					{Name: "spawn_agent", Args: map[string]any{"prompt": "c1 " + t.Name(), "tools": []string{"post_event"}}},
					{Name: "spawn_agent", Args: map[string]any{"prompt": "c2 " + t.Name(), "tools": []string{"post_event"}}},
					{Name: "spawn_agent", Args: map[string]any{"prompt": "c3 " + t.Name(), "tools": []string{"post_event"}}},
				},
			},
			{Match: modelscript.UserContains(prompt), Sticky: true, Text: "parent done"},
			{Match: modelscript.UserContains("c1 " + t.Name()), Sticky: true, Text: "child done"},
			{Match: modelscript.UserContains("c2 " + t.Name()), Sticky: true, Text: "child done"},
			{Match: modelscript.UserContains("c3 " + t.Name()), Sticky: true, Text: "child done"},
		}})
		sess, err := alice.Sessions.CreateSession(ctx, connect.NewRequest(&corev1.CreateSessionRequest{
			TenantId: string(stack.TenantA.ID), Title: "cap",
		}))
		if err != nil {
			t.Fatal(err)
		}
		parentRun, err := alice.Agents.StartRun(ctx, connect.NewRequest(&corev1.StartRunRequest{
			SessionId: sess.Msg.GetSession().GetId(),
			Prompt:    prompt,
			Policy: &corev1.RunPolicy{
				AllowTools:  []string{"spawn_agent", "post_event"},
				MaxChildren: 2,
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		// Wait for parent terminal.
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			got, err := stack.Store.Tenant(stack.TenantA.ID).Runs().Get(ctx, uc.RunID(parentRun.Msg.GetRun().GetId()))
			if err != nil {
				t.Fatal(err)
			}
			if got.State.Terminal() {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		kids, err := stack.Store.Tenant(stack.TenantA.ID).Runs().Children(ctx, uc.RunID(parentRun.Msg.GetRun().GetId()))
		if err != nil {
			t.Fatal(err)
		}
		if len(kids) != 2 {
			t.Fatalf("MaxChildren=2 but got %d children", len(kids))
		}
		// A permission_denied for the third spawn should exist.
		evs, err := stack.Store.Tenant(stack.TenantA.ID).Events().Range(ctx, uc.SessionID(sess.Msg.GetSession().GetId()), 0, 512)
		if err != nil {
			t.Fatal(err)
		}
		denied := false
		for _, e := range evs {
			if e.Kind == uc.EventKindPermissionDenied {
				denied = true
				break
			}
		}
		if !denied {
			t.Fatal("expected permission_denied when MaxChildren exceeded")
		}
	})
}
