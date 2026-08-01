package http

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/flowwork"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
)

type flowHandler struct {
	store ultra.Store
	// flows owns invocation orchestration: the handler validates, authorizes,
	// and delegates, so provisioning and topology are never handler-local.
	flows *flowwork.Service
}

func flowProto(f ultra.Flow) *ultrav1.Flow {
	return &ultrav1.Flow{
		Id: string(f.ID), OrgId: string(f.OrgID), Name: f.Name,
		Version: int32(f.Version), DefinitionJson: string(f.Definition),
		CreatedAt: timestamppb.New(f.CreatedAt),
	}
}

func flowInvocationStateToProto(s ultra.FlowInvocationState) ultrav1.FlowInvocationState {
	switch s {
	case ultra.FlowInvocationPending:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_PENDING
	case ultra.FlowInvocationProvisioning:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_PROVISIONING
	case ultra.FlowInvocationRunning:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_RUNNING
	case ultra.FlowInvocationCancelling:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLING
	case ultra.FlowInvocationCompleted:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_COMPLETED
	case ultra.FlowInvocationFailed:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_FAILED
	case ultra.FlowInvocationCancelled:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_CANCELLED
	default:
		return ultrav1.FlowInvocationState_FLOW_INVOCATION_STATE_UNSPECIFIED
	}
}

// validationError renders structured field errors as a typed InvalidArgument
// with machine-readable details, so the CLI and both applications render the
// same field paths rather than parsing a message.
func validationError(verr *ultra.FlowValidationError) error {
	connectErr := connect.NewError(connect.CodeInvalidArgument, verr)
	for _, item := range verr.Errors {
		detail, err := connect.NewErrorDetail(&ultrav1.FlowFieldError{
			Path: item.Path, Code: item.Code, Message: item.Message,
		})
		if err != nil {
			continue
		}
		connectErr.AddDetail(detail)
	}
	return connectErr
}

func fieldErrorsToProto(verr *ultra.FlowValidationError) []*ultrav1.FlowFieldError {
	if verr == nil {
		return nil
	}
	out := make([]*ultrav1.FlowFieldError, 0, len(verr.Errors))
	for _, item := range verr.Errors {
		out = append(out, &ultrav1.FlowFieldError{Path: item.Path, Code: item.Code, Message: item.Message})
	}
	return out
}

func (h *flowHandler) PutFlow(ctx context.Context, req *connect.Request[ultrav1.PutFlowRequest]) (*connect.Response[ultrav1.PutFlowResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, org); err != nil {
		return nil, err
	}
	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name required"))
	}
	if _, verr := ultra.ValidateFlowDefinition([]byte(req.Msg.GetDefinitionJson())); verr != nil {
		return nil, validationError(verr)
	}
	if req.Msg.GetVersion() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("version cannot be negative"))
	}
	f, err := h.store.Org(org).Flows().Put(ctx, ultra.Flow{
		ID: ultra.FlowID(uuid.NewString()), OrgID: org, Name: name,
		Version: int(req.Msg.GetVersion()), Definition: []byte(req.Msg.GetDefinitionJson()),
	})
	if errors.Is(err, ultra.ErrAlreadyExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists,
			errors.New("that flow version already exists; flow versions are immutable"))
	}
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.PutFlowResponse{Flow: flowProto(f)}), nil
}

// ValidateFlow reports structured errors without persisting. It is the surface
// the CLI and both applications use to show a definition's problems before an
// author commits to storing a version.
func (h *flowHandler) ValidateFlow(ctx context.Context, req *connect.Request[ultrav1.ValidateFlowRequest]) (*connect.Response[ultrav1.ValidateFlowResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, org); err != nil {
		return nil, err
	}
	_, verr := ultra.ValidateFlowDefinition([]byte(req.Msg.GetDefinitionJson()))
	return connect.NewResponse(&ultrav1.ValidateFlowResponse{
		Valid: verr == nil, Errors: fieldErrorsToProto(verr),
	}), nil
}

func (h *flowHandler) GetFlow(ctx context.Context, req *connect.Request[ultrav1.GetFlowRequest]) (*connect.Response[ultrav1.GetFlowResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, org); err != nil {
		return nil, err
	}
	f, err := h.store.Org(org).Flows().Get(ctx, req.Msg.GetName(), int(req.Msg.GetVersion()))
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.GetFlowResponse{Flow: flowProto(f)}), nil
}

func (h *flowHandler) ListFlows(ctx context.Context, req *connect.Request[ultrav1.ListFlowsRequest]) (*connect.Response[ultrav1.ListFlowsResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, org); err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Flows().List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListFlowsResponse{}
	for _, f := range items {
		resp.Flows = append(resp.Flows, flowProto(f))
	}
	return connect.NewResponse(resp), nil
}

func (h *flowHandler) ListFlowVersions(ctx context.Context, req *connect.Request[ultrav1.ListFlowVersionsRequest]) (*connect.Response[ultrav1.ListFlowVersionsResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if _, err := requireMember(ctx, h.store, org); err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Flows().ListVersions(ctx, req.Msg.GetName())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if len(items) == 0 {
		return nil, errNotFound()
	}
	resp := &ultrav1.ListFlowVersionsResponse{}
	for _, f := range items {
		resp.Flows = append(resp.Flows, flowProto(f))
	}
	return connect.NewResponse(resp), nil
}

func (h *flowHandler) InvokeFlow(ctx context.Context, req *connect.Request[ultrav1.InvokeFlowRequest]) (*connect.Response[ultrav1.InvokeFlowResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	flow, err := h.store.Org(org).Flows().Get(ctx, req.Msg.GetName(), int(req.Msg.GetVersion()))
	if err != nil {
		return nil, mapStoreErr(err)
	}
	supplied := map[string]any{}
	if raw := req.Msg.GetParamsJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &supplied); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("params_json must be a JSON object"))
		}
	}
	inv, seq, verr, err := h.flows.Invoke(ctx, org, session, flow, supplied)
	if verr != nil {
		return nil, validationError(verr)
	}
	if err != nil {
		return nil, mapStoreErr(err)
	}
	full, err := h.invocationProto(ctx, org, inv)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	// run_ids is empty at accept time by design: a flow's agents start only
	// after its declared environments are ready, so returning ids here would
	// promise runs that must not exist yet. Clients follow the invocation.
	return connect.NewResponse(&ultrav1.InvokeFlowResponse{
		InvocationId: string(inv.ID), EventSeq: seq, Invocation: full,
		RunIds: runIDsOf(full),
	}), nil
}

func runIDsOf(inv *ultrav1.FlowInvocation) []string {
	out := make([]string, 0, len(inv.GetRuns()))
	for _, run := range inv.GetRuns() {
		out = append(out, run.GetRunId())
	}
	return out
}

// resolveInvocation finds an invocation in one of the caller's orgs. A missing
// invocation and one in another org answer identically.
func (h *flowHandler) resolveInvocation(ctx context.Context, id ultra.FlowInvocationID) (ultra.OrgID, ultra.FlowInvocation, error) {
	user, ok := userFrom(ctx)
	if !ok {
		return "", ultra.FlowInvocation{}, errUnauthenticated()
	}
	orgs, err := h.store.Orgs().ListForUser(ctx, user.ID)
	if err != nil {
		return "", ultra.FlowInvocation{}, mapStoreErr(err)
	}
	for _, org := range orgs {
		inv, err := h.store.Org(org.ID).Flows().GetInvocation(ctx, id)
		if err == nil {
			return org.ID, inv, nil
		}
		if !errors.Is(err, ultra.ErrNotFound) {
			return "", ultra.FlowInvocation{}, mapStoreErr(err)
		}
	}
	return "", ultra.FlowInvocation{}, errNotFound()
}

// invocationProto assembles the whole invocation view: state, frozen
// rendering, ordered progress, and the runs and environments it owns. Clients
// need it in one response because walking it request by request would race the
// live event stream.
func (h *flowHandler) invocationProto(ctx context.Context, org ultra.OrgID, inv ultra.FlowInvocation) (*ultrav1.FlowInvocation, error) {
	out := &ultrav1.FlowInvocation{
		Id: string(inv.ID), SessionId: string(inv.SessionID), FlowId: string(inv.FlowID),
		FlowName: inv.FlowName, FlowVersion: int32(inv.FlowVersion),
		ParamsJson: string(inv.Params), RenderedJson: string(inv.Rendered),
		State: flowInvocationStateToProto(inv.State), TerminalReason: inv.TerminalReason,
		Message: inv.Message, CreatedAt: timestamppb.New(inv.CreatedAt),
		UpdatedAt: timestamppb.New(inv.UpdatedAt),
	}
	flows := h.store.Org(org).Flows()
	progress, err := flows.Progress(ctx, inv.ID)
	if err != nil {
		return nil, err
	}
	for _, entry := range progress {
		out.Progress = append(out.Progress, &ultrav1.FlowInvocationProgressEntry{
			Seq: entry.Seq, Stage: entry.Stage, Key: entry.Key,
			Detail: entry.Detail, At: timestamppb.New(entry.At),
		})
	}
	runs, err := flows.InvocationRuns(ctx, inv.ID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		entry := &ultrav1.FlowInvocationRun{
			RunId: string(run.ID), AgentName: run.FlowAgentName,
			State: runStateToProto(run.State), Prompt: run.Prompt,
			CohortId: run.CohortID, CohortOrdinal: int32(run.CohortOrdinal),
		}
		if run.ParentRunID != nil {
			entry.ParentRunId = string(*run.ParentRunID)
		}
		out.Runs = append(out.Runs, entry)
	}
	envs, err := flows.InvocationEnvs(ctx, inv.ID)
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		out.Envs = append(out.Envs, &ultrav1.FlowInvocationEnv{
			EnvId: string(env.ID), EnvName: env.FlowEnvName,
			State: envStateToProto(env.State), Endpoint: env.Endpoint,
			FailureMessage: env.FailureMessage,
		})
	}
	return out, nil
}

func (h *flowHandler) GetFlowInvocation(ctx context.Context, req *connect.Request[ultrav1.GetFlowInvocationRequest]) (*connect.Response[ultrav1.GetFlowInvocationResponse], error) {
	org, inv, err := h.resolveInvocation(ctx, ultra.FlowInvocationID(req.Msg.GetInvocationId()))
	if err != nil {
		return nil, err
	}
	full, err := h.invocationProto(ctx, org, inv)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.GetFlowInvocationResponse{Invocation: full}), nil
}

func (h *flowHandler) ListFlowInvocations(ctx context.Context, req *connect.Request[ultrav1.ListFlowInvocationsRequest]) (*connect.Response[ultrav1.ListFlowInvocationsResponse], error) {
	session := ultra.SessionID(req.Msg.GetSessionId())
	org, _, err := resolveSessionOrg(ctx, h.store, session)
	if err != nil {
		return nil, err
	}
	items, err := h.store.Org(org).Flows().ListInvocations(ctx, session)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &ultrav1.ListFlowInvocationsResponse{}
	for _, inv := range items {
		full, err := h.invocationProto(ctx, org, inv)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		resp.Invocations = append(resp.Invocations, full)
	}
	return connect.NewResponse(resp), nil
}

// CancelFlowInvocation is idempotent: cancelling an already-terminal
// invocation returns its current state rather than an error, so a client that
// retries after a lost response is not told its own request failed.
func (h *flowHandler) CancelFlowInvocation(ctx context.Context, req *connect.Request[ultrav1.CancelFlowInvocationRequest]) (*connect.Response[ultrav1.CancelFlowInvocationResponse], error) {
	org, inv, err := h.resolveInvocation(ctx, ultra.FlowInvocationID(req.Msg.GetInvocationId()))
	if err != nil {
		return nil, err
	}
	if err := h.flows.RequestCancel(ctx, org, inv.ID); err != nil {
		return nil, mapStoreErr(err)
	}
	current, err := h.store.Org(org).Flows().GetInvocation(ctx, inv.ID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	full, err := h.invocationProto(ctx, org, current)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.CancelFlowInvocationResponse{Invocation: full}), nil
}
