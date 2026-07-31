package loop

import (
	"context"
	"encoding/json"

	ultra "github.com/aleksclark/ultralogical"
)

// resolveChildWaits resumes parents exactly once when all members are terminal.
func (w *StepWorker) resolveChildWaits(ctx context.Context, txs ultra.Store, child ultra.AgentRun) error {
	scope := txs.Org(child.OrgID)
	waits, err := scope.Waits().ListOpenForChild(ctx, child.ID)
	if err != nil {
		return err
	}
	for _, wait := range waits {
		members, err := scope.Waits().Members(ctx, wait.ID)
		if err != nil {
			return err
		}
		results := make([]map[string]any, 0, len(members))
		all := true
		for _, member := range members {
			run, e := scope.Runs().Get(ctx, member.RunID)
			if e != nil || !run.State.Terminal() {
				all = false
				break
			}
			results = append(results, map[string]any{"run_id": run.ID, "state": run.State, "result": run.Result})
		}
		if !all {
			continue
		}
		raw, _ := json.Marshal(results)
		changed, err := scope.Waits().Resolve(ctx, wait.ID, raw)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		parent, err := scope.Runs().GetForUpdate(ctx, wait.ParentRunID)
		if err != nil {
			return err
		}
		history, err := AppendUserMessage(parent.History, "Child agent results: "+string(raw))
		if err != nil {
			return err
		}
		if err := scope.Runs().SetHistory(ctx, parent.ID, history); err != nil {
			return err
		}
		if err := scope.Runs().SetState(ctx, parent.ID, ultra.RunRunning, "", ""); err != nil {
			return err
		}
		steps, err := scope.Runs().Steps(ctx, parent.ID)
		if err != nil {
			return err
		}
		next := 0
		for _, s := range steps {
			if s.StepIndex >= next {
				next = s.StepIndex + 1
			}
		}
		if err := w.Enqueue.EnqueueInTx(ctx, txs, StepJob{RunID: string(parent.ID), OrgID: string(parent.OrgID), SessionID: string(parent.SessionID), StepIndex: next}); err != nil {
			return err
		}
	}
	return nil
}
