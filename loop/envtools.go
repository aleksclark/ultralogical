package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"charm.land/fantasy"
	ultra "github.com/aleksclark/ultralogical"
	"github.com/aleksclark/ultralogical/envwork"
	"github.com/aleksclark/ultralogical/mcp"
)

type ToolResolver interface {
	Tools(context.Context, ultra.AgentRun) ([]fantasy.AgentTool, error)
}
type EnvTools struct {
	Store ultra.Store
	Envs  *envwork.Service
}

func (r *EnvTools) deny(ctx context.Context, run ultra.AgentRun, tool string, envID *ultra.EnvID, reason string) fantasy.ToolResponse {
	payload, _ := json.Marshal(ultra.PermissionDeniedPayload{RunID: run.ID, Tool: tool, EnvID: envID, Reason: reason})
	_, _ = r.Store.Org(run.OrgID).Events().Append(ctx, run.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindPermissionDenied, Payload: payload})
	return fantasy.NewTextErrorResponse("permission denied")
}
func (r *EnvTools) Tools(ctx context.Context, run ultra.AgentRun) ([]fantasy.AgentTool, error) {
	envs, err := r.Store.Org(run.OrgID).Envs().List(ctx, run.SessionID)
	if err != nil {
		return nil, err
	}
	var tools []fantasy.AgentTool
	if run.Grants.AllowsTool("provision_env") {
		type input struct {
			Name             string `json:"name"`
			ProviderInstance string `json:"provider_instance,omitempty"`
			Image            string `json:"image,omitempty"`
		}
		tools = append(tools, fantasy.NewAgentTool("provision_env", "Provision a development environment.", func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !run.Grants.AllowsTool("provision_env") {
				return r.deny(ctx, run, "provision_env", nil, "tool not granted"), nil
			}
			id := run.ID
			env, _, err := r.Envs.Request(ctx, run.OrgID, run.SessionID, ultra.EnvSpec{Name: in.Name, Image: in.Image, Workdir: "/work"}, in.ProviderInstance, &id)
			if err != nil {
				return fantasy.NewTextErrorResponse("provision failed"), nil
			}
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				current, e := r.Store.Org(run.OrgID).Envs().Get(ctx, env.ID)
				if e != nil {
					return fantasy.NewTextErrorResponse("lookup failed"), nil
				}
				if current.State == ultra.EnvReady {
					b, _ := json.Marshal(map[string]string{"env_id": string(env.ID)})
					return fantasy.NewTextResponse(string(b)), nil
				}
				if current.State == ultra.EnvFailed {
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
	if run.Grants.AllowsTool("list_envs") {
		tools = append(tools, fantasy.NewAgentTool("list_envs", "List granted environments.", func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !run.Grants.AllowsTool("list_envs") {
				return r.deny(ctx, run, "list_envs", nil, "tool not granted"), nil
			}
			var allowed []ultra.DevEnv
			for _, e := range envs {
				if run.Grants.AllowsEnv(e.ID) {
					allowed = append(allowed, e)
				}
			}
			b, _ := json.Marshal(allowed)
			return fantasy.NewTextResponse(string(b)), nil
		}))
	}
	if run.Grants.AllowsTool("terminate_env") {
		type input struct {
			EnvID string `json:"env_id"`
		}
		tools = append(tools, fantasy.NewAgentTool("terminate_env", "Terminate a granted environment.", func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			id := ultra.EnvID(in.EnvID)
			if !run.Grants.AllowsTool("terminate_env") || !run.Grants.AllowsEnv(id) {
				return r.deny(ctx, run, "terminate_env", &id, "environment or tool not granted"), nil
			}
			if err := r.Envs.RequestTerminate(ctx, run.OrgID, id); err != nil {
				return fantasy.NewTextErrorResponse("terminate failed"), nil
			}
			return fantasy.NewTextResponse("termination requested"), nil
		}))
	}
	var ready []ultra.DevEnv
	for _, e := range envs {
		if e.State == ultra.EnvReady && run.Grants.AllowsEnv(e.ID) {
			ready = append(ready, e)
		}
	}
	for _, env := range ready {
		prefix := ""
		if len(ready) > 1 {
			prefix = "env:" + env.Spec.Name + "/"
		}
		// Discovery goes through the epoch-keyed client cache: a restarted
		// environment yields a fresh client and revokes the previous one,
		// so a rotated token can never be reused.
		client, e := r.Envs.ToolClient(ctx, env)
		if e == nil {
			var discovered []mcp.Tool
			discovered, e = client.Tools(ctx)
			if e == nil {
				names := make([]string, 0, len(discovered))
				for _, dt := range discovered {
					names = append(names, dt.Name)
					if !run.Grants.AllowsTool(dt.Name) {
						continue
					}
					tools = append(tools, newMCPTool(prefix+dt.Name, dt, client))
				}
				// Remember the toolset so a later step can still name
				// these tools if the environment disappears.
				r.Envs.RememberTools(env.ID, names)
				continue
			}
		}
		// The environment is marked ready but is unreachable: it died
		// between the last state write and this step. Dropping its tools
		// would silently shrink the model's capabilities mid-run, so the
		// previously discovered tools are offered as typed failures
		// instead. A step must be able to report that its environment is
		// gone, never quietly pretend it never had one.
		tools = append(tools, r.unreachableEnvTools(prefix, env, run.Grants, e)...)
	}
	return tools, nil
}

// unreachableEnvTools rebuilds the last known toolset for an environment that
// can no longer be reached, with every call failing in a typed way.
func (r *EnvTools) unreachableEnvTools(prefix string, env ultra.DevEnv, grants ultra.Grants, cause error) []fantasy.AgentTool {
	var tools []fantasy.AgentTool
	for _, name := range r.Envs.LastTools(env.ID) {
		if !grants.AllowsTool(name) {
			continue
		}
		reason := fmt.Sprintf("environment unavailable: %s is unreachable: %v", env.Spec.Name, cause)
		tools = append(tools, fantasy.NewAgentTool(prefix+name, "Unavailable: the environment is unreachable.",
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

// toolCallTimeout bounds one environment tool call. It matches Bezalel's
// long-command window: a wedged or vanished environment must produce a typed
// error response, never a hung step.
const toolCallTimeout = 5 * time.Minute

func (t *mcpTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, toolCallTimeout)
	defer cancel()
	result, err := t.client.Call(callCtx, t.remote, json.RawMessage(call.Input))
	if err != nil {
		// The environment is gone, revoked, or too slow. The step
		// continues with a typed error so the model can react.
		if ctx.Err() != nil {
			return fantasy.ToolResponse{}, ctx.Err()
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("environment unavailable: %v", err)), nil
	}
	resp := fantasy.NewTextResponse(result.Text)
	resp.IsError = result.IsError
	return resp, nil
}
