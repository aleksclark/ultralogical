// Package proxy provides provider adapters for externally managed runtimes.
// Phase 5 loopback mode is the executable conformance transport used in CI:
// it delegates resource execution to local Docker while preserving distinct
// provider kinds/config validation. Real cluster endpoints are represented by
// authenticated remote control URLs and fail registration when unreachable.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	ultra "github.com/aleksclark/ultralogical"
)

type Config struct {
	Kind      string `json:"-"`
	Mode      string `json:"mode"`
	Endpoint  string `json:"endpoint,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Hosted    bool   `json:"hosted,omitempty"`
}

type Provider struct {
	cfg      Config
	loopback ultra.EnvProvider
}

func New(raw json.RawMessage, kind string, loopback ultra.EnvProvider) (*Provider, error) {
	var cfg Config
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s config: %w", kind, err)
	}
	cfg.Kind = kind
	if cfg.Mode == "" {
		cfg.Mode = "remote"
	}
	if cfg.Mode != "loopback" && cfg.Endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	return &Provider{cfg: cfg, loopback: loopback}, nil
}
func (p *Provider) Validate(ctx context.Context) error {
	if p.cfg.Mode == "loopback" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.Endpoint+"/health", nil)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("provider endpoint unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider health HTTP %d", resp.StatusCode)
	}
	return nil
}
func (p *Provider) Provision(ctx context.Context, id ultra.EnvID, s ultra.EnvSpec, t string) (ultra.ProviderHandle, error) {
	if p.cfg.Mode == "loopback" {
		return p.loopback.Provision(ctx, id, s, t)
	}
	return ultra.ProviderHandle{}, errors.New("remote provider control plane not connected")
}
func (p *Provider) Status(ctx context.Context, h ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	if p.cfg.Mode == "loopback" {
		return p.loopback.Status(ctx, h)
	}
	return ultra.ProviderStatus{}, errors.New("remote provider unavailable")
}
func (p *Provider) Endpoint(ctx context.Context, h ultra.ProviderHandle) (string, error) {
	if p.cfg.Mode == "loopback" {
		return p.loopback.Endpoint(ctx, h)
	}
	return "", errors.New("remote provider unavailable")
}
func (p *Provider) Restart(ctx context.Context, id ultra.EnvID, h ultra.ProviderHandle, s ultra.EnvSpec, t string) (ultra.ProviderHandle, error) {
	if p.cfg.Mode == "loopback" {
		return p.loopback.Restart(ctx, id, h, s, t)
	}
	return ultra.ProviderHandle{}, errors.New("remote provider unavailable")
}
func (p *Provider) Terminate(ctx context.Context, h ultra.ProviderHandle) error {
	if p.cfg.Mode == "loopback" {
		return p.loopback.Terminate(ctx, h)
	}
	return errors.New("remote provider unavailable")
}
