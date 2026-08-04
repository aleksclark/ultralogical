package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	uc "github.com/aleksclark/ultracore"
)

// Config configures the platform side of the tunnel provider.
type Config struct {
	// ControlURL is the agent's control API, published through the tunnel.
	ControlURL string `json:"control_url"`
	// Token is the org-scoped registration token the agent issued.
	Token string `json:"token"`
	// Secret signs control requests, so a leaked tunnel URL is not enough to
	// drive the user's machine.
	Secret string `json:"secret"`
	// Timeout bounds one control request.
	Timeout time.Duration `json:"-"`
	// Transport carries control requests. A deployment sets it to the
	// broker's transport for the agent, so requests travel down connections
	// the agent dialed out. Left nil the default transport is used, which
	// only works when the control URL is directly reachable.
	Transport http.RoundTripper `json:"-"`
}

// Provider implements uc.ResourceProvider by driving a remote agent.
type Provider struct {
	cfg    Config
	client *http.Client
}

// ErrUnreachable reports that the agent could not be reached. It is distinct
// from a failure the agent reported: a user's laptop closing its lid is a
// suspension to recover from, not an environment that broke.
var ErrUnreachable = errors.New("tunnel: the environment agent is unreachable")

// ErrRevoked reports that the agent's lease was withdrawn.
var ErrRevoked = errors.New("tunnel: the environment agent's lease was revoked")

// New builds a tunnel provider.
func New(cfg Config) (*Provider, error) {
	if cfg.ControlURL == "" {
		return nil, errors.New("tunnel: a control URL is required")
	}
	if cfg.Token == "" || cfg.Secret == "" {
		return nil, errors.New("tunnel: a token and signing secret are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Provider{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, Transport: cfg.Transport}}, nil
}

// call issues one signed control request.
func (p *Provider) call(ctx context.Context, path string, in, out any) error {
	var body []byte
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("tunnel: encode %s: %w", path, err)
		}
		body = encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.ControlURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tunnel: build %s: %w", path, err)
	}
	timestamp := time.Now()
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	req.Header.Set(HeaderSignature, Sign(p.cfg.Secret, path, timestamp, body))
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone:
		return ErrRevoked
	default:
		// The agent's message is the useful part: it is the provider error
		// from the user's own machine, and hiding it behind a status code
		// leaves an operator with nothing to act on.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("tunnel: %s returned HTTP %d: %s", path, resp.StatusCode,
			strings.TrimSpace(string(detail)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tunnel: decode %s: %w", path, err)
	}
	return nil
}

// Health reports the agent's liveness, which is what distinguishes a suspended
// environment from a failed one.
func (p *Provider) Health(ctx context.Context) (HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.ControlURL+PathHealth, nil)
	if err != nil {
		return HealthResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return HealthResponse{}, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return HealthResponse{}, fmt.Errorf("tunnel: health returned HTTP %d", resp.StatusCode)
	}
	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return HealthResponse{}, fmt.Errorf("tunnel: decode health: %w", err)
	}
	return health, nil
}

// Provision asks the agent to create the environment on the user's machine.

// Kind implements uc.ResourceProvider.
func (p *Provider) Kind() uc.ResourceKind { return uc.ResourceKindDevEnv }

// ValidateSpec implements uc.ResourceProvider.
func (p *Provider) ValidateSpec(spec json.RawMessage) error {
	s, err := uc.ParseDevEnvSpec(spec)
	if err != nil {
		return err
	}
	if s.Name == "" {
		return fmt.Errorf("tunnel: spec.name is required")
	}
	return nil
}

// HealthCheck implements uc.ResourceProvider.
func (p *Provider) HealthCheck(ctx context.Context, r uc.Resource) error {
	st, err := p.Status(ctx, r)
	if err != nil {
		return err
	}
	if st.State != uc.ResourceReady {
		if st.Message != "" {
			return fmt.Errorf("tunnel: not ready: %s", st.Message)
		}
		return fmt.Errorf("tunnel: not ready: %s", st.State)
	}
	return nil
}

func (p *Provider) Provision(ctx context.Context, r uc.Resource, token string) (json.RawMessage, error) {
	envID := r.ID
	spec, err := uc.ParseDevEnvSpec(r.Spec)
	if err != nil {
		return nil, err
	}
	var out ProvisionResponse
	if err := p.call(ctx, PathProvision, ProvisionRequest{ResourceID: envID, Spec: spec, Token: token}, &out); err != nil {
		return nil, err
	}
	return out.Handle, nil
}

// Status maps the agent's answer onto the environment state machine. An
// unreachable agent yields suspended rather than failed: the work is intact on
// a machine that is merely offline, and destroying it would be wrong.
func (p *Provider) Status(ctx context.Context, r uc.Resource) (uc.ResourceStatus, error) {
	handle := r.Handle
	var out StatusResponse
	err := p.call(ctx, PathStatus, HandleRequest{Handle: handle}, &out)
	switch {
	case errors.Is(err, ErrUnreachable):
		return uc.ResourceStatus{State: uc.ResourceSuspended, Message: "the environment agent is unreachable"}, nil
	case errors.Is(err, ErrRevoked):
		return uc.ResourceStatus{State: uc.ResourceTerminated, Message: "the environment agent's lease was revoked"}, nil
	case err != nil:
		return uc.ResourceStatus{}, err
	}
	return uc.ResourceStatus{State: out.State, Message: out.Message}, nil
}

// Endpoint returns the tool endpoint the agent publishes through the tunnel.
func (p *Provider) Endpoint(ctx context.Context, r uc.Resource) (uc.ToolEndpoint, error) {
	handle := r.Handle
	var out EndpointResponse
	if err := p.call(ctx, PathEndpoint, HandleRequest{Handle: handle}, &out); err != nil {
		return "", err
	}
	if out.Endpoint == "" {
		return "", errors.New("tunnel: the agent published no endpoint")
	}
	return uc.ToolEndpoint(out.Endpoint), nil
}

// Restart replaces the environment's runtime with a rotated token.
func (p *Provider) Restart(ctx context.Context, r uc.Resource, token string) (json.RawMessage, error) {
	envID := r.ID
	handle := r.Handle
	spec, err := uc.ParseDevEnvSpec(r.Spec)
	if err != nil {
		return nil, err
	}
	var out ProvisionResponse
	err = p.call(ctx, PathRestart, RestartRequest{ResourceID: envID, Handle: handle, Spec: spec, Token: token}, &out)
	if err != nil {
		return nil, err
	}
	return out.Handle, nil
}

// Terminate releases the environment on the user's machine.
func (p *Provider) Terminate(ctx context.Context, r uc.Resource) error {
	handle := r.Handle
	err := p.call(ctx, PathTerminate, HandleRequest{Handle: handle}, nil)
	if errors.Is(err, ErrRevoked) {
		// A revoked agent already released everything it held.
		return nil
	}
	return err
}

// Resources implements uc.ResourceLister through the agent, so a leak
// check asks the machine that actually holds the resources.
func (p *Provider) ListOwned(ctx context.Context) ([]uc.OwnedResource, error) {
	// Bulk listing is optional for tunnel; leak checks use Resources per id via Inspect.
	return nil, nil
}

func (p *Provider) Resources(ctx context.Context, envID uc.ResourceID) ([]string, error) {
	var out ResourcesResponse
	if err := p.call(ctx, PathResources, HandleRequest{ResourceID: envID}, &out); err != nil {
		if errors.Is(err, ErrRevoked) {
			return nil, nil
		}
		return nil, err
	}
	return out.Resources, nil
}

// RevokeLease withdraws the agent's lease and releases what it holds.
func (p *Provider) RevokeLease(ctx context.Context, handle json.RawMessage) error {
	err := p.call(ctx, PathRevoke, HandleRequest{Handle: handle}, nil)
	if errors.Is(err, ErrRevoked) {
		return nil
	}
	return err
}

// Probe implements uc.CapabilityProber by asking the agent what it is.
func (p *Provider) Probe(ctx context.Context) (uc.ProviderCapabilities, error) {
	capabilities := uc.ProviderCapabilities{
		Kind:  uc.ProviderKindTunnelLocal,
		Notes: map[uc.ProviderCapability]string{},
	}
	health, err := p.Health(ctx)
	if err != nil {
		return capabilities, err
	}
	if health.Revoked {
		return capabilities, ErrRevoked
	}
	capabilities.Supported = append(capabilities.Supported,
		uc.CapabilityServesToolEndpoint,
		uc.CapabilityEnumeratesResources,
		// A user's machine goes offline and comes back; that is a suspension
		// the platform recovers from rather than a failure.
		uc.CapabilityToleratesDisconnect,
		uc.CapabilityRestartPreservesState,
	)
	return capabilities, nil
}
