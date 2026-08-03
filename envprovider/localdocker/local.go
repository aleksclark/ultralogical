// Package localdocker implements uc.EnvProvider with Docker containers.
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

	uc "github.com/aleksclark/ultracore"
)

const labelEnvID = "ultracore.env_id"

// Config configures the local Docker provider.
type Config struct {
	Image       string
	PullImage   bool
	Host        string
	HTTPClient  *http.Client
	WaitTimeout time.Duration
}

// Provider implements uc.EnvProvider.
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
		cfg.Image = "ultracore/bezalel:local"
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

func volumeName(id uc.EnvID) string { return "ultracore-env-" + string(id) }

func encodeHandle(h handleData) (uc.ProviderHandle, error) {
	b, err := json.Marshal(h)
	return uc.ProviderHandle{Version: 1, Data: b}, err
}

func decodeHandle(h uc.ProviderHandle) (handleData, error) {
	if h.Version != 1 {
		return handleData{}, fmt.Errorf("localdocker: unsupported handle version %d", h.Version)
	}
	var d handleData
	if err := json.Unmarshal(h.Data, &d); err != nil {
		return d, fmt.Errorf("localdocker: decode handle: %w", err)
	}
	return d, nil
}

// Provision implements uc.EnvProvider.
func (p *Provider) Provision(ctx context.Context, envID uc.EnvID, spec uc.EnvSpec, token string) (uc.ProviderHandle, error) {
	// A spec that names an image means it: silently substituting the
	// configured default would let a declaration ask for one runtime and
	// receive another, which no caller could detect.
	imageRef := p.cfg.Image
	if spec.Image != "" {
		imageRef = spec.Image
	}
	if p.cfg.PullImage {
		reader, err := p.docker.ImagePull(ctx, imageRef, image.PullOptions{})
		if err != nil {
			return uc.ProviderHandle{}, fmt.Errorf("localdocker: pull: %w", err)
		}
		_ = reader.Close()
	}
	vol := volumeName(envID)
	if _, err := p.docker.VolumeCreate(ctx, volume.CreateOptions{Name: vol}); err != nil {
		return uc.ProviderHandle{}, fmt.Errorf("localdocker: volume: %w", err)
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
			Image:        imageRef,
			Env:          env,
			Cmd:          []string{"--workdir", workdir},
			Labels:       map[string]string{labelEnvID: string(envID)},
			ExposedPorts: nat.PortSet{port: struct{}{}},
		},
		&containertypes.HostConfig{
			Binds:        []string{vol + ":" + workdir},
			PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}},
		},
		&network.NetworkingConfig{}, (*v1.Platform)(nil), "ultracore-env-"+string(envID))
	if err != nil {
		return uc.ProviderHandle{}, fmt.Errorf("localdocker: create: %w", err)
	}
	if err := p.docker.ContainerStart(ctx, resp.ID, containertypes.StartOptions{}); err != nil {
		// Docker publishes on a port from the kernel's ephemeral range, the
		// same range every outgoing connection draws from, so on a busy host
		// the port can be taken in the window before the bind. The container
		// is recreated rather than restarted: a container whose start failed
		// this way comes back with no port map at all, and restarting it in
		// place yields an environment nothing can reach.
		if !strings.Contains(err.Error(), "address already in use") {
			return uc.ProviderHandle{}, fmt.Errorf("localdocker: start: %w", err)
		}
		_ = p.docker.ContainerRemove(ctx, resp.ID, containertypes.RemoveOptions{Force: true})
		return p.provisionRetry(ctx, envID, spec, token)
	}
	d, err := p.inspect(ctx, resp.ID, vol)
	if err != nil {
		return uc.ProviderHandle{}, err
	}
	return encodeHandle(d)
}

// provisionRetry re-attempts provisioning after a transient host-port
// collision. The attempt budget is carried on the context so a pathological
// host cannot make this recurse forever.
func (p *Provider) provisionRetry(ctx context.Context, envID uc.EnvID, spec uc.EnvSpec, token string) (uc.ProviderHandle, error) {
	attempt, _ := ctx.Value(provisionAttemptKey{}).(int)
	if attempt >= 5 {
		return uc.ProviderHandle{}, errors.New("localdocker: every published port this host offered was already in use")
	}
	select {
	case <-ctx.Done():
		return uc.ProviderHandle{}, ctx.Err()
	case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
	}
	return p.Provision(context.WithValue(ctx, provisionAttemptKey{}, attempt+1), envID, spec, token)
}

// provisionAttemptKey carries the retry count without widening the provider
// interface, which every other adapter would then have to honor.
type provisionAttemptKey struct{}

func (p *Provider) inspect(ctx context.Context, containerID, vol string) (handleData, error) {
	// The port mapping appears a moment after start returns, so inspection
	// polls rather than reading once. Treating the first empty answer as
	// failure would make provisioning flaky on a loaded machine.
	var bindings []nat.PortBinding
	deadline := time.Now().Add(15 * time.Second)
	for {
		info, err := p.docker.ContainerInspect(ctx, containerID)
		if err != nil {
			return handleData{}, fmt.Errorf("localdocker: inspect: %w", err)
		}
		bindings = info.NetworkSettings.Ports[nat.Port("8080/tcp")]
		if len(bindings) > 0 {
			break
		}
		if !info.State.Running {
			return handleData{}, fmt.Errorf("localdocker: container exited before publishing a port: %s", info.State.Status)
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return handleData{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if len(bindings) == 0 {
		return handleData{}, errors.New("localdocker: no published Bezalel port")
	}
	port, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return handleData{}, fmt.Errorf("localdocker: host port: %w", err)
	}
	return handleData{ContainerID: containerID, VolumeName: vol, HostPort: port}, nil
}

// Status implements uc.EnvProvider.
func (p *Provider) Status(ctx context.Context, handle uc.ProviderHandle) (uc.ProviderStatus, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return uc.ProviderStatus{}, err
	}
	info, err := p.docker.ContainerInspect(ctx, d.ContainerID)
	if cerrdefs.IsNotFound(err) {
		return uc.ProviderStatus{State: uc.EnvFailed, Message: "container not found"}, nil
	}
	if err != nil {
		return uc.ProviderStatus{}, err
	}
	if info.State.Running {
		return uc.ProviderStatus{State: uc.EnvReady}, nil
	}
	return uc.ProviderStatus{State: uc.EnvFailed, Message: info.State.Status}, nil
}

// Endpoint implements uc.EnvProvider.
func (p *Provider) Endpoint(_ context.Context, handle uc.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", d.HostPort), nil
}

// Restart replaces the container while preserving the named workspace
// volume and rotating its bearer token.
func (p *Provider) Restart(ctx context.Context, envID uc.EnvID, handle uc.ProviderHandle, spec uc.EnvSpec, token string) (uc.ProviderHandle, error) {
	d, err := decodeHandle(handle)
	if err != nil {
		return uc.ProviderHandle{}, err
	}
	_ = p.docker.ContainerRemove(ctx, d.ContainerID, containertypes.RemoveOptions{Force: true})
	// Provision reuses the deterministic named volume.
	return p.Provision(ctx, envID, spec, token)
}

// Terminate implements uc.EnvProvider. It removes both container and
// workspace volume; double termination is idempotent.
func (p *Provider) Terminate(ctx context.Context, handle uc.ProviderHandle) error {
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
func ContainerID(handle uc.ProviderHandle) (string, error) {
	d, err := decodeHandle(handle)
	return d.ContainerID, err
}

// Adopt implements uc.EnvAdopter. Containers are labelled and named by
// environment id, so a provision retry after a control-plane death finds the
// container it already created instead of starting a second one.
func (p *Provider) Adopt(ctx context.Context, id uc.EnvID) (uc.ProviderHandle, bool, error) {
	list, err := p.docker.ContainerList(ctx, containertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelEnvID+"="+string(id))),
	})
	if err != nil {
		return uc.ProviderHandle{}, false, fmt.Errorf("localdocker: adopt list: %w", err)
	}
	if len(list) == 0 {
		return uc.ProviderHandle{}, false, nil
	}
	// An adopted container may have been created but never started if the
	// worker died mid-provision; start it before publishing a handle.
	info, err := p.docker.ContainerInspect(ctx, list[0].ID)
	if err != nil {
		return uc.ProviderHandle{}, false, fmt.Errorf("localdocker: adopt inspect: %w", err)
	}
	if !info.State.Running {
		if err := p.docker.ContainerStart(ctx, list[0].ID, containertypes.StartOptions{}); err != nil {
			return uc.ProviderHandle{}, false, fmt.Errorf("localdocker: adopt start: %w", err)
		}
	}
	data, err := p.inspect(ctx, list[0].ID, volumeName(id))
	if err != nil {
		return uc.ProviderHandle{}, false, err
	}
	handle, err := encodeHandle(data)
	return handle, err == nil, err
}

// Resources implements uc.EnvResourceLister: it enumerates the containers
// and volumes still labelled for an environment so leak checks can prove
// termination released them.
func (p *Provider) Resources(ctx context.Context, id uc.EnvID) ([]string, error) {
	var out []string
	containers, err := p.docker.ContainerList(ctx, containertypes.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelEnvID+"="+string(id))),
	})
	if err != nil {
		return nil, fmt.Errorf("localdocker: list containers: %w", err)
	}
	for _, c := range containers {
		out = append(out, "container:"+c.ID)
	}
	name := volumeName(id)
	if _, err := p.docker.VolumeInspect(ctx, name); err == nil {
		out = append(out, "volume:"+name)
	} else if !cerrdefs.IsNotFound(err) {
		return nil, fmt.Errorf("localdocker: inspect volume: %w", err)
	}
	return out, nil
}

// KillByEnvID kills the provider resource, used by reconciliation tests.
func (p *Provider) KillByEnvID(ctx context.Context, id uc.EnvID) error {
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
