package http

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
)

// orgHandler implements ultrav1connect.OrgServiceHandler.
type orgHandler struct {
	store ultra.Store
}

// requireMember returns the caller's role in the org, collapsing "no such
// org" and "not a member" into the same not-found denial.
func requireMember(ctx context.Context, store ultra.Store, org ultra.OrgID) (ultra.User, ultra.OrgRole, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return ultra.User{}, "", errUnauthenticated()
	}
	role, err := store.Orgs().MemberRole(ctx, org, user.ID)
	if err != nil {
		return ultra.User{}, "", errNotFound()
	}
	return user, role, nil
}

func (h *orgHandler) CreateOrg(ctx context.Context, req *connect.Request[ultrav1.CreateOrgRequest]) (*connect.Response[ultrav1.CreateOrgResponse], error) {
	user, ok := userFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	org := ultra.Org{ID: ultra.OrgID(uuid.NewString()), Name: req.Msg.GetName()}
	err := h.store.Tx(ctx, func(s ultra.Store) error {
		if err := s.Orgs().Create(ctx, org); err != nil {
			return err
		}
		return s.Orgs().AddMember(ctx, ultra.OrgMember{OrgID: org.ID, UserID: user.ID, Role: ultra.OrgRoleOwner})
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Orgs().Get(ctx, org.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.CreateOrgResponse{Org: orgToProto(created)}), nil
}

func (h *orgHandler) GetOrg(ctx context.Context, req *connect.Request[ultrav1.GetOrgRequest]) (*connect.Response[ultrav1.GetOrgResponse], error) {
	orgID := ultra.OrgID(req.Msg.GetOrgId())
	if _, _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	org, err := h.store.Orgs().Get(ctx, orgID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.GetOrgResponse{Org: orgToProto(org)}), nil
}

func (h *orgHandler) InviteMember(ctx context.Context, req *connect.Request[ultrav1.InviteMemberRequest]) (*connect.Response[ultrav1.InviteMemberResponse], error) {
	orgID := ultra.OrgID(req.Msg.GetOrgId())
	_, callerRole, err := requireMember(ctx, h.store, orgID)
	if err != nil {
		return nil, err
	}
	if callerRole != ultra.OrgRoleOwner && callerRole != ultra.OrgRoleAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("requires owner or admin role"))
	}
	role, ok := roleFromProto(req.Msg.GetRole())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role is required"))
	}
	invitee, err := h.store.Users().GetByEmail(ctx, req.Msg.GetEmail())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	m := ultra.OrgMember{OrgID: orgID, UserID: invitee.ID, Role: role}
	if err := h.store.Orgs().AddMember(ctx, m); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.InviteMemberResponse{Member: &ultrav1.OrgMember{
		OrgId:  string(orgID),
		UserId: string(invitee.ID),
		Email:  invitee.Email,
		Role:   roleToProto(role),
	}}), nil
}

func (h *orgHandler) ListMembers(ctx context.Context, req *connect.Request[ultrav1.ListMembersRequest]) (*connect.Response[ultrav1.ListMembersResponse], error) {
	orgID := ultra.OrgID(req.Msg.GetOrgId())
	if _, _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	members, err := h.store.Orgs().ListMembers(ctx, orgID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListMembersResponse{}
	for _, m := range members {
		user, err := h.store.Users().Get(ctx, m.UserID)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		resp.Members = append(resp.Members, &ultrav1.OrgMember{
			OrgId:  string(m.OrgID),
			UserId: string(m.UserID),
			Email:  user.Email,
			Role:   roleToProto(m.Role),
		})
	}
	return connect.NewResponse(resp), nil
}
