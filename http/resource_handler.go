package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/resourcework"
)

type resourceHandler struct {
	store     uc.Store
	resources *resourcework.Service
}

func resourceStateToProto(s uc.ResourceState) corev1.ResourceState {
	switch s {
	case uc.ResourceRequested:
		return corev1.ResourceState_RESOURCE_STATE_REQUESTED
	case uc.ResourceProvisioning:
		return corev1.ResourceState_RESOURCE_STATE_PROVISIONING
	case uc.ResourceReady:
		return corev1.ResourceState_RESOURCE_STATE_READY
	case uc.ResourceSuspended:
		return corev1.ResourceState_RESOURCE_STATE_SUSPENDED
	case uc.ResourceTerminating:
		return corev1.ResourceState_RESOURCE_STATE_TERMINATING
	case uc.ResourceTerminated:
		return corev1.ResourceState_RESOURCE_STATE_TERMINATED
	case uc.ResourceFailed:
		return corev1.ResourceState_RESOURCE_STATE_FAILED
	}
	return corev1.ResourceState_RESOURCE_STATE_UNSPECIFIED
}

func resourceToProtoWith(r uc.Resource, provider uc.ProviderInstance) *corev1.Resource {
	out := resourceToProto(r)
	out.ProviderName = provider.Name
	out.ProviderKind = provider.Kind
	out.ProviderState = provider.State
	return out
}

func resourceToProto(r uc.Resource) *corev1.Resource {
	spec := &corev1.DevEnvSpec{}
	if s, err := uc.ParseDevEnvSpec(r.Spec); err == nil {
		spec = &corev1.DevEnvSpec{Name: s.Name, Image: s.Image, Workdir: s.Workdir, Env: s.Env, Metadata: s.Metadata}
	}
	out := &corev1.Resource{
		Id: string(r.ID), SessionId: string(r.SessionID), ProviderInstanceId: string(r.ProviderInstanceID),
		State: resourceStateToProto(r.State), Spec: spec, Endpoint: string(r.Endpoint), Epoch: int32(r.Epoch),
		FailureMessage: r.FailureMessage, CreatedAt: timestamppb.New(r.CreatedAt), UpdatedAt: timestamppb.New(r.UpdatedAt),
		Kind: string(r.Kind),
	}
	if r.ReadyAt != nil {
		out.ReadyAt = timestamppb.New(*r.ReadyAt)
	}
	if r.TerminatedAt != nil {
		out.TerminatedAt = timestamppb.New(*r.TerminatedAt)
	}
	return out
}

func (h *resourceHandler) providersByID(ctx context.Context, org uc.OrgID) map[uc.ProviderInstanceID]uc.ProviderInstance {
	out := map[uc.ProviderInstanceID]uc.ProviderInstance{}
	items, err := h.store.Org(org).Providers().List(ctx)
	if err != nil {
		return out
	}
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func (h *resourceHandler) provider(ctx context.Context, org uc.OrgID, id uc.ProviderInstanceID) uc.ProviderInstance {
	instance, err := h.store.Org(org).Providers().Get(ctx, id)
	if err != nil {
		return uc.ProviderInstance{}
	}
	return instance
}

func (h *resourceHandler) resolve(ctx context.Context, id uc.ResourceID) (uc.Resource, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return uc.Resource{}, errUnauthenticated()
	}
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return uc.Resource{}, mapStoreErr(err)
	}
	for _, o := range orgs {
		e, err := h.store.Org(o.ID).Resources().Get(ctx, id)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, uc.ErrNotFound) {
			return uc.Resource{}, mapStoreErr(err)
		}
	}
	return uc.Resource{}, errNotFound()
}

func (h *resourceHandler) ProvisionResource(ctx context.Context, req *connect.Request[corev1.ProvisionResourceRequest]) (*connect.Response[corev1.ProvisionResourceResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	sp := req.Msg.GetSpec()
	kind := uc.ResourceKind(req.Msg.GetKind())
	if kind == "" {
		kind = uc.ResourceKindDevEnv
	}
	var spec json.RawMessage
	if kind == uc.ResourceKindDevEnv {
		if sp == nil || sp.GetName() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec.name is required"))
		}
		b, _ := json.Marshal(uc.DevEnvSpec{Name: sp.GetName(), Image: sp.GetImage(), Workdir: sp.GetWorkdir(), Env: sp.GetEnv(), Metadata: sp.GetMetadata()})
		spec = b
	} else {
		name := ""
		if sp != nil {
			name = sp.GetName()
		}
		b, _ := json.Marshal(map[string]string{"name": name})
		spec = b
	}
	created, seq, err := h.resources.Request(ctx, org, session, kind, spec, req.Msg.GetProviderInstance(), nil)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	got, err := h.store.Org(org).Resources().Get(ctx, created.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ProvisionResourceResponse{Resource: resourceToProto(got), EventSeq: seq}), nil
}

func (h *resourceHandler) GetResource(ctx context.Context, req *connect.Request[corev1.GetResourceRequest]) (*connect.Response[corev1.GetResourceResponse], error) {
	e, err := h.resolve(ctx, uc.ResourceID(req.Msg.GetResourceId()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&corev1.GetResourceResponse{
		Resource: resourceToProtoWith(e, h.provider(ctx, e.OrgID, e.ProviderInstanceID)),
	}), nil
}

func (h *resourceHandler) ListResources(ctx context.Context, req *connect.Request[corev1.ListResourcesRequest]) (*connect.Response[corev1.ListResourcesResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var items []uc.Resource
	if k := req.Msg.GetKind(); k != "" {
		items, err = h.store.Org(org).Resources().List(ctx, session, uc.ResourceKind(k))
	} else {
		items, err = h.store.Org(org).Resources().List(ctx, session)
	}
	if err != nil {
		return nil, mapStoreErr(err)
	}
	providers := h.providersByID(ctx, org)
	resp := &corev1.ListResourcesResponse{}
	for _, e := range items {
		resp.Resources = append(resp.Resources, resourceToProtoWith(e, providers[e.ProviderInstanceID]))
	}
	return connect.NewResponse(resp), nil
}

func (h *resourceHandler) TerminateResource(ctx context.Context, req *connect.Request[corev1.TerminateResourceRequest]) (*connect.Response[corev1.TerminateResourceResponse], error) {
	e, err := h.resolve(ctx, uc.ResourceID(req.Msg.GetResourceId()))
	if err != nil {
		return nil, err
	}
	if err := h.resources.RequestTerminate(ctx, e.OrgID, e.ID); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.TerminateResourceResponse{}), nil
}

func (h *resourceHandler) RestartResource(ctx context.Context, req *connect.Request[corev1.RestartResourceRequest]) (*connect.Response[corev1.RestartResourceResponse], error) {
	e, err := h.resolve(ctx, uc.ResourceID(req.Msg.GetResourceId()))
	if err != nil {
		return nil, err
	}
	seq, err := h.resources.RequestRestart(ctx, e.OrgID, e.ID)
	if err != nil {
		if errors.Is(err, uc.ErrNotFound) {
			return nil, errNotFound()
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("resource cannot be restarted"))
	}
	restarted, err := h.store.Org(e.OrgID).Resources().Get(ctx, e.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.RestartResourceResponse{Resource: resourceToProto(restarted), EventSeq: seq}), nil
}

func (h *resourceHandler) ExecPreview(ctx context.Context, req *connect.Request[corev1.ExecPreviewRequest]) (*connect.Response[corev1.ExecPreviewResponse], error) {
	e, err := h.resolve(ctx, uc.ResourceID(req.Msg.GetResourceId()))
	if err != nil {
		return nil, err
	}
	args, _ := json.Marshal(map[string]any{"command": req.Msg.GetCommand(), "description": "Human ExecPreview"})
	result, err := h.resources.Exec(ctx, e.OrgID, e.ID, "bash", args)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("resource unavailable"))
	}
	user, _ := userFrom(ctx)
	payload := uc.ExecPreviewRanPayload{ResourceID: e.ID, Command: req.Msg.GetCommand(), Output: result.Text, IsError: result.IsError}
	b, _ := json.Marshal(payload)
	seq, err := h.store.Org(e.OrgID).Events().Append(ctx, e.SessionID, uc.Event{Actor: uc.Actor{Type: uc.ActorUser, ID: string(user.ID)}, Kind: uc.EventKindExecPreviewRan, Payload: b})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ExecPreviewResponse{Output: result.Text, IsError: result.IsError, EventSeq: seq}), nil
}
