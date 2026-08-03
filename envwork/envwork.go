// Package envwork orchestrates durable environment lifecycle jobs. Provider
// implementations are injected by main; all state and idempotency live in
// the org-scoped store.
package envwork

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

// ProvisionJob provisions one requested env.
type ProvisionJob struct {
	OrgID string `json:"org_id"`
	EnvID string `json:"env_id"`
}

func (ProvisionJob) Kind() string { return "env.provision" }

// TerminateJob terminates one env.
type TerminateJob struct {
	OrgID string `json:"org_id"`
	EnvID string `json:"env_id"`
}

func (TerminateJob) Kind() string { return "env.terminate" }

// ReconcileJob verifies one active env and ticks metering.
type ReconcileJob struct {
	OrgID string `json:"org_id"`
	EnvID string `json:"env_id"`
}

// RestartJob replaces one env's runtime, preserving its workspace and
// rotating its bearer token.
type RestartJob struct {
	OrgID string `json:"org_id"`
	EnvID string `json:"env_id"`
}

func (RestartJob) Kind() string { return "env.restart" }

//nolint:staticcheck // explicit mapping keeps lifecycle job types decoupled.
func reconcileAfterProvision(job ProvisionJob) ReconcileJob {
	return ReconcileJob{OrgID: job.OrgID, EnvID: job.EnvID}
}

//nolint:staticcheck // explicit mapping keeps lifecycle job types decoupled.
func reconcileAfterRestart(job RestartJob) ReconcileJob {
	return ReconcileJob{OrgID: job.OrgID, EnvID: job.EnvID}
}

func (ReconcileJob) Kind() string { return "env.reconcile" }

// ProviderBuilder builds the adapter for one org registration. Environments
// are hosted per registration, not per kind: two orgs registering Kubernetes
// point at different clusters, so a single shared instance per kind would send
// one org's work to another org's control plane.
type ProviderBuilder interface {
	Build(ctx context.Context, kind string, config json.RawMessage) (uc.EnvProvider, error)
}

// Service creates env records and implements lifecycle workers.
type Service struct {
	Store             uc.Store
	Enqueue           jobqueue.TxEnqueuer
	Keyring           secrets.Keyring
	Providers         ProviderBuilder
	Log               *slog.Logger
	ReconcileInterval time.Duration
	ProvisionTimeout  time.Duration
	// Clients caches one MCP client per environment token epoch. Left nil
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

func eventPayload(env uc.DevEnv, message string) uc.EnvEventPayload {
	return uc.EnvEventPayload{EnvID: env.ID, Name: env.Spec.Name, ProviderInstanceID: env.ProviderInstanceID, Endpoint: env.Endpoint, Message: message, Epoch: env.Epoch}
}

func appendEvent(ctx context.Context, scope uc.OrgScope, session uc.SessionID, actor uc.Actor, kind string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return scope.Events().Append(ctx, session, uc.Event{Actor: actor, Kind: kind, Payload: b})
}

// Request atomically creates a requested environment, emits EnvRequested,
// and enqueues provisioning. providerName defaults to "default".
func (s *Service) Request(ctx context.Context, org uc.OrgID, session uc.SessionID, spec uc.EnvSpec, providerName string, createdBy *uc.RunID) (uc.DevEnv, int64, error) {
	if providerName == "" {
		providerName = "default"
	}
	instance, err := s.Store.Org(org).Providers().GetByName(ctx, providerName)
	if err != nil {
		return uc.DevEnv{}, 0, err
	}
	clear, hash, err := token()
	if err != nil {
		return uc.DevEnv{}, 0, err
	}
	enc, err := s.Keyring.Encrypt([]byte(clear))
	if err != nil {
		return uc.DevEnv{}, 0, err
	}
	secrets.DefaultRedactor.Register(clear)
	env := uc.DevEnv{ID: uc.EnvID(uuid.NewString()), OrgID: org, SessionID: session,
		ProviderInstanceID: instance.ID, State: uc.EnvRequested, Spec: spec,
		TokenHash: hash, TokenEnc: enc, Epoch: 1, CreatedByRunID: createdBy}
	var seq int64
	err = s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		if err := scope.Envs().Create(ctx, env); err != nil {
			return err
		}
		seq, err = appendEvent(ctx, scope, session, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvRequested, eventPayload(env, ""))
		if err != nil {
			return err
		}
		if err := s.Enqueue.EnqueueInTx(ctx, txs, ProvisionJob{OrgID: string(org), EnvID: string(env.ID)}); err != nil {
			return err
		}
		// Arm the reconcile watchdog now: if the worker dies mid-provision
		// and the provision job is not redelivered, reconciliation still
		// converges the environment instead of leaving it stuck.
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), EnvID: string(env.ID)},
			jobqueue.WithScheduledAt(time.Now().Add(s.provisionTimeout())))
	})
	return env, seq, err
}

// RequestTerminate marks and queues termination.
func (s *Service) RequestTerminate(ctx context.Context, org uc.OrgID, id uc.EnvID) error {
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		env, err := scope.Envs().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if env.State == uc.EnvTerminated {
			return nil
		}
		if err := scope.Envs().SetTerminating(ctx, id); err != nil {
			return err
		}
		_, err = appendEvent(ctx, scope, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvTerminating, eventPayload(env, ""))
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, TerminateJob{OrgID: string(org), EnvID: string(id)})
	})
}

func (s *Service) provider(ctx context.Context, scope uc.OrgScope, env uc.DevEnv) (uc.EnvProvider, uc.ProviderInstance, error) {
	instance, err := scope.Providers().Get(ctx, env.ProviderInstanceID)
	if err != nil {
		return nil, instance, err
	}
	if s.Providers == nil {
		return nil, instance, fmt.Errorf("envwork: no provider registry is configured")
	}
	provider, err := s.Providers.Build(ctx, instance.Kind, instance.Config)
	if err != nil {
		return nil, instance, fmt.Errorf("envwork: build provider for %s: %w", instance.Name, err)
	}
	return provider, instance, nil
}

func (s *Service) clearToken(env uc.DevEnv) (string, error) {
	plain, err := s.Keyring.Decrypt(env.TokenEnc)
	if err != nil {
		return "", err
	}
	clear := string(plain)
	secrets.DefaultRedactor.Register(clear)
	return clear, nil
}

// ClearTokenForTools decrypts an environment token at the point of MCP use.
func (s *Service) ClearTokenForTools(env uc.DevEnv) (string, error) { return s.clearToken(env) }

// acquire obtains the provider resource for an environment. A provision job
// that is redelivered after the control plane died between resource creation
// and handle persistence adopts the existing resource instead of creating a
// duplicate. Providers that cannot enumerate their resources fall back to
// plain provisioning.
func (s *Service) acquire(ctx context.Context, provider uc.EnvProvider, env uc.DevEnv, clearToken string) (uc.ProviderHandle, error) {
	if env.Handle.Version > 0 {
		// A handle is already persisted: the previous attempt got at
		// least that far, so never create a second resource.
		return env.Handle, nil
	}
	if adopter, ok := provider.(uc.EnvAdopter); ok {
		handle, found, err := adopter.Adopt(ctx, env.ID)
		if err != nil {
			return uc.ProviderHandle{}, err
		}
		if found {
			if s.Log != nil {
				s.Log.Info("envwork: adopted interrupted provisioning", "env_id", string(env.ID))
			}
			return handle, nil
		}
	}
	return provider.Provision(ctx, env.ID, env.Spec, clearToken)
}

// RequestRestart marks an environment for restart and enqueues the work. The
// restart preserves the workspace, mints a new bearer token, and increments
// the token epoch so cached tool clients are invalidated.
func (s *Service) RequestRestart(ctx context.Context, org uc.OrgID, id uc.EnvID) (int64, error) {
	var seq int64
	err := s.Store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		env, err := scope.Envs().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if env.State.Terminal() || env.State == uc.EnvTerminating {
			return fmt.Errorf("envwork: environment is %s and cannot restart", env.State)
		}
		if err := scope.Envs().SetProvisioning(ctx, id); err != nil {
			return err
		}
		env.State = uc.EnvProvisioning
		seq, err = appendEvent(ctx, scope, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvProvisioning, eventPayload(env, "restart requested"))
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, RestartJob{OrgID: string(org), EnvID: string(id)})
	})
	return seq, err
}

// Restart implements jobqueue.Worker[RestartJob]. It rotates the environment
// token first, so a crash after the provider swap can never leave the stored
// token able to authenticate against a container that no longer honors it:
// the rotation and the provider restart use the same new secret, and the
// epoch bump invalidates every cached client.
func (s *Service) Restart(ctx context.Context, job RestartJob) error {
	org, id := uc.OrgID(job.OrgID), uc.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State.Terminal() {
		return nil
	}
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		return s.fail(ctx, org, env, err.Error())
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
	if err := scope.Envs().RotateToken(ctx, id, hash, enc); err != nil {
		return err
	}
	// Revoke cached clients before the provider swap so no in-flight caller
	// can reach the environment with the rotated-away token.
	s.InvalidateToolClients(id)
	handle, err := provider.Restart(ctx, id, env.Handle, env.Spec, clear)
	if err != nil {
		return s.fail(ctx, org, env, "environment restart failed")
	}
	endpoint, err := s.awaitHealthy(ctx, provider, handle)
	if err != nil {
		_ = provider.Terminate(ctx, handle)
		return s.fail(ctx, org, env, "environment did not become healthy after restart")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State.Terminal() {
			return nil
		}
		if e = sc.Envs().SetReady(ctx, id, handle, endpoint); e != nil {
			return e
		}
		locked.Handle = handle
		locked.Endpoint = endpoint
		locked.State = uc.EnvReady
		_, e = appendEvent(ctx, sc, locked.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvReady, eventPayload(locked, "restarted"))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterRestart(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// awaitHealthy polls until the provider reports ready and Bezalel answers its
// health endpoint, or the provisioning deadline elapses.
func (s *Service) awaitHealthy(ctx context.Context, provider uc.EnvProvider, handle uc.ProviderHandle) (string, error) {
	deadline := time.Now().Add(s.provisionTimeout())
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, handle)
		if err == nil && status.State == uc.EnvReady {
			endpoint, err := provider.Endpoint(ctx, handle)
			if err == nil && mcp.Healthy(ctx, endpoint) == nil {
				return endpoint, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", errors.New("envwork: environment did not become healthy")
}

// Provision implements jobqueue.Worker[ProvisionJob].
func (s *Service) Provision(ctx context.Context, job ProvisionJob) error {
	org, id := uc.OrgID(job.OrgID), uc.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State == uc.EnvReady || env.State.Terminal() {
		return nil
	}
	if err := scope.Envs().SetProvisioning(ctx, id); err != nil {
		return err
	}
	_, _ = appendEvent(ctx, scope, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvProvisioning, eventPayload(env, ""))
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		return s.fail(ctx, org, env, err.Error())
	}
	clear, err := s.clearToken(env)
	if err != nil {
		return err
	}
	handle, err := s.acquire(ctx, provider, env, clear)
	if err != nil {
		return s.fail(ctx, org, env, "environment provisioning failed")
	}
	// Persist the handle before waiting for health so a control-plane death
	// during the wait cannot orphan the resource: the retry adopts it.
	if err := scope.Envs().SetHandle(ctx, id, handle); err != nil {
		return err
	}
	endpoint, err := s.awaitHealthy(ctx, provider, handle)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		_ = provider.Terminate(ctx, handle)
		return s.fail(ctx, org, env, "environment did not become healthy")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != uc.EnvProvisioning {
			return nil
		}
		if e = sc.Envs().SetReady(ctx, id, handle, endpoint); e != nil {
			return e
		}
		locked.Handle = handle
		locked.Endpoint = endpoint
		locked.State = uc.EnvReady
		_, e = appendEvent(ctx, sc, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvReady, eventPayload(locked, ""))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterProvision(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// suspend parks an environment whose host is unreachable.
func (s *Service) suspend(ctx context.Context, org uc.OrgID, env uc.DevEnv, message string) error {
	// Cached tool clients must not keep pointing at a host that is gone; the
	// next tool call has to observe the suspension.
	s.InvalidateToolClients(env.ID)
	if message == "" {
		message = "the environment host is unreachable"
	}
	err := s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, env.ID)
		if e != nil {
			return e
		}
		if locked.State != uc.EnvReady {
			return nil
		}
		if e = sc.Envs().SetSuspended(ctx, env.ID, message); e != nil {
			return e
		}
		env.State = uc.EnvSuspended
		_, e = appendEvent(ctx, sc, env.SessionID, uc.Actor{Type: uc.ActorSystem},
			uc.EventKindEnvSuspended, eventPayload(env, message))
		return e
	})
	if err != nil {
		return err
	}
	return s.rearmReconcile(ctx, org, env.ID)
}

// reconcileSuspended asks whether a suspended environment's host has come
// back. Recovery is a transition to ready, not a new
// provisioning: the workspace was never destroyed.
func (s *Service) reconcileSuspended(ctx context.Context, org uc.OrgID, env uc.DevEnv) error {
	scope := s.Store.Org(org)
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		// The provider registration is unusable; keep checking rather than
		// destroying an environment whose workspace is intact.
		return s.rearmReconcile(ctx, org, env.ID)
	}
	status, err := provider.Status(ctx, env.Handle)
	if err != nil || status.State != uc.EnvReady || mcp.Healthy(ctx, env.Endpoint) != nil {
		if err == nil && status.State.Terminal() {
			// The host came back and told us the resource is really gone.
			return s.fail(ctx, org, env, "environment resource is no longer healthy")
		}
		return s.rearmReconcile(ctx, org, env.ID)
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, env.ID)
		if e != nil {
			return e
		}
		if locked.State != uc.EnvSuspended {
			return nil
		}
		if e = sc.Envs().SetReady(ctx, env.ID, locked.Handle, locked.Endpoint); e != nil {
			return e
		}
		locked.State = uc.EnvReady
		if _, e = appendEvent(ctx, sc, env.SessionID, uc.Actor{Type: uc.ActorSystem},
			uc.EventKindEnvReady, eventPayload(locked, "resumed")); e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), EnvID: string(env.ID)},
			jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// rearmReconcile keeps an environment under observation without changing it.
func (s *Service) rearmReconcile(ctx context.Context, org uc.OrgID, id uc.EnvID) error {
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), EnvID: string(id)},
			jobqueue.WithScheduledAt(time.Now().Add(s.interval())))
	})
}

func (s *Service) fail(ctx context.Context, org uc.OrgID, env uc.DevEnv, message string) error {
	// A failed environment must not keep serving cached tool clients: the
	// next tool call has to observe the failure, not a stale connection.
	s.InvalidateToolClients(env.ID)
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		if err := sc.Envs().SetFailed(ctx, env.ID, message); err != nil {
			return err
		}
		_, err := appendEvent(ctx, sc, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvFailed, eventPayload(env, message))
		return err
	})
}

// Terminate implements jobqueue.Worker[TerminateJob].
func (s *Service) Terminate(ctx context.Context, job TerminateJob) error {
	org, id := uc.OrgID(job.OrgID), uc.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State == uc.EnvTerminated {
		return nil
	}
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		return err
	}
	s.InvalidateToolClients(id)
	if env.Handle.Version > 0 {
		if err := provider.Terminate(ctx, env.Handle); err != nil {
			return err
		}
	}
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		if err := sc.Envs().SetTerminated(ctx, id); err != nil {
			return err
		}
		_, err := appendEvent(ctx, sc, env.SessionID, uc.Actor{Type: uc.ActorSystem}, uc.EventKindEnvTerminated, eventPayload(env, ""))
		return err
	})
}

// Reconcile implements jobqueue.Worker[ReconcileJob] and self-reschedules.
// It is the only writer allowed to move ready → failed, and it is also the
// watchdog for provisioning that was interrupted by a control-plane death:
// a stalled requested/provisioning environment gets its provision job
// re-enqueued, which adopts any already-created resource.
func (s *Service) Reconcile(ctx context.Context, job ReconcileJob) error {
	org, id := uc.OrgID(job.OrgID), uc.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if errors.Is(err, uc.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if env.State == uc.EnvRequested || env.State == uc.EnvProvisioning {
		return s.recoverProvisioning(ctx, org, env)
	}
	if env.State == uc.EnvSuspended {
		// A suspended environment is still owned and still reconciled: its
		// host is expected back. Keep checking until it returns or is
		// terminated.
		return s.reconcileSuspended(ctx, org, env)
	}
	if env.State != uc.EnvReady {
		return nil
	}
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		return err
	}
	status, err := provider.Status(ctx, env.Handle)
	// A provider that reports suspension is describing a host that went away
	// with the workspace intact, which is not the same as a resource that
	// broke. Failing it would tell every other surface the work was destroyed.
	if err == nil && status.State == uc.EnvSuspended {
		return s.suspend(ctx, org, env, status.Message)
	}
	healthy := err == nil && status.State == uc.EnvReady && mcp.Healthy(ctx, env.Endpoint) == nil
	if !healthy {
		return s.fail(ctx, org, env, "environment resource is no longer healthy")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != uc.EnvReady {
			return nil
		}
		_ = sc.Providers().MarkHealthy(ctx, env.ProviderInstanceID)
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: job.OrgID, EnvID: job.EnvID}, jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// recoverProvisioning is the watchdog for an environment whose provisioning
// was interrupted. While provisioning is still within its deadline it only
// re-arms the watchdog. Past the deadline it re-enqueues the provision job,
// which adopts any already-created resource rather than creating a second
// one. An environment that cannot be provisioned within ten deadlines is
// failed so it cannot be retried forever.
func (s *Service) recoverProvisioning(ctx context.Context, org uc.OrgID, env uc.DevEnv) error {
	timeout := s.provisionTimeout()
	stalled := time.Since(env.UpdatedAt)
	if stalled < timeout {
		return s.Store.Tx(ctx, func(txs uc.Store) error {
			return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), EnvID: string(env.ID)},
				jobqueue.WithScheduledAt(time.Now().Add(timeout)))
		})
	}
	if stalled > 10*timeout {
		return s.fail(ctx, org, env, "environment provisioning did not converge")
	}
	if s.Log != nil {
		s.Log.Warn("envwork: re-driving interrupted provisioning",
			"env_id", string(env.ID), "stalled_for", stalled.String())
	}
	return s.Store.Tx(ctx, func(txs uc.Store) error {
		sc := txs.Org(org)
		locked, err := sc.Envs().GetForUpdate(ctx, env.ID)
		if err != nil {
			return err
		}
		if locked.State != uc.EnvRequested && locked.State != uc.EnvProvisioning {
			return nil
		}
		if err := s.Enqueue.EnqueueInTx(ctx, txs, ProvisionJob{OrgID: string(org), EnvID: string(env.ID)}); err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: string(org), EnvID: string(env.ID)},
			jobqueue.WithScheduledAt(time.Now().Add(timeout)))
	})
}

// ToolClient returns an MCP client for a ready environment, reusing the
// cached client for the environment's current token epoch. A restart bumps
// the epoch, which revokes the previous client so no caller can keep using
// the rotated-away token.
func (s *Service) ToolClient(ctx context.Context, env uc.DevEnv) (*mcp.Client, error) {
	clear, err := s.clearToken(env)
	if err != nil {
		return nil, err
	}
	client, err := s.clients().Client(string(env.ID), env.Epoch, env.Endpoint, clear)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// InvalidateToolClients drops any cached client for an environment.
func (s *Service) InvalidateToolClients(id uc.EnvID) { s.clients().Invalidate(string(id)) }

// RememberTools records the tools an environment offered, so a later step can
// still name them once the environment has become unreachable.
func (s *Service) RememberTools(id uc.EnvID, names []string) {
	s.clients().RememberTools(string(id), names)
}

// LastTools returns the tools an environment last offered.
func (s *Service) LastTools(id uc.EnvID) []string { return s.clients().LastTools(string(id)) }

func (s *Service) clients() *mcp.Cache {
	s.cacheOnce.Do(func() {
		if s.Clients == nil {
			s.Clients = mcp.NewCache()
		}
	})
	return s.Clients
}

// Exec runs a real Bezalel tool against a ready environment.
func (s *Service) Exec(ctx context.Context, org uc.OrgID, id uc.EnvID, name string, args json.RawMessage) (mcp.Result, error) {
	env, err := s.Store.Org(org).Envs().Get(ctx, id)
	if err != nil {
		return mcp.Result{}, err
	}
	if env.State != uc.EnvReady {
		return mcp.Result{}, errors.New("environment is not ready")
	}
	client, err := s.ToolClient(ctx, env)
	if err != nil {
		return mcp.Result{}, err
	}
	return client.Call(ctx, name, args)
}
