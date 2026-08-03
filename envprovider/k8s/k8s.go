// Package k8s implements the environment provider on Kubernetes. It creates
// and inspects real Kubernetes objects through a Kubernetes client; it never
// delegates lifecycle to another runtime.
//
// One environment is one Pod plus the Secret holding its bearer token and the
// Service that publishes its tool endpoint. All three carry deterministic
// labels derived from the environment id, which is what makes adoption,
// reconciliation, and leak detection exact rather than heuristic.
package k8s

import (
	"context"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	uc "github.com/aleksclark/ultracore"
)

// Labels applied to every object this provider creates. They are the identity
// the adapter uses to find its own resources again after a restart, a crash,
// or an out-of-band deletion.
const (
	LabelEnvID     = "ultracore.dev/env-id"
	LabelManagedBy = "app.kubernetes.io/managed-by"
	ManagedByValue = "ultracore"
)

// toolPort is the port Bezalel serves its authenticated tool endpoint on.
const toolPort = 8080

// Config configures the provider.
type Config struct {
	// Kubeconfig is the raw kubeconfig for the target cluster. Empty means
	// in-cluster configuration.
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// Context selects a context within the kubeconfig.
	Context string `json:"context,omitempty"`
	// Namespace is where environments are created.
	Namespace string `json:"namespace,omitempty"`
	// Image is the Bezalel image to run.
	Image string `json:"image,omitempty"`
	// CPURequest, CPULimit, MemoryRequest, and MemoryLimit bound each
	// environment. A pod with no bounds is a pod that can starve its cluster.
	CPURequest    string `json:"cpu_request,omitempty"`
	CPULimit      string `json:"cpu_limit,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty"`
	// EndpointMode selects how the tool endpoint is reached: "cluster" uses
	// the Service's cluster DNS name, which is correct when workers run in
	// the same cluster; "nodeport" publishes a node port, which is how a
	// worker outside the cluster reaches a kind node.
	EndpointMode string `json:"endpoint_mode,omitempty"`
	// EndpointHost overrides the host used with nodeport endpoints.
	EndpointHost string `json:"endpoint_host,omitempty"`
	// NodePortRange bounds the node ports this provider may assign, as
	// [low, high]. A worker outside the cluster can only reach ports its
	// host actually forwards, so a deployment that publishes a fixed range
	// must be able to say so instead of accepting a random assignment.
	NodePortRange [2]int32 `json:"node_port_range,omitempty"`
}

// Endpoint modes.
const (
	EndpointModeCluster  = "cluster"
	EndpointModeNodePort = "nodeport"
)

// Provider implements uc.EnvProvider on Kubernetes.
type Provider struct {
	client kubernetes.Interface
	cfg    Config
}

// handleData is the persisted, provider-native identity of one environment.
type handleData struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	EnvID     string `json:"env_id"`
	NodePort  int32  `json:"node_port,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
}

// New builds a provider from configuration.
func New(cfg Config) (*Provider, error) {
	restConfig, err := restConfigFor(cfg)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("k8s: build client: %w", err)
	}
	return NewWithClient(client, cfg)
}

// NewWithClient builds a provider on an existing client.
func NewWithClient(client kubernetes.Interface, cfg Config) (*Provider, error) {
	if cfg.Image == "" {
		cfg.Image = "ultracore/bezalel:local"
	}
	if cfg.EndpointMode == "" {
		cfg.EndpointMode = EndpointModeCluster
	}
	return &Provider{client: client, cfg: cfg}, nil
}

func restConfigFor(cfg Config) (*rest.Config, error) {
	if strings.TrimSpace(cfg.Kubeconfig) == "" {
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s: no kubeconfig and not running in a cluster: %w", err)
		}
		return restConfig, nil
	}
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(cfg.Kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("k8s: parse kubeconfig: %w", err)
	}
	if cfg.Context != "" {
		raw, err := clientConfig.RawConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s: read kubeconfig: %w", err)
		}
		clientConfig = clientcmd.NewNonInteractiveClientConfig(raw, cfg.Context, &clientcmd.ConfigOverrides{}, nil)
	}
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s: build rest config: %w", err)
	}
	return restConfig, nil
}

// Namespace is where this provider places environments.
func (p *Provider) Namespace() string {
	if p.cfg.Namespace != "" {
		return p.cfg.Namespace
	}
	return "ultracore-envs"
}

// objectName is the deterministic name every object for one environment
// shares, so an interrupted provisioning finds its own work again.
func objectName(envID uc.EnvID) string { return "ultra-env-" + sanitize(string(envID)) }

// sanitize reduces an identifier to a DNS-1123 label.
func sanitize(value string) string {
	lowered := strings.ToLower(value)
	var b strings.Builder
	for _, r := range lowered {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 55 {
		out = out[:55]
	}
	if out == "" {
		out = "unnamed"
	}
	return out
}

func (p *Provider) labels(envID uc.EnvID) map[string]string {
	return map[string]string{
		LabelEnvID:     sanitize(string(envID)),
		LabelManagedBy: ManagedByValue,
	}
}

func (p *Provider) selector(envID uc.EnvID) string {
	return fmt.Sprintf("%s=%s,%s=%s", LabelEnvID, sanitize(string(envID)), LabelManagedBy, ManagedByValue)
}

// Probe implements uc.CapabilityProber. It asks the cluster what it can
// actually do rather than inferring capability from the provider kind.
func (p *Provider) Probe(ctx context.Context) (uc.ProviderCapabilities, error) {
	capabilities := uc.ProviderCapabilities{
		Kind:  uc.ProviderKindBYOKubernetes,
		Notes: map[uc.ProviderCapability]string{},
	}
	if _, err := p.client.Discovery().ServerVersion(); err != nil {
		return capabilities, fmt.Errorf("k8s: control plane unreachable: %w", err)
	}
	if err := p.canCreate(ctx, "pods"); err != nil {
		return capabilities, fmt.Errorf("k8s: cannot create pods: %w", err)
	}
	capabilities.Supported = append(capabilities.Supported,
		uc.CapabilityAdoptsOrphans,
		uc.CapabilityEnumeratesResources,
	)
	if err := p.canCreate(ctx, "services"); err == nil {
		capabilities.Supported = append(capabilities.Supported, uc.CapabilityServesToolEndpoint)
	} else {
		capabilities.Notes[uc.CapabilityServesToolEndpoint] =
			"the cluster does not allow creating Services, so no tool endpoint can be published"
	}
	// emptyDir workspaces do not survive a restart.
	capabilities.Notes[uc.CapabilityRestartPreservesWorkspace] =
		"environment workspaces are emptyDir volumes"
	return capabilities, nil
}

// canCreate performs a read-only permission check for one resource.
func (p *Provider) canCreate(ctx context.Context, resourceName string) error {
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: p.Namespace(), Verb: "create", Resource: resourceName,
			},
		},
	}
	result, err := p.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("permission check for %s failed: %w", resourceName, err)
	}
	if !result.Status.Allowed {
		return fmt.Errorf("not permitted to create %s: %s", resourceName, result.Status.Reason)
	}
	return nil
}
