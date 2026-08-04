// Package sdk is the embeddable Go client for ultracore.
//
// It is a thin ergonomic layer over the generated ConnectRPC clients: auth
// transport, subscribe-with-reconnect, typed event helpers, run-await, and
// label selector builders. Functional tests exercise this package via
// testkit/testclient so the SDK is the real consumer path.
package sdk

import (
	"net/http"

	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
)

// Options configure a Client.
type Options struct {
	// BaseURL is the cored HTTP origin, e.g. "http://127.0.0.1:8080".
	BaseURL string
	// APIKey is the raw tenant API key (sent as Authorization: Bearer).
	APIKey string
	// Actor is the opaque X-Core-Actor value (kind/id[/display]).
	Actor string
	// HTTPClient overrides the underlying HTTP client. When nil a client with
	// the auth transport is constructed.
	HTTPClient *http.Client
}

// Client is an authenticated ultracore API client.
type Client struct {
	Tenants     corev1connect.TenantServiceClient
	Credentials corev1connect.CredentialServiceClient
	Providers   corev1connect.ProviderServiceClient
	Sessions    corev1connect.SessionServiceClient
	Runs        corev1connect.RunServiceClient
	Resources   corev1connect.ResourceServiceClient
	Events      corev1connect.EventServiceClient
	Automation  corev1connect.AutomationServiceClient

	baseURL string
	apiKey  string
	actor   string
	http    *http.Client
}

type authTransport struct {
	token string
	actor string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	if t.actor != "" {
		req.Header.Set("X-Core-Actor", t.actor)
	}
	return t.base.RoundTrip(req)
}

// New builds a Client from Options.
func New(opts Options) *Client {
	base := opts.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	httpClient := &http.Client{
		Transport:     &authTransport{token: opts.APIKey, actor: opts.Actor, base: rt},
		Timeout:       base.Timeout,
		CheckRedirect: base.CheckRedirect,
		Jar:           base.Jar,
	}
	return &Client{
		Tenants:     corev1connect.NewTenantServiceClient(httpClient, opts.BaseURL),
		Credentials: corev1connect.NewCredentialServiceClient(httpClient, opts.BaseURL),
		Providers:   corev1connect.NewProviderServiceClient(httpClient, opts.BaseURL),
		Sessions:    corev1connect.NewSessionServiceClient(httpClient, opts.BaseURL),
		Runs:        corev1connect.NewRunServiceClient(httpClient, opts.BaseURL),
		Resources:   corev1connect.NewResourceServiceClient(httpClient, opts.BaseURL),
		Events:      corev1connect.NewEventServiceClient(httpClient, opts.BaseURL),
		Automation:  corev1connect.NewAutomationServiceClient(httpClient, opts.BaseURL),
		baseURL:     opts.BaseURL,
		apiKey:      opts.APIKey,
		actor:       opts.Actor,
		http:        httpClient,
	}
}

// WithActor returns a new Client that sends a different X-Core-Actor header.
func (c *Client) WithActor(actor string) *Client {
	return New(Options{BaseURL: c.baseURL, APIKey: c.apiKey, Actor: actor, HTTPClient: &http.Client{
		Timeout: c.http.Timeout,
	}})
}
