package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/envwork"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

type envHandler struct {
	store uc.Store
	envs  *envwork.Service
}

func envStateToProto(s uc.EnvState) corev1.EnvState {
	switch s {
	case uc.EnvRequested:
		return corev1.EnvState_ENV_STATE_REQUESTED
	case uc.EnvProvisioning:
		return corev1.EnvState_ENV_STATE_PROVISIONING
	case uc.EnvReady:
		return corev1.EnvState_ENV_STATE_READY
	case uc.EnvSuspended:
		return corev1.EnvState_ENV_STATE_SUSPENDED
	case uc.EnvTerminating:
		return corev1.EnvState_ENV_STATE_TERMINATING
	case uc.EnvTerminated:
		return corev1.EnvState_ENV_STATE_TERMINATED
	case uc.EnvFailed:
		return corev1.EnvState_ENV_STATE_FAILED
	}
	return corev1.EnvState_ENV_STATE_UNSPECIFIED
}

// envToProtoWith renders an environment together with the registration hosting
// it. Naming the provider is what lets a client explain a fault: an
// environment failing because its cluster is unreachable is a different
// problem from one whose container crashed.
func envToProtoWith(e uc.DevEnv, provider uc.ProviderInstance) *corev1.DevEnv {
	out := envToProto(e)
	out.ProviderName = provider.Name
	out.ProviderKind = provider.Kind
	out.ProviderState = provider.State
	return out
}

func envToProto(e uc.DevEnv) *corev1.DevEnv {
	out := &corev1.DevEnv{Id: string(e.ID), SessionId: string(e.SessionID), ProviderInstanceId: string(e.ProviderInstanceID), State: envStateToProto(e.State), Spec: &corev1.EnvSpec{Name: e.Spec.Name, Image: e.Spec.Image, Workdir: e.Spec.Workdir, Env: e.Spec.Env, Metadata: e.Spec.Metadata}, Endpoint: e.Endpoint, Epoch: int32(e.Epoch), FailureMessage: e.FailureMessage, CreatedAt: timestamppb.New(e.CreatedAt), UpdatedAt: timestamppb.New(e.UpdatedAt)}
	if e.ReadyAt != nil {
		out.ReadyAt = timestamppb.New(*e.ReadyAt)
	}
	if e.TerminatedAt != nil {
		out.TerminatedAt = timestamppb.New(*e.TerminatedAt)
	}
	return out
}

// providersByID indexes an org's registrations for rendering.
func (h *envHandler) providersByID(ctx context.Context, org uc.OrgID) map[uc.ProviderInstanceID]uc.ProviderInstance {
	out := map[uc.ProviderInstanceID]uc.ProviderInstance{}
	items, err := h.store.Org(org).Providers().List(ctx)
	if err != nil {
		// A provider read failing must not hide the environments themselves:
		// they are still real and still need to be listed.
		return out
	}
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

// provider reads one registration for rendering, tolerating its absence.
func (h *envHandler) provider(ctx context.Context, org uc.OrgID, id uc.ProviderInstanceID) uc.ProviderInstance {
	instance, err := h.store.Org(org).Providers().Get(ctx, id)
	if err != nil {
		return uc.ProviderInstance{}
	}
	return instance
}

func (h *envHandler) resolve(ctx context.Context, id uc.EnvID) (uc.DevEnv, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return uc.DevEnv{}, errUnauthenticated()
	}
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return uc.DevEnv{}, mapStoreErr(err)
	}
	for _, o := range orgs {
		e, err := h.store.Org(o.ID).Envs().Get(ctx, id)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, uc.ErrNotFound) {
			return uc.DevEnv{}, mapStoreErr(err)
		}
	}
	return uc.DevEnv{}, errNotFound()
}
func (h *envHandler) ProvisionEnv(ctx context.Context, req *connect.Request[corev1.ProvisionEnvRequest]) (*connect.Response[corev1.ProvisionEnvResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	sp := req.Msg.GetSpec()
	if sp == nil || sp.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec.name is required"))
	}
	env, seq, err := h.envs.Request(ctx, org, session, uc.EnvSpec{Name: sp.GetName(), Image: sp.GetImage(), Workdir: sp.GetWorkdir(), Env: sp.GetEnv(), Metadata: sp.GetMetadata()}, req.Msg.GetProviderInstance(), nil)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Org(org).Envs().Get(ctx, env.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ProvisionEnvResponse{Env: envToProto(created), EventSeq: seq}), nil
}
func (h *envHandler) GetEnv(ctx context.Context, req *connect.Request[corev1.GetEnvRequest]) (*connect.Response[corev1.GetEnvResponse], error) {
	e, err := h.resolve(ctx, uc.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&corev1.GetEnvResponse{
		Env: envToProtoWith(e, h.provider(ctx, e.OrgID, e.ProviderInstanceID)),
	}), nil
}
func (h *envHandler) ListEnvs(ctx context.Context, req *connect.Request[corev1.ListEnvsRequest]) (*connect.Response[corev1.ListEnvsResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Envs().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	// The registrations are read once and shared, so listing many
	// environments does not issue one provider query per row.
	providers := h.providersByID(ctx, org)
	resp := &corev1.ListEnvsResponse{}
	for _, e := range items {
		resp.Envs = append(resp.Envs, envToProtoWith(e, providers[e.ProviderInstanceID]))
	}
	return connect.NewResponse(resp), nil
}
func (h *envHandler) TerminateEnv(ctx context.Context, req *connect.Request[corev1.TerminateEnvRequest]) (*connect.Response[corev1.TerminateEnvResponse], error) {
	e, err := h.resolve(ctx, uc.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	if err := h.envs.RequestTerminate(ctx, e.OrgID, e.ID); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.TerminateEnvResponse{}), nil
}
func (h *envHandler) RestartEnv(ctx context.Context, req *connect.Request[corev1.RestartEnvRequest]) (*connect.Response[corev1.RestartEnvResponse], error) {
	e, err := h.resolve(ctx, uc.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	seq, err := h.envs.RequestRestart(ctx, e.OrgID, e.ID)
	if err != nil {
		if errors.Is(err, uc.ErrNotFound) {
			return nil, errNotFound()
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("environment cannot be restarted"))
	}
	restarted, err := h.store.Org(e.OrgID).Envs().Get(ctx, e.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.RestartEnvResponse{Env: envToProto(restarted), EventSeq: seq}), nil
}

func (h *envHandler) ExecPreview(ctx context.Context, req *connect.Request[corev1.ExecPreviewRequest]) (*connect.Response[corev1.ExecPreviewResponse], error) {
	e, err := h.resolve(ctx, uc.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	args, _ := json.Marshal(map[string]any{"command": req.Msg.GetCommand(), "description": "Human ExecPreview"})
	result, err := h.envs.Exec(ctx, e.OrgID, e.ID, "bash", args)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("environment unavailable"))
	}
	user, _ := userFrom(ctx)
	payload := uc.ExecPreviewRanPayload{EnvID: e.ID, Command: req.Msg.GetCommand(), Output: result.Text, IsError: result.IsError}
	b, _ := json.Marshal(payload)
	seq, err := h.store.Org(e.OrgID).Events().Append(ctx, e.SessionID, uc.Event{Actor: uc.Actor{Type: uc.ActorUser, ID: string(user.ID)}, Kind: uc.EventKindExecPreviewRan, Payload: b})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ExecPreviewResponse{Output: result.Text, IsError: result.IsError, EventSeq: seq}), nil
}
