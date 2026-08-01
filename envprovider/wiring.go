package envprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envprovider/k8s"
	"github.com/aleksclark/ultralogical/envprovider/localdocker"
	"github.com/aleksclark/ultralogical/envprovider/nomad"
	"github.com/aleksclark/ultralogical/envprovider/tunnel"
)

// Deployment configures which provider kinds a binary can host and with what
// defaults.
type Deployment struct {
	// BezalelImage is the environment image adapters run.
	BezalelImage string
	// EnabledKinds restricts which kinds this deployment offers. Empty means
	// every kind the build knows about.
	EnabledKinds []string
	// HostedIngressCIDRs are the ranges hosted environments accept platform
	// traffic from.
	HostedIngressCIDRs []string
	// KubernetesEndpointMode and KubernetesNodePortRange configure how a
	// worker outside the cluster reaches an environment.
	KubernetesEndpointMode  string
	KubernetesEndpointHost  string
	KubernetesNodePortRange [2]int32
}

// StandardRegistry wires every real adapter this build ships.
//
// There is deliberately no alias here: each kind resolves to the adapter that
// drives its own control plane, so a deployment that enables a kind it cannot
// actually reach fails at registration rather than silently running
// environments somewhere else.
func StandardRegistry(deployment Deployment) *Registry {
	registry := NewRegistry()
	enabled := func(kind string) bool {
		if len(deployment.EnabledKinds) == 0 {
			return true
		}
		for _, candidate := range deployment.EnabledKinds {
			if candidate == kind {
				return true
			}
		}
		return false
	}

	if enabled(ultra.ProviderKindLocalDocker) {
		registry.Register(ultra.ProviderKindLocalDocker, func(_ context.Context, config json.RawMessage) (ultra.EnvProvider, error) {
			var cfg localdocker.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Image == "" {
				cfg.Image = deployment.BezalelImage
			}
			return localdocker.New(cfg)
		})
	}

	kubernetes := func(hosted bool) Factory {
		return func(_ context.Context, config json.RawMessage) (ultra.EnvProvider, error) {
			var cfg k8s.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Image == "" {
				cfg.Image = deployment.BezalelImage
			}
			cfg.Hosted = hosted
			if hosted && len(cfg.PlatformIngressCIDRs) == 0 {
				cfg.PlatformIngressCIDRs = deployment.HostedIngressCIDRs
			}
			if cfg.EndpointMode == "" {
				cfg.EndpointMode = deployment.KubernetesEndpointMode
			}
			if cfg.EndpointHost == "" {
				cfg.EndpointHost = deployment.KubernetesEndpointHost
			}
			if cfg.NodePortRange[1] == 0 {
				cfg.NodePortRange = deployment.KubernetesNodePortRange
			}
			return k8s.New(cfg)
		}
	}
	if enabled(ultra.ProviderKindBYOKubernetes) {
		registry.Register(ultra.ProviderKindBYOKubernetes, kubernetes(false))
	}
	if enabled(ultra.ProviderKindHostedEKS) {
		registry.Register(ultra.ProviderKindHostedEKS, kubernetes(true))
	}
	if enabled(ultra.ProviderKindBYONomad) {
		registry.Register(ultra.ProviderKindBYONomad, func(_ context.Context, config json.RawMessage) (ultra.EnvProvider, error) {
			var cfg nomad.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Image == "" {
				cfg.Image = deployment.BezalelImage
			}
			return nomad.New(cfg)
		})
	}
	if enabled(ultra.ProviderKindTunnelLocal) {
		registry.Register(ultra.ProviderKindTunnelLocal, func(_ context.Context, config json.RawMessage) (ultra.EnvProvider, error) {
			var cfg tunnel.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			cfg.Timeout = 60 * time.Second
			return tunnel.New(cfg)
		})
	}
	return registry
}

// decode reads a registration's configuration strictly: an unknown field is a
// typo an operator needs to see, not a setting to ignore.
func decode(config json.RawMessage, out any) error {
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("envprovider: invalid provider configuration: %w", err)
	}
	return nil
}
