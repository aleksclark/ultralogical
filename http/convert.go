package http

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
)

// errNotFound is the uniform denial: missing rows and cross-tenant access
// are indistinguishable so resource existence never leaks across orgs.
func errNotFound() *connect.Error {
	return connect.NewError(connect.CodeNotFound, errors.New("not found"))
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, ultra.ErrNotFound):
		return errNotFound()
	case errors.Is(err, ultra.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("already exists"))
	case errors.Is(err, ultra.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	default:
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

func orgToProto(o ultra.Org) *ultrav1.Org {
	return &ultrav1.Org{
		Id:        string(o.ID),
		Name:      o.Name,
		Plan:      o.Plan,
		CreatedAt: timestamppb.New(o.CreatedAt),
	}
}

func roleToProto(r ultra.OrgRole) ultrav1.OrgRole {
	switch r {
	case ultra.OrgRoleOwner:
		return ultrav1.OrgRole_ORG_ROLE_OWNER
	case ultra.OrgRoleAdmin:
		return ultrav1.OrgRole_ORG_ROLE_ADMIN
	case ultra.OrgRoleMember:
		return ultrav1.OrgRole_ORG_ROLE_MEMBER
	default:
		return ultrav1.OrgRole_ORG_ROLE_UNSPECIFIED
	}
}

func roleFromProto(r ultrav1.OrgRole) (ultra.OrgRole, bool) {
	switch r {
	case ultrav1.OrgRole_ORG_ROLE_OWNER:
		return ultra.OrgRoleOwner, true
	case ultrav1.OrgRole_ORG_ROLE_ADMIN:
		return ultra.OrgRoleAdmin, true
	case ultrav1.OrgRole_ORG_ROLE_MEMBER:
		return ultra.OrgRoleMember, true
	default:
		return "", false
	}
}

func sessionToProto(s ultra.Session) *ultrav1.Session {
	out := &ultrav1.Session{
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

func actorToProto(a ultra.Actor) *ultrav1.Actor {
	out := &ultrav1.Actor{Id: a.ID}
	switch a.Type {
	case ultra.ActorUser:
		out.Type = ultrav1.ActorType_ACTOR_TYPE_USER
	case ultra.ActorAgent:
		out.Type = ultrav1.ActorType_ACTOR_TYPE_AGENT
	case ultra.ActorSystem:
		out.Type = ultrav1.ActorType_ACTOR_TYPE_SYSTEM
	}
	return out
}

// payloadToDomain converts a proto EventPayload into (kind, JSON payload).
// The event log stores kind + protojson so the domain stays proto-agnostic
// while the wire representation round-trips exactly. Only human-appendable
// variants are accepted here; loop events are written by the worker.
func payloadToDomain(p *ultrav1.EventPayload) (kind string, payload []byte, err error) {
	if p == nil {
		return "", nil, errors.New("missing payload")
	}
	switch v := p.GetPayload().(type) {
	case *ultrav1.EventPayload_UserMessage:
		b, err := protojson.Marshal(v.UserMessage)
		return ultra.EventKindUserMessage, b, err
	case *ultrav1.EventPayload_Annotation:
		b, err := protojson.Marshal(v.Annotation)
		return ultra.EventKindAnnotation, b, err
	default:
		return "", nil, errors.New("unknown payload variant")
	}
}

// decodeAs unmarshals stored JSON into a proto message and wraps it.
func decodeAs[M proto.Message](payload []byte, m M, wrap func(M) *ultrav1.EventPayload) (*ultrav1.EventPayload, error) {
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, m); err != nil {
		return nil, err
	}
	return wrap(m), nil
}

// payloadFromDomain reconstructs the proto payload from kind + stored JSON.
// Domain payload JSON field names match proto field names, so protojson
// decodes them directly.
func payloadFromDomain(kind string, payload []byte) (*ultrav1.EventPayload, error) {
	switch kind {
	case ultra.EventKindUserMessage:
		return decodeAs(payload, &ultrav1.UserMessage{}, func(m *ultrav1.UserMessage) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_UserMessage{UserMessage: m}}
		})
	case ultra.EventKindAnnotation:
		return decodeAs(payload, &ultrav1.Annotation{}, func(m *ultrav1.Annotation) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_Annotation{Annotation: m}}
		})
	case ultra.EventKindRunStarted:
		return decodeAs(payload, &ultrav1.RunStarted{}, func(m *ultrav1.RunStarted) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunStarted{RunStarted: m}}
		})
	case ultra.EventKindStepStarted:
		return decodeAs(payload, &ultrav1.StepStarted{}, func(m *ultrav1.StepStarted) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_StepStarted{StepStarted: m}}
		})
	case ultra.EventKindTextDelta:
		return decodeAs(payload, &ultrav1.TextDelta{}, func(m *ultrav1.TextDelta) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_TextDelta{TextDelta: m}}
		})
	case ultra.EventKindReasoningDelta:
		return decodeAs(payload, &ultrav1.ReasoningDelta{}, func(m *ultrav1.ReasoningDelta) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ReasoningDelta{ReasoningDelta: m}}
		})
	case ultra.EventKindToolCallStart:
		return decodeAs(payload, &ultrav1.ToolCallStarted{}, func(m *ultrav1.ToolCallStarted) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ToolCallStarted{ToolCallStarted: m}}
		})
	case ultra.EventKindToolResult:
		return decodeAs(payload, &ultrav1.ToolResult{}, func(m *ultrav1.ToolResult) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ToolResult{ToolResult: m}}
		})
	case ultra.EventKindStepFinished:
		return decodeAs(payload, &ultrav1.StepFinished{}, func(m *ultrav1.StepFinished) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_StepFinished{StepFinished: m}}
		})
	case ultra.EventKindRunAwaiting:
		return decodeAs(payload, &ultrav1.RunAwaiting{}, func(m *ultrav1.RunAwaiting) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunAwaiting{RunAwaiting: m}}
		})
	case ultra.EventKindRunCompleted:
		return decodeAs(payload, &ultrav1.RunCompleted{}, func(m *ultrav1.RunCompleted) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunCompleted{RunCompleted: m}}
		})
	case ultra.EventKindRunFailed:
		return decodeAs(payload, &ultrav1.RunFailed{}, func(m *ultrav1.RunFailed) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunFailed{RunFailed: m}}
		})
	case ultra.EventKindRunCancelled:
		return decodeAs(payload, &ultrav1.RunCancelled{}, func(m *ultrav1.RunCancelled) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunCancelled{RunCancelled: m}}
		})
	case ultra.EventKindEnvRequested:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvRequested{EnvRequested: m}}
		})
	case ultra.EventKindEnvProvisioning:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvProvisioning{EnvProvisioning: m}}
		})
	case ultra.EventKindEnvReady:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvReady{EnvReady: m}}
		})
	case ultra.EventKindEnvFailed:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvFailed{EnvFailed: m}}
		})
	case ultra.EventKindEnvTerminating:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvTerminating{EnvTerminating: m}}
		})
	case ultra.EventKindEnvTerminated:
		return decodeAs(payload, &ultrav1.EnvLifecycle{}, func(m *ultrav1.EnvLifecycle) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_EnvTerminated{EnvTerminated: m}}
		})
	case ultra.EventKindExecPreviewRan:
		return decodeAs(payload, &ultrav1.ExecPreviewRan{}, func(m *ultrav1.ExecPreviewRan) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ExecPreviewRan{ExecPreviewRan: m}}
		})
	case ultra.EventKindParticipantJoined:
		return decodeAs(payload, &ultrav1.ParticipantTransition{}, func(m *ultrav1.ParticipantTransition) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ParticipantJoined{ParticipantJoined: m}}
		})
	case ultra.EventKindParticipantLeft:
		return decodeAs(payload, &ultrav1.ParticipantTransition{}, func(m *ultrav1.ParticipantTransition) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ParticipantLeft{ParticipantLeft: m}}
		})
	case ultra.EventKindParticipantIdle:
		return decodeAs(payload, &ultrav1.ParticipantTransition{}, func(m *ultrav1.ParticipantTransition) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ParticipantIdle{ParticipantIdle: m}}
		})
	case ultra.EventKindRunSpawned:
		return decodeAs(payload, &ultrav1.RunSpawned{}, func(m *ultrav1.RunSpawned) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_RunSpawned{RunSpawned: m}}
		})
	case ultra.EventKindMemorySet:
		return decodeAs(payload, &ultrav1.MemoryChanged{}, func(m *ultrav1.MemoryChanged) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_MemorySet{MemorySet: m}}
		})
	case ultra.EventKindMemoryDeleted:
		return decodeAs(payload, &ultrav1.MemoryChanged{}, func(m *ultrav1.MemoryChanged) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_MemoryDeleted{MemoryDeleted: m}}
		})
	case ultra.EventKindHistoryCompacted:
		return decodeAs(payload, &ultrav1.HistoryCompacted{}, func(m *ultrav1.HistoryCompacted) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_HistoryCompacted{HistoryCompacted: m}}
		})
	case ultra.EventKindModelFallback:
		return decodeAs(payload, &ultrav1.ModelFallback{}, func(m *ultrav1.ModelFallback) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_ModelFallback{ModelFallback: m}}
		})
	case ultra.EventKindHookFired:
		return decodeAs(payload, &ultrav1.HookFired{}, func(m *ultrav1.HookFired) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_HookFired{HookFired: m}}
		})
	case ultra.EventKindPeriodicPromptFired:
		return decodeAs(payload, &ultrav1.PeriodicPromptFired{}, func(m *ultrav1.PeriodicPromptFired) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_PeriodicPromptFired{PeriodicPromptFired: m}}
		})
	case ultra.EventKindPermissionDenied:
		return decodeAs(payload, &ultrav1.PermissionDenied{}, func(m *ultrav1.PermissionDenied) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_PermissionDenied{PermissionDenied: m}}
		})
	case ultra.EventKindFlowInvoked:
		return decodeAs(payload, &ultrav1.FlowInvoked{}, func(m *ultrav1.FlowInvoked) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_FlowInvoked{FlowInvoked: m}}
		})
	case ultra.EventKindFlowProgressed:
		return decodeAs(payload, &ultrav1.FlowInvocationProgressed{}, func(m *ultrav1.FlowInvocationProgressed) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_FlowInvocationProgressed{FlowInvocationProgressed: m}}
		})
	case ultra.EventKindFlowTerminal:
		return decodeAs(payload, &ultrav1.FlowInvocationTerminal{}, func(m *ultrav1.FlowInvocationTerminal) *ultrav1.EventPayload {
			return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_FlowInvocationTerminal{FlowInvocationTerminal: m}}
		})
	default:
		return nil, errors.New("unknown event kind " + kind)
	}
}

func runStateToProto(s ultra.RunState) ultrav1.RunState {
	switch s {
	case ultra.RunPending:
		return ultrav1.RunState_RUN_STATE_PENDING
	case ultra.RunRunning:
		return ultrav1.RunState_RUN_STATE_RUNNING
	case ultra.RunAwaiting:
		return ultrav1.RunState_RUN_STATE_AWAITING
	case ultra.RunCompleted:
		return ultrav1.RunState_RUN_STATE_COMPLETED
	case ultra.RunFailed:
		return ultrav1.RunState_RUN_STATE_FAILED
	case ultra.RunCancelled:
		return ultrav1.RunState_RUN_STATE_CANCELLED
	default:
		return ultrav1.RunState_RUN_STATE_UNSPECIFIED
	}
}

func runToProto(r ultra.AgentRun) *ultrav1.AgentRun {
	out := &ultrav1.AgentRun{
		Id:          string(r.ID),
		SessionId:   string(r.SessionID),
		State:       runStateToProto(r.State),
		LoopKind:    r.LoopKind,
		LoopVersion: int32(r.LoopVersion),
		ModelConfig: &ultrav1.ModelConfig{
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
		FlowAgentName:  r.FlowAgentName,
	}
	if r.ParentRunID != nil {
		out.ParentRunId = string(*r.ParentRunID)
	}
	if r.FlowInvocationID != nil {
		out.FlowInvocationId = string(*r.FlowInvocationID)
	}
	return out
}

// grantsToProto exposes a run's authority so clients can show what a child was
// actually allowed to do, rather than implying it inherited everything.
func grantsToProto(g ultra.Grants) *ultrav1.Grants {
	out := &ultrav1.Grants{
		Tools:       g.Tools,
		EnvAll:      g.EnvAll,
		MaySpawn:    g.MaySpawn,
		MaxChildren: int32(g.MaxChildren),
	}
	for _, id := range g.Envs {
		out.EnvIds = append(out.EnvIds, string(id))
	}
	return out
}

// grantsFromProto reads a caller-supplied grant request. It performs no
// authority checks itself: the caller must validate the result against what
// it is allowed to delegate.
func grantsFromProto(g *ultrav1.Grants) ultra.Grants {
	out := ultra.Grants{
		Tools:       g.GetTools(),
		EnvAll:      g.GetEnvAll(),
		MaySpawn:    g.GetMaySpawn(),
		MaxChildren: int(g.GetMaxChildren()),
	}
	for _, id := range g.GetEnvIds() {
		out.Envs = append(out.Envs, ultra.EnvID(id))
	}
	return out
}

func waitToProto(w ultra.RunWait, members []ultra.RunWaitMember) *ultrav1.RunWait {
	out := &ultrav1.RunWait{
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

func credentialInfoToProto(c ultra.CredentialInfo) *ultrav1.CredentialInfo {
	return &ultrav1.CredentialInfo{
		Kind:      c.Kind,
		Name:      c.Name,
		CreatedAt: timestamppb.New(c.CreatedAt),
		RotatedAt: timestamppb.New(c.RotatedAt),
	}
}

func eventToProto(e ultra.Event) (*ultrav1.SessionEvent, error) {
	payload, err := payloadFromDomain(e.Kind, e.Payload)
	if err != nil {
		return nil, err
	}
	return &ultrav1.SessionEvent{
		SessionId: string(e.SessionID),
		Seq:       e.Seq,
		Ts:        timestamppb.New(e.TS),
		Actor:     actorToProto(e.Actor),
		Payload:   payload,
	}, nil
}
