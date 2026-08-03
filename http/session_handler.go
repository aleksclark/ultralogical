package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

// sessionHandler implements corev1connect.SessionServiceHandler.
type sessionHandler struct {
	store uc.Store
}

// resolveSessionOrg maps a session id to its org and verifies the caller is
// a member. Missing sessions and cross-tenant access return the identical
// not-found error.
func resolveSessionOrg(ctx context.Context, store uc.Store, session uc.SessionID) (uc.OrgID, uc.User, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return "", uc.User{}, errUnauthenticated()
	}
	org, err := store.SessionOrg(ctx, session)
	if err != nil {
		return "", uc.User{}, errNotFound()
	}
	if _, err := store.Orgs().MemberRole(ctx, org, user.ID); err != nil {
		return "", uc.User{}, errNotFound()
	}
	return org, user, nil
}

func (h *sessionHandler) CreateSession(ctx context.Context, req *connect.Request[corev1.CreateSessionRequest]) (*connect.Response[corev1.CreateSessionResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	sess := uc.Session{ID: uc.SessionID(uuid.NewString()), Title: req.Msg.GetTitle()}
	if err := h.store.Org(orgID).Sessions().Create(ctx, sess); err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Org(orgID).Sessions().Get(ctx, sess.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.CreateSessionResponse{Session: sessionToProto(created)}), nil
}

func (h *sessionHandler) GetSession(ctx context.Context, req *connect.Request[corev1.GetSessionRequest]) (*connect.Response[corev1.GetSessionResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	sess, err := h.store.Org(org).Sessions().Get(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetSessionResponse{Session: sessionToProto(sess)}), nil
}

func (h *sessionHandler) ListSessions(ctx context.Context, req *connect.Request[corev1.ListSessionsRequest]) (*connect.Response[corev1.ListSessionsResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	sessions, err := h.store.Org(orgID).Sessions().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListSessionsResponse{}
	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, sessionToProto(s))
	}
	return connect.NewResponse(resp), nil
}

// eventHandler implements corev1connect.EventServiceHandler.
type eventHandler struct {
	store uc.Store
	auth  uc.Authenticator
	bus   uc.EventBus
}

func (h *eventHandler) Append(ctx context.Context, req *connect.Request[corev1.AppendRequest]) (*connect.Response[corev1.AppendResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	kind, payload, err := payloadToDomain(req.Msg.GetPayload())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	seq, err := h.store.Org(org).Events().Append(ctx, sessionID, uc.Event{
		Actor:   uc.Actor{Type: uc.ActorUser, ID: string(user.ID)},
		Kind:    kind,
		Payload: payload,
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.AppendResponse{Seq: seq}), nil
}

func (h *eventHandler) Subscribe(ctx context.Context, req *connect.Request[corev1.SubscribeRequest], stream *connect.ServerStream[corev1.SubscribeResponse]) error {
	// Streaming RPCs bypass unary interceptors; authenticate here.
	ctx, err := authenticate(ctx, h.auth, req.Header().Get("Authorization"))
	if err != nil {
		return err
	}
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return err
	}
	events, err := h.bus.Subscribe(ctx, org, sessionID, req.Msg.GetFromSeq())
	if err != nil {
		return mapStoreErr(err)
	}
	// Keepalive with no event: flushes response headers so clients observe
	// stream establishment immediately instead of blocking until the first
	// event arrives.
	if err := stream.Send(&corev1.SubscribeResponse{}); err != nil {
		return err
	}
	for e := range events {
		msg, err := eventToProto(e)
		if err != nil {
			return connect.NewError(connect.CodeInternal, errors.New("event encoding failed"))
		}
		if err := stream.Send(&corev1.SubscribeResponse{Event: msg}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func memoryToProto(e uc.SessionMemoryEntry) *corev1.MemoryEntry {
	return &corev1.MemoryEntry{Key: e.Key, ValueJson: string(e.Value), UpdatedByType: string(e.UpdatedBy.Type), UpdatedById: e.UpdatedBy.ID, UpdatedAt: timestamppb.New(e.UpdatedAt)}
}
func appendTransition(ctx context.Context, scope uc.OrgScope, session uc.SessionID, kind string, payload any) (int64, error) {
	b, _ := json.Marshal(payload)
	return scope.Events().Append(ctx, session, uc.Event{Actor: uc.Actor{Type: uc.ActorSystem}, Kind: kind, Payload: b})
}
func (h *sessionHandler) SetMemory(ctx context.Context, req *connect.Request[corev1.SetMemoryRequest]) (*connect.Response[corev1.SetMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	value := json.RawMessage(req.Msg.GetValueJson())
	if !json.Valid(value) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("value_json is invalid"))
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		entry := uc.SessionMemoryEntry{SessionID: session, Key: req.Msg.GetKey(), Value: value, UpdatedBy: uc.Actor{Type: uc.ActorUser, ID: string(user.ID)}}
		if e := scope.Memory().Set(ctx, entry); e != nil {
			return e
		}
		inline := value
		if len(inline) > 1024 {
			inline = nil
		}
		payload := uc.NewMemoryEventPayload(entry.Key, entry.UpdatedBy, inline)
		seq, err = appendTransition(ctx, scope, session, uc.EventKindMemorySet, payload)
		return err
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&corev1.SetMemoryResponse{EventSeq: seq}), nil
}
func (h *sessionHandler) GetMemory(ctx context.Context, req *connect.Request[corev1.GetMemoryRequest]) (*connect.Response[corev1.GetMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	e, err := h.store.Org(org).Memory().Get(ctx, session, req.Msg.GetKey())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetMemoryResponse{Entry: memoryToProto(e)}), nil
}
func (h *sessionHandler) ListMemory(ctx context.Context, req *connect.Request[corev1.ListMemoryRequest]) (*connect.Response[corev1.ListMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Memory().List(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListMemoryResponse{}
	for _, e := range items {
		resp.Entries = append(resp.Entries, memoryToProto(e))
	}
	return connect.NewResponse(resp), nil
}
func (h *sessionHandler) DeleteMemory(ctx context.Context, req *connect.Request[corev1.DeleteMemoryRequest]) (*connect.Response[corev1.DeleteMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Org(org)
		if e := scope.Memory().Delete(ctx, session, req.Msg.GetKey()); e != nil {
			return e
		}
		seq, err = appendTransition(ctx, scope, session, uc.EventKindMemoryDeleted, uc.NewMemoryEventPayload(req.Msg.GetKey(), uc.Actor{Type: uc.ActorUser, ID: string(user.ID)}, nil))
		return err
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteMemoryResponse{EventSeq: seq}), nil
}

