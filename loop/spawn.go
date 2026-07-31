package loop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"charm.land/fantasy"
	ultra "github.com/aleksclark/ultralogical"
	"github.com/google/uuid"
)

func (w *StepWorker) spawnTools(run ultra.AgentRun, job StepJob, rec *stepRecorder) []fantasy.AgentTool {
	if !run.Grants.MaySpawn || !run.Grants.AllowsTool("spawn_agent") {
		return nil
	}
	type input struct {
		Prompt      string   `json:"prompt"`
		Tools       []string `json:"tools,omitempty"`
		EnvIDs      []string `json:"env_ids,omitempty"`
		MaySpawn    bool     `json:"may_spawn,omitempty"`
		MaxChildren int      `json:"max_children,omitempty"`
	}
	tool := fantasy.NewAgentTool("spawn_agent", "Spawn a child agent with narrower grants.", func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		grants := ultra.Grants{Tools: in.Tools, MaySpawn: in.MaySpawn, MaxChildren: in.MaxChildren}
		for _, id := range in.EnvIDs {
			grants.Envs = append(grants.Envs, ultra.EnvID(id))
		}
		if !grants.SubsetOf(run.Grants) {
			return w.permissionDenied(ctx, run, "spawn_agent", nil, "requested grants exceed parent"), nil
		}
		count, err := w.Store.Org(run.OrgID).Runs().CountChildren(ctx, run.ID)
		if err != nil {
			return fantasy.NewTextErrorResponse("child count failed"), nil
		}
		if count >= run.Grants.MaxChildren {
			return w.permissionDenied(ctx, run, "spawn_agent", nil, "max children reached"), nil
		}
		history, _ := InitialEnvelope(in.Prompt)
		child := ultra.AgentRun{ID: ultra.RunID(uuid.NewString()), SessionID: run.SessionID, OrgID: run.OrgID, ParentRunID: &run.ID, Grants: grants, LoopKind: run.LoopKind, LoopVersion: run.LoopVersion, ModelConfig: run.ModelConfig, Prompt: in.Prompt, History: history}
		err = w.Store.Tx(ctx, func(txs ultra.Store) error {
			scope := txs.Org(run.OrgID)
			if err := scope.Runs().Create(ctx, child); err != nil {
				return err
			}
			payload, _ := json.Marshal(ultra.RunSpawnedPayload{ParentRunID: run.ID, ChildRunID: child.ID})
			if _, err := scope.Events().Append(ctx, run.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)}, Kind: ultra.EventKindRunSpawned, Payload: payload}); err != nil {
				return err
			}
			return w.Enqueue.EnqueueInTx(ctx, txs, StepJob{RunID: string(child.ID), OrgID: job.OrgID, SessionID: job.SessionID, StepIndex: 0})
		})
		if err != nil {
			return fantasy.NewTextErrorResponse("spawn failed"), nil
		}
		b, _ := json.Marshal(map[string]string{"run_id": string(child.ID)})
		return fantasy.NewTextResponse(string(b)), nil
	})
	type waitInput struct {
		RunIDs  []string `json:"run_ids"`
		Timeout string   `json:"timeout,omitempty"`
	}
	wait := fantasy.NewAgentTool("wait_for_agents", "Pause until child agents finish.", func(_ context.Context, in waitInput, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
		timeout, _ := time.ParseDuration(in.Timeout)
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		rec.waitRunIDs = nil
		for _, id := range in.RunIDs {
			rec.waitRunIDs = append(rec.waitRunIDs, ultra.RunID(id))
		}
		rec.waitToolCallID = call.ID
		rec.waitTimeout = timeout
		resp := fantasy.NewTextResponse("waiting for child agents")
		resp.StopTurn = true
		return resp, nil
	})
	return []fantasy.AgentTool{tool, wait}
}
func (w *StepWorker) permissionDenied(ctx context.Context, run ultra.AgentRun, tool string, envID *ultra.EnvID, reason string) fantasy.ToolResponse {
	payload, _ := json.Marshal(ultra.PermissionDeniedPayload{RunID: run.ID, Tool: tool, EnvID: envID, Reason: reason})
	_, _ = w.Store.Org(run.OrgID).Events().Append(ctx, run.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindPermissionDenied, Payload: payload})
	return fantasy.NewTextErrorResponse("permission denied")
}

var _ = errors.New
