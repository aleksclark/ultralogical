package http

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/google/uuid"
)

type automationHandler struct{ store ultra.Store }

func periodicProto(p ultra.PeriodicPrompt) *ultrav1.PeriodicPrompt {
	return &ultrav1.PeriodicPrompt{Id: string(p.ID), SessionId: string(p.SessionID), RunId: string(p.RunID), Schedule: p.Schedule.String(), Prompt: p.Prompt, Enabled: p.Enabled}
}
func (h *automationHandler) PutPeriodicPrompt(ctx context.Context, req *connect.Request[ultrav1.PutPeriodicPromptRequest]) (*connect.Response[ultrav1.PutPeriodicPromptResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	schedule, err := time.ParseDuration(req.Msg.GetSchedule())
	if err != nil || schedule < time.Second {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("schedule must be duration >=1s"))
	}
	p := ultra.PeriodicPrompt{ID: ultra.PeriodicPromptID(uuid.NewString()), OrgID: org, SessionID: session, RunID: ultra.RunID(req.Msg.GetRunId()), Schedule: schedule, Prompt: req.Msg.GetPrompt(), Enabled: true, NextAt: time.Now().Add(schedule)}
	if err := h.store.Org(org).PeriodicPrompts().Create(ctx, p); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.PutPeriodicPromptResponse{PeriodicPrompt: periodicProto(p)}), nil
}
func (h *automationHandler) ListPeriodicPrompts(ctx context.Context, req *connect.Request[ultrav1.ListPeriodicPromptsRequest]) (*connect.Response[ultrav1.ListPeriodicPromptsResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).PeriodicPrompts().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListPeriodicPromptsResponse{}
	for _, p := range items {
		resp.PeriodicPrompts = append(resp.PeriodicPrompts, periodicProto(p))
	}
	return connect.NewResponse(resp), nil
}
func (h *automationHandler) SetPeriodicPromptEnabled(ctx context.Context, req *connect.Request[ultrav1.SetPeriodicPromptEnabledRequest]) (*connect.Response[ultrav1.SetPeriodicPromptEnabledResponse], error) {
	user, ok := userFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	orgs, _ := h.store.Orgs().ListForUser(ctx, user.ID)
	for _, org := range orgs {
		if _, err := h.store.Org(org.ID).PeriodicPrompts().GetForUpdate(ctx, ultra.PeriodicPromptID(req.Msg.GetPeriodicPromptId())); err == nil {
			if err := h.store.Org(org.ID).PeriodicPrompts().SetEnabled(ctx, ultra.PeriodicPromptID(req.Msg.GetPeriodicPromptId()), req.Msg.GetEnabled()); err != nil {
				return nil, mapStoreErr(err)
			}
			return connect.NewResponse(&ultrav1.SetPeriodicPromptEnabledResponse{}), nil
		}
	}
	return nil, errNotFound()
}
