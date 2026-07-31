package loop

import (
	"context"
	"encoding/json"

	ultra "github.com/aleksclark/ultralogical"
)

// AccountStepCost is the built-in cost-accounting hook. It folds usage into
// durable session memory and emits a visible HookFired event.
func AccountStepCost(ctx context.Context, store ultra.Store, run ultra.AgentRun, step ultra.RunStep) error {
	return store.Tx(ctx, func(txs ultra.Store) error {
		scope := txs.Org(run.OrgID)
		value, _ := json.Marshal(map[string]int64{"input_tokens": step.TokensIn, "output_tokens": step.TokensOut})
		if err := scope.Memory().Set(ctx, ultra.SessionMemoryEntry{SessionID: run.SessionID, Key: "system.cost.latest", Value: value, UpdatedBy: ultra.Actor{Type: ultra.ActorSystem}}); err != nil {
			return err
		}
		payload, _ := json.Marshal(ultra.HookFiredPayload{Hook: "cost-accounting", RunID: run.ID})
		_, err := scope.Events().Append(ctx, run.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindHookFired, Payload: payload})
		return err
	})
}
