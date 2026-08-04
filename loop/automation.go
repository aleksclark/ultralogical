package loop

import (
	"context"
	"encoding/json"

	uc "github.com/aleksclark/ultracore"
)

// AccountStepCost is the built-in cost-accounting hook. It folds usage into
// durable session memory and emits a visible HookFired event.
func AccountStepCost(ctx context.Context, store uc.Store, run uc.AgentRun, step uc.RunStep) error {
	return store.Tx(ctx, func(txs uc.Store) error {
		scope := txs.Tenant(run.TenantID)
		value, _ := json.Marshal(map[string]int64{"input_tokens": step.TokensIn, "output_tokens": step.TokensOut})
		if err := scope.Memory().Set(ctx, uc.SessionMemoryEntry{SessionID: run.SessionID, Key: "system.cost.latest", Value: value, UpdatedBy: uc.ActorSystem()}); err != nil {
			return err
		}
		payload, _ := json.Marshal(uc.HookFiredPayload{Hook: "cost-accounting", RunID: run.ID})
		_, err := scope.Events().Append(ctx, run.SessionID, uc.Event{Actor: uc.ActorSystem(), Kind: uc.EventKindHookFired, Payload: payload})
		return err
	})
}
