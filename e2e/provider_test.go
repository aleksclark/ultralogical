package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/testkit/harness"
)

func TestA51_ProviderKindsRegistration(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	ctx := context.Background()
	for _, kind := range []string{ultra.ProviderKindBYOKubernetes, ultra.ProviderKindHostedEKS, ultra.ProviderKindBYONomad, ultra.ProviderKindTunnelLocal} {
		_, err := c.Orgs.RegisterProvider(ctx, connect.NewRequest(&ultrav1.RegisterProviderRequest{OrgId: string(stack.OrgA.ID), Kind: kind, Name: kind, ConfigJson: `{"mode":"loopback"}`}))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	items, err := c.Orgs.ListProviders(ctx, connect.NewRequest(&ultrav1.ListProvidersRequest{OrgId: string(stack.OrgA.ID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(items.Msg.GetProviders()) < 5 {
		t.Fatalf("providers=%d", len(items.Msg.GetProviders()))
	}
	for _, p := range items.Msg.GetProviders() {
		if p.GetKind() == ultra.ProviderKindHostedEKS && p.GetRateClass() != ultra.RateClassHosted {
			t.Fatal("hosted rate class missing")
		}
	}
}
func TestA54_InvalidProviderConfig(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	_, err := c.Orgs.RegisterProvider(context.Background(), connect.NewRequest(&ultrav1.RegisterProviderRequest{OrgId: string(stack.OrgA.ID), Kind: ultra.ProviderKindBYOKubernetes, Name: "bad", ConfigJson: `{}`}))
	if err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
