package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

// healthReadinessFlow declares an environment that must pass a health check
// before its agent starts.
const healthReadinessFlow = `{
  "envs": {"main": {"provider_instance": "%s", "workdir": "/work", "readiness": "health"}},
  "agents": {"worker": {"prompt": "flow reviewer: capability probe", "entry": true,
                        "envs": ["main"], "tools": ["post_event"]}}
}`

// seedProvider registers a provider directly with the capabilities a probe
// would have reported. Registration itself probes a live control plane, and
// the point of this test is a provider whose control plane genuinely lacks a
// capability, which is not something the harness can stand up.
func seedProvider(t *testing.T, stack *harness.Stack, name string, capabilities ultra.ProviderCapabilities) {
	t.Helper()
	err := stack.Store.Org(stack.OrgA.ID).Providers().Create(context.Background(), ultra.ProviderInstance{
		ID: ultra.ProviderInstanceID(uuid.NewString()), OrgID: stack.OrgA.ID,
		Kind: ultra.ProviderKindLocalDocker, Name: name,
		RateClass: ultra.RateClassBYO, State: "ready", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A10.10 — a flow declaring a readiness policy a provider genuinely cannot
// serve is refused at invoke time with the typed field error, and the same
// flow succeeds against a capable provider.
//
// The two registrations share a kind deliberately: if the check still read a
// hard-coded kind list, both would behave identically and this test would
// fail. Only a check that reads what the control plane actually reported can
// tell them apart.
func TestA1010_ProviderCapabilityIsBehavioral(t *testing.T) {
	stack := harness.Up(t)
	alice := stack.AliceClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()
	org := string(stack.OrgA.ID)

	seedProvider(t, stack, "incapable", ultra.ProviderCapabilities{
		Kind:      ultra.ProviderKindLocalDocker,
		Supported: []ultra.ProviderCapability{ultra.CapabilityEnumeratesResources},
		Notes: map[ultra.ProviderCapability]string{
			ultra.CapabilityServesToolEndpoint: "this control plane publishes no reachable tool endpoint",
		},
	})
	seedProvider(t, stack, "capable", ultra.ProviderCapabilities{
		Kind: ultra.ProviderKindLocalDocker,
		Supported: []ultra.ProviderCapability{
			ultra.CapabilityServesToolEndpoint,
			ultra.CapabilityRestartPreservesWorkspace,
			ultra.CapabilityEnumeratesResources,
		},
	})

	putFlow(t, alice, org, "needs-health", fmt.Sprintf(healthReadinessFlow, "incapable"))
	session := createSession(t, alice, org, "capability")

	_, err := alice.Flows.InvokeFlow(ctx, connect.NewRequest(&ultrav1.InvokeFlowRequest{
		SessionId: session.GetId(), Name: "needs-health",
	}))
	if err == nil {
		t.Fatal("a flow requiring health readiness was accepted against a provider that cannot serve it")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("refusal code = %v, want invalid_argument", connect.CodeOf(err))
	}
	fields := flowFieldErrors(t, err)
	if fields["envs.main.readiness"] != ultra.FlowErrProviderMismatch {
		t.Fatalf("field errors = %v, want %s at envs.main.readiness",
			fields, ultra.FlowErrProviderMismatch)
	}
	// The refusal explains what is missing, so an operator can fix it.
	if !strings.Contains(err.Error(), "publishes no reachable tool endpoint") {
		t.Fatalf("the refusal does not explain the missing capability: %v", err)
	}

	// Nothing was persisted: a refused invocation must leave no trace.
	invocations, err := alice.Flows.ListFlowInvocations(ctx,
		connect.NewRequest(&ultrav1.ListFlowInvocationsRequest{SessionId: session.GetId()}))
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations.Msg.GetInvocations()) != 0 {
		t.Fatalf("a refused invocation was persisted: %d", len(invocations.Msg.GetInvocations()))
	}

	// The identical flow against a capable provider of the same kind runs.
	putFlow(t, alice, org, "health-ok", fmt.Sprintf(healthReadinessFlow, "capable"))
	invoked := invokeFlow(t, alice, session.GetId(), "health-ok", `{}`)
	final := invocationTerminal(t, alice, invoked.GetInvocationId(), 180*time.Second)
	if final.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("state = %v reason %q %q", final.GetState(), final.GetTerminalReason(), final.GetMessage())
	}
	if len(final.GetEnvs()) != 1 || final.GetEnvs()[0].GetEnvName() != "main" {
		t.Fatalf("the capable provider did not create the declared environment: %v", final.GetEnvs())
	}
}

// A10.11 — an invocation belonging to another org is refused exactly like one
// that does not exist. Anything else is an existence oracle: a caller could
// discover which invocation identifiers are real by comparing denials.
func TestA1011_DirectInvocationTenancy(t *testing.T) {
	stack := harness.Up(t)
	alice, bob := stack.AliceClient(), stack.BobClient()
	stack.Model.SetScript(flowScript())
	ctx := context.Background()

	putFlow(t, alice, string(stack.OrgA.ID), "tenancy", singleAgentFlow)
	session := createSession(t, alice, string(stack.OrgA.ID), "tenancy")
	invoked := invokeFlow(t, alice, session.GetId(), "tenancy", `{"subject":"tenancy"}`)
	invocationTerminal(t, alice, invoked.GetInvocationId(), 90*time.Second)

	// Bob asks for Alice's real invocation, and for one that exists nowhere.
	real, realErr := bob.Flows.GetFlowInvocation(ctx, connect.NewRequest(&ultrav1.GetFlowInvocationRequest{
		InvocationId: invoked.GetInvocationId(),
	}))
	missing, missingErr := bob.Flows.GetFlowInvocation(ctx, connect.NewRequest(&ultrav1.GetFlowInvocationRequest{
		InvocationId: uuid.NewString(),
	}))
	if realErr == nil || missingErr == nil {
		t.Fatalf("a cross-org read succeeded: %v %v", real, missing)
	}
	if connect.CodeOf(realErr) != connect.CodeNotFound {
		t.Fatalf("cross-org code = %v, want not_found", connect.CodeOf(realErr))
	}
	if connect.CodeOf(realErr) != connect.CodeOf(missingErr) {
		t.Fatalf("codes differ: %v vs %v", connect.CodeOf(realErr), connect.CodeOf(missingErr))
	}
	if realErr.Error() != missingErr.Error() {
		t.Fatalf("messages differ, which reveals that one identifier is real:\n%q\n%q",
			realErr.Error(), missingErr.Error())
	}

	// Cancelling someone else's invocation is refused the same way, and leaves
	// it untouched.
	_, cancelErr := bob.Flows.CancelFlowInvocation(ctx, connect.NewRequest(&ultrav1.CancelFlowInvocationRequest{
		InvocationId: invoked.GetInvocationId(),
	}))
	if cancelErr == nil {
		t.Fatal("bob cancelled alice's invocation")
	}
	if cancelErr.Error() != missingErr.Error() {
		t.Fatalf("cancel denial %q differs from missing %q", cancelErr.Error(), missingErr.Error())
	}
	still := getInvocation(t, alice, invoked.GetInvocationId())
	if still.GetState() != ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED {
		t.Fatalf("a refused cross-org cancel changed the invocation to %v", still.GetState())
	}
}
