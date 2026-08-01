package http

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/loop"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func participantToProto(p ultra.Participant) *ultrav1.Participant {
	return &ultrav1.Participant{SessionId: string(p.SessionID), Kind: string(p.Kind), ParticipantId: p.ParticipantID, Display: p.Display, State: string(p.State), JoinedAt: timestamppb.New(p.JoinedAt), LastSeenAt: timestamppb.New(p.LastSeenAt)}
}
func memoryToProto(e ultra.SessionMemoryEntry) *ultrav1.MemoryEntry {
	return &ultrav1.MemoryEntry{Key: e.Key, ValueJson: string(e.Value), UpdatedByType: string(e.UpdatedBy.Type), UpdatedById: e.UpdatedBy.ID, UpdatedAt: timestamppb.New(e.UpdatedAt)}
}
func appendTransition(ctx context.Context, scope ultra.OrgScope, session ultra.SessionID, kind string, payload any) (int64, error) {
	b, _ := json.Marshal(payload)
	return scope.Events().Append(ctx, session, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: kind, Payload: b})
}

func (h *sessionHandler) Join(ctx context.Context, req *connect.Request[ultrav1.JoinRequest]) (*connect.Response[ultrav1.JoinResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		changed, e := scope.Participants().Join(ctx, ultra.Participant{SessionID: session, Kind: ultra.ParticipantHuman, ParticipantID: string(user.ID), Display: req.Msg.GetDisplay()})
		if e != nil {
			return e
		}
		if changed {
			seq, e = appendTransition(ctx, scope, session, ultra.EventKindParticipantJoined, ultra.ParticipantEventPayload{Kind: ultra.ParticipantHuman, ParticipantID: string(user.ID), Display: req.Msg.GetDisplay()})
			if e != nil {
				return e
			}
			// Arm presence expiry in the same transaction as the join: a
			// participant can never become present without something
			// scheduled to notice when they go quiet.
			e = h.enqueue.EnqueueInTx(ctx, txs, loop.PresenceReapJob{OrgID: string(org)},
				jobqueue.WithScheduledAt(time.Now().Add(loop.DefaultPresenceAfter)))
		}
		return e
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	list, _ := h.store.Org(org).Participants().List(ctx, session)
	resp := &ultrav1.JoinResponse{EventSeq: seq}
	for _, p := range list {
		resp.Participants = append(resp.Participants, participantToProto(p))
	}
	return connect.NewResponse(resp), nil
}
func (h *sessionHandler) Leave(ctx context.Context, req *connect.Request[ultrav1.LeaveRequest]) (*connect.Response[ultrav1.LeaveResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		changed, e := scope.Participants().Leave(ctx, session, ultra.ParticipantHuman, string(user.ID))
		if e != nil {
			return e
		}
		if changed {
			seq, e = appendTransition(ctx, scope, session, ultra.EventKindParticipantLeft, ultra.ParticipantEventPayload{Kind: ultra.ParticipantHuman, ParticipantID: string(user.ID)})
		}
		return e
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.LeaveResponse{EventSeq: seq}), nil
}
func (h *sessionHandler) Heartbeat(ctx context.Context, req *connect.Request[ultrav1.HeartbeatRequest]) (*connect.Response[ultrav1.HeartbeatResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	if err := h.store.Org(org).Participants().Heartbeat(ctx, session, ultra.ParticipantHuman, string(user.ID)); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.HeartbeatResponse{}), nil
}
func (h *sessionHandler) ListParticipants(ctx context.Context, req *connect.Request[ultrav1.ListParticipantsRequest]) (*connect.Response[ultrav1.ListParticipantsResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Participants().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListParticipantsResponse{}
	for _, p := range items {
		resp.Participants = append(resp.Participants, participantToProto(p))
	}
	return connect.NewResponse(resp), nil
}
func (h *sessionHandler) SetMemory(ctx context.Context, req *connect.Request[ultrav1.SetMemoryRequest]) (*connect.Response[ultrav1.SetMemoryResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	value := json.RawMessage(req.Msg.GetValueJson())
	if !json.Valid(value) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("value_json is invalid"))
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		entry := ultra.SessionMemoryEntry{SessionID: session, Key: req.Msg.GetKey(), Value: value, UpdatedBy: ultra.Actor{Type: ultra.ActorUser, ID: string(user.ID)}}
		if e := scope.Memory().Set(ctx, entry); e != nil {
			return e
		}
		inline := value
		if len(inline) > 1024 {
			inline = nil
		}
		payload := ultra.NewMemoryEventPayload(entry.Key, entry.UpdatedBy, inline)
		seq, err = appendTransition(ctx, scope, session, ultra.EventKindMemorySet, payload)
		return err
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&ultrav1.SetMemoryResponse{EventSeq: seq}), nil
}
func (h *sessionHandler) GetMemory(ctx context.Context, req *connect.Request[ultrav1.GetMemoryRequest]) (*connect.Response[ultrav1.GetMemoryResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	e, err := h.store.Org(org).Memory().Get(ctx, session, req.Msg.GetKey())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.GetMemoryResponse{Entry: memoryToProto(e)}), nil
}
func (h *sessionHandler) ListMemory(ctx context.Context, req *connect.Request[ultrav1.ListMemoryRequest]) (*connect.Response[ultrav1.ListMemoryResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Memory().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListMemoryResponse{}
	for _, e := range items {
		resp.Entries = append(resp.Entries, memoryToProto(e))
	}
	return connect.NewResponse(resp), nil
}
func (h *sessionHandler) DeleteMemory(ctx context.Context, req *connect.Request[ultrav1.DeleteMemoryRequest]) (*connect.Response[ultrav1.DeleteMemoryResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		if e := scope.Memory().Delete(ctx, session, req.Msg.GetKey()); e != nil {
			return e
		}
		seq, err = appendTransition(ctx, scope, session, ultra.EventKindMemoryDeleted, ultra.NewMemoryEventPayload(req.Msg.GetKey(), ultra.Actor{Type: ultra.ActorUser, ID: string(user.ID)}, nil))
		return err
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.DeleteMemoryResponse{EventSeq: seq}), nil
}
