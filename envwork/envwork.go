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

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/mcp"
	"github.com/aleksclark/ultralogical/secrets"
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

// Registry maps provider-instance kinds to concrete providers.
type Registry map[string]ultra.EnvProvider

// Service creates env records and implements lifecycle workers.
type Service struct {
	Store             ultra.Store
	Enqueue           jobqueue.TxEnqueuer
	Keyring           secrets.Keyring
	Providers         Registry
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

func eventPayload(env ultra.DevEnv, message string) ultra.EnvEventPayload {
	return ultra.EnvEventPayload{EnvID: env.ID, Name: env.Spec.Name, ProviderInstanceID: env.ProviderInstanceID, Endpoint: env.Endpoint, Message: message, Epoch: env.Epoch}
}

func appendEvent(ctx context.Context, scope ultra.OrgScope, session ultra.SessionID, actor ultra.Actor, kind string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	return scope.Events().Append(ctx, session, ultra.Event{Actor: actor, Kind: kind, Payload: b})
}

// Request atomically creates a requested environment, emits EnvRequested,
// and enqueues provisioning. providerName defaults to "default".
func (s *Service) Request(ctx context.Context, org ultra.OrgID, session ultra.SessionID, spec ultra.EnvSpec, providerName string, createdBy *ultra.RunID) (ultra.DevEnv, int64, error) {
	if providerName == "" {
		providerName = "default"
	}
	instance, err := s.Store.Org(org).Providers().GetByName(ctx, providerName)
	if err != nil {
		return ultra.DevEnv{}, 0, err
	}
	clear, hash, err := token()
	if err != nil {
		return ultra.DevEnv{}, 0, err
	}
	enc, err := s.Keyring.Encrypt([]byte(clear))
	if err != nil {
		return ultra.DevEnv{}, 0, err
	}
	secrets.DefaultRedactor.Register(clear)
	env := ultra.DevEnv{ID: ultra.EnvID(uuid.NewString()), OrgID: org, SessionID: session, ProviderInstanceID: instance.ID, State: ultra.EnvRequested, Spec: spec, TokenHash: hash, TokenEnc: enc, Epoch: 1, CreatedByRunID: createdBy}
	var seq int64
	err = s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		if err := scope.Envs().Create(ctx, env); err != nil {
			return err
		}
		seq, err = appendEvent(ctx, scope, session, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvRequested, eventPayload(env, ""))
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
func (s *Service) RequestTerminate(ctx context.Context, org ultra.OrgID, id ultra.EnvID) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		env, err := scope.Envs().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if env.State == ultra.EnvTerminated {
			return nil
		}
		if err := scope.Envs().SetTerminating(ctx, id); err != nil {
			return err
		}
		_, err = appendEvent(ctx, scope, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvTerminating, eventPayload(env, ""))
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, TerminateJob{OrgID: string(org), EnvID: string(id)})
	})
}

func (s *Service) provider(ctx context.Context, scope ultra.OrgScope, env ultra.DevEnv) (ultra.EnvProvider, ultra.ProviderInstance, error) {
	instance, err := scope.Providers().Get(ctx, env.ProviderInstanceID)
	if err != nil {
		return nil, instance, err
	}
	provider := s.Providers[instance.Kind]
	if provider == nil {
		return nil, instance, fmt.Errorf("envwork: provider kind %q disabled", instance.Kind)
	}
	return provider, instance, nil
}

func (s *Service) clearToken(env ultra.DevEnv) (string, error) {
	plain, err := s.Keyring.Decrypt(env.TokenEnc)
	if err != nil {
		return "", err
	}
	clear := string(plain)
	secrets.DefaultRedactor.Register(clear)
	return clear, nil
}

// ClearTokenForTools decrypts an environment token at the point of MCP use.
func (s *Service) ClearTokenForTools(env ultra.DevEnv) (string, error) { return s.clearToken(env) }

// acquire obtains the provider resource for an environment. A provision job
// that is redelivered after the control plane died between resource creation
// and handle persistence adopts the existing resource instead of creating a
// duplicate. Providers that cannot enumerate their resources fall back to
// plain provisioning.
func (s *Service) acquire(ctx context.Context, provider ultra.EnvProvider, env ultra.DevEnv, clearToken string) (ultra.ProviderHandle, error) {
	if env.Handle.Version > 0 {
		// A handle is already persisted: the previous attempt got at
		// least that far, so never create a second resource.
		return env.Handle, nil
	}
	if adopter, ok := provider.(ultra.EnvAdopter); ok {
		handle, found, err := adopter.Adopt(ctx, env.ID)
		if err != nil {
			return ultra.ProviderHandle{}, err
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
func (s *Service) RequestRestart(ctx context.Context, org ultra.OrgID, id ultra.EnvID) (int64, error) {
	var seq int64
	err := s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		env, err := scope.Envs().GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if env.State.Terminal() || env.State == ultra.EnvTerminating {
			return fmt.Errorf("envwork: environment is %s and cannot restart", env.State)
		}
		if err := scope.Envs().SetProvisioning(ctx, id); err != nil {
			return err
		}
		env.State = ultra.EnvProvisioning
		seq, err = appendEvent(ctx, scope, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvProvisioning, eventPayload(env, "restart requested"))
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
	org, id := ultra.OrgID(job.OrgID), ultra.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State.Terminal() {
		return nil
	}
	provider, instance, err := s.provider(ctx, scope, env)
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
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
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
		locked.State = ultra.EnvReady
		// Restart keeps the environment billable: reopen only when the
		// prior interval was closed by a failure.
		if e = sc.Usage().Open(ctx, ultra.EnvUsage{ID: uuid.NewString(), OrgID: org, EnvID: id, ProviderInstanceID: instance.ID, StartedAt: now, RateClass: instance.RateClass}); e != nil {
			return e
		}
		_, e = appendEvent(ctx, sc, locked.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvReady, eventPayload(locked, "restarted"))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterRestart(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

// awaitHealthy polls until the provider reports ready and Bezalel answers its
// health endpoint, or the provisioning deadline elapses.
func (s *Service) awaitHealthy(ctx context.Context, provider ultra.EnvProvider, handle ultra.ProviderHandle) (string, error) {
	deadline := time.Now().Add(s.provisionTimeout())
	for time.Now().Before(deadline) {
		status, err := provider.Status(ctx, handle)
		if err == nil && status.State == ultra.EnvReady {
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
	org, id := ultra.OrgID(job.OrgID), ultra.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State == ultra.EnvReady || env.State.Terminal() {
		return nil
	}
	if err := scope.Envs().SetProvisioning(ctx, id); err != nil {
		return err
	}
	_, _ = appendEvent(ctx, scope, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvProvisioning, eventPayload(env, ""))
	provider, instance, err := s.provider(ctx, scope, env)
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
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != ultra.EnvProvisioning {
			return nil
		}
		if e = sc.Envs().SetReady(ctx, id, handle, endpoint); e != nil {
			return e
		}
		locked.Handle = handle
		locked.Endpoint = endpoint
		locked.State = ultra.EnvReady
		if e = sc.Usage().Open(ctx, ultra.EnvUsage{ID: uuid.NewString(), OrgID: org, EnvID: id, ProviderInstanceID: instance.ID, StartedAt: now, RateClass: instance.RateClass}); e != nil {
			return e
		}
		_, e = appendEvent(ctx, sc, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvReady, eventPayload(locked, ""))
		if e != nil {
			return e
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, reconcileAfterProvision(job), jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

func (s *Service) fail(ctx context.Context, org ultra.OrgID, env ultra.DevEnv, message string) error {
	// A failed environment must not keep serving cached tool clients: the
	// next tool call has to observe the failure, not a stale connection.
	s.InvalidateToolClients(env.ID)
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		sc := txs.Org(org)
		if err := sc.Envs().SetFailed(ctx, env.ID, message); err != nil {
			return err
		}
		_ = sc.Usage().Close(ctx, env.ID, time.Now())
		_, err := appendEvent(ctx, sc, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvFailed, eventPayload(env, message))
		return err
	})
}

// Terminate implements jobqueue.Worker[TerminateJob].
func (s *Service) Terminate(ctx context.Context, job TerminateJob) error {
	org, id := ultra.OrgID(job.OrgID), ultra.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if err != nil {
		return err
	}
	if env.State == ultra.EnvTerminated {
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
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		sc := txs.Org(org)
		if err := sc.Envs().SetTerminated(ctx, id); err != nil {
			return err
		}
		_ = sc.Usage().Close(ctx, id, time.Now())
		_, err := appendEvent(ctx, sc, env.SessionID, ultra.Actor{Type: ultra.ActorSystem}, ultra.EventKindEnvTerminated, eventPayload(env, ""))
		return err
	})
}

// Reconcile implements jobqueue.Worker[ReconcileJob] and self-reschedules.
// It is the only writer allowed to move ready → failed, and it is also the
// watchdog for provisioning that was interrupted by a control-plane death:
// a stalled requested/provisioning environment gets its provision job
// re-enqueued, which adopts any already-created resource.
func (s *Service) Reconcile(ctx context.Context, job ReconcileJob) error {
	org, id := ultra.OrgID(job.OrgID), ultra.EnvID(job.EnvID)
	scope := s.Store.Org(org)
	env, err := scope.Envs().Get(ctx, id)
	if errors.Is(err, ultra.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if env.State == ultra.EnvRequested || env.State == ultra.EnvProvisioning {
		return s.recoverProvisioning(ctx, org, env)
	}
	if env.State != ultra.EnvReady {
		// The environment is terminal or terminating. If a control-plane
		// death left its metering interval open, close it at the persisted
		// heartbeat: never bill for the window the control plane was dead.
		return scope.Usage().CloseAtWatermark(ctx, id)
	}
	provider, _, err := s.provider(ctx, scope, env)
	if err != nil {
		return err
	}
	status, err := provider.Status(ctx, env.Handle)
	healthy := err == nil && status.State == ultra.EnvReady && mcp.Healthy(ctx, env.Endpoint) == nil
	if !healthy {
		return s.fail(ctx, org, env, "environment resource is no longer healthy")
	}
	now := time.Now()
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		sc := txs.Org(org)
		locked, e := sc.Envs().GetForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if locked.State != ultra.EnvReady {
			return nil
		}
		if e = sc.Usage().Tick(ctx, id, now); e != nil {
			return e
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
func (s *Service) recoverProvisioning(ctx context.Context, org ultra.OrgID, env ultra.DevEnv) error {
	timeout := s.provisionTimeout()
	stalled := time.Since(env.UpdatedAt)
	if stalled < timeout {
		return s.Store.Tx(ctx, func(txs ultra.Store) error {
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
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		sc := txs.Org(org)
		locked, err := sc.Envs().GetForUpdate(ctx, env.ID)
		if err != nil {
			return err
		}
		if locked.State != ultra.EnvRequested && locked.State != ultra.EnvProvisioning {
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
func (s *Service) ToolClient(ctx context.Context, env ultra.DevEnv) (*mcp.Client, error) {
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
func (s *Service) InvalidateToolClients(id ultra.EnvID) { s.clients().Invalidate(string(id)) }

func (s *Service) clients() *mcp.Cache {
	s.cacheOnce.Do(func() {
		if s.Clients == nil {
			s.Clients = mcp.NewCache()
		}
	})
	return s.Clients
}

// Exec runs a real Bezalel tool against a ready environment.
func (s *Service) Exec(ctx context.Context, org ultra.OrgID, id ultra.EnvID, name string, args json.RawMessage) (mcp.Result, error) {
	env, err := s.Store.Org(org).Envs().Get(ctx, id)
	if err != nil {
		return mcp.Result{}, err
	}
	if env.State != ultra.EnvReady {
		return mcp.Result{}, errors.New("environment is not ready")
	}
	client, err := s.ToolClient(ctx, env)
	if err != nil {
		return mcp.Result{}, err
	}
	return client.Call(ctx, name, args)
}
