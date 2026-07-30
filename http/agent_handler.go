package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/loop"
)

// agentHandler implements ultrav1connect.AgentServiceHandler.
type agentHandler struct {
	store        ultra.Store
	enqueue      jobqueue.TxEnqueuer
	defaultModel ultra.ModelConfig
}

// resolveRunOrg maps a run id to its org and session, verifying membership.
// Missing runs and cross-tenant access are indistinguishable.
func (h *agentHandler) resolveRun(ctx context.Context, runID ultra.RunID) (ultra.AgentRun, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return ultra.AgentRun{}, errUnauthenticated()
	}
	// Directory lookup: find the run's org via its session. We scan the
	// caller's orgs — a run is only visible inside an org the caller
	// belongs to.
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return ultra.AgentRun{}, mapStoreErr(err)
	}
	for _, org := range orgs {
		run, err := h.store.Org(org.ID).Runs().Get(ctx, runID)
		if err == nil {
			return run, nil
		}
		if !errors.Is(err, ultra.ErrNotFound) {
			return ultra.AgentRun{}, mapStoreErr(err)
		}
	}
	return ultra.AgentRun{}, errNotFound()
}

func (h *agentHandler) StartRun(ctx context.Context, req *connect.Request[ultrav1.StartRunRequest]) (*connect.Response[ultrav1.StartRunResponse], error) {
	sessionID := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetPrompt() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prompt is required"))
	}

	modelConfig := h.defaultModel
	if mc := req.Msg.GetModelConfig(); mc != nil {
		if mc.GetProvider() != "" {
			modelConfig.Provider = mc.GetProvider()
		}
		if mc.GetModelId() != "" {
			modelConfig.ModelID = mc.GetModelId()
		}
		if mc.GetCredential() != "" {
			modelConfig.Credential = mc.GetCredential()
		}
	}

	history, err := loop.InitialEnvelope(req.Msg.GetPrompt())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	run := ultra.AgentRun{
		ID:          ultra.RunID(uuid.NewString()),
		SessionID:   sessionID,
		OrgID:       org,
		LoopKind:    loop.DefaultLoopKind,
		LoopVersion: loop.DefaultLoopVersion,
		ModelConfig: modelConfig,
		Prompt:      req.Msg.GetPrompt(),
		History:     history,
	}

	var eventSeq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		if err := scope.Runs().Create(ctx, run); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.RunStartedPayload{RunID: run.ID, Prompt: run.Prompt})
		if err != nil {
			return err
		}
		eventSeq, err = scope.Events().Append(ctx, sessionID, ultra.Event{
			Actor:   ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)},
			Kind:    ultra.EventKindRunStarted,
			Payload: payload,
		})
		if err != nil {
			return err
		}
		// Run row + RunStarted event + first step job commit atomically:
		// no orphaned runs, ever.
		return h.enqueue.EnqueueInTx(ctx, txs, loop.StepJob{
			RunID: string(run.ID), OrgID: string(org), SessionID: string(sessionID), StepIndex: 0,
		})
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}

	created, err := h.store.Org(org).Runs().Get(ctx, run.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.StartRunResponse{Run: runToProto(created), EventSeq: eventSeq}), nil
}

func (h *agentHandler) PromptRun(ctx context.Context, req *connect.Request[ultrav1.PromptRunRequest]) (*connect.Response[ultrav1.PromptRunResponse], error) {
	run, err := h.resolveRun(ctx, ultra.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	if req.Msg.GetMessage() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message is required"))
	}
	user, _ := userFrom(ctx)

	var eventSeq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(run.OrgID)
		locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
		if err != nil {
			return err
		}
		if locked.State != ultra.RunAwaiting && locked.State != ultra.RunCompleted {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("run is not awaiting input or completed"))
		}
		history, err := loop.AppendUserMessage(locked.History, req.Msg.GetMessage())
		if err != nil {
			return err
		}
		if err := scope.Runs().SetHistory(ctx, run.ID, history); err != nil {
			return err
		}
		if err := scope.Runs().SetState(ctx, run.ID, ultra.RunRunning, "", ""); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.UserMessagePayload{Text: req.Msg.GetMessage()})
		if err != nil {
			return err
		}
		eventSeq, err = scope.Events().Append(ctx, run.SessionID, ultra.Event{
			Actor:   ultra.Actor{Type: ultra.ActorUser, ID: string(user.ID)},
			Kind:    ultra.EventKindUserMessage,
			Payload: payload,
		})
		if err != nil {
			return err
		}
		steps, err := scope.Runs().Steps(ctx, run.ID)
		if err != nil {
			return err
		}
		nextIndex := 0
		for _, s := range steps {
			if s.StepIndex >= nextIndex {
				nextIndex = s.StepIndex + 1
			}
		}
		return h.enqueue.EnqueueInTx(ctx, txs, loop.StepJob{
			RunID: string(run.ID), OrgID: string(run.OrgID), SessionID: string(run.SessionID),
			StepIndex: nextIndex,
		})
	})
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) {
			return nil, cerr
		}
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.PromptRunResponse{EventSeq: eventSeq}), nil
}

func (h *agentHandler) CancelRun(ctx context.Context, req *connect.Request[ultrav1.CancelRunRequest]) (*connect.Response[ultrav1.CancelRunResponse], error) {
	run, err := h.resolveRun(ctx, ultra.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(run.OrgID)
		locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
		if err != nil {
			return err
		}
		if locked.State.Terminal() {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("run already finished"))
		}
		if err := scope.Runs().RequestCancel(ctx, run.ID); err != nil {
			return err
		}
		// Runs not actively executing a step (pending queue-side, awaiting)
		// are cancelled immediately; a running step's worker observes the
		// flag and finalizes.
		if locked.State == ultra.RunAwaiting {
			if err := scope.Runs().SetState(ctx, run.ID, ultra.RunCancelled, "", ""); err != nil {
				return err
			}
			payload, _ := json.Marshal(ultra.RunCancelledPayload{RunID: run.ID})
			_, err = scope.Events().Append(ctx, run.SessionID, ultra.Event{
				Actor:   ultra.Actor{Type: ultra.ActorSystem},
				Kind:    ultra.EventKindRunCancelled,
				Payload: payload,
			})
			return err
		}
		return nil
	})
	if err != nil {
		var cerr *connect.Error
		if errors.As(err, &cerr) {
			return nil, cerr
		}
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.CancelRunResponse{}), nil
}

func (h *agentHandler) GetRun(ctx context.Context, req *connect.Request[ultrav1.GetRunRequest]) (*connect.Response[ultrav1.GetRunResponse], error) {
	run, err := h.resolveRun(ctx, ultra.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ultrav1.GetRunResponse{Run: runToProto(run)}), nil
}

func (h *agentHandler) ListRuns(ctx context.Context, req *connect.Request[ultrav1.ListRunsRequest]) (*connect.Response[ultrav1.ListRunsResponse], error) {
	sessionID := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	runs, err := h.store.Org(org).Runs().List(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListRunsResponse{}
	for _, r := range runs {
		resp.Runs = append(resp.Runs, runToProto(r))
	}
	return connect.NewResponse(resp), nil
}
