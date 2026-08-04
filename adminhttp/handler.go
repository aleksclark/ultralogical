package adminhttp

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/admin/authz"
	"github.com/aleksclark/ultracore/admin/query"
	adminstore "github.com/aleksclark/ultracore/admin/store"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
)

type readService struct {
	store         *adminstore.AdminStore
	tokens        *authz.TokenDirectory
	revealEnabled bool
}

func (s *readService) DescribeCollection(ctx context.Context, req *connect.Request[adminv1.DescribeCollectionRequest]) (*connect.Response[adminv1.DescribeCollectionResponse], error) {
	reg := s.store.Registry()
	var cols []query.Collection
	if name := req.Msg.GetName(); name != "" {
		c, ok := reg.Get(name)
		if !ok {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown collection"))
		}
		cols = []query.Collection{c}
	} else {
		cols = reg.All()
	}
	out := &adminv1.DescribeCollectionResponse{}
	for _, c := range cols {
		out.Collections = append(out.Collections, query.CollectionToProto(c))
	}
	return connect.NewResponse(out), nil
}

func (s *readService) ListTenants(ctx context.Context, req *connect.Request[adminv1.ListTenantsRequest]) (*connect.Response[adminv1.ListTenantsResponse], error) {
	items, page, err := s.store.ListTenants(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListTenantsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetTenant(ctx context.Context, req *connect.Request[adminv1.GetTenantRequest]) (*connect.Response[adminv1.GetTenantResponse], error) {
	item, err := s.store.GetTenant(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetTenantResponse{Item: item}), nil
}

func (s *readService) ListAPIKeys(ctx context.Context, req *connect.Request[adminv1.ListAPIKeysRequest]) (*connect.Response[adminv1.ListAPIKeysResponse], error) {
	items, page, err := s.store.ListAPIKeys(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListAPIKeysResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetAPIKey(ctx context.Context, req *connect.Request[adminv1.GetAPIKeyRequest]) (*connect.Response[adminv1.GetAPIKeyResponse], error) {
	item, err := s.store.GetAPIKey(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetAPIKeyResponse{Item: item}), nil
}

func (s *readService) ListSessions(ctx context.Context, req *connect.Request[adminv1.ListSessionsRequest]) (*connect.Response[adminv1.ListSessionsResponse], error) {
	items, page, err := s.store.ListSessions(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListSessionsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetSession(ctx context.Context, req *connect.Request[adminv1.GetSessionRequest]) (*connect.Response[adminv1.GetSessionResponse], error) {
	item, err := s.store.GetSession(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetSessionResponse{Item: item}), nil
}

func (s *readService) ListEvents(ctx context.Context, req *connect.Request[adminv1.ListEventsRequest]) (*connect.Response[adminv1.ListEventsResponse], error) {
	items, page, err := s.store.ListEvents(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListEventsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetEvent(ctx context.Context, req *connect.Request[adminv1.GetEventRequest]) (*connect.Response[adminv1.GetEventResponse], error) {
	item, err := s.store.GetEvent(ctx, req.Msg.GetSessionId(), req.Msg.GetSeq())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetEventResponse{Item: item}), nil
}

func (s *readService) ListRuns(ctx context.Context, req *connect.Request[adminv1.ListRunsRequest]) (*connect.Response[adminv1.ListRunsResponse], error) {
	items, page, err := s.store.ListRuns(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListRunsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetRun(ctx context.Context, req *connect.Request[adminv1.GetRunRequest]) (*connect.Response[adminv1.GetRunResponse], error) {
	item, err := s.store.GetRun(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetRunResponse{Item: item}), nil
}

func (s *readService) GetRunHistory(ctx context.Context, req *connect.Request[adminv1.GetRunHistoryRequest]) (*connect.Response[adminv1.GetRunHistoryResponse], error) {
	item, err := s.store.GetRunHistory(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetRunHistoryResponse{Item: item}), nil
}

func (s *readService) ListRunSteps(ctx context.Context, req *connect.Request[adminv1.ListRunStepsRequest]) (*connect.Response[adminv1.ListRunStepsResponse], error) {
	items, page, err := s.store.ListRunSteps(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListRunStepsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) ListResources(ctx context.Context, req *connect.Request[adminv1.ListResourcesRequest]) (*connect.Response[adminv1.ListResourcesResponse], error) {
	items, page, err := s.store.ListResources(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListResourcesResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetResource(ctx context.Context, req *connect.Request[adminv1.GetResourceRequest]) (*connect.Response[adminv1.GetResourceResponse], error) {
	item, err := s.store.GetResource(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetResourceResponse{Item: item}), nil
}

func (s *readService) ListProviders(ctx context.Context, req *connect.Request[adminv1.ListProvidersRequest]) (*connect.Response[adminv1.ListProvidersResponse], error) {
	items, page, err := s.store.ListProviders(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListProvidersResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetProvider(ctx context.Context, req *connect.Request[adminv1.GetProviderRequest]) (*connect.Response[adminv1.GetProviderResponse], error) {
	item, err := s.store.GetProvider(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetProviderResponse{Item: item}), nil
}

func (s *readService) ListCredentials(ctx context.Context, req *connect.Request[adminv1.ListCredentialsRequest]) (*connect.Response[adminv1.ListCredentialsResponse], error) {
	items, page, err := s.store.ListCredentials(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListCredentialsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetCredential(ctx context.Context, req *connect.Request[adminv1.GetCredentialRequest]) (*connect.Response[adminv1.GetCredentialResponse], error) {
	item, err := s.store.GetCredential(ctx, req.Msg.GetTenantId(), req.Msg.GetKind(), req.Msg.GetName())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetCredentialResponse{Item: item}), nil
}

func (s *readService) ListPeriodicPrompts(ctx context.Context, req *connect.Request[adminv1.ListPeriodicPromptsRequest]) (*connect.Response[adminv1.ListPeriodicPromptsResponse], error) {
	items, page, err := s.store.ListPeriodicPrompts(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListPeriodicPromptsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetPeriodicPrompt(ctx context.Context, req *connect.Request[adminv1.GetPeriodicPromptRequest]) (*connect.Response[adminv1.GetPeriodicPromptResponse], error) {
	item, err := s.store.GetPeriodicPrompt(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetPeriodicPromptResponse{Item: item}), nil
}

func (s *readService) ListMemory(ctx context.Context, req *connect.Request[adminv1.ListMemoryRequest]) (*connect.Response[adminv1.ListMemoryResponse], error) {
	items, page, err := s.store.ListMemory(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListMemoryResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetMemory(ctx context.Context, req *connect.Request[adminv1.GetMemoryRequest]) (*connect.Response[adminv1.GetMemoryResponse], error) {
	item, err := s.store.GetMemory(ctx, req.Msg.GetSessionId(), req.Msg.GetKey())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetMemoryResponse{Item: item}), nil
}

func (s *readService) ListWaits(ctx context.Context, req *connect.Request[adminv1.ListWaitsRequest]) (*connect.Response[adminv1.ListWaitsResponse], error) {
	items, page, err := s.store.ListWaits(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListWaitsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetWait(ctx context.Context, req *connect.Request[adminv1.GetWaitRequest]) (*connect.Response[adminv1.GetWaitResponse], error) {
	item, err := s.store.GetWait(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetWaitResponse{Item: item}), nil
}

func (s *readService) ListJobs(ctx context.Context, req *connect.Request[adminv1.ListJobsRequest]) (*connect.Response[adminv1.ListJobsResponse], error) {
	items, page, err := s.store.ListJobs(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListJobsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetJob(ctx context.Context, req *connect.Request[adminv1.GetJobRequest]) (*connect.Response[adminv1.GetJobResponse], error) {
	item, err := s.store.GetJob(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetJobResponse{Item: item}), nil
}

func (s *readService) GetRuntimeHealth(ctx context.Context, req *connect.Request[adminv1.GetRuntimeHealthRequest]) (*connect.Response[adminv1.GetRuntimeHealthResponse], error) {
	h, err := s.store.GetRuntimeHealth(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetRuntimeHealthResponse{Health: h}), nil
}

func (s *readService) GetSessionTimeline(ctx context.Context, req *connect.Request[adminv1.GetSessionTimelineRequest]) (*connect.Response[adminv1.GetSessionTimelineResponse], error) {
	page := query.Page{}
	if p := req.Msg.GetPage(); p != nil {
		page = query.Page{Limit: p.GetLimit(), Cursor: p.GetCursor()}
	}
	items, info, err := s.store.GetSessionTimeline(ctx, req.Msg.GetSessionId(), page)
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetSessionTimelineResponse{Items: items, Page: query.PageInfoToProto(info)}), nil
}

func (s *readService) ListRelated(ctx context.Context, req *connect.Request[adminv1.ListRelatedRequest]) (*connect.Response[adminv1.ListRelatedResponse], error) {
	page := query.Page{}
	if p := req.Msg.GetPage(); p != nil {
		page = query.Page{Limit: p.GetLimit(), Cursor: p.GetCursor()}
	}
	items, info, err := s.store.ListRelated(ctx, req.Msg.GetCollection(), req.Msg.GetId(), req.Msg.GetRelation(), page)
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListRelatedResponse{Items: items, Page: query.PageInfoToProto(info)}), nil
}

func (s *readService) WhoAmI(ctx context.Context, _ *connect.Request[adminv1.WhoAmIRequest]) (*connect.Response[adminv1.WhoAmIResponse], error) {
	op, ok := operatorFrom(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing operator"))
	}
	return connect.NewResponse(&adminv1.WhoAmIResponse{
		Operator: &adminv1.OperatorIdentity{
			Id:            op.ID,
			Name:          op.Name,
			Role:          string(op.Role),
			Permissions:   authz.Permissions(op.Role),
			RevealEnabled: s.revealEnabled && authz.Can(op.Role, authz.CmdRevealSecret),
		},
	}), nil
}

func (s *readService) ListAuditEvents(ctx context.Context, req *connect.Request[adminv1.ListAuditEventsRequest]) (*connect.Response[adminv1.ListAuditEventsResponse], error) {
	items, page, err := s.store.ListAuditEvents(ctx, query.SearchFromProto(req.Msg.GetSearch()))
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.ListAuditEventsResponse{Items: items, Page: query.PageInfoToProto(page)}), nil
}

func (s *readService) GetAuditEvent(ctx context.Context, req *connect.Request[adminv1.GetAuditEventRequest]) (*connect.Response[adminv1.GetAuditEventResponse], error) {
	item, err := s.store.GetAuditEvent(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return connect.NewResponse(&adminv1.GetAuditEventResponse{Item: item}), nil
}

func mapErr(err error) error {
	var ve *query.ValidationError
	switch {
	case errors.As(err, &ve):
		return connect.NewError(connect.CodeInvalidArgument, errors.New(ve.Error()))
	case errors.Is(err, query.ErrInvalidLimit), errors.Is(err, query.ErrInvalidField),
		errors.Is(err, query.ErrInvalidOp), errors.Is(err, query.ErrInvalidSort),
		errors.Is(err, query.ErrTooManyFilters), errors.Is(err, query.ErrTooManySorts),
		errors.Is(err, query.ErrQueryTooLong), errors.Is(err, query.ErrBadCursor),
		errors.Is(err, query.ErrCursorMismatch), errors.Is(err, query.ErrBadValue),
		errors.Is(err, query.ErrUnknownCollection):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, uc.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	default:
		// Operator surface: include the error text so debugging is possible.
		// Secrets must never appear in store errors (no plaintext selected).
		return connect.NewError(connect.CodeInternal, err)
	}
}
