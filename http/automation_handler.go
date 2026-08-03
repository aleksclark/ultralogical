package http

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/google/uuid"
)

type automationHandler struct{ store uc.Store }

func periodicProto(p uc.PeriodicPrompt) *corev1.PeriodicPrompt {
	return &corev1.PeriodicPrompt{Id: string(p.ID), SessionId: string(p.SessionID), RunId: string(p.RunID), Schedule: p.Schedule.String(), Prompt: p.Prompt, Enabled: p.Enabled}
}
func (h *automationHandler) PutPeriodicPrompt(ctx context.Context, req *connect.Request[corev1.PutPeriodicPromptRequest]) (*connect.Response[corev1.PutPeriodicPromptResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	schedule, err := time.ParseDuration(req.Msg.GetSchedule())
	if err != nil || schedule < time.Second {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("schedule must be duration >=1s"))
	}
	p := uc.PeriodicPrompt{ID: uc.PeriodicPromptID(uuid.NewString()), OrgID: org, SessionID: session, RunID: uc.RunID(req.Msg.GetRunId()), Schedule: schedule, Prompt: req.Msg.GetPrompt(), Enabled: true, NextAt: time.Now().Add(schedule)}
	if err := h.store.Org(org).PeriodicPrompts().Create(ctx, p); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.PutPeriodicPromptResponse{PeriodicPrompt: periodicProto(p)}), nil
}
func (h *automationHandler) ListPeriodicPrompts(ctx context.Context, req *connect.Request[corev1.ListPeriodicPromptsRequest]) (*connect.Response[corev1.ListPeriodicPromptsResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).PeriodicPrompts().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListPeriodicPromptsResponse{}
	for _, p := range items {
		resp.PeriodicPrompts = append(resp.PeriodicPrompts, periodicProto(p))
	}
	return connect.NewResponse(resp), nil
}
func (h *automationHandler) SetPeriodicPromptEnabled(ctx context.Context, req *connect.Request[corev1.SetPeriodicPromptEnabledRequest]) (*connect.Response[corev1.SetPeriodicPromptEnabledResponse], error) {
	user, ok := userFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	orgs, _ := h.store.Orgs().ListForUser(ctx, user.ID)
	for _, org := range orgs {
		if _, err := h.store.Org(org.ID).PeriodicPrompts().GetForUpdate(ctx, uc.PeriodicPromptID(req.Msg.GetPeriodicPromptId())); err == nil {
			if err := h.store.Org(org.ID).PeriodicPrompts().SetEnabled(ctx, uc.PeriodicPromptID(req.Msg.GetPeriodicPromptId()), req.Msg.GetEnabled()); err != nil {
				return nil, mapStoreErr(err)
			}
			return connect.NewResponse(&corev1.SetPeriodicPromptEnabledResponse{}), nil
		}
	}
	return nil, errNotFound()
}
