package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/jobqueue"
	"github.com/aleksclark/ultracore/loop"
)

// agentHandler implements corev1connect.AgentServiceHandler.
type agentHandler struct {
	store        uc.Store
	enqueue      jobqueue.TxEnqueuer
	defaultModel uc.ModelConfig
}

// resolveRunOrg maps a run id to its org and session, verifying membership.
// Missing runs and cross-tenant access are indistinguishable.
func (h *agentHandler) resolveRun(ctx context.Context, runID uc.RunID) (uc.AgentRun, error) {
	a, ok := identityFrom(ctx)
	if !ok {
		return uc.AgentRun{}, errUnauthenticated()
	}
	run, err := h.store.Tenant(a.Identity.TenantID).Runs().Get(ctx, runID)
	if err != nil {
		if errors.Is(err, uc.ErrNotFound) {
			return uc.AgentRun{}, errNotFound()
		}
		return uc.AgentRun{}, mapStoreErr(err)
	}
	return run, nil
}

func (h *agentHandler) StartRun(ctx context.Context, req *connect.Request[corev1.StartRunRequest]) (*connect.Response[corev1.StartRunResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
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
	// API-started runs receive DefaultRunPolicy unless the caller supplies an
	// explicit policy. The caller's Actor is captured on the run so children
	// and audit surfaces can attribute the tree without re-reading headers.
	policy := uc.DefaultRunPolicy()
	if requested := req.Msg.GetPolicy(); requested != nil {
		policy = policyFromProto(requested)
	}
	run := uc.AgentRun{
		ID:          uc.RunID(uuid.NewString()),
		SessionID:   sessionID,
		TenantID:    org,
		LoopKind:    loop.DefaultLoopKind,
		LoopVersion: loop.DefaultLoopVersion,
		ModelConfig: modelConfig,
		Prompt:      req.Msg.GetPrompt(),
		History:     history,
		Policy:      policy,
		Actor:       actorFrom(ctx),
	}

	var eventSeq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(org)
		if err := scope.Runs().Create(ctx, run); err != nil {
			return err
		}
		payload, err := json.Marshal(uc.RunStartedPayload{RunID: run.ID, Prompt: run.Prompt})
		if err != nil {
			return err
		}
		eventSeq, err = scope.Events().Append(ctx, sessionID, uc.Event{
			Actor:   uc.ActorAgent(uc.RunID(string(run.ID))),
			Kind:    uc.EventKindRunStarted,
			Payload: payload,
		})
		if err != nil {
			return err
		}
		// Run row + RunStarted event + first step job commit atomically:
		// no orphaned runs, ever.
		return h.enqueue.EnqueueInTx(ctx, txs, loop.StepJob{
			RunID: string(run.ID), TenantID: string(org), SessionID: string(sessionID), StepIndex: 0,
		})
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}

	created, err := h.store.Tenant(org).Runs().Get(ctx, run.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.StartRunResponse{Run: runToProto(created), EventSeq: eventSeq}), nil
}

func (h *agentHandler) PromptRun(ctx context.Context, req *connect.Request[corev1.PromptRunRequest]) (*connect.Response[corev1.PromptRunResponse], error) {
	run, err := h.resolveRun(ctx, uc.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	if req.Msg.GetMessage() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message is required"))
	}
	caller := actorFrom(ctx)

	var eventSeq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(run.TenantID)
		locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
		if err != nil {
			return err
		}
		if locked.State != uc.RunAwaiting && locked.State != uc.RunCompleted {
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
		if err := scope.Runs().SetState(ctx, run.ID, uc.RunRunning, "", ""); err != nil {
			return err
		}
		payload, err := json.Marshal(uc.UserMessagePayload{Text: req.Msg.GetMessage()})
		if err != nil {
			return err
		}
		eventSeq, err = scope.Events().Append(ctx, run.SessionID, uc.Event{
			Actor:   caller,
			Kind:    uc.EventKindUserMessage,
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
			RunID: string(run.ID), TenantID: string(run.TenantID), SessionID: string(run.SessionID),
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
	return connect.NewResponse(&corev1.PromptRunResponse{EventSeq: eventSeq}), nil
}

func (h *agentHandler) CancelRun(ctx context.Context, req *connect.Request[corev1.CancelRunRequest]) (*connect.Response[corev1.CancelRunResponse], error) {
	run, err := h.resolveRun(ctx, uc.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(run.TenantID)
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
		if locked.State == uc.RunAwaiting {
			if err := scope.Runs().SetState(ctx, run.ID, uc.RunCancelled, "", ""); err != nil {
				return err
			}
			// A run awaiting child agents holds an open wait. Closing it here
			// is what stops a child that finishes later from resuming a run
			// the user has already cancelled.
			if err := loop.AbandonWaits(ctx, txs, run.TenantID, run.ID); err != nil {
				return err
			}
			payload, _ := json.Marshal(uc.RunCancelledPayload{RunID: run.ID})
			_, err = scope.Events().Append(ctx, run.SessionID, uc.Event{
				Actor:   uc.ActorSystem(),
				Kind:    uc.EventKindRunCancelled,
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
	return connect.NewResponse(&corev1.CancelRunResponse{}), nil
}

func (h *agentHandler) GetRun(ctx context.Context, req *connect.Request[corev1.GetRunRequest]) (*connect.Response[corev1.GetRunResponse], error) {
	run, err := h.resolveRun(ctx, uc.RunID(req.Msg.GetRunId()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&corev1.GetRunResponse{Run: runToProto(run)}), nil
}

// GetRunTree returns a session's runs as parent/child trees with their waits.
// Clients need the whole shape at once to render a spawn tree or lane view;
// walking it request by request would race the live event stream.
func (h *agentHandler) GetRunTree(ctx context.Context, req *connect.Request[corev1.GetRunTreeRequest]) (*connect.Response[corev1.GetRunTreeResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	scope := h.store.Tenant(org)
	runs, err := scope.Runs().List(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}

	nodes := make(map[uc.RunID]*corev1.RunTreeNode, len(runs))
	order := make([]uc.RunID, 0, len(runs))
	for _, run := range runs {
		node := &corev1.RunTreeNode{Run: runToProto(run)}
		waits, err := scope.Waits().ListForParent(ctx, run.ID)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		for _, wait := range waits {
			members, err := scope.Waits().Members(ctx, wait.ID)
			if err != nil {
				return nil, mapStoreErr(err)
			}
			node.Waits = append(node.Waits, waitToProto(wait, members))
		}
		nodes[run.ID] = node
		order = append(order, run.ID)
	}

	resp := &corev1.GetRunTreeResponse{}
	for _, id := range order {
		node := nodes[id]
		parentID := node.GetRun().GetParentRunId()
		// A child whose parent is in this session hangs off it; anything else
		// (including a child of a run in another session) is a root here.
		if parent, ok := nodes[uc.RunID(parentID)]; ok && parentID != "" {
			parent.Children = append(parent.Children, node)
			continue
		}
		resp.Roots = append(resp.Roots, node)
	}
	return connect.NewResponse(resp), nil
}

func (h *agentHandler) ListRuns(ctx context.Context, req *connect.Request[corev1.ListRunsRequest]) (*connect.Response[corev1.ListRunsResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	runs, err := h.store.Tenant(org).Runs().List(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListRunsResponse{}
	for _, r := range runs {
		resp.Runs = append(resp.Runs, runToProto(r))
	}
	return connect.NewResponse(resp), nil
}
