package http

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

// errNotFound is the uniform denial: missing rows and cross-tenant access
// are indistinguishable so resource existence never leaks across orgs.
func errNotFound() *connect.Error {
	return connect.NewError(connect.CodeNotFound, errors.New("not found"))
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, uc.ErrNotFound):
		return errNotFound()
	case errors.Is(err, uc.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("already exists"))
	case errors.Is(err, uc.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

func orgToProto(o uc.Org) *corev1.Org {
	return &corev1.Org{
		Id:        string(o.ID),
		Name:      o.Name,
		CreatedAt: timestamppb.New(o.CreatedAt),
	}
}

func roleToProto(r uc.OrgRole) corev1.OrgRole {
	switch r {
	case uc.OrgRoleOwner:
		return corev1.OrgRole_ORG_ROLE_OWNER
	case uc.OrgRoleAdmin:
		return corev1.OrgRole_ORG_ROLE_ADMIN
	case uc.OrgRoleMember:
		return corev1.OrgRole_ORG_ROLE_MEMBER
	default:
		return corev1.OrgRole_ORG_ROLE_UNSPECIFIED
	}
}

func roleFromProto(r corev1.OrgRole) (uc.OrgRole, bool) {
	switch r {
	case corev1.OrgRole_ORG_ROLE_OWNER:
		return uc.OrgRoleOwner, true
	case corev1.OrgRole_ORG_ROLE_ADMIN:
		return uc.OrgRoleAdmin, true
	case corev1.OrgRole_ORG_ROLE_MEMBER:
		return uc.OrgRoleMember, true
	default:
		return "", false
	}
}

func sessionToProto(s uc.Session) *corev1.Session {
	out := &corev1.Session{
		Id:        string(s.ID),
		OrgId:     string(s.OrgID),
		Title:     s.Title,
		CreatedAt: timestamppb.New(s.CreatedAt),
	}
	if s.ArchivedAt != nil {
		out.ArchivedAt = timestamppb.New(*s.ArchivedAt)
	}
	return out
}

func actorToProto(a uc.Actor) *corev1.Actor {
	out := &corev1.Actor{Id: a.ID}
	switch a.Type {
	case uc.ActorUser:
		out.Type = corev1.ActorType_ACTOR_TYPE_USER
	case uc.ActorAgent:
		out.Type = corev1.ActorType_ACTOR_TYPE_AGENT
	case uc.ActorSystem:
		out.Type = corev1.ActorType_ACTOR_TYPE_SYSTEM
	}
	return out
}

// payloadToDomain converts a proto EventPayload into (kind, JSON payload).
// The event log stores kind + protojson so the domain stays proto-agnostic
// while the wire representation round-trips exactly. Only human-appendable
// variants are accepted here; loop events are written by the worker.
func payloadToDomain(p *corev1.EventPayload) (kind string, payload []byte, err error) {
	if p == nil {
		return "", nil, errors.New("missing payload")
	}
	switch v := p.GetPayload().(type) {
	case *corev1.EventPayload_UserMessage:
		b, err := protojson.Marshal(v.UserMessage)
		return uc.EventKindUserMessage, b, err
	case *corev1.EventPayload_Annotation:
		b, err := protojson.Marshal(v.Annotation)
		return uc.EventKindAnnotation, b, err
	default:
		return "", nil, errors.New("unknown payload variant")
	}
}

// decodeAs unmarshals stored JSON into a proto message and wraps it.
func decodeAs[M proto.Message](payload []byte, m M, wrap func(M) *corev1.EventPayload) (*corev1.EventPayload, error) {
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, m); err != nil {
		return nil, err
	}
	return wrap(m), nil
}

// payloadFromDomain reconstructs the proto payload from kind + stored JSON.
// Domain payload JSON field names match proto field names, so protojson
// decodes them directly.
func payloadFromDomain(kind string, payload []byte) (*corev1.EventPayload, error) {
	switch kind {
	case uc.EventKindUserMessage:
		return decodeAs(payload, &corev1.UserMessage{}, func(m *corev1.UserMessage) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_UserMessage{UserMessage: m}}
		})
	case uc.EventKindAnnotation:
		return decodeAs(payload, &corev1.Annotation{}, func(m *corev1.Annotation) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_Annotation{Annotation: m}}
		})
	case uc.EventKindRunStarted:
		return decodeAs(payload, &corev1.RunStarted{}, func(m *corev1.RunStarted) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunStarted{RunStarted: m}}
		})
	case uc.EventKindStepStarted:
		return decodeAs(payload, &corev1.StepStarted{}, func(m *corev1.StepStarted) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_StepStarted{StepStarted: m}}
		})
	case uc.EventKindTextDelta:
		return decodeAs(payload, &corev1.TextDelta{}, func(m *corev1.TextDelta) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_TextDelta{TextDelta: m}}
		})
	case uc.EventKindReasoningDelta:
		return decodeAs(payload, &corev1.ReasoningDelta{}, func(m *corev1.ReasoningDelta) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ReasoningDelta{ReasoningDelta: m}}
		})
	case uc.EventKindToolCallStart:
		return decodeAs(payload, &corev1.ToolCallStarted{}, func(m *corev1.ToolCallStarted) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ToolCallStarted{ToolCallStarted: m}}
		})
	case uc.EventKindToolResult:
		return decodeAs(payload, &corev1.ToolResult{}, func(m *corev1.ToolResult) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ToolResult{ToolResult: m}}
		})
	case uc.EventKindStepFinished:
		return decodeAs(payload, &corev1.StepFinished{}, func(m *corev1.StepFinished) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_StepFinished{StepFinished: m}}
		})
	case uc.EventKindRunAwaiting:
		return decodeAs(payload, &corev1.RunAwaiting{}, func(m *corev1.RunAwaiting) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunAwaiting{RunAwaiting: m}}
		})
	case uc.EventKindRunCompleted:
		return decodeAs(payload, &corev1.RunCompleted{}, func(m *corev1.RunCompleted) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunCompleted{RunCompleted: m}}
		})
	case uc.EventKindRunFailed:
		return decodeAs(payload, &corev1.RunFailed{}, func(m *corev1.RunFailed) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunFailed{RunFailed: m}}
		})
	case uc.EventKindRunCancelled:
		return decodeAs(payload, &corev1.RunCancelled{}, func(m *corev1.RunCancelled) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunCancelled{RunCancelled: m}}
		})
	case uc.EventKindResourceRequested:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceRequested{ResourceRequested: m}}
		})
	case uc.EventKindResourceProvisioning:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceProvisioning{ResourceProvisioning: m}}
		})
	case uc.EventKindResourceReady:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceReady{ResourceReady: m}}
		})
	case uc.EventKindResourceSuspended:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceSuspended{ResourceSuspended: m}}
		})
	case uc.EventKindResourceFailed:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceFailed{ResourceFailed: m}}
		})
	case uc.EventKindResourceTerminating:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceTerminating{ResourceTerminating: m}}
		})
	case uc.EventKindResourceTerminated:
		return decodeAs(payload, &corev1.ResourceLifecycle{}, func(m *corev1.ResourceLifecycle) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ResourceTerminated{ResourceTerminated: m}}
		})
	case uc.EventKindExecPreviewRan:
		return decodeAs(payload, &corev1.ExecPreviewRan{}, func(m *corev1.ExecPreviewRan) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ExecPreviewRan{ExecPreviewRan: m}}
		})
	case uc.EventKindRunSpawned:
		return decodeAs(payload, &corev1.RunSpawned{}, func(m *corev1.RunSpawned) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_RunSpawned{RunSpawned: m}}
		})
	case uc.EventKindMemorySet:
		return decodeAs(payload, &corev1.MemoryChanged{}, func(m *corev1.MemoryChanged) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_MemorySet{MemorySet: m}}
		})
	case uc.EventKindMemoryDeleted:
		return decodeAs(payload, &corev1.MemoryChanged{}, func(m *corev1.MemoryChanged) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_MemoryDeleted{MemoryDeleted: m}}
		})
	case uc.EventKindHistoryCompacted:
		return decodeAs(payload, &corev1.HistoryCompacted{}, func(m *corev1.HistoryCompacted) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_HistoryCompacted{HistoryCompacted: m}}
		})
	case uc.EventKindModelFallback:
		return decodeAs(payload, &corev1.ModelFallback{}, func(m *corev1.ModelFallback) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_ModelFallback{ModelFallback: m}}
		})
	case uc.EventKindHookFired:
		return decodeAs(payload, &corev1.HookFired{}, func(m *corev1.HookFired) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_HookFired{HookFired: m}}
		})
	case uc.EventKindPeriodicPromptFired:
		return decodeAs(payload, &corev1.PeriodicPromptFired{}, func(m *corev1.PeriodicPromptFired) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_PeriodicPromptFired{PeriodicPromptFired: m}}
		})
	case uc.EventKindPermissionDenied:
		return decodeAs(payload, &corev1.PermissionDenied{}, func(m *corev1.PermissionDenied) *corev1.EventPayload {
			return &corev1.EventPayload{Payload: &corev1.EventPayload_PermissionDenied{PermissionDenied: m}}
		})
	default:
		return nil, errors.New("unknown event kind " + kind)
	}
}

func runStateToProto(s uc.RunState) corev1.RunState {
	switch s {
	case uc.RunPending:
		return corev1.RunState_RUN_STATE_PENDING
	case uc.RunRunning:
		return corev1.RunState_RUN_STATE_RUNNING
	case uc.RunAwaiting:
		return corev1.RunState_RUN_STATE_AWAITING
	case uc.RunCompleted:
		return corev1.RunState_RUN_STATE_COMPLETED
	case uc.RunFailed:
		return corev1.RunState_RUN_STATE_FAILED
	case uc.RunCancelled:
		return corev1.RunState_RUN_STATE_CANCELLED
	default:
		return corev1.RunState_RUN_STATE_UNSPECIFIED
	}
}

func runToProto(r uc.AgentRun) *corev1.AgentRun {
	out := &corev1.AgentRun{
		Id:          string(r.ID),
		SessionId:   string(r.SessionID),
		State:       runStateToProto(r.State),
		LoopKind:    r.LoopKind,
		LoopVersion: int32(r.LoopVersion),
		ModelConfig: &corev1.ModelConfig{
			Provider:   r.ModelConfig.Provider,
			ModelId:    r.ModelConfig.ModelID,
			Credential: r.ModelConfig.Credential,
		},
		Prompt:         r.Prompt,
		FailureReason:  r.FailureReason,
		FailureMessage: r.FailureMessage,
		CreatedAt:      timestamppb.New(r.CreatedAt),
		UpdatedAt:      timestamppb.New(r.UpdatedAt),
		Grants:         grantsToProto(r.Grants),
		ResultJson:     string(r.Result),
		CohortId:       r.CohortID,
		CohortOrdinal:  int32(r.CohortOrdinal),
	}
	if r.ParentRunID != nil {
		out.ParentRunId = string(*r.ParentRunID)
	}
	return out
}

// grantsToProto exposes a run's authority so clients can show what a child was
// actually allowed to do, rather than implying it inherited everything.
func grantsToProto(g uc.Grants) *corev1.Grants {
	return &corev1.Grants{Tools: g.Tools}
}

// grantsFromProto reads a caller-supplied grant request. It performs no
// authority checks itself: the caller must validate the result against what
// it is allowed to delegate.
func grantsFromProto(g *corev1.Grants) uc.Grants {
	if g == nil {
		return uc.DefaultGrants()
	}
	tools := g.GetTools()
	if len(tools) == 0 {
		return uc.DefaultGrants()
	}
	return uc.Grants{Tools: append([]string(nil), tools...)}
}

func waitToProto(w uc.RunWait, members []uc.RunWaitMember) *corev1.RunWait {
	out := &corev1.RunWait{
		Id:            w.ID,
		ParentRunId:   string(w.ParentRunID),
		StepIndex:     int32(w.StepIndex),
		ToolCallId:    w.ToolCallID,
		Kind:          w.Kind,
		State:         w.State,
		TimeoutPolicy: w.TimeoutPolicy,
		Deadline:      timestamppb.New(w.Deadline),
		ResultJson:    string(w.Result),
	}
	for _, m := range members {
		out.MemberRunIds = append(out.MemberRunIds, string(m.RunID))
	}
	return out
}

func credentialInfoToProto(c uc.CredentialInfo) *corev1.CredentialInfo {
	return &corev1.CredentialInfo{
		Kind:      c.Kind,
		Name:      c.Name,
		CreatedAt: timestamppb.New(c.CreatedAt),
		RotatedAt: timestamppb.New(c.RotatedAt),
	}
}

func eventToProto(e uc.Event) (*corev1.SessionEvent, error) {
	payload, err := payloadFromDomain(e.Kind, e.Payload)
	if err != nil {
		return nil, err
	}
	return &corev1.SessionEvent{
		SessionId: string(e.SessionID),
		Seq:       e.Seq,
		Ts:        timestamppb.New(e.TS),
		Actor:     actorToProto(e.Actor),
		Payload:   payload,
	}, nil
}
