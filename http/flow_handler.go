package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"text/template"

	"connectrpc.com/connect"
	ultra "github.com/aleksclark/ultralogical"
	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/jobqueue"
	"github.com/aleksclark/ultralogical/loop"
	"github.com/google/uuid"
)

type flowHandler struct {
	store        ultra.Store
	enqueue      jobqueue.TxEnqueuer
	defaultModel ultra.ModelConfig
}
type flowDefinition struct {
	Params map[string]struct {
		Type     string `json:"type"`
		Required bool   `json:"required"`
		Default  any    `json:"default"`
	} `json:"params"`
	Agents map[string]struct {
		Prompt string            `json:"prompt"`
		Entry  bool              `json:"entry"`
		Model  ultra.ModelConfig `json:"model"`
		Tools  []string          `json:"tools"`
	} `json:"agents"`
}

func validateFlow(raw []byte) (flowDefinition, error) {
	var d flowDefinition
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, errors.New("definition_json is invalid")
	}
	entries := 0
	for name, a := range d.Agents {
		if _, err := template.New(name).Option("missingkey=error").Parse(a.Prompt); err != nil {
			return d, errors.New("agents." + name + ".prompt: invalid template")
		}
		if a.Entry {
			entries++
		}
	}
	if entries == 0 {
		return d, errors.New("agents: at least one entry agent is required")
	}
	return d, nil
}
func flowProto(f ultra.Flow) *ultrav1.Flow {
	return &ultrav1.Flow{Id: string(f.ID), OrgId: string(f.OrgID), Name: f.Name, Version: int32(f.Version), DefinitionJson: string(f.Definition)}
}
func (h *flowHandler) PutFlow(ctx context.Context, req *connect.Request[ultrav1.PutFlowRequest]) (*connect.Response[ultrav1.PutFlowResponse], error) {
	org := ultra.OrgID(req.Msg.GetOrgId())
	if err := requireAdmin(ctx, h.store, org); err != nil {
		return nil, err
	}
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name required"))
	}
	if _, err := validateFlow([]byte(req.Msg.GetDefinitionJson())); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	f, err := h.store.Org(org).Flows().Put(ctx, ultra.Flow{ID: ultra.FlowID(uuid.NewString()), OrgID: org, Name: req.Msg.GetName(), Definition: []byte(req.Msg.GetDefinitionJson())})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.PutFlowResponse{Flow: flowProto(f)}), nil
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
	def, err := validateFlow(flow.Definition)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	params := map[string]any{}
	if req.Msg.GetParamsJson() != "" {
		if err := json.Unmarshal([]byte(req.Msg.GetParamsJson()), &params); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("params_json invalid"))
		}
	}
	for name, p := range def.Params {
		if _, ok := params[name]; !ok {
			if p.Default != nil {
				params[name] = p.Default
			} else if p.Required {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("missing parameter "+name))
			}
		}
	}
	inv := ultra.FlowInvocation{ID: ultra.FlowInvocationID(uuid.NewString()), OrgID: org, SessionID: session, FlowID: flow.ID, FlowName: flow.Name, FlowVersion: flow.Version, Params: []byte(req.Msg.GetParamsJson())}
	var ids []string
	var seq int64
	err = h.store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(org)
		if err := scope.Flows().CreateInvocation(ctx, inv); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"invocation_id": inv.ID, "flow_name": flow.Name, "flow_version": flow.Version})
		seq, err = scope.Events().Append(ctx, session, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: "flow_invoked", Payload: payload})
		if err != nil {
			return err
		}
		for name, a := range def.Agents {
			if !a.Entry {
				continue
			}
			tmpl, _ := template.New(name).Option("missingkey=error").Parse(a.Prompt)
			var rendered bytes.Buffer
			if err := tmpl.Execute(&rendered, params); err != nil {
				return err
			}
			history, _ := loop.InitialEnvelope(rendered.String())
			cfg := a.Model
			if cfg.Provider == "" {
				cfg = h.defaultModel
			}
			run := ultra.AgentRun{ID: ultra.RunID(uuid.NewString()), SessionID: session, OrgID: org, FlowInvocationID: &inv.ID, LoopKind: loop.DefaultLoopKind, LoopVersion: loop.DefaultLoopVersion, ModelConfig: cfg, Prompt: rendered.String(), History: history, Grants: ultra.RootGrants()}
			if err := scope.Runs().Create(ctx, run); err != nil {
				return err
			}
			ids = append(ids, string(run.ID))
			if err := h.enqueue.EnqueueInTx(ctx, txs, loop.StepJob{RunID: string(run.ID), OrgID: string(org), SessionID: string(session), StepIndex: 0}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return connect.NewResponse(&ultrav1.InvokeFlowResponse{InvocationId: string(inv.ID), RunIds: ids, EventSeq: seq}), nil
}
