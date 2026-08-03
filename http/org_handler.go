package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/provider"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/secrets"
)

// orgHandler implements corev1connect.OrgServiceHandler.
type orgHandler struct {
	store     uc.Store
	keyring   secrets.Keyring
	providers *provider.Registry
}

// requireMember returns the caller's role in the org, collapsing "no such
// org" and "not a member" into the same not-found denial.
func requireMember(ctx context.Context, store uc.Store, org uc.OrgID) (uc.OrgRole, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return "", errUnauthenticated()
	}
	role, err := store.Orgs().MemberRole(ctx, org, user.ID)
	if err != nil {
		return "", errNotFound()
	}
	return role, nil
}

func (h *orgHandler) CreateOrg(ctx context.Context, req *connect.Request[corev1.CreateOrgRequest]) (*connect.Response[corev1.CreateOrgResponse], error) {
	user, ok := userFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	org := uc.Org{ID: uc.OrgID(uuid.NewString()), Name: req.Msg.GetName()}
	err := h.store.Tx(ctx, func(s uc.Store) error {
		if err := s.Orgs().Create(ctx, org); err != nil {
			return err
		}
		return s.Orgs().AddMember(ctx, uc.OrgMember{OrgID: org.ID, UserID: user.ID, Role: uc.OrgRoleOwner})
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Orgs().Get(ctx, org.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.CreateOrgResponse{Org: orgToProto(created)}), nil
}

func (h *orgHandler) GetOrg(ctx context.Context, req *connect.Request[corev1.GetOrgRequest]) (*connect.Response[corev1.GetOrgResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	org, err := h.store.Orgs().Get(ctx, orgID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetOrgResponse{Org: orgToProto(org)}), nil
}

func (h *orgHandler) InviteMember(ctx context.Context, req *connect.Request[corev1.InviteMemberRequest]) (*connect.Response[corev1.InviteMemberResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	callerRole, err := requireMember(ctx, h.store, orgID)
	if err != nil {
		return nil, err
	}
	if callerRole != uc.OrgRoleOwner && callerRole != uc.OrgRoleAdmin {
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
	m := uc.OrgMember{OrgID: orgID, UserID: invitee.ID, Role: role}
	if err := h.store.Orgs().AddMember(ctx, m); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.InviteMemberResponse{Member: &corev1.OrgMember{
		OrgId:  string(orgID),
		UserId: string(invitee.ID),
		Email:  invitee.Email,
		Role:   roleToProto(role),
	}}), nil
}

func (h *orgHandler) ListMembers(ctx context.Context, req *connect.Request[corev1.ListMembersRequest]) (*connect.Response[corev1.ListMembersResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	members, err := h.store.Orgs().ListMembers(ctx, orgID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListMembersResponse{}
	for _, m := range members {
		user, err := h.store.Users().Get(ctx, m.UserID)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		resp.Members = append(resp.Members, &corev1.OrgMember{
			OrgId:  string(m.OrgID),
			UserId: string(m.UserID),
			Email:  user.Email,
			Role:   roleToProto(m.Role),
		})
	}
	return connect.NewResponse(resp), nil
}

func (h *orgHandler) ListOrgs(ctx context.Context, _ *connect.Request[corev1.ListOrgsRequest]) (*connect.Response[corev1.ListOrgsResponse], error) {
	user, ok := userFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListOrgsResponse{}
	for _, o := range orgs {
		resp.Orgs = append(resp.Orgs, orgToProto(o))
	}
	return connect.NewResponse(resp), nil
}

// requireAdmin ensures the caller is an owner or admin of the org.
func requireAdmin(ctx context.Context, store uc.Store, org uc.OrgID) error {
	role, err := requireMember(ctx, store, org)
	if err != nil {
		return err
	}
	if role != uc.OrgRoleOwner && role != uc.OrgRoleAdmin {
		return connect.NewError(connect.CodePermissionDenied, errors.New("requires owner or admin role"))
	}
	return nil
}

func validCredentialKind(kind string) bool {
	switch kind {
	case uc.CredentialKindOpenAI, uc.CredentialKindAnthropic, uc.CredentialKindBedrock:
		return true
	default:
		return false
	}
}

func (h *orgHandler) PutCredential(ctx context.Context, req *connect.Request[corev1.PutCredentialRequest]) (*connect.Response[corev1.PutCredentialResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	if !validCredentialKind(req.Msg.GetKind()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown credential kind"))
	}
	if req.Msg.GetApiKey() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("api_key is required"))
	}
	// Register before downstream handling so unexpected errors are scrubbed
	// from this process's logs too.
	secrets.DefaultRedactor.Register(req.Msg.GetApiKey())
	name := req.Msg.GetName()
	if name == "" {
		name = "default"
	}
	extraHeaders := map[string]string{}
	if raw := req.Msg.GetExtraHeadersJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &extraHeaders); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("extra_headers_json must be a JSON object of string values"))
		}
	}
	for _, value := range extraHeaders {
		secrets.DefaultRedactor.Register(value)
	}
	payload, err := json.Marshal(uc.InferencePayload{
		APIKey: req.Msg.GetApiKey(), BaseURL: req.Msg.GetBaseUrl(), ExtraHeaders: extraHeaders,
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	enc, err := h.keyring.Encrypt(payload)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := h.store.Org(orgID).Credentials().Put(ctx, uc.Credential{
		Kind: req.Msg.GetKind(), Name: name, EncPayload: enc,
	}); err != nil {
		return nil, mapStoreErr(err)
	}
	info, err := h.store.Org(orgID).Credentials().Get(ctx, req.Msg.GetKind(), name)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.PutCredentialResponse{Credential: credentialInfoToProto(uc.CredentialInfo{
		Kind: info.Kind, Name: info.Name, CreatedAt: info.CreatedAt, RotatedAt: info.RotatedAt,
	})}), nil
}

func (h *orgHandler) ListCredentials(ctx context.Context, req *connect.Request[corev1.ListCredentialsRequest]) (*connect.Response[corev1.ListCredentialsResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	infos, err := h.store.Org(orgID).Credentials().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListCredentialsResponse{}
	for _, info := range infos {
		resp.Credentials = append(resp.Credentials, credentialInfoToProto(info))
	}
	return connect.NewResponse(resp), nil
}

func (h *orgHandler) DeleteCredential(ctx context.Context, req *connect.Request[corev1.DeleteCredentialRequest]) (*connect.Response[corev1.DeleteCredentialResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	if err := h.store.Org(orgID).Credentials().Delete(ctx, req.Msg.GetKind(), req.Msg.GetName()); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteCredentialResponse{}), nil
}

func providerToProto(p uc.ProviderInstance) *corev1.ProviderInstance {
	out := &corev1.ProviderInstance{Id: string(p.ID), OrgId: string(p.OrgID), Kind: p.Kind, Name: p.Name, State: p.State, CreatedAt: timestamppb.New(p.CreatedAt)}
	if p.LastHealthyAt != nil {
		out.LastHealthyAt = timestamppb.New(*p.LastHealthyAt)
	}
	// Every optional capability is reported, supported or not, with the reason
	// when it is not. A client showing only what works cannot tell an operator
	// why a flow was refused against this provider.
	for _, capability := range uc.OptionalProviderCapabilities() {
		out.Capabilities = append(out.Capabilities, &corev1.ProviderCapability{
			Name:      string(capability),
			Supported: p.Capabilities.Has(capability),
			Reason:    p.Capabilities.Reason(capability),
		})
	}
	return out
}

func (h *orgHandler) RegisterProvider(ctx context.Context, req *connect.Request[corev1.RegisterProviderRequest]) (*connect.Response[corev1.RegisterProviderResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	if h.providers == nil || !h.providers.Enabled(req.Msg.GetKind()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("provider kind is not enabled"))
	}
	name := req.Msg.GetName()
	if name == "" {
		name = "default"
	}
	config := json.RawMessage(req.Msg.GetConfigJson())
	if len(config) == 0 {
		config = []byte(`{}`)
	}
	if !json.Valid(config) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("config_json is invalid"))
	}
	// The dry run is read-only and happens before anything is persisted: a
	// registration that cannot reach its control plane is refused rather than
	// stored as a provider that has never answered.
	capabilities, err := h.providers.DryRun(ctx, req.Msg.GetKind(), config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(secrets.DefaultRedactor.Redact(err.Error())))
	}
	p := uc.ProviderInstance{ID: uc.ProviderInstanceID(uuid.NewString()), OrgID: orgID, Kind: req.Msg.GetKind(), Name: name, Config: config, State: "ready", Capabilities: capabilities}
	if err := h.store.Org(orgID).Providers().Create(ctx, p); err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Org(orgID).Providers().Get(ctx, p.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.RegisterProviderResponse{Provider: providerToProto(created)}), nil
}
func (h *orgHandler) ListProviders(ctx context.Context, req *connect.Request[corev1.ListProvidersRequest]) (*connect.Response[corev1.ListProvidersResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	items, err := h.store.Org(orgID).Providers().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListProvidersResponse{}
	for _, p := range items {
		resp.Providers = append(resp.Providers, providerToProto(p))
	}
	return connect.NewResponse(resp), nil
}
func (h *orgHandler) DeleteProvider(ctx context.Context, req *connect.Request[corev1.DeleteProviderRequest]) (*connect.Response[corev1.DeleteProviderResponse], error) {
	orgID := uc.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, orgID); err != nil {
		return nil, err
	}
	// A provider that still hosts live environments cannot be removed. Doing
	// so would orphan them: the records would survive with no adapter able to
	// reach, reconcile, or terminate the resources they represent, and the
	// user would be left paying for containers nothing can find.
	providerID := uc.ProviderInstanceID(req.Msg.GetProviderId())
	active, err := h.store.Org(orgID).Resources().ListActive(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	hosted := 0
	for _, env := range active {
		if env.ProviderInstanceID == providerID {
			hosted++
		}
	}
	if hosted > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"this provider still hosts %d environment(s); terminate them before removing it", hosted))
	}
	if err := h.store.Org(orgID).Providers().Delete(ctx, providerID); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteProviderResponse{}), nil
}
