package loop

import (
	"context"
	"encoding/json"

	"charm.land/fantasy"
	ultra "github.com/aleksclark/ultralogical"
)

// maxInlineMemoryValue bounds the value copied into an event. Larger values
// stay out of the log and are fetched through the API instead, so the event log
// does not become a second copy of session memory.
const maxInlineMemoryValue = 1024

// appendMemoryEvent records a memory change so every subscriber sees it.
func appendMemoryEvent(ctx context.Context, scope ultra.OrgScope, run ultra.AgentRun, kind, key string, value []byte, actor ultra.Actor) error {
	inline := value
	if len(inline) > maxInlineMemoryValue {
		inline = nil
	}
	payload := ultra.NewMemoryEventPayload(key, actor, inline)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = scope.Events().Append(ctx, run.SessionID, ultra.Event{
		Actor: actor, Kind: kind, Payload: encoded,
	})
	return err
}

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
		actor := ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)}
		err = store.Tx(ctx, func(txs ultra.Store) error {
			scope := txs.Org(run.OrgID)
			entry := ultra.SessionMemoryEntry{SessionID: run.SessionID, Key: in.Key, Value: b, UpdatedBy: actor}
			if e := scope.Memory().Set(ctx, entry); e != nil {
				return e
			}
			// The write and its event commit together. Memory is shared with
			// humans and other agents, so a silent write would leave every
			// subscriber with a stale view until they happened to re-read.
			return appendMemoryEvent(ctx, scope, run, ultra.EventKindMemorySet, entry.Key, b, actor)
		})
		if err != nil {
			return fantasy.NewTextErrorResponse(err.Error()), nil
		}
		return fantasy.NewTextResponse("stored"), nil
	})
	del := fantasy.NewAgentTool("session_memory_delete", "Delete session-scoped durable memory.", func(ctx context.Context, in keyInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
		actor := ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)}
		err := store.Tx(ctx, func(txs ultra.Store) error {
			scope := txs.Org(run.OrgID)
			if e := scope.Memory().Delete(ctx, run.SessionID, in.Key); e != nil {
				return e
			}
			return appendMemoryEvent(ctx, scope, run, ultra.EventKindMemoryDeleted, in.Key, nil, actor)
		})
		if err != nil {
			return fantasy.NewTextErrorResponse("delete failed"), nil
		}
		return fantasy.NewTextResponse("deleted"), nil
	})
	return []fantasy.AgentTool{get, list, set, del}
}
