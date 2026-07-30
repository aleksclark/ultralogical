package loop

import (
	"context"
	"encoding/json"

	"charm.land/fantasy"
	ultra "github.com/aleksclark/ultralogical"
)

func memoryTools(store ultra.Store, run ultra.AgentRun) []fantasy.AgentTool {
	scope := store.Org(run.OrgID)
	type keyInput struct {
		Key string `json:"key"`
	}
	type setInput struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	get := fantasy.NewAgentTool("session_memory_get", "Read session-scoped durable memory.", func(ctx context.Context, in keyInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		e, err := scope.Memory().Get(ctx, run.SessionID, in.Key)
		if err != nil {
			return fantasy.NewTextErrorResponse("memory not found"), nil
		}
		return fantasy.NewTextResponse(string(e.Value)), nil
	})
	list := fantasy.NewAgentTool("session_memory_list", "List session-scoped durable memory.", func(ctx context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		items, err := scope.Memory().List(ctx, run.SessionID)
		if err != nil {
			return fantasy.NewTextErrorResponse("memory list failed"), nil
		}
		b, _ := json.Marshal(items)
		return fantasy.NewTextResponse(string(b)), nil
	})
	set := fantasy.NewAgentTool("session_memory_set", "Write session-scoped durable memory.", func(ctx context.Context, in setInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		b, err := json.Marshal(in.Value)
		if err != nil {
			return fantasy.NewTextErrorResponse("invalid value"), nil
		}
		err = store.Tx(ctx, func(txs ultra.Store) error {
			return txs.Org(run.OrgID).Memory().Set(ctx, ultra.SessionMemoryEntry{SessionID: run.SessionID, Key: in.Key, Value: b, UpdatedBy: ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)}})
		})
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		return fantasy.NewTextResponse("stored"), nil
	})
	del := fantasy.NewAgentTool("session_memory_delete", "Delete session-scoped durable memory.", func(ctx context.Context, in keyInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		err := store.Tx(ctx, func(txs ultra.Store) error { return txs.Org(run.OrgID).Memory().Delete(ctx, run.SessionID, in.Key) })
		if err != nil {
			return fantasy.NewTextErrorResponse("delete failed"), nil
		}
		return fantasy.NewTextResponse("deleted"), nil
	})
	return []fantasy.AgentTool{get, list, set, del}
}
