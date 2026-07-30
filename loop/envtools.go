package loop

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/fantasy"

	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envwork"
	"github.com/aleksclark/ultralogical/mcp"
)

// ToolResolver resolves dynamic tools for a run on every step.
type ToolResolver interface {
	Tools(context.Context, ultra.AgentRun) ([]fantasy.AgentTool, error)
}

// EnvTools exposes session environments and their Bezalel MCP tools.
type EnvTools struct {
	Store ultra.Store
	Envs  *envwork.Service
}

func (r *EnvTools) Tools(ctx context.Context, run ultra.AgentRun) ([]fantasy.AgentTool, error) {
	envs, err := r.Store.Org(run.OrgID).Envs().List(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	ready := make([]ultra.DevEnv, 0, len(envs))
	for _, e := range envs {
		if e.State == ultra.EnvReady {
			ready = append(ready, e)
		}
	}
	var tools []fantasy.AgentTool
	// Provision is always available.
	type provisionInput struct {
		Name             string `json:"name"`
		ProviderInstance string `json:"provider_instance,omitempty"`
		Image            string `json:"image,omitempty"`
	}
	tools = append(tools, fantasy.NewAgentTool("provision_env", "Provision a development environment for this session and wait until it is ready.", func(ctx context.Context, in provisionInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		id := run.ID
		env, _, err := r.Envs.Request(ctx, run.OrgID, run.SessionID, ultra.EnvSpec{Name: in.Name, Image: in.Image, Workdir: "/work"}, in.ProviderInstance, &id)
		if err != nil {
			return fantasy.NewTextErrorResponse("provision request failed"), nil
		}
		for {
			current, err := r.Store.Org(run.OrgID).Envs().Get(ctx, env.ID)
			if err != nil {
				return fantasy.NewTextErrorResponse("environment lookup failed"), nil
			}
			switch current.State {
			case ultra.EnvReady:
				b, _ := json.Marshal(map[string]string{"env_id": string(current.ID), "name": current.Spec.Name})
				return fantasy.NewTextResponse(string(b)), nil
			case ultra.EnvFailed:
				return fantasy.NewTextErrorResponse(current.FailureMessage), nil
			}
			select {
			case <-ctx.Done():
				return fantasy.ToolResponse{}, ctx.Err()
			default:
			}
		}
	}))
	type listInput struct{}
	tools = append(tools, fantasy.NewAgentTool("list_envs", "List development environments in this session.", func(context.Context, listInput, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		b, _ := json.Marshal(envs)
		return fantasy.NewTextResponse(string(b)), nil
	}))
	type terminateInput struct {
		EnvID string `json:"env_id"`
	}
	tools = append(tools, fantasy.NewAgentTool("terminate_env", "Terminate a development environment.", func(ctx context.Context, in terminateInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		if err := r.Envs.RequestTerminate(ctx, run.OrgID, ultra.EnvID(in.EnvID)); err != nil {
			return fantasy.NewTextErrorResponse("terminate failed"), nil
		}
		return fantasy.NewTextResponse("termination requested"), nil
	}))
	for _, env := range ready {
		clear, err := r.Envs.ClearTokenForTools(env)
		if err != nil {
			return nil, err
		}
		client := mcp.NewClient(env.Endpoint, clear)
		if err := client.Initialize(ctx); err != nil {
			continue
		}
		discovered, err := client.Tools(ctx)
		if err != nil {
			continue
		}
		for _, dt := range discovered {
			name := dt.Name
			if len(ready) > 1 {
				name = "env:" + env.Spec.Name + "/" + dt.Name
			}
			tools = append(tools, newMCPTool(name, dt, client))
		}
	}
	return tools, nil
}

type mcpTool struct {
	name   string
	info   fantasy.ToolInfo
	remote string
	client *mcp.Client
}

func newMCPTool(name string, t mcp.Tool, c *mcp.Client) fantasy.AgentTool {
	var schema map[string]any
	_ = json.Unmarshal(t.InputSchema, &schema)
	params, _ := schema["properties"].(map[string]any)
	var required []string
	if raw, ok := schema["required"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	return &mcpTool{name: name, remote: t.Name, client: c, info: fantasy.ToolInfo{Name: name, Description: t.Description, Parameters: params, Required: required}}
}
func (t *mcpTool) Info() fantasy.ToolInfo                     { return t.info }
func (t *mcpTool) ProviderOptions() fantasy.ProviderOptions   { return nil }
func (t *mcpTool) SetProviderOptions(fantasy.ProviderOptions) {}
func (t *mcpTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	result, err := t.client.Call(ctx, t.remote, json.RawMessage(call.Input))
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("environment unavailable: %v", err)), nil
	}
	resp := fantasy.NewTextResponse(result.Text)
	resp.IsError = result.IsError
	return resp, nil
}
