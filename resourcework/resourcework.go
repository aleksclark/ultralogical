// Package resourcework orchestrates durable resource lifecycle jobs. Provider
// implementations are injected by main; all state and idempotency live in
// the org-scoped store.
package resourcework

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/jobqueue"
	"github.com/aleksclark/ultracore/mcp"
	"github.com/aleksclark/ultracore/secrets"
)

// ProvisionJob provisions one requested resource.
type ProvisionJob struct {
	OrgID      string `json:"org_id"`
	ResourceID string `json:"resource_id"`
}

func (ProvisionJob) Kind() string { return "resource.provision" }

// TerminateJob terminates one resource.
type TerminateJob struct {
	OrgID      string `json:"org_id"`
	ResourceID string `json:"resource_id"`
}

func (TerminateJob) Kind() string { return "resource.terminate" }

// ReconcileJob verifies one active resource and ticks metering.
type ReconcileJob struct {
	OrgID      string `json:"org_id"`
	ResourceID string `json:"resource_id"`
}

// RestartJob replaces one resource's runtime, preserving durable state when
// the provider claims restart_preserves_state, and rotating its bearer token.
type RestartJob struct {
	OrgID      string `json:"org_id"`
	ResourceID string `json:"resource_id"`
}

func (RestartJob) Kind() string { return "resource.restart" }

//nolint:staticcheck // explicit mapping keeps lifecycle job types decoupled.
func reconcileAfterProvision(job ProvisionJob) ReconcileJob {
	return ReconcileJob{OrgID: job.OrgID, ResourceID: job.ResourceID}
}

//nolint:staticcheck // explicit mapping keeps lifecycle job types decoupled.
func reconcileAfterRestart(job RestartJob) ReconcileJob {
	return ReconcileJob{OrgID: job.OrgID, ResourceID: job.ResourceID}
}

func (ReconcileJob) Kind() string { return "resource.reconcile" }

// ProviderBuilder builds the adapter for one org registration. Resources
// are hosted per registration, not per kind: two orgs registering Kubernetes
// point at different clusters, so a single shared instance per kind would send
// one org's work to another org's control plane.
type ProviderBuilder interface {
	Build(ctx context.Context, kind string, config json.RawMessage) (uc.ResourceProvider, error)
}

// Service creates resource records and implements lifecycle workers.
type Service struct {
	Store             uc.Store
	Enqueue           jobqueue.TxEnqueuer
	Keyring           secrets.Keyring
	Providers         ProviderBuilder
	Log               *slog.Logger
	ReconcileInterval time.Duration
	ProvisionTimeout  time.Duration
	// Clients caches one MCP client per resource token epoch. Left nil
	// it is created on first use.
	Clients *mcp.Cache

	cacheOnce sync.Once
}

func (s *Service) interval() time.Duration {
	if s.ReconcileInterval > 0 {
		return s.ReconcileInterval
	}
	return 5 * time.Second
}
func (s *Service) provisionTimeout() time.Duration {
	if s.ProvisionTimeout > 0 {
		return s.ProvisionTimeout
	}
	return time.Minute
}

func token() (clear string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	clear = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(clear))
	return clear, sum[:], nil
}

func resourceName(r uc.Resource) string {
	if r.Kind == uc.ResourceKindDevEnv || r.Kind == "" {
		if s, err := uc.ParseDevEnvSpec(r.Spec); err == nil {
			return s.Name
		}
	}
	var probe struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(r.Spec, &probe)
	return probe.Name
}

func eventPayload(r uc.Resource, message string) uc.ResourceEventPayload {
	return uc.ResourceEventPayload{
		ResourceID:         r.ID,
		Kind:               r.Kind,
		Name:               resourceName(r),
		ProviderInstanceID: r.ProviderInstanceID,
		Endpoint:           string(r.Endpoint),
		Message:            message,
		Epoch:              r.Epoch,
	}
}

func appendEvent(ctx context.Context, scope uc.OrgScope, session uc.SessionID, actor uc.Actor, kind string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return scope.Events().Append(ctx, session, uc.Event{Actor: actor, Kind: kind, Payload: b})
}

// Request atomically creates a requested resource, emits ResourceRequested,
// and enqueues provisioning. providerName defaults to "default". Empty kind
// is filled from the adapter's Kind(); if the adapter is silent, dev_env.
func (s *Service) Request(ctx context.Context, org uc.OrgID, session uc.SessionID, kind uc.ResourceKind, spec json.RawMessage, providerName string, createdBy *uc.RunID) (uc.Resource, int64, error) {
	if providerName == "" {
		providerName = "default"
	}
	instance, err := s.Store.Org(org).Providers().GetByName(ctx, providerName)
	if err != nil {
		return uc.Resource{}, 0, err
	}
	// Validate before create so a bad spec never leaves a durable record.
	provider, err := s.Providers.Build(ctx, instance.Kind, instance.Config)
	if err != nil {
		return uc.Resource{}, 0, fmt.Errorf("resourcework: build provider for %s: %w", instance.Name, err)
	}
	if err := provider.ValidateSpec(spec); err != nil {
		return uc.Resource{}, 0, fmt.Errorf("resourcework: invalid spec: %w", err)
	}
	// Adapter declares the resource kind it hosts. Reject mismatches so a
	// caller cannot provision null_resource against a dev_env adapter (or
	// the reverse) and silently get the wrong kind.
	if k := provider.Kind(); k != "" {
		if kind != "" && kind != k {
			return uc.Resource{}, 0, fmt.Errorf("resourcework: provider %q hosts kind %q, not %q", instance.Name, k, kind)
		}
		kind = k
	} else if kind == "" {
		kind = uc.ResourceKindDevEnv
	}
	clear, hash, err := token()
	if err != nil {
		return uc.Resource{}, 0, err
	}
	enc, err := s.Keyring.Encrypt([]byte(clear))
	if err != nil {
		return uc.Resource{}, 0, err
	}
	secrets.DefaultRedactor.Register(clear)
	r := uc.Resource{
		ID: uc.ResourceID(uuid.NewString()), OrgID: org, SessionID: session, Kind: kind,
		ProviderInstanceID: instance.ID, State: uc.ResourceRequested, Spec: spec,
		TokenHash: hash, TokenEnc: enc, Epoch: 1, CreatedByRunID: createdBy,
	}
	var seq int64
	err = s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		if err := scope.Resources().Create(ctx, r); err != nil {
			return err
		}
		seq, err = appendEvent(ctx, scope, session, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceRequested, eventPayload(r, ""))
		if err != nil {
			return err
		}
		if err := s.Enqueue.EnqueueInTx(ctx, txs, ProvisionJob{OrgID: string(org), ResourceID: string(r.ID)}); err != nil {
			return err
		}
		// Arm the reconcile watchdog now: if the worker dies mid-provision
		// and the provision job is not redelivered, reconciliation still
		// converges the resource instead of leaving it stuck.
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), ResourceID: string(r.ID)},
			jobqueue.WithScheduledAt(time.Now().Add(s.provisionTimeout())))
	})
	return r, seq, err
}

// RequestTerminate marks and queues termination.
func (s *Service) RequestTerminate(ctx context.Context, org uc.OrgID, id uc.ResourceID) error {
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		r, err := scope.Resources().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if r.State == uc.ResourceTerminated {
			return nil
		}
		if err := scope.Resources().SetTerminating(ctx, id); err != nil {
			return err
		}
		_, err = appendEvent(ctx, scope, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceTerminating, eventPayload(r, ""))
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, TerminateJob{OrgID: string(org), ResourceID: string(id)})
	})
}

func (s *Service) provider(ctx context.Context, scope uc.OrgScope, r uc.Resource) (uc.ResourceProvider, uc.ProviderInstance, error) {
	instance, err := scope.Providers().Get(ctx, r.ProviderInstanceID)
	if err != nil {
		return nil, instance, err
	}
	if s.Providers == nil {
		return nil, instance, fmt.Errorf("resourcework: no provider registry is configured")
	}
	provider, err := s.Providers.Build(ctx, instance.Kind, instance.Config)
	if err != nil {
		return nil, instance, fmt.Errorf("resourcework: build provider for %s: %w", instance.Name, err)
	}
	return provider, instance, nil
}

func (s *Service) clearToken(r uc.Resource) (string, error) {
	plain, err := s.Keyring.Decrypt(r.TokenEnc)
	if err != nil {
		return "", err
	}
	clear := string(plain)
	secrets.DefaultRedactor.Register(clear)
	return clear, nil
}

// ClearTokenForTools decrypts a resource token at the point of MCP use.
func (s *Service) ClearTokenForTools(r uc.Resource) (string, error) { return s.clearToken(r) }

// acquire obtains the provider resource. A provision job that is redelivered
// after the control plane died between resource creation and handle
// persistence adopts the existing resource instead of creating a duplicate.
// Providers that cannot enumerate their resources fall back to plain provisioning.
func (s *Service) acquire(ctx context.Context, provider uc.ResourceProvider, r uc.Resource, clearToken string) (json.RawMessage, error) {
	if uc.HandlePresent(r.Handle) {
		return r.Handle, nil
	}
	if adopter, ok := provider.(uc.ResourceAdopter); ok {
		handle, found, err := adopter.Adopt(ctx, r)
		if err != nil {
			return nil, err
		}
		if found {
			if s.Log != nil {
				s.Log.Info("resourcework: adopted interrupted provisioning", "resource_id", string(r.ID))
			}
			return handle, nil
		}
	}
	return provider.Provision(ctx, r, clearToken)
}

// RequestRestart marks a resource for restart and enqueues the work. The
// restart preserves durable state when the provider claims it, mints a new
// bearer token, and increments the token epoch so cached tool clients are
// invalidated.
func (s *Service) RequestRestart(ctx context.Context, org uc.OrgID, id uc.ResourceID) (int64, error) {
	var seq int64
	err := s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		r, err := scope.Resources().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if r.State.Terminal() || r.State == uc.ResourceTerminating {
			return fmt.Errorf("resourcework: resource is %s and cannot restart", r.State)
		}
		if err := scope.Resources().SetProvisioning(ctx, id); err != nil {
			return err
		}
		r.State = uc.ResourceProvisioning
		seq, err = appendEvent(ctx, scope, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceProvisioning, eventPayload(r, "restart requested"))
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, RestartJob{OrgID: string(org), ResourceID: string(id)})
	})
	return seq, err
}

// Restart implements jobqueue.Worker[RestartJob].
func (s *Service) Restart(ctx context.Context, job RestartJob) error {
	org, id := uc.OrgID(job.OrgID), uc.ResourceID(job.ResourceID)
	scope := s.Store.Org(org)
	r, err := scope.Resources().Get(ctx, id)
	if err != nil {
		return err
	}
	if r.State.Terminal() {
		return nil
	}
	provider, instance, err := s.provider(ctx, scope, r)
	if err != nil {
		return s.fail(ctx, org, r, err.Error())
	}
	clear, hash, err := token()
	if err != nil {
		return err
	}
	enc, err := s.Keyring.Encrypt([]byte(clear))
	if err != nil {
		return err
	}
	secrets.DefaultRedactor.Register(clear)
	if err := scope.Resources().RotateToken(ctx, id, hash, enc); err != nil {
		return err
	}
	s.InvalidateToolClients(id)
	handle, err := provider.Restart(ctx, r, clear)
	if err != nil {
		return s.fail(ctx, org, r, "resource restart failed")
	}
	endpoint, err := s.awaitHealthy(ctx, provider, rWithHandle(r, handle), instance.Capabilities)
	if err != nil {
		_ = provider.Terminate(ctx, rWithHandle(r, handle))
		return s.fail(ctx, org, r, "resource did not become healthy after restart")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Resources().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State.Terminal() {
			return nil
		}
		if e = sc.Resources().SetReady(ctx, id, handle, endpoint); e != nil {
			return e
		}
		locked.Handle = handle
		locked.Endpoint = endpoint
		locked.State = uc.ResourceReady
		_, e = appendEvent(ctx, sc, locked.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceReady, eventPayload(locked, "restarted"))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterRestart(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

func rWithHandle(r uc.Resource, handle json.RawMessage) uc.Resource {
	r.Handle = handle
	return r
}

// awaitHealthy polls until the provider reports ready. When the registration
// serves a tool endpoint, Bezalel must also answer its health endpoint.
func (s *Service) awaitHealthy(ctx context.Context, provider uc.ResourceProvider, r uc.Resource, caps uc.ProviderCapabilities) (uc.ToolEndpoint, error) {
	deadline := time.Now().Add(s.provisionTimeout())
	servesTools := caps.Has(uc.CapabilityServesToolEndpoint)
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, r)
		if err == nil && status.State == uc.ResourceReady {
			endpoint, err := provider.Endpoint(ctx, r)
			if err != nil {
				// keep polling
			} else if servesTools && endpoint != "" {
				if mcp.Healthy(ctx, string(endpoint)) == nil {
					return endpoint, nil
				}
			} else {
				// Lifecycle-only, or no endpoint required yet.
				return endpoint, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", errors.New("resourcework: resource did not become healthy")
}

// Provision implements jobqueue.Worker[ProvisionJob].
func (s *Service) Provision(ctx context.Context, job ProvisionJob) error {
	org, id := uc.OrgID(job.OrgID), uc.ResourceID(job.ResourceID)
	scope := s.Store.Org(org)
	r, err := scope.Resources().Get(ctx, id)
	if err != nil {
		return err
	}
	if r.State == uc.ResourceReady || r.State.Terminal() {
		return nil
	}
	if err := scope.Resources().SetProvisioning(ctx, id); err != nil {
		return err
	}
	_, _ = appendEvent(ctx, scope, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceProvisioning, eventPayload(r, ""))
	provider, instance, err := s.provider(ctx, scope, r)
	if err != nil {
		return s.fail(ctx, org, r, err.Error())
	}
	clear, err := s.clearToken(r)
	if err != nil {
		return err
	}
	handle, err := s.acquire(ctx, provider, r, clear)
	if err != nil {
		return s.fail(ctx, org, r, "resource provisioning failed")
	}
	// Persist the handle before waiting for health so a control-plane death
	// during the wait cannot orphan the resource: the retry adopts it.
	if err := scope.Resources().SetHandle(ctx, id, handle); err != nil {
		return err
	}
	r.Handle = handle
	endpoint, err := s.awaitHealthy(ctx, provider, r, instance.Capabilities)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		_ = provider.Terminate(ctx, r)
		return s.fail(ctx, org, r, "resource did not become healthy")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Resources().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != uc.ResourceProvisioning {
			return nil
		}
		if e = sc.Resources().SetReady(ctx, id, handle, endpoint); e != nil {
			return e
		}
		locked.Handle = handle
		locked.Endpoint = endpoint
		locked.State = uc.ResourceReady
		_, e = appendEvent(ctx, sc, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceReady, eventPayload(locked, ""))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterProvision(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// suspend parks a resource whose host is unreachable.
func (s *Service) suspend(ctx context.Context, org uc.OrgID, r uc.Resource, message string) error {
	s.InvalidateToolClients(r.ID)
	if message == "" {
		message = "the resource host is unreachable"
	}
	err := s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Resources().GetForUpdate(ctx, r.ID)
		if e != nil {
			return e
		}
		if locked.State != uc.ResourceReady {
			return nil
		}
		if e = sc.Resources().SetSuspended(ctx, r.ID, message); e != nil {
			return e
		}
		r.State = uc.ResourceSuspended
		_, e = appendEvent(ctx, sc, r.SessionID, uc.Actor{Type: uc.ActorSystem},
			uc.EventKindResourceSuspended, eventPayload(r, message))
		return e
	})
	if err != nil {
		return err
	}
	return s.rearmReconcile(ctx, org, r.ID)
}

// reconcileSuspended asks whether a suspended resource's host has come back.
func (s *Service) reconcileSuspended(ctx context.Context, org uc.OrgID, r uc.Resource) error {
	scope := s.Store.Org(org)
	provider, instance, err := s.provider(ctx, scope, r)
	if err != nil {
		return s.rearmReconcile(ctx, org, r.ID)
	}
	status, err := provider.Status(ctx, r)
	healthy := err == nil && status.State == uc.ResourceReady
	if healthy && instance.Capabilities.Has(uc.CapabilityServesToolEndpoint) && r.Endpoint != "" {
		healthy = mcp.Healthy(ctx, string(r.Endpoint)) == nil
	}
	if !healthy {
		if err == nil && status.State.Terminal() {
			return s.fail(ctx, org, r, "resource is no longer healthy")
		}
		return s.rearmReconcile(ctx, org, r.ID)
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Resources().GetForUpdate(ctx, r.ID)
		if e != nil {
			return e
		}
		if locked.State != uc.ResourceSuspended {
			return nil
		}
		if e = sc.Resources().SetReady(ctx, r.ID, locked.Handle, locked.Endpoint); e != nil {
			return e
		}
		locked.State = uc.ResourceReady
		if _, e = appendEvent(ctx, sc, r.SessionID, uc.Actor{Type: uc.ActorSystem},
			uc.EventKindResourceReady, eventPayload(locked, "resumed")); e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), ResourceID: string(r.ID)},
			jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// rearmReconcile keeps a resource under observation without changing it.
func (s *Service) rearmReconcile(ctx context.Context, org uc.OrgID, id uc.ResourceID) error {
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), ResourceID: string(id)},
			jobqueue.WithScheduledAt(time.Now().Add(s.interval())))
	})
}

func (s *Service) fail(ctx context.Context, org uc.OrgID, r uc.Resource, message string) error {
	s.InvalidateToolClients(r.ID)
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		if err := sc.Resources().SetFailed(ctx, r.ID, message); err != nil {
			return err
		}
		_, err := appendEvent(ctx, sc, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceFailed, eventPayload(r, message))
		return err
	})
}

// Terminate implements jobqueue.Worker[TerminateJob].
func (s *Service) Terminate(ctx context.Context, job TerminateJob) error {
	org, id := uc.OrgID(job.OrgID), uc.ResourceID(job.ResourceID)
	scope := s.Store.Org(org)
	r, err := scope.Resources().Get(ctx, id)
	if err != nil {
		return err
	}
	if r.State == uc.ResourceTerminated {
		return nil
	}
	provider, _, err := s.provider(ctx, scope, r)
	if err != nil {
		return err
	}
	s.InvalidateToolClients(id)
	if uc.HandlePresent(r.Handle) {
		if err := provider.Terminate(ctx, r); err != nil {
			return err
		}
	}
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		if err := sc.Resources().SetTerminated(ctx, id); err != nil {
			return err
		}
		_, err := appendEvent(ctx, sc, r.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindResourceTerminated, eventPayload(r, ""))
		return err
	})
}

// Reconcile implements jobqueue.Worker[ReconcileJob] and self-reschedules.
func (s *Service) Reconcile(ctx context.Context, job ReconcileJob) error {
	org, id := uc.OrgID(job.OrgID), uc.ResourceID(job.ResourceID)
	scope := s.Store.Org(org)
	r, err := scope.Resources().Get(ctx, id)
	if errors.Is(err, uc.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.State == uc.ResourceRequested || r.State == uc.ResourceProvisioning {
		return s.recoverProvisioning(ctx, org, r)
	}
	if r.State == uc.ResourceSuspended {
		return s.reconcileSuspended(ctx, org, r)
	}
	if r.State != uc.ResourceReady {
		return nil
	}
	provider, instance, err := s.provider(ctx, scope, r)
	if err != nil {
		return err
	}
	status, err := provider.Status(ctx, r)
	if err == nil && status.State == uc.ResourceSuspended {
		return s.suspend(ctx, org, r, status.Message)
	}
	healthy := err == nil && status.State == uc.ResourceReady
	if healthy && instance.Capabilities.Has(uc.CapabilityServesToolEndpoint) && r.Endpoint != "" {
		healthy = mcp.Healthy(ctx, string(r.Endpoint)) == nil
	}
	if !healthy {
		return s.fail(ctx, org, r, "resource is no longer healthy")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Resources().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != uc.ResourceReady {
			return nil
		}
		_ = sc.Providers().MarkHealthy(ctx, r.ProviderInstanceID)
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: job.OrgID, ResourceID: job.ResourceID}, jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// recoverProvisioning is the watchdog for a resource whose provisioning was interrupted.
func (s *Service) recoverProvisioning(ctx context.Context, org uc.OrgID, r uc.Resource) error {
	timeout := s.provisionTimeout()
	stalled := time.Since(r.UpdatedAt)
	if stalled < timeout {
		return s.Store.Tx(ctx, func(txs uc.Store) error {
			return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), ResourceID: string(r.ID)},
				jobqueue.WithScheduledAt(time.Now().Add(timeout)))
		})
	}
	if stalled > 10*timeout {
		return s.fail(ctx, org, r, "resource provisioning did not converge")
	}
	if s.Log != nil {
		s.Log.Warn("resourcework: re-driving interrupted provisioning",
			"resource_id", string(r.ID), "stalled_for", stalled.String())
	}
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, err := sc.Resources().GetForUpdate(ctx, r.ID)
		if err != nil {
			return err
		}
		if locked.State != uc.ResourceRequested && locked.State != uc.ResourceProvisioning {
			return nil
		}
		if err := s.Enqueue.EnqueueInTx(ctx, txs, ProvisionJob{OrgID: string(org), ResourceID: string(r.ID)}); err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), ResourceID: string(r.ID)},
			jobqueue.WithScheduledAt(time.Now().Add(timeout)))
	})
}

// ToolClient returns an MCP client for a ready resource, reusing the
// cached client for the resource's current token epoch.
func (s *Service) ToolClient(ctx context.Context, r uc.Resource) (*mcp.Client, error) {
	clear, err := s.clearToken(r)
	if err != nil {
		return nil, err
	}
	client, err := s.clients().Client(string(r.ID), r.Epoch, string(r.Endpoint), clear)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// InvalidateToolClients drops any cached client for a resource.
func (s *Service) InvalidateToolClients(id uc.ResourceID) { s.clients().Invalidate(string(id)) }

// RememberTools records the tools a resource offered.
func (s *Service) RememberTools(id uc.ResourceID, names []string) {
	s.clients().RememberTools(string(id), names)
}

// LastTools returns the tools a resource last offered.
func (s *Service) LastTools(id uc.ResourceID) []string { return s.clients().LastTools(string(id)) }

func (s *Service) clients() *mcp.Cache {
	s.cacheOnce.Do(func() {
		if s.Clients == nil {
			s.Clients = mcp.NewCache()
		}
	})
	return s.Clients
}

// Exec runs a real tool against a ready resource.
func (s *Service) Exec(ctx context.Context, org uc.OrgID, id uc.ResourceID, name string, args json.RawMessage) (mcp.Result, error) {
	r, err := s.Store.Org(org).Resources().Get(ctx, id)
	if err != nil {
		return mcp.Result{}, err
	}
	if r.State != uc.ResourceReady {
		return mcp.Result{}, errors.New("resource is not ready")
	}
	client, err := s.ToolClient(ctx, r)
	if err != nil {
		return mcp.Result{}, err
	}
	return client.Call(ctx, name, args)
}
