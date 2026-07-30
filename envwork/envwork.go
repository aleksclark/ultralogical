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
	return ultra.EnvEventPayload{EnvID: env.ID, Name: env.Spec.Name, ProviderInstanceID: env.ProviderInstanceID, Endpoint: env.Endpoint, Message: message}
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
		return s.Enqueue.EnqueueInTx(ctx, txs, ProvisionJob{OrgID: string(org), EnvID: string(env.ID)})
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
	handle, err := provider.Provision(ctx, id, env.Spec, clear)
	if err != nil {
		return s.fail(ctx, org, env, "environment provisioning failed")
	}
	deadline := time.Now().Add(s.provisionTimeout())
	var endpoint string
	for time.Now().Before(deadline) {
		status, e := provider.Status(ctx, handle)
		if e == nil && status.State == ultra.EnvReady {
			endpoint, e = provider.Endpoint(ctx, handle)
			if e == nil && mcp.Healthy(ctx, endpoint) == nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if endpoint == "" {
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
		return s.Enqueue.EnqueueInTx(ctx, txs, ReconcileJob{OrgID: job.OrgID, EnvID: job.EnvID}, jobqueue.WithScheduledAt(now.Add(s.interval())))
	})
}

func (s *Service) fail(ctx context.Context, org ultra.OrgID, env ultra.DevEnv, message string) error {
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
	if env.State != ultra.EnvReady {
		return nil
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

// Exec runs a real Bezalel tool against a ready environment.
func (s *Service) Exec(ctx context.Context, org ultra.OrgID, id ultra.EnvID, name string, args json.RawMessage) (mcp.Result, error) {
	env, err := s.Store.Org(org).Envs().Get(ctx, id)
	if err != nil {
		return mcp.Result{}, err
	}
	if env.State != ultra.EnvReady {
		return mcp.Result{}, errors.New("environment is not ready")
	}
	clear, err := s.clearToken(env)
	if err != nil {
		return mcp.Result{}, err
	}
	client := mcp.NewClient(env.Endpoint, clear)
	if err := client.Initialize(ctx); err != nil {
		return mcp.Result{}, err
	}
	return client.Call(ctx, name, args)
}
