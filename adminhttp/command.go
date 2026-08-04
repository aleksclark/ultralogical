package adminhttp

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/aleksclark/ultracore/admin/command"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
)

type commandService struct {
	engine        *command.Engine
	revealEnabled bool
}

func (s *commandService) meta(ctx context.Context) (command.Meta, error) {
	op, ok := operatorFrom(ctx)
	if !ok {
		return command.Meta{}, connect.NewError(connect.CodeUnauthenticated, errors.New("missing operator"))
	}
	return command.Meta{
		Operator:  op,
		RequestID: requestID(ctx),
		SourceIP:  sourceIP(ctx),
		ReauthOK:  reauthOK(ctx),
	}, nil
}

func (s *commandService) mapResult(res *command.Result, err error) (*adminv1.CommandOutcome, error) {
	if err != nil {
		return nil, mapCmdErr(err)
	}
	return command.OutcomeProto(res), nil
}

func mapCmdErr(err error) error {
	code, msg := command.MapError(err)
	switch code {
	case "permission_denied":
		return connect.NewError(connect.CodePermissionDenied, errors.New(msg))
	case "failed_precondition":
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg))
	case "resource_exhausted":
		return connect.NewError(connect.CodeResourceExhausted, errors.New(msg))
	case "unauthenticated":
		return connect.NewError(connect.CodeUnauthenticated, errors.New(msg))
	case "not_found":
		return connect.NewError(connect.CodeNotFound, errors.New(msg))
	case "invalid_argument":
		// Distinguish validation vs internal when message looks internal.
		if strings.Contains(msg, "unavailable") || strings.Contains(msg, "audit failed") {
			return connect.NewError(connect.CodeInternal, errors.New(msg))
		}
		return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func (s *commandService) RetryQueueJob(ctx context.Context, req *connect.Request[adminv1.RetryQueueJobRequest]) (*connect.Response[adminv1.RetryQueueJobResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.RetryQueueJob(ctx, meta, req.Msg.GetOptions(), req.Msg.GetJobId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.RetryQueueJobResponse{Outcome: out}), nil
}

func (s *commandService) CancelQueueJob(ctx context.Context, req *connect.Request[adminv1.CancelQueueJobRequest]) (*connect.Response[adminv1.CancelQueueJobResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.CancelQueueJob(ctx, meta, req.Msg.GetOptions(), req.Msg.GetJobId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.CancelQueueJobResponse{Outcome: out}), nil
}

func (s *commandService) CancelRun(ctx context.Context, req *connect.Request[adminv1.CancelRunRequest]) (*connect.Response[adminv1.CancelRunResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.CancelRun(ctx, meta, req.Msg.GetOptions(), req.Msg.GetRunId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.CancelRunResponse{Outcome: out}), nil
}

func (s *commandService) AnswerAwait(ctx context.Context, req *connect.Request[adminv1.AnswerAwaitRequest]) (*connect.Response[adminv1.AnswerAwaitResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.AnswerAwait(ctx, meta, req.Msg.GetOptions(), req.Msg.GetRunId(), req.Msg.GetMessage())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.AnswerAwaitResponse{Outcome: out}), nil
}

func (s *commandService) ExpireAwait(ctx context.Context, req *connect.Request[adminv1.ExpireAwaitRequest]) (*connect.Response[adminv1.ExpireAwaitResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ExpireAwait(ctx, meta, req.Msg.GetOptions(), req.Msg.GetRunId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ExpireAwaitResponse{Outcome: out}), nil
}

func (s *commandService) ResourceReconcile(ctx context.Context, req *connect.Request[adminv1.ResourceReconcileRequest]) (*connect.Response[adminv1.ResourceReconcileResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResourceReconcile(ctx, meta, req.Msg.GetOptions(), req.Msg.GetResourceId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResourceReconcileResponse{Outcome: out}), nil
}

func (s *commandService) ResourceRestart(ctx context.Context, req *connect.Request[adminv1.ResourceRestartRequest]) (*connect.Response[adminv1.ResourceRestartResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResourceRestart(ctx, meta, req.Msg.GetOptions(), req.Msg.GetResourceId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResourceRestartResponse{Outcome: out}), nil
}

func (s *commandService) ResourceSuspend(ctx context.Context, req *connect.Request[adminv1.ResourceSuspendRequest]) (*connect.Response[adminv1.ResourceSuspendResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResourceSuspend(ctx, meta, req.Msg.GetOptions(), req.Msg.GetResourceId(), req.Msg.GetMessage())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResourceSuspendResponse{Outcome: out}), nil
}

func (s *commandService) ResourceTerminate(ctx context.Context, req *connect.Request[adminv1.ResourceTerminateRequest]) (*connect.Response[adminv1.ResourceTerminateResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResourceTerminate(ctx, meta, req.Msg.GetOptions(), req.Msg.GetResourceId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResourceTerminateResponse{Outcome: out}), nil
}

func (s *commandService) ResourceAdoptionProbe(ctx context.Context, req *connect.Request[adminv1.ResourceAdoptionProbeRequest]) (*connect.Response[adminv1.ResourceAdoptionProbeResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResourceAdoptionProbe(ctx, meta, req.Msg.GetOptions(), req.Msg.GetResourceId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResourceAdoptionProbeResponse{Outcome: out}), nil
}

func (s *commandService) ReprobeProvider(ctx context.Context, req *connect.Request[adminv1.ReprobeProviderRequest]) (*connect.Response[adminv1.ReprobeProviderResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ReprobeProvider(ctx, meta, req.Msg.GetOptions(), req.Msg.GetProviderId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ReprobeProviderResponse{Outcome: out}), nil
}

func (s *commandService) RevokeAPIKey(ctx context.Context, req *connect.Request[adminv1.RevokeAPIKeyRequest]) (*connect.Response[adminv1.RevokeAPIKeyResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.RevokeAPIKey(ctx, meta, req.Msg.GetOptions(), req.Msg.GetApiKeyId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.RevokeAPIKeyResponse{Outcome: out}), nil
}

func (s *commandService) DisableCredential(ctx context.Context, req *connect.Request[adminv1.DisableCredentialRequest]) (*connect.Response[adminv1.DisableCredentialResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.DisableCredential(ctx, meta, req.Msg.GetOptions(), req.Msg.GetTenantId(), req.Msg.GetKind(), req.Msg.GetName())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.DisableCredentialResponse{Outcome: out}), nil
}

func (s *commandService) PausePeriodicPrompt(ctx context.Context, req *connect.Request[adminv1.PausePeriodicPromptRequest]) (*connect.Response[adminv1.PausePeriodicPromptResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.PausePeriodicPrompt(ctx, meta, req.Msg.GetOptions(), req.Msg.GetPeriodicPromptId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.PausePeriodicPromptResponse{Outcome: out}), nil
}

func (s *commandService) ResumePeriodicPrompt(ctx context.Context, req *connect.Request[adminv1.ResumePeriodicPromptRequest]) (*connect.Response[adminv1.ResumePeriodicPromptResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ResumePeriodicPrompt(ctx, meta, req.Msg.GetOptions(), req.Msg.GetPeriodicPromptId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.ResumePeriodicPromptResponse{Outcome: out}), nil
}

func (s *commandService) DisconnectSubscriber(ctx context.Context, req *connect.Request[adminv1.DisconnectSubscriberRequest]) (*connect.Response[adminv1.DisconnectSubscriberResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.DisconnectSubscriber(ctx, meta, req.Msg.GetOptions(), req.Msg.GetSessionId(), req.Msg.GetSubscriberId())
	out, err := s.mapResult(res, err)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&adminv1.DisconnectSubscriberResponse{Outcome: out}), nil
}

func (s *commandService) ExportIncidentEvidence(ctx context.Context, req *connect.Request[adminv1.ExportIncidentEvidenceRequest]) (*connect.Response[adminv1.ExportIncidentEvidenceResponse], error) {
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.ExportIncidentEvidence(ctx, meta, req.Msg.GetOptions(), req.Msg.GetSessionId(), req.Msg.GetRunId(), req.Msg.GetResourceId(), req.Msg.GetMaxEvents())
	if err != nil {
		return nil, mapCmdErr(err)
	}
	return connect.NewResponse(&adminv1.ExportIncidentEvidenceResponse{
		Outcome:      command.OutcomeProto(res),
		EvidenceJson: res.EvidenceJSON,
	}), nil
}

func (s *commandService) RevealSecret(ctx context.Context, req *connect.Request[adminv1.RevealSecretRequest]) (*connect.Response[adminv1.RevealSecretResponse], error) {
	// Kill switch: when CORE_ADMIN_REVEAL_ENABLED is off the RPC is treated as
	// absent (Unimplemented) rather than a soft precondition failure.
	if !s.revealEnabled {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("secret reveal is disabled"))
	}
	meta, err := s.meta(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.engine.RevealSecret(ctx, meta, req.Msg.GetOptions(),
		req.Msg.GetSecretKind(), req.Msg.GetApiKeyId(), req.Msg.GetTenantId(),
		req.Msg.GetCredentialKind(), req.Msg.GetCredentialName())
	if err != nil {
		return nil, mapCmdErr(err)
	}
	out := &adminv1.RevealSecretResponse{Outcome: command.OutcomeProto(res)}
	// Only attach plaintext on successful non-dry-run execute.
	if !res.DryRun && res.Plaintext != "" && (res.Outcome == "ok" || res.Outcome == "already_applied") {
		out.Plaintext = res.Plaintext
		if !res.RevealExpires.IsZero() {
			out.ExpiresAt = timestamppb.New(res.RevealExpires)
		}
	}
	return connect.NewResponse(out), nil
}
