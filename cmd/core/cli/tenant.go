package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

const tenantUsage = `core tenant — manage tenants and API keys

Usage:
  core tenant create --name NAME [--json]
  core tenant key create --name NAME --scope admin|sessions [--org ID] [--json]
  core tenant key list [--org ID] [--json]
  core tenant key revoke KEY_ID [--org ID] [--json]
`

func runTenant(args []string, environment Env, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		sink := newOut(stderr)
		sink.printf("%s", tenantUsage)
		return ExitUsage, sink.Err()
	}
	if environment.Token == "" {
		return ExitUsage, errors.New("CORE_TOKEN is required")
	}
	clients := NewClients(environment.URL, environment.Token)
	ctx := context.Background()
	switch args[0] {
	case "create":
		return tenantCreate(ctx, clients, args[1:], stdout, stderr)
	case "key":
		return tenantKey(ctx, clients, environment, args[1:], stdout, stderr)
	default:
		newOut(stderr).printf("%s", tenantUsage)
		return ExitUsage, fmt.Errorf("unknown tenant subcommand %q", args[0])
	}
}

func tenantCreate(ctx context.Context, clients *Clients, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("core tenant create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "tenant name")
	asJSON := fs.Bool("json", false, "machine-readable output")
	_, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if *name == "" {
		return ExitUsage, errors.New("usage: core tenant create --name NAME")
	}
	resp, err := clients.Orgs.CreateTenant(ctx, connect.NewRequest(&corev1.CreateTenantRequest{Name: *name}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	view := map[string]string{
		"id":        resp.Msg.GetTenant().GetId(),
		"name":      resp.Msg.GetTenant().GetName(),
		"admin_key": resp.Msg.GetAdminKey(),
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, view)
	}
	sink := newOut(stdout)
	sink.line(fmt.Sprintf("tenant %s created", view["id"]))
	sink.line(fmt.Sprintf("admin_key %s", view["admin_key"]))
	return ExitOK, sink.Err()
}

func tenantKey(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		newOut(stderr).printf("%s", tenantUsage)
		return ExitUsage, errors.New("usage: core tenant key create|list|revoke")
	}
	switch args[0] {
	case "create":
		return tenantKeyCreate(ctx, clients, environment, args[1:], stdout, stderr)
	case "list":
		return tenantKeyList(ctx, clients, environment, args[1:], stdout, stderr)
	case "revoke":
		return tenantKeyRevoke(ctx, clients, environment, args[1:], stdout, stderr)
	default:
		newOut(stderr).printf("%s", tenantUsage)
		return ExitUsage, fmt.Errorf("unknown tenant key subcommand %q", args[0])
	}
}

func tenantKeyCreate(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("core tenant key create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "key name")
	scope := fs.String("scope", "sessions", "admin or sessions")
	org := fs.String("org", "", "tenant id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	_, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if *name == "" {
		return ExitUsage, errors.New("usage: core tenant key create --name NAME --scope admin|sessions")
	}
	tenantID, err := requireTenantID(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	var protoScope corev1.KeyScope
	switch *scope {
	case "admin":
		protoScope = corev1.KeyScope_KEY_SCOPE_ADMIN
	case "sessions":
		protoScope = corev1.KeyScope_KEY_SCOPE_SESSIONS
	default:
		return ExitUsage, fmt.Errorf("unknown scope %q (want admin or sessions)", *scope)
	}
	resp, err := clients.Orgs.CreateAPIKey(ctx, connect.NewRequest(&corev1.CreateAPIKeyRequest{
		TenantId: tenantID, Name: *name, Scope: protoScope,
	}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	view := map[string]string{
		"id":     resp.Msg.GetKey().GetId(),
		"prefix": resp.Msg.GetKey().GetPrefix(),
		"scope":  *scope,
		"raw":    resp.Msg.GetRawKey(),
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, view)
	}
	sink := newOut(stdout)
	sink.line(fmt.Sprintf("key %s created (prefix %s)", view["id"], view["prefix"]))
	sink.line(fmt.Sprintf("raw %s", view["raw"]))
	return ExitOK, sink.Err()
}

func tenantKeyList(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("core tenant key list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "tenant id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	_, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	tenantID, err := requireTenantID(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	resp, err := clients.Orgs.ListAPIKeys(ctx, connect.NewRequest(&corev1.ListAPIKeysRequest{TenantId: tenantID}))
	if err != nil {
		return report(stderr, *asJSON, err)
	}
	type keyView struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Scope  string `json:"scope"`
		Prefix string `json:"prefix"`
	}
	var out []keyView
	for _, k := range resp.Msg.GetKeys() {
		scope := "sessions"
		if k.GetScope() == corev1.KeyScope_KEY_SCOPE_ADMIN {
			scope = "admin"
		}
		out = append(out, keyView{ID: k.GetId(), Name: k.GetName(), Scope: scope, Prefix: k.GetPrefix()})
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, out)
	}
	sink := newOut(stdout)
	for _, k := range out {
		sink.line(fmt.Sprintf("%s  %s  %s  %s", k.ID, k.Scope, k.Prefix, k.Name))
	}
	return ExitOK, sink.Err()
}

func tenantKeyRevoke(ctx context.Context, clients *Clients, environment Env, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("core tenant key revoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "tenant id")
	asJSON := fs.Bool("json", false, "machine-readable output")
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return ExitUsage, nil
	}
	if len(positional) != 1 {
		return ExitUsage, errors.New("usage: core tenant key revoke KEY_ID")
	}
	tenantID, err := requireTenantID(environment, *org)
	if err != nil {
		return ExitUsage, err
	}
	if _, err := clients.Orgs.RevokeAPIKey(ctx, connect.NewRequest(&corev1.RevokeAPIKeyRequest{
		TenantId: tenantID, KeyId: positional[0],
	})); err != nil {
		return report(stderr, *asJSON, err)
	}
	if *asJSON {
		return ExitOK, writeJSON(stdout, map[string]string{"revoked": positional[0]})
	}
	sink := newOut(stdout)
	sink.line(fmt.Sprintf("key %s revoked", positional[0]))
	return ExitOK, sink.Err()
}
