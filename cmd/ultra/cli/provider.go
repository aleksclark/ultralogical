package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"connectrpc.com/connect"

	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
)

const providerUsage = `ultra provider — where this org's environments run

Usage:
  ultra provider register NAME --kind KIND --config JSON [--json]
  ultra provider list [--json]
  ultra provider show NAME [--json]
  ultra provider remove NAME [--json]

Kinds:
  local_docker  the machine running the worker
  byo_k8s       your own Kubernetes cluster
  hosted_eks    the platform's cluster, with per-org isolation
  byo_nomad     your own Nomad cluster
  tunnel_local  your own machine, through an outbound tunnel
`

func runProvider(args []string, environment Env, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		sink := newOut(stderr)
		sink.printf("%s", providerUsage)
		return ExitUsage, sink.Err()
	}
	if environment.Token == "" {
		return ExitUsage, errors.New("ULTRA_TOKEN is required")
	}
	clients := NewClients(environment.URL, environment.Token)
	ctx := context.Background()
	switch args[0] {
	case "register":
		return providerRegister(ctx, clients, environment, args[1:], stdout, stderr)
	case "list":
		return providerList(ctx, clients, environment, args[1:], stdout, stderr)
	case "show":
		return providerShow(ctx, clients, environment, args[1:], stdout, stderr)
	case "remove":
		return providerRemove(ctx, clients, environment, args[1:], stdout, stderr)
	default:
		newOut(stderr).printf("%s", providerUsage)
		return ExitUsage, fmt.Errorf("unknown provider subcommand %q", args[0])
	}
}

// providerView is the CLI's stable shape for a registration. Capabilities are
// included in full, supported or not: the reason a capability is missing is
// what explains a later refusal.
type providerView struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	RateClass    string           `json:"rate_class"`
	State        string           `json:"state"`
	Capabilities []capabilityView `json:"capabilities,omitempty"`
}

type capabilityView struct {
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

func toProviderView(p *ultrav1.ProviderInstance) providerView {
	out := providerView{
		ID: p.GetId(), Name: p.GetName(), Kind: p.GetKind(),
		RateClass: p.GetRateClass(), State: p.GetState(),
	}
	for _, capability := range p.GetCapabilities() {
		out.Capabilities = append(out.Capabilities, capabilityView{
			Name: capability.GetName(), Supported: capability.GetSupported(), Reason: capability.GetReason(),
		})
	}
	return out
}

func providerRegister(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra provider register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "provider kind")
	config := fs.String("config", "{}", "provider configuration as JSON")
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra provider register NAME --kind KIND --config JSON")
	}
	if *kind == "" {
		return ExitUsage, errors.New("--kind is required")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Orgs.RegisterProvider(ctx, connect.NewRequest(&ultrav1.RegisterProviderRequest{
		OrgId: orgID, Kind: *kind, Name: positional[0], ConfigJson: *config,
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	view := toProviderView(resp.Msg.GetProvider())
	if *asJSON {
		return ExitOK, writeJSON(stdout, view)
	}
	sink := newOut(stdout)
	sink.printf("%s (%s) registered, metered as %s\n", view.Name, view.Kind, view.RateClass)
	for _, capability := range view.Capabilities {
		if !capability.Supported {
			sink.printf("  unavailable: %s (%s)\n", capability.Name, capability.Reason)
		}
	}
	return ExitOK, sink.Err()
}

func providerList(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra provider list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitUsage, nil
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	items, err := listProviders(ctx, clients, orgID)
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, map[string]any{"providers": items})
	}
	sink := newOut(stdout)
	for _, item := range items {
		sink.printf("%s\t%s\t%s\t%s\n", item.Name, item.Kind, item.RateClass, item.State)
	}
	return ExitOK, sink.Err()
}

func listProviders(ctx context.Context, clients *Clients, orgID string) ([]providerView, error) {
	resp, err := clients.Orgs.ListProviders(ctx, connect.NewRequest(&ultrav1.ListProvidersRequest{OrgId: orgID}))
	if err != nil {
		return nil, err
	}
	out := make([]providerView, 0, len(resp.Msg.GetProviders()))
	for _, item := range resp.Msg.GetProviders() {
		out = append(out, toProviderView(item))
	}
	return out, nil
}

// findProvider resolves a registration by name. Names are what an operator
// knows; identifiers are what the API takes.
func findProvider(ctx context.Context, clients *Clients, orgID, name string) (providerView, error) {
	items, err := listProviders(ctx, clients, orgID)
	if err != nil {
		return providerView{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return providerView{}, fmt.Errorf("no provider named %q is registered", name)
}

func providerShow(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra provider show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra provider show NAME")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	item, err := findProvider(ctx, clients, orgID, positional[0])
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, item)
	}
	sink := newOut(stdout)
	sink.printf("%s (%s) %s %s\n", item.Name, item.Kind, item.RateClass, item.State)
	for _, capability := range item.Capabilities {
		if capability.Supported {
			sink.printf("  %s available\n", capability.Name)
			continue
		}
		sink.printf("  %s unavailable: %s\n", capability.Name, capability.Reason)
	}
	return ExitOK, sink.Err()
}

func providerRemove(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("ultra provider remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "org id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: ultra provider remove NAME")
	}
	orgID, err := requireOrg(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	item, err := findProvider(ctx, clients, orgID, positional[0])
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	// Removal is refused while the provider still hosts environments, so the
	// error is the useful answer here rather than an exception.
	if _, err := clients.Orgs.DeleteProvider(ctx, connect.NewRequest(&ultrav1.DeleteProviderRequest{
		OrgId: orgID, ProviderId: item.ID,
	})); err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, map[string]any{"removed": item.Name})
	}
	sink := newOut(stdout)
	sink.printf("%s removed\n", item.Name)
	return ExitOK, sink.Err()
}
