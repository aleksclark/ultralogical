package http

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envwork"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
)

type envHandler struct {
	store ultra.Store
	envs  *envwork.Service
}
type billingHandler struct{ store ultra.Store }

func envStateToProto(s ultra.EnvState) ultrav1.EnvState {
	switch s {
	case ultra.EnvRequested:
		return ultrav1.EnvState_ENV_STATE_REQUESTED
	case ultra.EnvProvisioning:
		return ultrav1.EnvState_ENV_STATE_PROVISIONING
	case ultra.EnvReady:
		return ultrav1.EnvState_ENV_STATE_READY
	case ultra.EnvSuspended:
		return ultrav1.EnvState_ENV_STATE_SUSPENDED
	case ultra.EnvTerminating:
		return ultrav1.EnvState_ENV_STATE_TERMINATING
	case ultra.EnvTerminated:
		return ultrav1.EnvState_ENV_STATE_TERMINATED
	case ultra.EnvFailed:
		return ultrav1.EnvState_ENV_STATE_FAILED
	}
	return ultrav1.EnvState_ENV_STATE_UNSPECIFIED
}
func envToProto(e ultra.DevEnv) *ultrav1.DevEnv {
	out := &ultrav1.DevEnv{Id: string(e.ID), SessionId: string(e.SessionID), ProviderInstanceId: string(e.ProviderInstanceID), State: envStateToProto(e.State), Spec: &ultrav1.EnvSpec{Name: e.Spec.Name, Image: e.Spec.Image, Workdir: e.Spec.Workdir, Env: e.Spec.Env, Metadata: e.Spec.Metadata}, Endpoint: e.Endpoint, Epoch: int32(e.Epoch), FailureMessage: e.FailureMessage, CreatedAt: timestamppb.New(e.CreatedAt), UpdatedAt: timestamppb.New(e.UpdatedAt)}
	if e.ReadyAt != nil {
		out.ReadyAt = timestamppb.New(*e.ReadyAt)
	}
	if e.TerminatedAt != nil {
		out.TerminatedAt = timestamppb.New(*e.TerminatedAt)
	}
	return out
}
func (h *envHandler) resolve(ctx context.Context, id ultra.EnvID) (ultra.DevEnv, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return ultra.DevEnv{}, errUnauthenticated()
	}
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return ultra.DevEnv{}, mapStoreErr(err)
	}
	for _, o := range orgs {
		e, err := h.store.Org(o.ID).Envs().Get(ctx, id)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, ultra.ErrNotFound) {
			return ultra.DevEnv{}, mapStoreErr(err)
		}
	}
	return ultra.DevEnv{}, errNotFound()
}
func (h *envHandler) ProvisionEnv(ctx context.Context, req *connect.Request[ultrav1.ProvisionEnvRequest]) (*connect.Response[ultrav1.ProvisionEnvResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	sp := req.Msg.GetSpec()
	if sp == nil || sp.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec.name is required"))
	}
	env, seq, err := h.envs.Request(ctx, org, session, ultra.EnvSpec{Name: sp.GetName(), Image: sp.GetImage(), Workdir: sp.GetWorkdir(), Env: sp.GetEnv(), Metadata: sp.GetMetadata()}, req.Msg.GetProviderInstance(), nil)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Org(org).Envs().Get(ctx, env.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.ProvisionEnvResponse{Env: envToProto(created), EventSeq: seq}), nil
}
func (h *envHandler) GetEnv(ctx context.Context, req *connect.Request[ultrav1.GetEnvRequest]) (*connect.Response[ultrav1.GetEnvResponse], error) {
	e, err := h.resolve(ctx, ultra.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&ultrav1.GetEnvResponse{Env: envToProto(e)}), nil
}
func (h *envHandler) ListEnvs(ctx context.Context, req *connect.Request[ultrav1.ListEnvsRequest]) (*connect.Response[ultrav1.ListEnvsResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Envs().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListEnvsResponse{}
	for _, e := range items {
		resp.Envs = append(resp.Envs, envToProto(e))
	}
	return connect.NewResponse(resp), nil
}
func (h *envHandler) TerminateEnv(ctx context.Context, req *connect.Request[ultrav1.TerminateEnvRequest]) (*connect.Response[ultrav1.TerminateEnvResponse], error) {
	e, err := h.resolve(ctx, ultra.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	if err := h.envs.RequestTerminate(ctx, e.OrgID, e.ID); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.TerminateEnvResponse{}), nil
}
func (h *envHandler) ExecPreview(ctx context.Context, req *connect.Request[ultrav1.ExecPreviewRequest]) (*connect.Response[ultrav1.ExecPreviewResponse], error) {
	e, err := h.resolve(ctx, ultra.EnvID(req.Msg.GetEnvId()))
	if err != nil {
		return nil, err
	}
	args, _ := json.Marshal(map[string]any{"command": req.Msg.GetCommand(), "description": "Human ExecPreview"})
	result, err := h.envs.Exec(ctx, e.OrgID, e.ID, "bash", args)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("environment unavailable"))
	}
	user, _ := userFrom(ctx)
	payload := ultra.ExecPreviewRanPayload{EnvID: e.ID, Command: req.Msg.GetCommand(), Output: result.Text, IsError: result.IsError}
	b, _ := json.Marshal(payload)
	seq, err := h.store.Org(e.OrgID).Events().Append(ctx, e.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorUser, ID: string(user.ID)}, Kind: ultra.EventKindExecPreviewRan, Payload: b})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.ExecPreviewResponse{Output: result.Text, IsError: result.IsError, EventSeq: seq}), nil
}
func (h *billingHandler) GetUsage(ctx context.Context, req *connect.Request[ultrav1.GetUsageRequest]) (*connect.Response[ultrav1.GetUsageResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, org); err != nil {
		return nil, err
	}
	from, to := time.Time{}, time.Now()
	if req.Msg.GetFrom() != nil {
		from = req.Msg.GetFrom().AsTime()
	}
	if req.Msg.GetTo() != nil {
		to = req.Msg.GetTo().AsTime()
	}
	items, err := h.store.Org(org).Usage().List(ctx, from, to)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.GetUsageResponse{}
	for _, u := range items {
		p := &ultrav1.UsageInterval{EnvId: string(u.EnvID), ProviderInstanceId: string(u.ProviderInstanceID), StartedAt: timestamppb.New(u.StartedAt), Seconds: u.Seconds, RateClass: u.RateClass}
		if u.EndedAt != nil {
			p.EndedAt = timestamppb.New(*u.EndedAt)
		}
		resp.Intervals = append(resp.Intervals, p)
		resp.TotalSeconds += u.Seconds
	}
	return connect.NewResponse(resp), nil
}
