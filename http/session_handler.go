package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// resolveSessionTenant maps a session id to its tenant and verifies the
// caller's key belongs to that tenant. Missing sessions and cross-tenant
// access return the identical not-found error.
// resolveSessionTenant maps a session id to its tenant and verifies the
// caller's key belongs to that tenant. Missing sessions and cross-tenant
// access return the identical not-found error.
func resolveSessionTenant(ctx context.Context, store uc.Store, session uc.SessionID) (uc.TenantID, authContext, error) {
	a, ok := identityFrom(ctx)
	if !ok {
		return "", authContext{}, errUnauthenticated()
	}
	tenant, err := store.SessionTenant(ctx, session)
	if err != nil {
		return "", authContext{}, errNotFound()
	}
	if a.Identity.TenantID != tenant {
		return "", authContext{}, errNotFound()
	}
	return tenant, a, nil
}

func (h *sessionHandler) CreateSession(ctx context.Context, req *connect.Request[corev1.CreateSessionRequest]) (*connect.Response[corev1.CreateSessionResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	labels := req.Msg.GetLabels()
	if err := uc.ValidateLabels(labels); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	sess := uc.Session{ID: uc.SessionID(uuid.NewString()), Title: req.Msg.GetTitle(), Labels: labels}
	if err := h.store.Tenant(tenantID).Sessions().Create(ctx, sess); err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Tenant(tenantID).Sessions().Get(ctx, sess.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.CreateSessionResponse{Session: sessionToProto(created)}), nil
}

func (h *sessionHandler) GetSession(ctx context.Context, req *connect.Request[corev1.GetSessionRequest]) (*connect.Response[corev1.GetSessionResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	sess, err := h.store.Tenant(org).Sessions().Get(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetSessionResponse{Session: sessionToProto(sess)}), nil
}

func (h *sessionHandler) ListSessions(ctx context.Context, req *connect.Request[corev1.ListSessionsRequest]) (*connect.Response[corev1.ListSessionsResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	var selectors []uc.LabelSelector
	for _, s := range req.Msg.GetLabelSelectors() {
		op := s.GetOp()
		if op == "" {
			op = "="
		}
		selectors = append(selectors, uc.LabelSelector{Key: s.GetKey(), Op: op, Values: append([]string(nil), s.GetValues()...)})
	}
	sessions, err := h.store.Tenant(tenantID).Sessions().List(ctx, selectors)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListSessionsResponse{}
	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, sessionToProto(s))
	}
	return connect.NewResponse(resp), nil
}

func (h *sessionHandler) UpdateSessionLabels(ctx context.Context, req *connect.Request[corev1.UpdateSessionLabelsRequest]) (*connect.Response[corev1.UpdateSessionLabelsResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, a, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	labels := req.Msg.GetLabels()
	if err := uc.ValidateLabels(labels); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var seq int64
	var updated uc.Session
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(org)
		var e error
		updated, e = scope.Sessions().UpdateLabels(ctx, sessionID, labels)
		if e != nil {
			return e
		}
		payload, e := json.Marshal(uc.SessionLabelsChangedPayload{Labels: labels})
		if e != nil {
			return e
		}
		seq, e = scope.Events().Append(ctx, sessionID, uc.Event{Actor: a.Actor, Kind: uc.EventKindSessionLabelsChanged, Payload: payload})
		return e
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.UpdateSessionLabelsResponse{Session: sessionToProto(updated), EventSeq: seq}), nil
}

// eventHandler implements corev1connect.EventServiceHandler.
type eventHandler struct {
	store uc.Store
	auth  uc.Authenticator
	bus   uc.EventBus
}

func (h *eventHandler) Append(ctx context.Context, req *connect.Request[corev1.AppendRequest]) (*connect.Response[corev1.AppendResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, a, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	kind, payload, err := payloadToDomain(req.Msg.GetPayload())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	seq, err := h.store.Tenant(org).Events().Append(ctx, sessionID, uc.Event{
		Actor:   a.Actor,
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
	authorization := req.Header().Get("Authorization")
	ctx, err := authenticate(ctx, h.auth, authorization, req.Header().Get("X-Core-Actor"))
	if err != nil {
		return err
	}
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
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
	// A3.3: a revoked key must fail closed mid-stream. Re-authenticate on
	// each delivery (and on a slow poll tick when the bus is idle) so an open
	// Subscribe does not outlive the key that opened it.
	recheck := time.NewTicker(time.Second)
	defer recheck.Stop()
	token := bearer(authorization)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return ctx.Err()
			}
			if _, err := h.auth.Authenticate(ctx, token); err != nil {
				return errUnauthenticated()
			}
			msg, err := eventToProto(e)
			if err != nil {
				return connect.NewError(connect.CodeInternal, errors.New("event encoding failed"))
			}
			if err := stream.Send(&corev1.SubscribeResponse{Event: msg}); err != nil {
				return err
			}
		case <-recheck.C:
			if _, err := h.auth.Authenticate(ctx, token); err != nil {
				return errUnauthenticated()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func memoryToProto(e uc.SessionMemoryEntry) *corev1.MemoryEntry {
	return &corev1.MemoryEntry{Key: e.Key, ValueJson: string(e.Value), UpdatedByType: e.UpdatedBy.Kind, UpdatedById: e.UpdatedBy.ID, UpdatedAt: timestamppb.New(e.UpdatedAt)}
}
func appendTransition(ctx context.Context, scope uc.TenantScope, session uc.SessionID, kind string, payload any) (int64, error) {
	b, _ := json.Marshal(payload)
	return scope.Events().Append(ctx, session, uc.Event{Actor: uc.ActorSystem(), Kind: kind, Payload: b})
}
func (h *sessionHandler) ArchiveSession(ctx context.Context, req *connect.Request[corev1.ArchiveSessionRequest]) (*connect.Response[corev1.ArchiveSessionResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	s, err := h.store.Tenant(org).Sessions().Archive(ctx, sessionID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ArchiveSessionResponse{Session: sessionToProto(s)}), nil
}

func (h *sessionHandler) SetMemory(ctx context.Context, req *connect.Request[corev1.SetMemoryRequest]) (*connect.Response[corev1.SetMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, a, err := resolveSessionTenant(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	value := json.RawMessage(req.Msg.GetValueJson())
	if !json.Valid(value) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("value_json is invalid"))
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(org)
		entry := uc.SessionMemoryEntry{SessionID: session, Key: req.Msg.GetKey(), Value: value, UpdatedBy: a.Actor}
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
	org, _, err := resolveSessionTenant(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	e, err := h.store.Tenant(org).Memory().Get(ctx, session, req.Msg.GetKey())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetMemoryResponse{Entry: memoryToProto(e)}), nil
}
func (h *sessionHandler) ListMemory(ctx context.Context, req *connect.Request[corev1.ListMemoryRequest]) (*connect.Response[corev1.ListMemoryResponse], error) {
	session := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Tenant(org).Memory().List(ctx, session)
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
	org, a, err := resolveSessionTenant(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	var seq int64
	err = h.store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(org)
		if e := scope.Memory().Delete(ctx, session, req.Msg.GetKey()); e != nil {
			return e
		}
		seq, err = appendTransition(ctx, scope, session, uc.EventKindMemoryDeleted, uc.NewMemoryEventPayload(req.Msg.GetKey(), a.Actor, nil))
		return err
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteMemoryResponse{EventSeq: seq}), nil
}

func (h *eventHandler) Get(ctx context.Context, req *connect.Request[corev1.GetRequest]) (*connect.Response[corev1.GetResponse], error) {
	sessionID := uc.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionTenant(ctx, h.store, sessionID)
	if err != nil {
		return nil, err
	}
	from := req.Msg.GetFromSeq()
	if tok := req.Msg.GetPageToken(); tok != "" {
		// page tokens are the last delivered seq as decimal.
		var n int64
		for _, c := range tok {
			if c < '0' || c > '9' {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid page_token"))
			}
			n = n*10 + int64(c-'0')
		}
		from = n
	}
	limit := int(req.Msg.GetPageSize())
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	// Over-fetch one to detect more pages when to_seq is unset.
	events, err := h.store.Tenant(org).Events().Range(ctx, sessionID, from, limit+1)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	to := req.Msg.GetToSeq()
	resp := &corev1.GetResponse{}
	for _, e := range events {
		if to > 0 && e.Seq > to {
			break
		}
		if len(resp.Events) >= limit {
			resp.NextPageToken = fmt.Sprintf("%d", resp.Events[len(resp.Events)-1].GetSeq())
			break
		}
		pe, err := eventToProto(e)
		if err != nil {
			return nil, err
		}
		resp.Events = append(resp.Events, pe)
	}
	return connect.NewResponse(resp), nil
}
