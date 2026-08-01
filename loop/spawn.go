package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"charm.land/fantasy"
	"github.com/google/uuid"

	ultra "github.com/aleksclark/ultralogical"
)

// defaultWaitTimeout bounds a wait that names no timeout, so a parent can
// never park forever on a child that will not finish.
const defaultWaitTimeout = 5 * time.Minute

// maxCohortSize bounds one fan-out call independently of max_children, so a
// single tool call cannot enqueue unbounded work.
const maxCohortSize = 16

// waitOnAllChildren lets wait_for_agents name every child of the calling run
// without the model having to echo back generated identifiers.
const waitOnAllChildren = "*"

// spawnKey identifies the tool call that created a child: the same parent,
// step, and model tool-call id always produce the same key, which is what
// makes spawning idempotent under queue redelivery.
func spawnKey(parent ultra.RunID, stepIndex int, toolCallID string) string {
	return fmt.Sprintf("%s:%d:%s", parent, stepIndex, toolCallID)
}

// childSpec is one requested child, shared by spawn_agent and
// run_agent_cohort so both produce identical children.
type childSpec struct {
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
	EnvIDs      []string `json:"env_ids,omitempty"`
	MaySpawn    bool     `json:"may_spawn,omitempty"`
	MaxChildren int      `json:"max_children,omitempty"`
}

func (s childSpec) grants() ultra.Grants {
	g := ultra.Grants{Tools: s.Tools, MaySpawn: s.MaySpawn, MaxChildren: s.MaxChildren}
	for _, id := range s.EnvIDs {
		g.Envs = append(g.Envs, ultra.EnvID(id))
	}
	return g
}

// spawnOutcome is what a spawn tool returns to the model.
type spawnOutcome struct {
	RunID    ultra.RunID `json:"run_id"`
	Adopted  bool        `json:"adopted,omitempty"`
	CohortID string      `json:"cohort_id,omitempty"`
	Ordinal  int         `json:"ordinal,omitempty"`
}

func (w *StepWorker) spawnTools(run ultra.AgentRun, job StepJob, rec *stepRecorder) []fantasy.AgentTool {
	if !run.Grants.MaySpawn || !run.Grants.AllowsTool("spawn_agent") {
		return nil
	}
	tools := []fantasy.AgentTool{w.spawnAgentTool(run, job), w.waitForAgentsTool(run, rec)}
	if run.Grants.AllowsTool("run_agent_cohort") {
		tools = append(tools, w.runAgentCohortTool(run, job, rec))
	}
	return tools
}

// spawnAgentTool creates one child agent with strictly narrower authority.
func (w *StepWorker) spawnAgentTool(run ultra.AgentRun, job StepJob) fantasy.AgentTool {
	return fantasy.NewAgentTool("spawn_agent", "Spawn a child agent with narrower grants.",
		func(ctx context.Context, in childSpec, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// Re-check at dispatch: discovery-time filtering is a
			// convenience, but authority is decided here.
			if !run.Grants.MaySpawn || !run.Grants.AllowsTool("spawn_agent") {
				return w.permissionDenied(ctx, run, "spawn_agent", nil, "tool not granted"), nil
			}
			child, adopted, err := w.spawnChild(ctx, run, job, spawnRequest{spec: in, toolCallID: call.ID})
			if err != nil {
				var denied *deniedError
				if errors.As(err, &denied) {
					return w.permissionDenied(ctx, run, "spawn_agent", nil, denied.reason), nil
				}
				return fantasy.NewTextErrorResponse("spawn failed"), nil
			}
			b, _ := json.Marshal(spawnOutcome{RunID: child.ID, Adopted: adopted})
			return fantasy.NewTextResponse(string(b)), nil
		})
}

// waitForAgentsTool parks the parent until the named children are terminal. It
// holds no worker: the run leaves the queue entirely and is resumed by
// whichever path closes the wait.
func (w *StepWorker) waitForAgentsTool(run ultra.AgentRun, rec *stepRecorder) fantasy.AgentTool {
	type waitInput struct {
		RunIDs        []string `json:"run_ids"`
		Timeout       string   `json:"timeout,omitempty"`
		TimeoutPolicy string   `json:"timeout_policy,omitempty"`
	}
	return fantasy.NewAgentTool("wait_for_agents", "Pause until the named child agents finish.",
		func(ctx context.Context, in waitInput, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(in.RunIDs) == 0 {
				return fantasy.NewTextErrorResponse("wait_for_agents requires at least one run id"), nil
			}
			// A parent may only wait on runs it actually parented: waiting on
			// an arbitrary run would leak both its existence and its result.
			children, err := w.Store.Org(run.OrgID).Runs().Children(ctx, run.ID)
			if err != nil {
				return fantasy.NewTextErrorResponse("wait failed"), nil
			}
			owned := map[ultra.RunID]bool{}
			for _, c := range children {
				owned[c.ID] = true
			}
			ids := make([]ultra.RunID, 0, len(in.RunIDs))
			for _, raw := range in.RunIDs {
				// "*" waits on every child this run has spawned so far, which
				// a model can express without knowing generated run ids.
				if raw == waitOnAllChildren {
					for _, c := range children {
						ids = append(ids, c.ID)
					}
					continue
				}
				id := ultra.RunID(raw)
				if !owned[id] {
					return w.permissionDenied(ctx, run, "wait_for_agents", nil, "run is not a child of this agent"), nil
				}
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				return fantasy.NewTextErrorResponse("wait_for_agents matched no child agents"), nil
			}
			// Deduplicate: a wait may name a child once, and its member rows
			// are keyed by (wait, run).
			seen := map[ultra.RunID]bool{}
			unique := ids[:0]
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				unique = append(unique, id)
			}
			ids = unique
			rec.waitRunIDs = ids
			rec.waitToolCallID = call.ID
			rec.waitTimeout = parseWaitTimeout(in.Timeout)
			rec.waitKind = ultra.WaitKindWait
			rec.waitPolicy = normalizeTimeoutPolicy(in.TimeoutPolicy)
			resp := fantasy.NewTextResponse("waiting for child agents")
			resp.StopTurn = true
			return resp, nil
		})
}

// runAgentCohortTool is server-side fan-out/fan-in: one tool call spawns every
// child and installs the wait that collects them. The model neither polls nor
// sequences the children itself.
func (w *StepWorker) runAgentCohortTool(run ultra.AgentRun, job StepJob, rec *stepRecorder) fantasy.AgentTool {
	type cohortInput struct {
		Specs         []childSpec `json:"specs"`
		Timeout       string      `json:"timeout,omitempty"`
		TimeoutPolicy string      `json:"timeout_policy,omitempty"`
	}
	return fantasy.NewAgentTool("run_agent_cohort", "Run several child agents concurrently and collect their results.",
		func(ctx context.Context, in cohortInput, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !run.Grants.MaySpawn || !run.Grants.AllowsTool("run_agent_cohort") {
				return w.permissionDenied(ctx, run, "run_agent_cohort", nil, "tool not granted"), nil
			}
			if len(in.Specs) == 0 {
				return fantasy.NewTextErrorResponse("run_agent_cohort requires at least one spec"), nil
			}
			if len(in.Specs) > maxCohortSize {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("cohort size %d exceeds the limit of %d", len(in.Specs), maxCohortSize)), nil
			}
			// The cohort id is derived from the originating tool call, so a
			// redelivered step re-derives the same id and adopts the same
			// children instead of launching a second cohort.
			cohortID := uuid.NewSHA1(cohortNamespace, []byte(spawnKey(run.ID, job.StepIndex, call.ID))).String()
			ids := make([]ultra.RunID, 0, len(in.Specs))
			for i, spec := range in.Specs {
				child, _, err := w.spawnChild(ctx, run, job, spawnRequest{
					spec:       spec,
					toolCallID: fmt.Sprintf("%s#%d", call.ID, i),
					cohortID:   cohortID,
					ordinal:    i,
				})
				if err != nil {
					var denied *deniedError
					if errors.As(err, &denied) {
						return w.permissionDenied(ctx, run, "run_agent_cohort", nil, denied.reason), nil
					}
					return fantasy.NewTextErrorResponse("cohort spawn failed"), nil
				}
				ids = append(ids, child.ID)
			}
			rec.waitRunIDs = ids
			rec.waitToolCallID = call.ID
			rec.waitTimeout = parseWaitTimeout(in.Timeout)
			rec.waitKind = ultra.WaitKindCohort
			rec.waitPolicy = normalizeTimeoutPolicy(in.TimeoutPolicy)
			resp := fantasy.NewTextResponse("cohort launched; waiting for members")
			resp.StopTurn = true
			return resp, nil
		})
}

// cohortNamespace derives stable cohort ids from spawn keys.
var cohortNamespace = uuid.MustParse("6f2b1a54-6a1b-4b7f-9a1a-6d0f1b2c3d4e")

func parseWaitTimeout(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return defaultWaitTimeout
}

func normalizeTimeoutPolicy(raw string) string {
	if raw == ultra.TimeoutPolicyFail {
		return ultra.TimeoutPolicyFail
	}
	return ultra.TimeoutPolicyResolve
}

// deniedError marks an authority failure so callers emit PermissionDenied
// rather than a generic error.
type deniedError struct{ reason string }

func (e *deniedError) Error() string { return e.reason }

type spawnRequest struct {
	spec       childSpec
	toolCallID string
	cohortID   string
	ordinal    int
}

// spawnChild durably creates one child. It is idempotent: the child's spawn
// key is derived from the originating tool call, so a redelivered step adopts
// the existing child and enqueues no second first step.
func (w *StepWorker) spawnChild(ctx context.Context, run ultra.AgentRun, job StepJob, req spawnRequest) (ultra.AgentRun, bool, error) {
	grants := req.spec.grants()
	if !grants.SubsetOf(run.Grants) {
		return ultra.AgentRun{}, false, &deniedError{reason: "requested grants exceed parent"}
	}
	key := spawnKey(run.ID, job.StepIndex, req.toolCallID)
	scope := w.Store.Org(run.OrgID)

	// Fast path: this spawn already happened on an earlier delivery.
	if existing, err := scope.Runs().GetBySpawnKey(ctx, key); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ultra.ErrNotFound) {
		return ultra.AgentRun{}, false, err
	}

	history, err := InitialEnvelope(req.spec.Prompt)
	if err != nil {
		return ultra.AgentRun{}, false, err
	}
	parentID := run.ID
	child := ultra.AgentRun{
		ID: ultra.RunID(uuid.NewString()), SessionID: run.SessionID, OrgID: run.OrgID,
		ParentRunID: &parentID, SpawnKey: key, CohortID: req.cohortID, CohortOrdinal: req.ordinal,
		Grants: grants, LoopKind: run.LoopKind, LoopVersion: run.LoopVersion,
		ModelConfig: run.ModelConfig, Prompt: req.spec.Prompt, History: history,
		FlowInvocationID: run.FlowInvocationID,
	}

	var denied *deniedError
	err = w.Store.Tx(ctx, func(txs ultra.Store) error {
		txScope := txs.Org(run.OrgID)
		// Lock the parent so concurrent spawns within one step serialize:
		// otherwise two children could each read a count below the limit.
		if _, err := txScope.Runs().GetForUpdate(ctx, run.ID); err != nil {
			return err
		}
		if existing, err := txScope.Runs().GetBySpawnKey(ctx, key); err == nil {
			child = existing
			return errAdoptedChild
		} else if !errors.Is(err, ultra.ErrNotFound) {
			return err
		}
		count, err := txScope.Runs().CountChildren(ctx, run.ID)
		if err != nil {
			return err
		}
		if count >= run.Grants.MaxChildren {
			denied = &deniedError{reason: "max children reached"}
			return denied
		}
		if err := txScope.Runs().Create(ctx, child); err != nil {
			return err
		}
		payload, err := json.Marshal(ultra.RunSpawnedPayload{ParentRunID: run.ID, ChildRunID: child.ID})
		if err != nil {
			return err
		}
		if _, err := txScope.Events().Append(ctx, run.SessionID, ultra.Event{
			Actor: ultra.Actor{Type: ultra.ActorAgent, ID: string(run.ID)}, Kind: ultra.EventKindRunSpawned, Payload: payload,
		}); err != nil {
			return err
		}
		// Creation and the child's first step commit together, so a child row
		// can never exist without work scheduled for it.
		return w.Enqueue.EnqueueInTx(ctx, txs, StepJob{
			RunID: string(child.ID), OrgID: job.OrgID, SessionID: job.SessionID, StepIndex: 0,
		})
	})
	switch {
	case errors.Is(err, errAdoptedChild):
		return child, true, nil
	case denied != nil:
		return ultra.AgentRun{}, false, denied
	case err != nil:
		return ultra.AgentRun{}, false, err
	}
	return child, false, nil
}

// errAdoptedChild aborts the spawn transaction when a concurrent or earlier
// delivery already created the child. It is a control signal, not a failure.
var errAdoptedChild = errors.New("loop: child already spawned for this tool call")

func (w *StepWorker) permissionDenied(ctx context.Context, run ultra.AgentRun, tool string, envID *ultra.EnvID, reason string) fantasy.ToolResponse {
	payload, _ := json.Marshal(ultra.PermissionDeniedPayload{RunID: run.ID, Tool: tool, EnvID: envID, Reason: reason})
	_, _ = w.Store.Org(run.OrgID).Events().Append(ctx, run.SessionID, ultra.Event{Actor: ultra.Actor{Type: ultra.ActorSystem}, Kind: ultra.EventKindPermissionDenied, Payload: payload})
	// Every denial returns the same opaque message: a caller must not be able
	// to tell "you may not touch this" from "this does not exist".
	return fantasy.NewTextErrorResponse("permission denied")
}
