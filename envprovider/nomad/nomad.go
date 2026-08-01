// Package nomad implements the environment provider on HashiCorp Nomad. It
// registers and inspects real Nomad jobs and allocations through the Nomad
// API; it never delegates lifecycle to another runtime.
//
// One environment is one Nomad job with a single Bezalel task. The job's name
// is derived from the environment id, so adoption, reconciliation, and purge
// are exact rather than heuristic.
package nomad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	nomadapi "github.com/hashicorp/nomad/api"

	ultra "github.com/aleksclark/ultralogical"
)

// toolPort is the port Bezalel serves its authenticated tool endpoint on.
const toolPort = 8080

// handleVersion is the persisted handle schema version.
const handleVersion = 1

// Config configures the provider.
type Config struct {
	// Address is the Nomad API address.
	Address string `json:"address,omitempty"`
	// Token is the Nomad ACL token, decrypted at the point of use.
	Token string `json:"token,omitempty"`
	// Region and Namespace scope the job.
	Region    string `json:"region,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// Datacenters restricts placement.
	Datacenters []string `json:"datacenters,omitempty"`
	// Image is the Bezalel image to run.
	Image string `json:"image,omitempty"`
	// CPU and Memory bound each task, in Nomad's units (MHz and MiB).
	CPU    int `json:"cpu,omitempty"`
	Memory int `json:"memory,omitempty"`
	// EndpointHost is the host a worker reaches allocations on. Empty means
	// the address Nomad advertises for the allocation.
	EndpointHost string `json:"endpoint_host,omitempty"`
}

// Provider implements ultra.EnvProvider on Nomad.
type Provider struct {
	client *nomadapi.Client
	cfg    Config
}

// handleData is the persisted, provider-native identity of one environment.
type handleData struct {
	JobID string `json:"job_id"`
	EnvID string `json:"env_id"`
}

// New builds a provider from configuration.
func New(cfg Config) (*Provider, error) {
	if cfg.Address == "" {
		cfg.Address = "http://127.0.0.1:4646"
	}
	if cfg.Image == "" {
		cfg.Image = "ultralogical/bezalel:local"
	}
	if cfg.CPU <= 0 {
		cfg.CPU = 500
	}
	if cfg.Memory <= 0 {
		cfg.Memory = 512
	}
	if len(cfg.Datacenters) == 0 {
		cfg.Datacenters = []string{"*"}
	}
	client, err := nomadapi.NewClient(&nomadapi.Config{
		Address: cfg.Address, SecretID: cfg.Token,
		Region: cfg.Region, Namespace: cfg.Namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("nomad: build client: %w", err)
	}
	return &Provider{client: client, cfg: cfg}, nil
}

// jobID is the deterministic job name for one environment, so a redelivered
// provisioning finds its own work again instead of registering a second job.
func jobID(envID ultra.EnvID) string { return "ultra-env-" + sanitize(string(envID)) }

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "unnamed"
	}
	return out
}

func encodeHandle(d handleData) (ultra.ProviderHandle, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("nomad: encode handle: %w", err)
	}
	return ultra.ProviderHandle{Version: handleVersion, Data: body}, nil
}

func decodeHandle(h ultra.ProviderHandle) (handleData, error) {
	var d handleData
	if len(h.Data) == 0 {
		return d, errors.New("nomad: empty provider handle")
	}
	if err := json.Unmarshal(h.Data, &d); err != nil {
		return d, fmt.Errorf("nomad: decode handle: %w", err)
	}
	return d, nil
}

func stringPtr(v string) *string { return &v }
func intPtr(v int) *int          { return &v }

// Provision registers the environment's job. Registration is idempotent by
// job id: a redelivered provisioning updates the same job rather than
// creating a second one.
func (p *Provider) Provision(ctx context.Context, envID ultra.EnvID, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	id := jobID(envID)
	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	image := p.cfg.Image
	if spec.Image != "" {
		image = spec.Image
	}
	env := map[string]string{"BEZALEL_AUTH_TOKEN": token}
	for key, value := range spec.Env {
		env[key] = value
	}

	job := &nomadapi.Job{
		ID:          stringPtr(id),
		Name:        stringPtr(id),
		Type:        stringPtr("service"),
		Datacenters: p.cfg.Datacenters,
		Meta: map[string]string{
			"ultralogical.env_id":     string(envID),
			"ultralogical.managed_by": "ultralogical",
		},
		TaskGroups: []*nomadapi.TaskGroup{{
			Name:  stringPtr("env"),
			Count: intPtr(1),
			// A failed environment must surface rather than be restarted
			// forever behind the platform's back: the control plane owns
			// recovery, so Nomad's own restart loop is disabled.
			RestartPolicy: &nomadapi.RestartPolicy{Attempts: intPtr(0), Mode: stringPtr("fail")},
			ReschedulePolicy: &nomadapi.ReschedulePolicy{
				Attempts: intPtr(0), Unlimited: boolPtr(false),
			},
			Networks: []*nomadapi.NetworkResource{{
				DynamicPorts: []nomadapi.Port{{Label: "tools", To: toolPort}},
			}},
			Tasks: []*nomadapi.Task{{
				Name:   "bezalel",
				Driver: "docker",
				Config: map[string]any{
					"image":          image,
					"ports":          []string{"tools"},
					"network_mode":   "bridge",
					"force_pull":     false,
					"auth_soft_fail": true,
					// The working directory must exist before the agent
					// starts: it is not part of the image, and every command
					// would otherwise fail before running because its working
					// directory is missing. Nomad's Docker driver disables
					// volume mounts by default, so the directory is created
					// inside the container rather than mounted from the host.
					// Deployments that want a durable workspace attach a
					// Nomad host volume, which needs no change here.
					"entrypoint": []string{"/bin/sh", "-c"},
					"args":       []string{fmt.Sprintf("mkdir -p %s && exec bezalel --workdir %s", workdir, workdir)},
				},
				Env: env,
				Resources: &nomadapi.Resources{
					CPU:      intPtr(p.cfg.CPU),
					MemoryMB: intPtr(p.cfg.Memory),
				},
			}},
		}},
	}
	if _, _, err := p.client.Jobs().Register(job, p.write(ctx)); err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("nomad: register job: %w", err)
	}
	return encodeHandle(handleData{JobID: id, EnvID: string(envID)})
}

func boolPtr(v bool) *bool { return &v }

func (p *Provider) write(ctx context.Context) *nomadapi.WriteOptions {
	return (&nomadapi.WriteOptions{}).WithContext(ctx)
}

func (p *Provider) query(ctx context.Context) *nomadapi.QueryOptions {
	return (&nomadapi.QueryOptions{}).WithContext(ctx)
}

// runningAllocation returns the environment's live allocation, if any.
func (p *Provider) runningAllocation(ctx context.Context, id string) (*nomadapi.AllocationListStub, error) {
	allocations, _, err := p.client.Jobs().Allocations(id, true, p.query(ctx))
	if err != nil {
		return nil, err
	}
	for _, allocation := range allocations {
		if allocation.ClientStatus == "running" {
			return allocation, nil
		}
	}
	return nil, nil
}

// Status maps the job's real allocation state onto the environment state
// machine. A job whose allocation is gone is failed, not merely unready:
// something outside the platform stopped it, and reconciliation must see it.
func (p *Provider) Status(ctx context.Context, handle ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	job, _, err := p.client.Jobs().Info(d.JobID, p.query(ctx))
	if err != nil {
		if isNotFound(err) {
			return ultra.ProviderStatus{State: ultra.EnvFailed, Message: "job not found"}, nil
		}
		return ultra.ProviderStatus{}, fmt.Errorf("nomad: job info: %w", err)
	}
	if job.Stop != nil && *job.Stop {
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: "job is stopped"}, nil
	}
	allocations, _, err := p.client.Jobs().Allocations(d.JobID, true, p.query(ctx))
	if err != nil {
		return ultra.ProviderStatus{}, fmt.Errorf("nomad: job allocations: %w", err)
	}
	if len(allocations) == 0 {
		return ultra.ProviderStatus{State: ultra.EnvProvisioning, Message: "no allocation yet"}, nil
	}
	for _, allocation := range allocations {
		switch allocation.ClientStatus {
		case "running":
			return ultra.ProviderStatus{State: ultra.EnvReady}, nil
		case "pending":
			return ultra.ProviderStatus{State: ultra.EnvProvisioning, Message: "allocation pending"}, nil
		}
	}
	return ultra.ProviderStatus{State: ultra.EnvFailed, Message: allocations[0].ClientStatus}, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

// Endpoint discovers the tool endpoint from the allocation's advertised
// address. A guessed address would be wrong the moment Nomad rescheduled the
// allocation onto another node or port.
func (p *Provider) Endpoint(ctx context.Context, handle ultra.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return "", err
	}
	stub, err := p.runningAllocation(ctx, d.JobID)
	if err != nil {
		return "", fmt.Errorf("nomad: allocation lookup: %w", err)
	}
	if stub == nil {
		return "", errors.New("nomad: no running allocation")
	}
	allocation, _, err := p.client.Allocations().Info(stub.ID, p.query(ctx))
	if err != nil {
		return "", fmt.Errorf("nomad: allocation info: %w", err)
	}
	host, port := allocationAddress(allocation)
	if port == 0 {
		return "", errors.New("nomad: allocation advertises no tool port")
	}
	if p.cfg.EndpointHost != "" {
		host = p.cfg.EndpointHost
	}
	return fmt.Sprintf("http://%s:%d/mcp", host, port), nil
}

// allocationAddress reads the advertised host address of the tools port.
func allocationAddress(allocation *nomadapi.Allocation) (string, int) {
	if allocation.AllocatedResources != nil {
		for _, network := range allocation.AllocatedResources.Shared.Networks {
			for _, port := range network.DynamicPorts {
				if port.Label == "tools" {
					return network.IP, port.Value
				}
			}
		}
		for _, port := range allocation.AllocatedResources.Shared.Ports {
			if port.Label == "tools" {
				return port.HostIP, port.Value
			}
		}
	}
	return "", 0
}

// Restart replaces the allocation with a new one carrying the rotated token.
func (p *Provider) Restart(ctx context.Context, envID ultra.EnvID, handle ultra.ProviderHandle, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	if err := p.Terminate(ctx, handle); err != nil {
		return ultra.ProviderHandle{}, err
	}
	if err := p.awaitJobAbsent(ctx, jobID(envID)); err != nil {
		return ultra.ProviderHandle{}, err
	}
	return p.Provision(ctx, envID, spec, token)
}

func (p *Provider) awaitJobAbsent(ctx context.Context, id string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		_, _, err := p.client.Jobs().Info(id, p.query(ctx))
		if isNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("nomad: job was not purged within the deadline")
}

// Terminate deregisters and purges the job. Purging rather than stopping is
// what makes the leak check meaningful: a stopped job is still a job.
func (p *Provider) Terminate(ctx context.Context, handle ultra.ProviderHandle) error {
	d, err := decodeHandle(handle)
	if err != nil {
		return nil
	}
	if _, _, err := p.client.Jobs().Deregister(d.JobID, true, p.write(ctx)); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("nomad: purge job: %w", err)
	}
	return nil
}

// Adopt implements ultra.EnvAdopter: it finds a job this provider already
// registered for an environment, so an interrupted provisioning resumes
// instead of registering a second one.
func (p *Provider) Adopt(ctx context.Context, envID ultra.EnvID) (ultra.ProviderHandle, bool, error) {
	id := jobID(envID)
	job, _, err := p.client.Jobs().Info(id, p.query(ctx))
	if isNotFound(err) {
		return ultra.ProviderHandle{}, false, nil
	}
	if err != nil {
		return ultra.ProviderHandle{}, false, fmt.Errorf("nomad: adopt lookup: %w", err)
	}
	if job.Stop != nil && *job.Stop {
		return ultra.ProviderHandle{}, false, nil
	}
	handle, err := encodeHandle(handleData{JobID: id, EnvID: string(envID)})
	return handle, err == nil, err
}

// Resources implements ultra.EnvResourceLister by enumerating the live Nomad
// objects this provider owns for an environment.
func (p *Provider) Resources(ctx context.Context, envID ultra.EnvID) ([]string, error) {
	id := jobID(envID)
	job, _, err := p.client.Jobs().Info(id, p.query(ctx))
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("nomad: list job: %w", err)
	}
	if job.Stop != nil && *job.Stop {
		return nil, nil
	}
	out := []string{"job/" + id}
	allocations, _, err := p.client.Jobs().Allocations(id, true, p.query(ctx))
	if err != nil {
		return nil, fmt.Errorf("nomad: list allocations: %w", err)
	}
	for _, allocation := range allocations {
		if allocation.ClientStatus == "running" || allocation.ClientStatus == "pending" {
			out = append(out, "allocation/"+allocation.ID)
		}
	}
	return out, nil
}

// Probe implements ultra.CapabilityProber by asking the Nomad control plane
// what it can do, rather than inferring capability from the provider kind.
func (p *Provider) Probe(ctx context.Context) (ultra.ProviderCapabilities, error) {
	capabilities := ultra.ProviderCapabilities{
		Kind:  ultra.ProviderKindBYONomad,
		Notes: map[ultra.ProviderCapability]string{},
	}
	if _, err := p.client.Agent().Self(); err != nil {
		return capabilities, fmt.Errorf("nomad: control plane unreachable: %w", err)
	}
	nodes, _, err := p.client.Nodes().List(p.query(ctx))
	if err != nil {
		return capabilities, fmt.Errorf("nomad: cannot list nodes: %w", err)
	}
	ready := 0
	for _, node := range nodes {
		if node.Status == "ready" {
			ready++
		}
	}
	if ready == 0 {
		return capabilities, errors.New("nomad: the cluster has no ready client nodes")
	}
	capabilities.Supported = append(capabilities.Supported,
		ultra.CapabilityAdoptsOrphans,
		ultra.CapabilityEnumeratesResources,
		ultra.CapabilityServesToolEndpoint,
	)
	// A Nomad task's workspace lives in its allocation directory, which a
	// replacement allocation does not inherit.
	capabilities.Notes[ultra.CapabilityRestartPreservesWorkspace] =
		"a replacement allocation receives a fresh allocation directory"
	capabilities.Notes[ultra.CapabilityNamespaceIsolation] =
		"Nomad namespaces are not provisioned per organization by this adapter"
	return capabilities, nil
}
