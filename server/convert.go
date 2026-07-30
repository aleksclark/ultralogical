package server

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
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
// while the wire representation round-trips exactly.
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

// payloadFromDomain reconstructs the proto payload from kind + stored JSON.
func payloadFromDomain(kind string, payload []byte) (*ultrav1.EventPayload, error) {
	switch kind {
	case ultra.EventKindUserMessage:
		var m ultrav1.UserMessage
		if err := protojson.Unmarshal(payload, &m); err != nil {
			return nil, err
		}
		return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_UserMessage{UserMessage: &m}}, nil
	case ultra.EventKindAnnotation:
		var m ultrav1.Annotation
		if err := protojson.Unmarshal(payload, &m); err != nil {
			return nil, err
		}
		return &ultrav1.EventPayload{Payload: &ultrav1.EventPayload_Annotation{Annotation: &m}}, nil
	default:
		return nil, errors.New("unknown event kind " + kind)
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
