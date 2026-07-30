// Package localdocker implements ultra.EnvProvider with Docker containers.
// Each environment gets a label-addressable Bezalel container and named
// workspace volume; the volume survives container restart and worker death.
package localdocker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"

	ultra "github.com/aleksclark/ultralogical"
)

const labelEnvID = "ultralogical.env_id"

// Config configures the local Docker provider.
type Config struct {
	Image       string
	PullImage   bool
	Host        string
	HTTPClient  *http.Client
	WaitTimeout time.Duration
}

// Provider implements ultra.EnvProvider.
type Provider struct {
	docker *client.Client
	cfg    Config
}

type handleData struct {
	ContainerID string `json:"container_id"`
	VolumeName  string `json:"volume_name"`
	HostPort    int    `json:"host_port"`
}

// New builds a Docker provider from environment configuration.
func New(cfg Config) (*Provider, error) {
	if cfg.Image == "" {
		cfg.Image = "ultralogical/bezalel:local"
	}
	if cfg.WaitTimeout <= 0 {
		cfg.WaitTimeout = 30 * time.Second
	}
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if cfg.Host != "" {
		opts = append(opts, client.WithHost(cfg.Host))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("localdocker: client: %w", err)
	}
	return &Provider{docker: cli, cfg: cfg}, nil
}

// Close closes the Docker API client.
func (p *Provider) Close() error { return p.docker.Close() }

func volumeName(id ultra.EnvID) string { return "ultralogical-env-" + string(id) }

func encodeHandle(h handleData) (ultra.ProviderHandle, error) {
	b, err := json.Marshal(h)
	return ultra.ProviderHandle{Version: 1, Data: b}, err
}

func decodeHandle(h ultra.ProviderHandle) (handleData, error) {
	if h.Version != 1 {
		return handleData{}, fmt.Errorf("localdocker: unsupported handle version %d", h.Version)
	}
	var d handleData
	if err := json.Unmarshal(h.Data, &d); err != nil {
		return d, fmt.Errorf("localdocker: decode handle: %w", err)
	}
	return d, nil
}

// Provision implements ultra.EnvProvider.
func (p *Provider) Provision(ctx context.Context, envID ultra.EnvID, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	if p.cfg.PullImage {
		reader, err := p.docker.ImagePull(ctx, p.cfg.Image, image.PullOptions{})
		if err != nil {
			return ultra.ProviderHandle{}, fmt.Errorf("localdocker: pull: %w", err)
		}
		_ = reader.Close()
	}
	vol := volumeName(envID)
	if _, err := p.docker.VolumeCreate(ctx, volume.CreateOptions{Name: vol}); err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("localdocker: volume: %w", err)
	}

	workdir := spec.Workdir
	if workdir == "" {
		workdir = "/work"
	}
	port, _ := nat.NewPort("tcp", "8080")
	env := []string{"BEZALEL_AUTH_TOKEN=" + token}
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	resp, err := p.docker.ContainerCreate(ctx,
		&containertypes.Config{
			Image:        p.cfg.Image,
			Env:          env,
			Cmd:          []string{"--workdir", workdir},
			Labels:       map[string]string{labelEnvID: string(envID)},
			ExposedPorts: nat.PortSet{port: struct{}{}},
		},
		&containertypes.HostConfig{
			Binds:        []string{vol + ":" + workdir},
			PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}},
		},
		&network.NetworkingConfig{}, (*v1.Platform)(nil), "ultralogical-env-"+string(envID))
	if err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("localdocker: create: %w", err)
	}
	if err := p.docker.ContainerStart(ctx, resp.ID, containertypes.StartOptions{}); err != nil {
		return ultra.ProviderHandle{}, fmt.Errorf("localdocker: start: %w", err)
	}
	d, err := p.inspect(ctx, resp.ID, vol)
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	return encodeHandle(d)
}

func (p *Provider) inspect(ctx context.Context, containerID, vol string) (handleData, error) {
	info, err := p.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return handleData{}, fmt.Errorf("localdocker: inspect: %w", err)
	}
	bindings := info.NetworkSettings.Ports[nat.Port("8080/tcp")]
	if len(bindings) == 0 {
		return handleData{}, errors.New("localdocker: no published Bezalel port")
	}
	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return handleData{}, fmt.Errorf("localdocker: host port: %w", err)
	}
	return handleData{ContainerID: containerID, VolumeName: vol, HostPort: port}, nil
}

// Status implements ultra.EnvProvider.
func (p *Provider) Status(ctx context.Context, handle ultra.ProviderHandle) (ultra.ProviderStatus, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	info, err := p.docker.ContainerInspect(ctx, d.ContainerID)
	if cerrdefs.IsNotFound(err) {
		return ultra.ProviderStatus{State: ultra.EnvFailed, Message: "container not found"}, nil
	}
	if err != nil {
		return ultra.ProviderStatus{}, err
	}
	if info.State.Running {
		return ultra.ProviderStatus{State: ultra.EnvReady}, nil
	}
	return ultra.ProviderStatus{State: ultra.EnvFailed, Message: info.State.Status}, nil
}

// Endpoint implements ultra.EnvProvider.
func (p *Provider) Endpoint(_ context.Context, handle ultra.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", d.HostPort), nil
}

// Restart replaces the container while preserving the named workspace
// volume and rotating its bearer token.
func (p *Provider) Restart(ctx context.Context, envID ultra.EnvID, handle ultra.ProviderHandle, spec ultra.EnvSpec, token string) (ultra.ProviderHandle, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return ultra.ProviderHandle{}, err
	}
	_ = p.docker.ContainerRemove(ctx, d.ContainerID, containertypes.RemoveOptions{Force: true})
	// Provision reuses the deterministic named volume.
	return p.Provision(ctx, envID, spec, token)
}

// Terminate implements ultra.EnvProvider. It removes both container and
// workspace volume; double termination is idempotent.
func (p *Provider) Terminate(ctx context.Context, handle ultra.ProviderHandle) error {
	d, err := decodeHandle(handle)
	if err != nil {
		return err
	}
	if err := p.docker.ContainerRemove(ctx, d.ContainerID, containertypes.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("localdocker: remove container: %w", err)
	}
	if err := p.docker.VolumeRemove(ctx, d.VolumeName, true); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("localdocker: remove volume: %w", err)
	}
	return nil
}

// ContainerID returns the provider container ID for harness verification.
func ContainerID(handle ultra.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	return d.ContainerID, err
}

// KillByEnvID kills the provider resource, used by reconciliation tests.
func (p *Provider) KillByEnvID(ctx context.Context, id ultra.EnvID) error {
	list, err := p.docker.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", labelEnvID+"="+string(id)))})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errors.New("localdocker: env container not found")
	}
	return p.docker.ContainerKill(ctx, list[0].ID, "SIGKILL")
}

// NormalizeImageName validates configured image names early.
func NormalizeImageName(name string) string { return strings.TrimSpace(name) }
