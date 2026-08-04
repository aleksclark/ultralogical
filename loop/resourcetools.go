package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"charm.land/fantasy"
	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/mcp"
	"github.com/aleksclark/ultracore/resourcework"
)

// ToolResolver resolves the tool surface for one run.
type ToolResolver interface {
	Tools(context.Context, uc.AgentRun) ([]fantasy.AgentTool, error)
}

// ResourceTools exposes resource lifecycle tools and ready-resource MCP tools.
type ResourceTools struct {
	Store     uc.Store
	Resources *resourcework.Service
}

func resourceDisplayName(r uc.Resource) string {
	if r.Kind == uc.ResourceKindDevEnv || r.Kind == "" {
		if s, err := uc.ParseDevEnvSpec(r.Spec); err == nil && s.Name != "" {
			return s.Name
		}
	}
	var probe struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(r.Spec, &probe)
	if probe.Name != "" {
		return probe.Name
	}
	return string(r.ID)
}

func (r *ResourceTools) deny(ctx context.Context, run uc.AgentRun, tool string, resourceID *uc.ResourceID, reason string) fantasy.ToolResponse {
	payload, _ := json.Marshal(uc.PermissionDeniedPayload{RunID: run.ID, Tool: tool, ResourceID: resourceID, Reason: reason})
	_, _ = r.Store.Tenant(run.TenantID).Events().Append(ctx, run.SessionID, uc.Event{Actor: uc.ActorSystem(), Kind: uc.EventKindPermissionDenied, Payload: payload})
	return fantasy.NewTextErrorResponse("permission denied")
}

func (r *ResourceTools) Tools(ctx context.Context, run uc.AgentRun) ([]fantasy.AgentTool, error) {
	resources, err := r.Store.Tenant(run.TenantID).Resources().List(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	var tools []fantasy.AgentTool

	// provision_resource and its provision_env alias share behavior.
	addProvision := func(name, desc string) {
		if !run.Policy.AllowsTool(name) {
			return
		}
		type input struct {
			Kind             string `json:"kind,omitempty"`
			Name             string `json:"name"`
			ProviderInstance string `json:"provider_instance,omitempty"`
			Image            string `json:"image,omitempty"`
		}
		tools = append(tools, fantasy.NewAgentTool(name, desc, func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !run.Policy.AllowsTool(name) {
				return r.deny(ctx, run, name, nil, "tool not granted"), nil
			}
			kind := uc.ResourceKind(in.Kind)
			if kind == "" {
				kind = uc.ResourceKindDevEnv
			}
			if !run.Policy.AllowsResourceKind(kind) {
				return r.deny(ctx, run, name, nil, "resource kind not permitted"), nil
			}
			var spec json.RawMessage
			if kind == uc.ResourceKindDevEnv {
				b, _ := json.Marshal(uc.DevEnvSpec{Name: in.Name, Image: in.Image, Workdir: "/work"})
				spec = b
			} else {
				b, _ := json.Marshal(map[string]string{"name": in.Name})
				spec = b
			}
			id := run.ID
			res, _, err := r.Resources.Request(ctx, run.TenantID, run.SessionID, kind, spec, in.ProviderInstance, &id)
			if err != nil {
				return fantasy.NewTextErrorResponse("provision failed"), nil
			}
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				current, e := r.Store.Tenant(run.TenantID).Resources().Get(ctx, res.ID)
				if e != nil {
					return fantasy.NewTextErrorResponse("lookup failed"), nil
				}
				if current.State == uc.ResourceReady {
					b, _ := json.Marshal(map[string]string{"resource_id": string(res.ID), "kind": string(current.Kind)})
					return fantasy.NewTextResponse(string(b)), nil
				}
				if current.State == uc.ResourceFailed {
					return fantasy.NewTextErrorResponse(current.FailureMessage), nil
				}
				select {
				case <-ctx.Done():
					return fantasy.ToolResponse{}, ctx.Err()
				case <-ticker.C:
				}
			}
		}))
	}
	addProvision("provision_resource", "Provision a session resource.")
	addProvision("provision_env", "Provision a development environment.")

	addList := func(name, desc string) {
		if !run.Policy.AllowsTool(name) {
			return
		}
		tools = append(tools, fantasy.NewAgentTool(name, desc, func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !run.Policy.AllowsTool(name) {
				return r.deny(ctx, run, name, nil, "tool not granted"), nil
			}
			b, _ := json.Marshal(resources)
			return fantasy.NewTextResponse(string(b)), nil
		}))
	}
	addList("list_resources", "List session resources.")
	addList("list_envs", "List session environments.")

	addTerminate := func(name, desc string) {
		if !run.Policy.AllowsTool(name) {
			return
		}
		type input struct {
			ResourceID string `json:"resource_id"`
			// EnvID is accepted for the provision_env-era alias.
			EnvID string `json:"env_id"`
		}
		tools = append(tools, fantasy.NewAgentTool(name, desc, func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			raw := in.ResourceID
			if raw == "" {
				raw = in.EnvID
			}
			id := uc.ResourceID(raw)
			if !run.Policy.AllowsTool(name) {
				return r.deny(ctx, run, name, &id, "resource or tool not granted"), nil
			}
			if err := r.Resources.RequestTerminate(ctx, run.TenantID, id); err != nil {
				return fantasy.NewTextErrorResponse("terminate failed"), nil
			}
			return fantasy.NewTextResponse("termination requested"), nil
		}))
	}
	addTerminate("terminate_resource", "Terminate a session resource.")
	addTerminate("terminate_env", "Terminate a session environment.")

	var ready []uc.Resource
	for _, e := range resources {
		if e.State == uc.ResourceReady && e.Endpoint != "" {
			ready = append(ready, e)
		}
	}
	// Namespace MCP tools when more than one ready resource of the same kind
	// publishes an endpoint (plan T2.4 / resources.md).
	byKind := map[uc.ResourceKind]int{}
	for _, e := range ready {
		byKind[e.Kind]++
	}
	for _, res := range ready {
		prefix := ""
		if byKind[res.Kind] > 1 {
			kind := string(res.Kind)
			if kind == "" {
				kind = string(uc.ResourceKindDevEnv)
			}
			prefix = kind + ":" + resourceDisplayName(res) + "/"
		}
		client, e := r.Resources.ToolClient(ctx, res)
		if e == nil {
			var discovered []mcp.Tool
			discovered, e = client.Tools(ctx)
			if e == nil {
				names := make([]string, 0, len(discovered))
				for _, dt := range discovered {
					names = append(names, dt.Name)
					if !run.Policy.AllowsTool(dt.Name) {
						continue
					}
					tools = append(tools, newMCPTool(prefix+dt.Name, dt, client, r, run, res.ID))
				}
				r.Resources.RememberTools(res.ID, names)
				continue
			}
		}
		tools = append(tools, r.unreachableResourceTools(prefix, res, run.Policy, e)...)
	}
	return tools, nil
}

func (r *ResourceTools) unreachableResourceTools(prefix string, res uc.Resource, grants uc.RunPolicy, cause error) []fantasy.AgentTool {
	var tools []fantasy.AgentTool
	for _, name := range r.Resources.LastTools(res.ID) {
		if !grants.AllowsTool(name) {
			continue
		}
		reason := fmt.Sprintf("resource unavailable: %s is unreachable: %v", resourceDisplayName(res), cause)
		tools = append(tools, fantasy.NewAgentTool(prefix+name, "Unavailable: the resource is unreachable.",
			func(context.Context, map[string]any, fantasy.ToolCall) (fantasy.ToolResponse, error) {
				return fantasy.NewTextErrorResponse(reason), nil
			}))
	}
	return tools
}

type mcpTool struct {
	name   string
	info   fantasy.ToolInfo
	remote string
	client *mcp.Client
	tools  *ResourceTools
	run    uc.AgentRun
	resID  uc.ResourceID
}

func newMCPTool(name string, t mcp.Tool, c *mcp.Client, owner *ResourceTools, run uc.AgentRun, resID uc.ResourceID) fantasy.AgentTool {
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
	return &mcpTool{
		name: name, remote: t.Name, client: c, tools: owner, run: run, resID: resID,
		info: fantasy.ToolInfo{Name: name, Description: t.Description, Parameters: params, Required: required},
	}
}
func (t *mcpTool) Info() fantasy.ToolInfo                     { return t.info }
func (t *mcpTool) ProviderOptions() fantasy.ProviderOptions   { return nil }
func (t *mcpTool) SetProviderOptions(fantasy.ProviderOptions) {}

const toolCallTimeout = 5 * time.Minute

func (t *mcpTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if t.tools != nil {
		if !t.run.Policy.AllowsTool(t.remote) {
			return t.tools.deny(ctx, t.run, t.remote, nil, "tool not granted"), nil
		}
		res, err := t.tools.Store.Tenant(t.run.TenantID).Resources().Get(ctx, t.resID)
		if err != nil || res.State != uc.ResourceReady {
			return fantasy.NewTextErrorResponse("resource unavailable"), nil
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, toolCallTimeout)
	defer cancel()
	result, err := t.client.Call(callCtx, t.remote, json.RawMessage(call.Input))
	if err != nil {
		if ctx.Err() != nil {
			return fantasy.ToolResponse{}, ctx.Err()
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("resource unavailable: %v", err)), nil
	}
	resp := fantasy.NewTextResponse(result.Text)
	resp.IsError = result.IsError
	return resp, nil
}
