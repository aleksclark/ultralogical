package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider/k8s"
	"github.com/aleksclark/ultracore/provider/localdocker"
	"github.com/aleksclark/ultracore/provider/nullresource"
	"github.com/aleksclark/ultracore/provider/nomad"
	"github.com/aleksclark/ultracore/provider/static"
	"github.com/aleksclark/ultracore/provider/tunnel"
)

// Deployment configures which provider kinds a binary can host and with what
// defaults.
type Deployment struct {
	// BezalelImage is the environment image adapters run.
	BezalelImage string
	// BezalelBinary is the Bezalel executable the static walkthrough provider
	// runs. Empty means that kind cannot be built until a registration names
	// its own binary, which is how a default deployment stays free of a
	// half-configured example.
	BezalelBinary string
	// EnabledKinds restricts which kinds this deployment offers. Empty means
	// every kind the build knows about.
	EnabledKinds []string
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

	if enabled(uc.ProviderKindLocalDocker) {
		registry.Register(uc.ProviderKindLocalDocker, func(_ context.Context, config json.RawMessage) (uc.ResourceProvider, error) {
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

	if enabled(uc.ProviderKindBYOKubernetes) {
		registry.Register(uc.ProviderKindBYOKubernetes, func(_ context.Context, config json.RawMessage) (uc.ResourceProvider, error) {
			var cfg k8s.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Image == "" {
				cfg.Image = deployment.BezalelImage
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
		})
	}
	if enabled(uc.ProviderKindBYONomad) {
		registry.Register(uc.ProviderKindBYONomad, func(_ context.Context, config json.RawMessage) (uc.ResourceProvider, error) {
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
	if enabled(uc.ProviderKindTunnelLocal) {
		registry.Register(uc.ProviderKindTunnelLocal, func(_ context.Context, config json.RawMessage) (uc.ResourceProvider, error) {
			var cfg tunnel.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			cfg.Timeout = 60 * time.Second
			return tunnel.New(cfg)
		})
	}
	if enabled(uc.ProviderKindStatic) {
		registry.Register(uc.ProviderKindStatic, func(_ context.Context, config json.RawMessage) (uc.ResourceProvider, error) {
			var cfg static.Config
			if err := decode(config, &cfg); err != nil {
				return nil, err
			}
			if cfg.Binary == "" {
				cfg.Binary = deployment.BezalelBinary
			}
			return static.New(cfg)
		})
	}
	// Null is always registered: it is test-only, has no host requirements,
	// and lets lifecycle-only resource kinds exercise the control plane.
	// Share one in-memory instance per process so adopt/list/terminate stay
	// coherent across job-scoped Build calls (each job builds a fresh adapter).
	sharedNull := nullresource.New()
	registry.Register(uc.ProviderKindNull, func(_ context.Context, _ json.RawMessage) (uc.ResourceProvider, error) {
		return sharedNull, nil
	})
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
		return fmt.Errorf("provider: invalid provider configuration: %w", err)
	}
	return nil
}
