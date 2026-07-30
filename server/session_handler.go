package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/server/eventbus"
)

// sessionHandler implements ultrav1connect.SessionServiceHandler.
type sessionHandler struct {
	store ultra.Store
}

// resolveSessionOrg maps a session id to its org and verifies the caller is
// a member. Missing sessions and cross-tenant access return the identical
// not-found error.
func resolveSessionOrg(ctx context.Context, store ultra.Store, session ultra.SessionID) (ultra.OrgID, ultra.User, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return "", ultra.User{}, errUnauthenticated()
	}
	org, err := store.SessionOrg(ctx, session)
	if err != nil {
		return "", ultra.User{}, errNotFound()
	}
	if _, err := store.Orgs().MemberRole(ctx, org, user.ID); err != nil {
		return "", ultra.User{}, errNotFound()
	}
	return org, user, nil
}

func (h *sessionHandler) CreateSession(ctx context.Context, req *connect.Request[ultrav1.CreateSessionRequest]) (*connect.Response[ultrav1.CreateSessionResponse], error) {
	orgID := ultra.OrgID(req.Msg.GetOrgId())
	if _, _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	sess := ultra.Session{ID: ultra.SessionID(uuid.NewString()), Title: req.Msg.GetTitle()}
	if err := h.store.Org(orgID).Sessions().Create(ctx, sess); err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Org(orgID).Sessions().Get(ctx, sess.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.CreateSessionResponse{Session: sessionToProto(created)}), nil
}

func (h *sessionHandler) GetSession(ctx context.Context, req *connect.Request[ultrav1.GetSessionRequest]) (*connect.Response[ultrav1.GetSessionResponse], error) {
	sessionID := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	sess, err := h.store.Org(org).Sessions().Get(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.GetSessionResponse{Session: sessionToProto(sess)}), nil
}

func (h *sessionHandler) ListSessions(ctx context.Context, req *connect.Request[ultrav1.ListSessionsRequest]) (*connect.Response[ultrav1.ListSessionsResponse], error) {
	orgID := ultra.OrgID(req.Msg.GetOrgId())
	if _, _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	sessions, err := h.store.Org(orgID).Sessions().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListSessionsResponse{}
	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, sessionToProto(s))
	}
	return connect.NewResponse(resp), nil
}

// eventHandler implements ultrav1connect.EventServiceHandler.
type eventHandler struct {
	store ultra.Store
	auth  Authenticator
	bus   *eventbus.Bus
}

func (h *eventHandler) Append(ctx context.Context, req *connect.Request[ultrav1.AppendRequest]) (*connect.Response[ultrav1.AppendResponse], error) {
	sessionID := ultra.SessionID(req.Msg.GetSessionId())
	org, user, err := resolveSessionOrg(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	kind, payload, err := payloadToDomain(req.Msg.GetPayload())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	seq, err := h.store.Org(org).Events().Append(ctx, sessionID, ultra.Event{
		Actor:   ultra.Actor{Type: ultra.ActorUser, ID: string(user.ID)},
		Kind:    kind,
		Payload: payload,
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.AppendResponse{Seq: seq}), nil
}

func (h *eventHandler) Subscribe(ctx context.Context, req *connect.Request[ultrav1.SubscribeRequest], stream *connect.ServerStream[ultrav1.SubscribeResponse]) error {
	// Streaming RPCs bypass unary interceptors; authenticate here.
	ctx, err := authenticate(ctx, h.auth, req.Header().Get("Authorization"))
	if err != nil {
		return err
	}
	sessionID := ultra.SessionID(req.Msg.GetSessionId())
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
	if err := stream.Send(&ultrav1.SubscribeResponse{}); err != nil {
		return err
	}
	for e := range events {
		msg, err := eventToProto(e)
		if err != nil {
			return connect.NewError(connect.CodeInternal, errors.New("event encoding failed"))
		}
		if err := stream.Send(&ultrav1.SubscribeResponse{Event: msg}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
