// Package flowwork orchestrates flow invocations as a durable state machine.
//
// Everything an invocation does — provisioning declared environments, waiting
// for readiness, launching the declared topology, converging on failure,
// cleaning up what it owns, and honoring cancellation — is driven by a
// self-rescheduling queue job reading persisted state. No step depends on the
// process that started the invocation still being alive, and every step is
// keyed so redelivery repeats nothing.
package flowwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envwork"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/loop"
)

// AdvanceJob drives one invocation one step forward. It is enqueued with the
// invocation and re-armed after every advance until the invocation is
// terminal, so an invocation is never waiting on a timer held in memory.
type AdvanceJob struct {
	OrgID        string `json:"org_id"`
	InvocationID string `json:"invocation_id"`
}

// Kind implements jobqueue.Job.
func (AdvanceJob) Kind() string { return "flow.advance" }

// DefaultAdvanceInterval is how often an active invocation is re-examined.
const DefaultAdvanceInterval = 500 * time.Millisecond

// DefaultReadinessTimeout bounds an environment's readiness gate when the flow
// declares no timeout of its own. An invocation must converge: waiting forever
// on an environment that will never be ready is not a state, it is a leak.
const DefaultReadinessTimeout = 5 * time.Minute

// DefaultInvocationTimeout is the outer bound on any single invocation. Every
// stage has its own deadline, but a stage that keeps erroring transiently would
// otherwise retry forever. This guarantees an invocation always converges and
// always releases what it owns.
const DefaultInvocationTimeout = time.Hour

// cohortNamespace derives stable cohort ids from invocation stages, so a
// redelivered advance re-derives the same cohort instead of inventing one.
var cohortNamespace = uuid.MustParse("9d5f4b28-1c6e-4a70-9f3c-2b8e7a1d4c05")

// Service creates and advances flow invocations.
type Service struct {
	Store   ultra.Store
	Enqueue jobqueue.TxEnqueuer
	// Envs provisions and terminates the environments a flow declares.
	Envs *envwork.Service
	Log  *slog.Logger
	// DefaultModel fills agents that declare no model of their own.
	DefaultModel ultra.ModelConfig
	// AdvanceInterval overrides DefaultAdvanceInterval.
	AdvanceInterval time.Duration
	// ReadinessTimeout overrides DefaultReadinessTimeout.
	ReadinessTimeout time.Duration
	// InvocationTimeout overrides DefaultInvocationTimeout.
	InvocationTimeout time.Duration
}

func (s *Service) interval() time.Duration {
	if s.AdvanceInterval > 0 {
		return s.AdvanceInterval
	}
	return DefaultAdvanceInterval
}

func (s *Service) readinessTimeout() time.Duration {
	if s.ReadinessTimeout > 0 {
		return s.ReadinessTimeout
	}
	return DefaultReadinessTimeout
}

func (s *Service) invocationTimeout() time.Duration {
	if s.InvocationTimeout > 0 {
		return s.InvocationTimeout
	}
	return DefaultInvocationTimeout
}

// Invoke validates, renders, and durably accepts one invocation of a flow
// version. The invocation row, its FlowInvoked event, and its first advance
// job commit together: an accepted invocation always has work scheduled, and a
// rejected one leaves nothing behind.
func (s *Service) Invoke(ctx context.Context, org ultra.OrgID, session ultra.SessionID,
	flow ultra.Flow, supplied map[string]any) (ultra.FlowInvocation, int64, *ultra.FlowValidationError, error) {
	def, verr := ultra.ValidateFlowDefinition(flow.Definition)
	if verr != nil {
		return ultra.FlowInvocation{}, 0, verr, nil
	}
	rendered, verr := ultra.RenderFlow(def, supplied)
	if verr != nil {
		return ultra.FlowInvocation{}, 0, verr, nil
	}
	// Provider instances are late-bound by name so a flow is portable across
	// orgs; the binding is checked here, before anything is persisted.
	if verr := s.checkProviders(ctx, org, rendered); verr != nil {
		return ultra.FlowInvocation{}, 0, verr, nil
	}
	renderedJSON, err := json.Marshal(rendered)
	if err != nil {
		return ultra.FlowInvocation{}, 0, nil, err
	}
	paramsJSON, err := json.Marshal(rendered.Params)
	if err != nil {
		return ultra.FlowInvocation{}, 0, nil, err
	}
	now := time.Now()
	inv := ultra.FlowInvocation{
		ID: ultra.FlowInvocationID(uuid.NewString()), OrgID: org, SessionID: session,
		FlowID: flow.ID, FlowName: flow.Name, FlowVersion: flow.Version,
		Params: paramsJSON, Rendered: renderedJSON,
		State: ultra.FlowInvocationPending, AdvanceAt: &now,
	}
	var seq int64
	err = s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		if err := scope.Flows().CreateInvocation(ctx, inv); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.FlowInvokedPayload{
			InvocationID: inv.ID, FlowID: flow.ID, FlowName: flow.Name,
			FlowVersion: flow.Version, ParamsJSON: string(paramsJSON),
		})
		if err != nil {
			return err
		}
		seq, err = scope.Events().Append(ctx, session, ultra.Event{
			Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindFlowInvoked, Payload: payload,
		})
		if err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, AdvanceJob{OrgID: string(org), InvocationID: string(inv.ID)})
	})
	if err != nil {
		return ultra.FlowInvocation{}, 0, nil, err
	}
	return inv, seq, nil, nil
}

// checkProviders rejects an environment whose provider instance the org has
// not registered, or whose provider cannot do what the declaration needs.
func (s *Service) checkProviders(ctx context.Context, org ultra.OrgID, rendered ultra.RenderedFlow) *ultra.FlowValidationError {
	var errs []ultra.FlowFieldError
	for _, env := range rendered.Envs {
		instance, err := s.Store.Org(org).Providers().GetByName(ctx, env.ProviderInstance)
		if err != nil {
			errs = append(errs, ultra.FlowFieldError{
				Path: "envs." + env.Name + ".provider_instance", Code: ultra.FlowErrUnknownProvider,
				Message: fmt.Sprintf("org has no provider instance named %q", env.ProviderInstance),
			})
			continue
		}
		// Health readiness and setup commands both require environments that
		// serve the tool endpoint. Whether this registration can is answered
		// by what its control plane reported when it was probed, not by its
		// kind: two clusters registered under the same kind can differ, and a
		// flow must be refused against the one that genuinely cannot rather
		// than hanging on a gate that can never open.
		if env.Readiness == ultra.FlowReadinessHealth &&
			!instance.Capabilities.Has(ultra.CapabilityServesToolEndpoint) {
			errs = append(errs, ultra.FlowFieldError{
				Path: "envs." + env.Name + ".readiness", Code: ultra.FlowErrProviderMismatch,
				Message: fmt.Sprintf("provider %q cannot serve environment health checks: %s",
					instance.Name, instance.Capabilities.Reason(ultra.CapabilityServesToolEndpoint)),
			})
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ultra.FlowValidationError{Errors: errs}
}

// RequestCancel asks an invocation to converge on cancelled. It is idempotent
// and never blocks on the work being cancelled: the advance loop performs the
// cleanup, so cancellation survives the death of the caller's process.
func (s *Service) RequestCancel(ctx context.Context, org ultra.OrgID, id ultra.FlowInvocationID) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		inv, err := scope.Flows().GetInvocationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if inv.State.Terminal() {
			return nil
		}
		if err := scope.Flows().RequestInvocationCancel(ctx, id); err != nil {
			return err
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, AdvanceJob{OrgID: string(org), InvocationID: string(id)})
	})
}

// Advance implements jobqueue.Worker[AdvanceJob]. One delivery performs at most
// one stage transition and then re-arms itself, so progress is a sequence of
// small committed steps rather than one long-lived process.
func (s *Service) Advance(ctx context.Context, job AdvanceJob) error {
	org := ultra.OrgID(job.OrgID)
	id := ultra.FlowInvocationID(job.InvocationID)
	scope := s.Store.Org(org)

	inv, err := scope.Flows().GetInvocation(ctx, id)
	if errors.Is(err, ultra.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if inv.State.Terminal() {
		return nil
	}
	// Claiming the tick is what keeps redelivery from multiplying advance
	// chains: exactly one worker owns an invocation at a time, and a worker
	// that dies loses the claim when its watermark comes due again.
	claimed, err := scope.Flows().ClaimAdvance(ctx, id, time.Now().Add(s.interval()))
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	// An invocation past its outer deadline converges rather than retrying
	// indefinitely: a stage that keeps failing transiently would otherwise
	// hold its environments open forever.
	if time.Since(inv.CreatedAt) > s.invocationTimeout() {
		if err := s.cleanup(ctx, org, inv); err != nil {
			return err
		}
		return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
			ultra.FlowTerminalTimedOut, "the invocation did not converge in time")
	}

	rendered, decodeErr := decodeRendered(inv)
	switch {
	case decodeErr != nil:
		// The rendering persisted with the invocation cannot be executed.
		// Converge rather than retrying something unexecutable — and still
		// release what the invocation owns, which is known from the rows
		// themselves rather than from the unreadable rendering.
		if err := s.cleanup(ctx, org, inv); err != nil {
			return err
		}
		return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
			ultra.FlowTerminalInvalidDefinition, "flow definition could not be executed")
	case inv.CancelRequestedAt != nil:
		return s.cancel(ctx, org, inv, rendered)
	}

	switch inv.State {
	case ultra.FlowInvocationCancelling:
		return s.cancel(ctx, org, inv, rendered)
	case ultra.FlowInvocationPending:
		err = s.provision(ctx, org, inv, rendered)
	case ultra.FlowInvocationProvisioning:
		err = s.awaitReadiness(ctx, org, inv, rendered)
	case ultra.FlowInvocationRunning:
		err = s.runTopology(ctx, org, inv, rendered)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	return s.rearm(ctx, org, id)
}

func decodeRendered(inv ultra.FlowInvocation) (ultra.RenderedFlow, error) {
	var rendered ultra.RenderedFlow
	if err := json.Unmarshal(inv.Rendered, &rendered); err != nil {
		return rendered, err
	}
	if rendered.SchemaVersion != ultra.FlowDefinitionVersion {
		return rendered, fmt.Errorf("flowwork: unsupported rendered schema version %d", rendered.SchemaVersion)
	}
	if len(rendered.Agents) == 0 {
		return rendered, errors.New("flowwork: rendered flow declares no agents")
	}
	return rendered, nil
}

func (s *Service) rearm(ctx context.Context, org ultra.OrgID, id ultra.FlowInvocationID) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		inv, err := txs.Org(org).Flows().GetInvocation(ctx, id)
		if err != nil {
			return err
		}
		if inv.State.Terminal() {
			return nil
		}
		return s.Enqueue.EnqueueInTx(ctx, txs, AdvanceJob{OrgID: string(org), InvocationID: string(id)},
			jobqueue.WithScheduledAt(time.Now().Add(s.interval())))
	})
}

// record appends one keyed progress step and mirrors it into the session log.
// The key makes it idempotent, and the mirrored event is what lets a client
// reconstruct ordered progress from the log alone.
func (s *Service) record(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, stage, key, detail string) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		first, err := scope.Flows().AppendProgress(ctx, ultra.FlowInvocationProgress{
			InvocationID: inv.ID, Stage: stage, Key: key, Detail: detail,
		})
		if err != nil || !first {
			return err
		}
		payload, err := json.Marshal(ultra.FlowProgressedPayload{
			InvocationID: inv.ID, Stage: stage, Key: key, Detail: detail,
		})
		if err != nil {
			return err
		}
		_, err = scope.Events().Append(ctx, inv.SessionID, ultra.Event{
			Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindFlowProgressed, Payload: payload,
		})
		return err
	})
}

// converge writes the invocation's terminal state and its closing event
// together, so a client can never see a terminal row without its reason.
func (s *Service) converge(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation,
	state ultra.FlowInvocationState, reason, message string) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		locked, err := scope.Flows().GetInvocationForUpdate(ctx, inv.ID)
		if err != nil {
			return err
		}
		if locked.State.Terminal() {
			return nil
		}
		if err := scope.Flows().SetInvocationState(ctx, inv.ID, state, reason, message); err != nil {
			return err
		}
		if _, err := scope.Flows().AppendProgress(ctx, ultra.FlowInvocationProgress{
			InvocationID: inv.ID, Stage: ultra.FlowStageTerminal,
			Key: "terminal", Detail: reason,
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.FlowTerminalPayload{
			InvocationID: inv.ID, State: string(state), TerminalReason: reason, Message: message,
		})
		if err != nil {
			return err
		}
		_, err = scope.Events().Append(ctx, inv.SessionID, ultra.Event{
			Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindFlowTerminal, Payload: payload,
		})
		return err
	})
}

// provision creates every declared environment, exactly once each. A flow with
// no environments skips straight to running: there is nothing to gate on.
func (s *Service) provision(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, rendered ultra.RenderedFlow) error {
	if err := s.record(ctx, org, inv, ultra.FlowStageAccepted, "accepted",
		fmt.Sprintf("%s v%d", inv.FlowName, inv.FlowVersion)); err != nil {
		return err
	}
	if len(rendered.Envs) == 0 {
		return s.transition(ctx, org, inv, ultra.FlowInvocationRunning)
	}
	if err := s.record(ctx, org, inv, ultra.FlowStageProvisioning, "provisioning",
		fmt.Sprintf("%d environment(s)", len(rendered.Envs))); err != nil {
		return err
	}
	existing, err := s.envsByName(ctx, org, inv.ID)
	if err != nil {
		return err
	}
	invocationID := inv.ID
	for _, env := range rendered.Envs {
		if _, ok := existing[env.Name]; ok {
			// A retried provisioning adopts the environment it already
			// created: the unique (invocation, declaration) index makes "one
			// environment per declaration" a database fact.
			continue
		}
		created, _, err := s.Envs.RequestWith(ctx, org, inv.SessionID, env.Spec,
			env.ProviderInstance, nil, envwork.Provenance{FlowInvocationID: &invocationID, FlowEnvName: env.Name})
		if errors.Is(err, ultra.ErrAlreadyExists) {
			continue
		}
		if err != nil {
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalEnvironmentFailed, "environment "+env.Name+" could not be requested")
		}
		if err := s.record(ctx, org, inv, ultra.FlowStageEnvRequested,
			"env_requested:"+env.Name, string(created.ID)); err != nil {
			return err
		}
	}
	return s.transition(ctx, org, inv, ultra.FlowInvocationProvisioning)
}

func (s *Service) transition(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, state ultra.FlowInvocationState) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		locked, err := scope.Flows().GetInvocationForUpdate(ctx, inv.ID)
		if err != nil {
			return err
		}
		if locked.State.Terminal() || locked.State == state {
			return nil
		}
		return scope.Flows().SetInvocationState(ctx, inv.ID, state, "", "")
	})
}

func (s *Service) envsByName(ctx context.Context, org ultra.OrgID, id ultra.FlowInvocationID) (map[string]ultra.DevEnv, error) {
	envs, err := s.Store.Org(org).Flows().InvocationEnvs(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ultra.DevEnv, len(envs))
	for _, env := range envs {
		out[env.FlowEnvName] = env
	}
	return out, nil
}

// awaitReadiness is the gate: no agent starts until every required
// environment has passed its declared readiness check. A failed required
// environment converges the invocation instead of starting agents that would
// find their declared resources missing.
func (s *Service) awaitReadiness(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, rendered ultra.RenderedFlow) error {
	existing, err := s.envsByName(ctx, org, inv.ID)
	if err != nil {
		return err
	}
	pending := 0
	for _, declared := range rendered.Envs {
		env, ok := existing[declared.Name]
		if !ok {
			// The declaration has no environment yet: go back and create it.
			return s.provision(ctx, org, inv, rendered)
		}
		switch {
		case env.State == ultra.EnvReady:
			if err := s.record(ctx, org, inv, ultra.FlowStageEnvReady,
				"env_ready:"+declared.Name, string(env.ID)); err != nil {
				return err
			}
			if err := s.runSetup(ctx, org, inv, declared, env); err != nil {
				return err
			}
		case env.State.Terminal():
			if !declared.Required {
				if err := s.record(ctx, org, inv, ultra.FlowStageEnvFailed,
					"env_failed:"+declared.Name, env.FailureMessage); err != nil {
					return err
				}
				continue
			}
			if err := s.record(ctx, org, inv, ultra.FlowStageEnvFailed,
				"env_failed:"+declared.Name, env.FailureMessage); err != nil {
				return err
			}
			if err := s.cleanup(ctx, org, inv); err != nil {
				return err
			}
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalEnvironmentFailed,
				"required environment "+declared.Name+" did not become ready")
		default:
			pending++
		}
	}
	if pending > 0 {
		if deadline := s.readinessDeadline(inv, rendered); time.Now().After(deadline) {
			if err := s.cleanup(ctx, org, inv); err != nil {
				return err
			}
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalTimedOut, "environments did not become ready in time")
		}
		return nil
	}
	return s.transition(ctx, org, inv, ultra.FlowInvocationRunning)
}

// readinessDeadline is the latest moment the gate may still be waiting. It is
// the longest declared environment timeout, so a flow that asks for a slow
// environment gets it without letting any invocation wait forever.
func (s *Service) readinessDeadline(inv ultra.FlowInvocation, rendered ultra.RenderedFlow) time.Time {
	longest := s.readinessTimeout()
	for _, env := range rendered.Envs {
		if env.Timeout == "" {
			continue
		}
		if d, err := time.ParseDuration(env.Timeout); err == nil && d > longest {
			longest = d
		}
	}
	return inv.CreatedAt.Add(longest)
}

// runSetup executes an environment's declared setup commands once each. The
// progress key is the idempotency guard: a redelivered advance re-derives the
// same key and skips a command that already ran.
func (s *Service) runSetup(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation,
	declared ultra.RenderedEnv, env ultra.DevEnv) error {
	if len(declared.Setup) == 0 {
		return nil
	}
	recorded, err := s.recordedKeys(ctx, org, inv.ID)
	if err != nil {
		return err
	}
	for i, command := range declared.Setup {
		key := fmt.Sprintf("env_setup:%s:%d", declared.Name, i)
		if recorded[key] {
			continue
		}
		args, err := json.Marshal(map[string]any{"command": command, "description": "flow setup"})
		if err != nil {
			return err
		}
		result, err := s.Envs.Exec(ctx, org, env.ID, "bash", args)
		if err != nil {
			// The environment is momentarily unreachable; the next advance
			// retries. Nothing is recorded, so nothing is skipped.
			return nil
		}
		if result.IsError {
			if err := s.record(ctx, org, inv, ultra.FlowStageEnvFailed,
				"env_setup_failed:"+declared.Name, command); err != nil {
				return err
			}
			if err := s.cleanup(ctx, org, inv); err != nil {
				return err
			}
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalEnvironmentFailed,
				"setup failed for environment "+declared.Name)
		}
		if err := s.record(ctx, org, inv, ultra.FlowStageEnvReady, key, command); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordedKeys(ctx context.Context, org ultra.OrgID, id ultra.FlowInvocationID) (map[string]bool, error) {
	entries, err := s.Store.Org(org).Flows().Progress(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		out[entry.Key] = true
	}
	return out, nil
}

// runTopology launches declared stages in order and converges when the last
// one finishes. A stage starts only when every run of every earlier stage is
// terminal, which is what makes "after" a durable guarantee rather than a
// scheduling hint.
func (s *Service) runTopology(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, rendered ultra.RenderedFlow) error {
	stages := rendered.Stages()
	if len(stages) == 0 {
		return s.converge(ctx, org, inv, ultra.FlowInvocationCompleted, ultra.FlowTerminalCompleted, "")
	}
	envs, err := s.envsByName(ctx, org, inv.ID)
	if err != nil {
		return err
	}
	runs, err := s.Store.Org(org).Flows().InvocationRuns(ctx, inv.ID)
	if err != nil {
		return err
	}
	byAgent := map[string]ultra.AgentRun{}
	for _, run := range runs {
		if run.FlowAgentName != "" {
			byAgent[run.FlowAgentName] = run
		}
	}

	for index, stage := range stages {
		launched, terminal, failed := 0, 0, 0
		for _, agent := range stage {
			run, ok := byAgent[agent.Name]
			if !ok {
				continue
			}
			launched++
			if run.State.Terminal() {
				terminal++
				if run.State != ultra.RunCompleted {
					failed++
				}
			}
		}
		if launched < len(stage) {
			if err := s.launchStage(ctx, org, inv, index, stage, byAgent, envs); err != nil {
				return err
			}
			return nil
		}
		if terminal < launched {
			// This stage is still working; a later stage must not start.
			return nil
		}
		if failed > 0 {
			// A stage that did not fully succeed stops the topology: a later
			// agent declared a dependency, and running it anyway would give it
			// inputs the flow says it must not have.
			if err := s.cleanup(ctx, org, inv); err != nil {
				return err
			}
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalAgentFailed,
				fmt.Sprintf("%d agent(s) in stage %d did not complete", failed, index))
		}
		if err := s.record(ctx, org, inv, ultra.FlowStageAgentTerminal,
			fmt.Sprintf("stage_complete:%d", index), fmt.Sprintf("%d agent(s)", launched)); err != nil {
			return err
		}
	}
	if err := s.cleanup(ctx, org, inv); err != nil {
		return err
	}
	return s.converge(ctx, org, inv, ultra.FlowInvocationCompleted, ultra.FlowTerminalCompleted, "")
}

// launchStage creates every run of one stage and its first step in one
// transaction per agent. Agents that share a stage share a deterministic
// cohort id and keep their declaration ordinals, so a rendered client can show
// the topology the flow declared.
func (s *Service) launchStage(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation,
	index int, stage []ultra.RenderedAgent, byAgent map[string]ultra.AgentRun, envs map[string]ultra.DevEnv) error {
	cohortID := ""
	if len(stage) > 1 {
		cohortID = uuid.NewSHA1(cohortNamespace,
			fmt.Appendf(nil, "%s:%d", inv.ID, index)).String()
	}
	invocationID := inv.ID
	started := 0
	for ordinal, agent := range stage {
		if _, ok := byAgent[agent.Name]; ok {
			continue
		}
		grants, err := s.grantsFor(agent, envs)
		if err != nil {
			if cleanupErr := s.cleanup(ctx, org, inv); cleanupErr != nil {
				return cleanupErr
			}
			return s.converge(ctx, org, inv, ultra.FlowInvocationFailed,
				ultra.FlowTerminalInvalidDefinition, err.Error())
		}
		history, err := loop.InitialEnvelope(agent.Prompt)
		if err != nil {
			return err
		}
		model := agent.Model
		if model.Provider == "" {
			model = s.DefaultModel
		}
		if model.Credential == "" {
			model.Credential = s.DefaultModel.Credential
		}
		run := ultra.AgentRun{
			ID: ultra.RunID(uuid.NewString()), SessionID: inv.SessionID, OrgID: org,
			FlowInvocationID: &invocationID, FlowAgentName: agent.Name,
			CohortID: cohortID, CohortOrdinal: ordinal,
			LoopKind: loop.DefaultLoopKind, LoopVersion: loop.DefaultLoopVersion,
			ModelConfig: model, Prompt: agent.Prompt, History: history, Grants: grants,
		}
		// A dependent agent is parented by the first agent it declared, so a
		// rendered run tree shows the declared dependency rather than a flat
		// list of unrelated roots.
		for _, dep := range agent.After {
			if parent, ok := byAgent[dep]; ok {
				parentID := parent.ID
				run.ParentRunID = &parentID
				break
			}
		}
		err = s.Store.Tx(ctx, func(txs ultra.Store) error {
			scope := txs.Org(org)
			locked, err := scope.Flows().GetInvocationForUpdate(ctx, inv.ID)
			if err != nil {
				return err
			}
			if locked.State.Terminal() || locked.CancelRequestedAt != nil {
				// The invocation stopped mattering while this stage was being
				// launched: start nothing further.
				return nil
			}
			if err := scope.Runs().Create(ctx, run); err != nil {
				return err
			}
			payload, err := json.Marshal(ultra.RunStartedPayload{RunID: run.ID, Prompt: run.Prompt})
			if err != nil {
				return err
			}
			if _, err := scope.Events().Append(ctx, inv.SessionID, ultra.Event{
				Actor: ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)},
				Kind:  ultra.EventKindRunStarted, Payload: payload,
			}); err != nil {
				return err
			}
			if run.ParentRunID != nil {
				spawned, err := json.Marshal(ultra.RunSpawnedPayload{
					ParentRunID: *run.ParentRunID, ChildRunID: run.ID,
				})
				if err != nil {
					return err
				}
				if _, err := scope.Events().Append(ctx, inv.SessionID, ultra.Event{
					Actor: ultra.Actor{Type: ultra.ActorSystem},
					Kind:  ultra.EventKindRunSpawned, Payload: spawned,
				}); err != nil {
					return err
				}
			}
			return s.Enqueue.EnqueueInTx(ctx, txs, loop.StepJob{
				RunID: string(run.ID), OrgID: string(org),
				SessionID: string(inv.SessionID), StepIndex: 0,
			})
		})
		if errors.Is(err, ultra.ErrAlreadyExists) {
			// Another delivery already launched this declaration.
			continue
		}
		if err != nil {
			return err
		}
		byAgent[agent.Name] = run
		started++
	}
	if started == 0 {
		return nil
	}
	return s.record(ctx, org, inv, ultra.FlowStageAgentsStarted,
		fmt.Sprintf("stage_started:%d", index), fmt.Sprintf("%d agent(s)", started))
}

// grantsFor resolves an agent's declared authority into concrete grants. An
// agent receives exactly the environments it declared and nothing else: env_all
// is never granted to a flow agent, because a flow's declaration is the whole
// contract.
func (s *Service) grantsFor(agent ultra.RenderedAgent, envs map[string]ultra.DevEnv) (ultra.Grants, error) {
	grants := agent.Grants
	grants.EnvAll = false
	grants.Envs = nil
	for _, name := range agent.EnvNames {
		env, ok := envs[name]
		if !ok {
			return ultra.Grants{}, fmt.Errorf("agent %q declares environment %q, which the invocation did not create", agent.Name, name)
		}
		grants.Envs = append(grants.Envs, env.ID)
	}
	if !grants.SubsetOf(ultra.RootGrants()) {
		return ultra.Grants{}, fmt.Errorf("agent %q declares authority beyond the platform ceiling", agent.Name)
	}
	return grants, nil
}

// cancel converges a cancelled invocation: its runs are cancelled, the
// resources it owns are released, and nothing further is launched.
//
// The invocation only reaches its terminal state once every run it owns is
// terminal. Declaring the invocation cancelled while one of its agents was
// still executing would make the terminal state a lie, and would let a
// replayed log show an invocation ending before the work it owned did.
func (s *Service) cancel(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation, _ ultra.RenderedFlow) error {
	if err := s.cleanup(ctx, org, inv); err != nil {
		return err
	}
	if err := s.transition(ctx, org, inv, ultra.FlowInvocationCancelling); err != nil {
		return err
	}
	runs, err := s.Store.Org(org).Flows().InvocationRuns(ctx, inv.ID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !run.State.Terminal() {
			// Still winding down; the next advance checks again.
			return nil
		}
	}
	return s.converge(ctx, org, inv, ultra.FlowInvocationCancelled, ultra.FlowTerminalCancelled, "")
}

// cleanup releases exactly the resources this invocation owns, exactly once.
// Ownership is the persisted invocation id on the row, so a session's other
// environments and runs are never touched, and an already-terminating resource
// is not asked to terminate again.
func (s *Service) cleanup(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation) error {
	scope := s.Store.Org(org)
	runs, err := scope.Flows().InvocationRuns(ctx, inv.ID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State.Terminal() {
			continue
		}
		if err := s.cancelRun(ctx, org, run); err != nil {
			return err
		}
	}
	envs, err := scope.Flows().InvocationEnvs(ctx, inv.ID)
	if err != nil {
		return err
	}
	for _, env := range envs {
		if env.State.Terminal() || env.State == ultra.EnvTerminating {
			continue
		}
		if err := s.Envs.RequestTerminate(ctx, org, env.ID); err != nil && !errors.Is(err, ultra.ErrNotFound) {
			return err
		}
		if err := s.record(ctx, org, inv, ultra.FlowStageCleanup,
			"cleanup_env:"+env.FlowEnvName, string(env.ID)); err != nil {
			return err
		}
	}
	return nil
}

// cancelRun stamps a cancel request and finalizes the runs that hold no worker
// (pending and awaiting). A run mid-step observes the flag and finalizes
// itself, which is the same path the API's own cancel uses.
func (s *Service) cancelRun(ctx context.Context, org ultra.OrgID, run ultra.AgentRun) error {
	return s.Store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
		if err != nil {
			return err
		}
		if locked.State.Terminal() {
			return nil
		}
		if err := scope.Runs().RequestCancel(ctx, run.ID); err != nil {
			return err
		}
		if locked.State != ultra.RunPending && locked.State != ultra.RunAwaiting {
			return nil
		}
		if err := scope.Runs().SetState(ctx, run.ID, ultra.RunCancelled, "", ""); err != nil {
			return err
		}
		// An awaiting run may hold an open wait; closing it stops a child that
		// finishes later from resuming a run the invocation has cancelled.
		if err := loop.AbandonWaits(ctx, txs, org, run.ID); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.RunCancelledPayload{RunID: run.ID})
		if err != nil {
			return err
		}
		_, err = scope.Events().Append(ctx, run.SessionID, ultra.Event{
			Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindRunCancelled, Payload: payload,
		})
		return err
	})
}
