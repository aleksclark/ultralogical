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

// tenantHandler implements corev1connect.TenantServiceHandler.
type tenantHandler struct {
	store     uc.Store
	keyring   secrets.Keyring
	providers *provider.Registry
}

func keyScopeToProto(s uc.KeyScope) corev1.KeyScope {
	switch s {
	case uc.KeyScopeAdmin:
		return corev1.KeyScope_KEY_SCOPE_ADMIN
	case uc.KeyScopeSessions:
		return corev1.KeyScope_KEY_SCOPE_SESSIONS
	default:
		return corev1.KeyScope_KEY_SCOPE_UNSPECIFIED
	}
}

func keyScopeFromProto(s corev1.KeyScope) (uc.KeyScope, bool) {
	switch s {
	case corev1.KeyScope_KEY_SCOPE_ADMIN:
		return uc.KeyScopeAdmin, true
	case corev1.KeyScope_KEY_SCOPE_SESSIONS:
		return uc.KeyScopeSessions, true
	default:
		return "", false
	}
}

func apiKeyInfoToProto(k uc.APIKeyInfo) *corev1.APIKeyInfo {
	out := &corev1.APIKeyInfo{
		Id: string(k.ID), TenantId: string(k.TenantID), Name: k.Name,
		Scope: keyScopeToProto(k.Scope), Prefix: k.Prefix,
		CreatedAt: timestamppb.New(k.CreatedAt),
	}
	if k.RevokedAt != nil {
		out.RevokedAt = timestamppb.New(*k.RevokedAt)
	}
	return out
}

func (h *tenantHandler) mintKey(ctx context.Context, tenant uc.TenantID, name string, scope uc.KeyScope) (uc.APIKeyInfo, string, error) {
	raw, prefix, err := uc.GenerateAPIKey()
	if err != nil {
		return uc.APIKeyInfo{}, "", err
	}
	secrets.DefaultRedactor.Register(raw)
	enc, err := h.keyring.Encrypt([]byte(raw))
	if err != nil {
		return uc.APIKeyInfo{}, "", err
	}
	key := uc.APIKey{
		ID: uc.APIKeyID(uuid.NewString()), TenantID: tenant, Name: name,
		Scope: scope, Prefix: prefix, KeyHash: uc.HashAPIKey(raw), KeyEnc: enc,
	}
	if err := h.store.APIKeys().Create(ctx, key); err != nil {
		return uc.APIKeyInfo{}, "", err
	}
	return key.Info(), raw, nil
}

func (h *tenantHandler) CreateTenant(ctx context.Context, req *connect.Request[corev1.CreateTenantRequest]) (*connect.Response[corev1.CreateTenantResponse], error) {
	// Bootstrap: any authenticated admin key (or, during first boot, any auth
	// once a key exists). Sessions-scope keys are refused.
	a, ok := identityFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	if a.Identity.Scope != uc.KeyScopeAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("requires admin key"))
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	tenant := uc.Tenant{ID: uc.TenantID(uuid.NewString()), Name: req.Msg.GetName()}
	var adminRaw string
	err := h.store.Tx(ctx, func(s uc.Store) error {
		if err := s.Tenants().Create(ctx, tenant); err != nil {
			return err
		}
		// Temporarily swap store for mint inside tx: use outer h.store which
		// shares the tx when nested. Rebind.
		prev := h.store
		h.store = s
		defer func() { h.store = prev }()
		_, raw, err := h.mintKey(ctx, tenant.ID, "owner", uc.KeyScopeAdmin)
		adminRaw = raw
		return err
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Tenants().Get(ctx, tenant.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.CreateTenantResponse{Tenant: tenantToProto(created), AdminKey: adminRaw}), nil
}

func (h *tenantHandler) GetTenant(ctx context.Context, req *connect.Request[corev1.GetTenantRequest]) (*connect.Response[corev1.GetTenantResponse], error) {
	id := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireTenant(ctx, id); err != nil {
		return nil, err
	}
	tenant, err := h.store.Tenants().Get(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.GetTenantResponse{Tenant: tenantToProto(tenant)}), nil
}

func (h *tenantHandler) ListTenants(ctx context.Context, _ *connect.Request[corev1.ListTenantsRequest]) (*connect.Response[corev1.ListTenantsResponse], error) {
	a, ok := identityFrom(ctx)
	if !ok {
		return nil, errUnauthenticated()
	}
	tenant, err := h.store.Tenants().Get(ctx, a.Identity.TenantID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.ListTenantsResponse{Tenants: []*corev1.Tenant{tenantToProto(tenant)}}), nil
}

func (h *tenantHandler) CreateAPIKey(ctx context.Context, req *connect.Request[corev1.CreateAPIKeyRequest]) (*connect.Response[corev1.CreateAPIKeyResponse], error) {
	id := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	scope, ok := keyScopeFromProto(req.Msg.GetScope())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	}
	info, raw, err := h.mintKey(ctx, id, req.Msg.GetName(), scope)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.CreateAPIKeyResponse{Key: apiKeyInfoToProto(info), RawKey: raw}), nil
}

func (h *tenantHandler) ListAPIKeys(ctx context.Context, req *connect.Request[corev1.ListAPIKeysRequest]) (*connect.Response[corev1.ListAPIKeysResponse], error) {
	id := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	keys, err := h.store.APIKeys().List(ctx, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListAPIKeysResponse{}
	for _, k := range keys {
		resp.Keys = append(resp.Keys, apiKeyInfoToProto(k))
	}
	return connect.NewResponse(resp), nil
}

func (h *tenantHandler) RevokeAPIKey(ctx context.Context, req *connect.Request[corev1.RevokeAPIKeyRequest]) (*connect.Response[corev1.RevokeAPIKeyResponse], error) {
	id := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, id); err != nil {
		return nil, err
	}
	if err := h.store.APIKeys().Revoke(ctx, id, uc.APIKeyID(req.Msg.GetKeyId())); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.RevokeAPIKeyResponse{}), nil
}

func validCredentialKind(kind string) bool {
	switch kind {
	case uc.CredentialKindOpenAI, uc.CredentialKindAnthropic, uc.CredentialKindBedrock:
		return true
	default:
		return false
	}
}

func (h *tenantHandler) PutCredential(ctx context.Context, req *connect.Request[corev1.PutCredentialRequest]) (*connect.Response[corev1.PutCredentialResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, tenantID); err != nil {
		return nil, err
	}
	if !validCredentialKind(req.Msg.GetKind()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown credential kind"))
	}
	if req.Msg.GetApiKey() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("api_key is required"))
	}
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
	if err := h.store.Tenant(tenantID).Credentials().Put(ctx, uc.Credential{
		Kind: req.Msg.GetKind(), Name: name, EncPayload: enc,
	}); err != nil {
		return nil, mapStoreErr(err)
	}
	info, err := h.store.Tenant(tenantID).Credentials().Get(ctx, req.Msg.GetKind(), name)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.PutCredentialResponse{Credential: credentialInfoToProto(uc.CredentialInfo{
		Kind: info.Kind, Name: info.Name, CreatedAt: info.CreatedAt, RotatedAt: info.RotatedAt,
	})}), nil
}

func (h *tenantHandler) ListCredentials(ctx context.Context, req *connect.Request[corev1.ListCredentialsRequest]) (*connect.Response[corev1.ListCredentialsResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	infos, err := h.store.Tenant(tenantID).Credentials().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListCredentialsResponse{}
	for _, info := range infos {
		resp.Credentials = append(resp.Credentials, credentialInfoToProto(info))
	}
	return connect.NewResponse(resp), nil
}

func (h *tenantHandler) DeleteCredential(ctx context.Context, req *connect.Request[corev1.DeleteCredentialRequest]) (*connect.Response[corev1.DeleteCredentialResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, tenantID); err != nil {
		return nil, err
	}
	if err := h.store.Tenant(tenantID).Credentials().Delete(ctx, req.Msg.GetKind(), req.Msg.GetName()); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteCredentialResponse{}), nil
}

func providerToProto(p uc.ProviderInstance) *corev1.ProviderInstance {
	out := &corev1.ProviderInstance{Id: string(p.ID), TenantId: string(p.TenantID), Kind: p.Kind, Name: p.Name, State: p.State, CreatedAt: timestamppb.New(p.CreatedAt)}
	if p.LastHealthyAt != nil {
		out.LastHealthyAt = timestamppb.New(*p.LastHealthyAt)
	}
	for _, capability := range uc.OptionalProviderCapabilities() {
		out.Capabilities = append(out.Capabilities, &corev1.ProviderCapability{
			Name: string(capability), Supported: p.Capabilities.Has(capability), Reason: p.Capabilities.Reason(capability),
		})
	}
	return out
}

func (h *tenantHandler) RegisterProvider(ctx context.Context, req *connect.Request[corev1.RegisterProviderRequest]) (*connect.Response[corev1.RegisterProviderResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, tenantID); err != nil {
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
	capabilities, err := h.providers.DryRun(ctx, req.Msg.GetKind(), config)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(secrets.DefaultRedactor.Redact(err.Error())))
	}
	p := uc.ProviderInstance{ID: uc.ProviderInstanceID(uuid.NewString()), TenantID: tenantID, Kind: req.Msg.GetKind(), Name: name, Config: config, State: "ready", Capabilities: capabilities}
	if err := h.store.Tenant(tenantID).Providers().Create(ctx, p); err != nil {
		return nil, mapStoreErr(err)
	}
	created, err := h.store.Tenant(tenantID).Providers().Get(ctx, p.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.RegisterProviderResponse{Provider: providerToProto(created)}), nil
}

func (h *tenantHandler) ListProviders(ctx context.Context, req *connect.Request[corev1.ListProvidersRequest]) (*connect.Response[corev1.ListProvidersResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	items, err := h.store.Tenant(tenantID).Providers().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &corev1.ListProvidersResponse{}
	for _, p := range items {
		resp.Providers = append(resp.Providers, providerToProto(p))
	}
	return connect.NewResponse(resp), nil
}

func (h *tenantHandler) DeleteProvider(ctx context.Context, req *connect.Request[corev1.DeleteProviderRequest]) (*connect.Response[corev1.DeleteProviderResponse], error) {
	tenantID := uc.TenantID(req.Msg.GetTenantId())
	if _, err := requireAdmin(ctx, tenantID); err != nil {
		return nil, err
	}
	providerID := uc.ProviderInstanceID(req.Msg.GetProviderId())
	active, err := h.store.Tenant(tenantID).Resources().ListActive(ctx)
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
			"this provider still hosts %d resource(s); terminate them before removing it", hosted))
	}
	if err := h.store.Tenant(tenantID).Providers().Delete(ctx, providerID); err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&corev1.DeleteProviderResponse{}), nil
}
