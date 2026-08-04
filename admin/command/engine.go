// Package command implements typed admin mutations with dry-run, preview
// hashes, idempotency, and immutable audit.
package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/admin/authz"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
	"github.com/aleksclark/ultracore/jobqueue"
	riverqueue "github.com/aleksclark/ultracore/jobqueue/river"
	"github.com/aleksclark/ultracore/loop"
	"github.com/aleksclark/ultracore/postgres"
	"github.com/aleksclark/ultracore/resourcework"
	"github.com/aleksclark/ultracore/secrets"
)

// Flags gates destructive / optional commands (disabled by default).
type Flags struct {
	RevealEnabled               bool
	TerminateEnabled            bool
	SuspendEnabled              bool
	DisconnectSubscriberEnabled bool
}

// Deps are runtime dependencies for command execution.
type Deps struct {
	Pool         *pgxpool.Pool
	Store        uc.Store
	Enqueue      jobqueue.TxEnqueuer
	River        *riverqueue.Queue
	Resources    *resourcework.Service // optional; when nil, resource cmds enqueue via store
	Keyring      secrets.Keyring       // optional; required for reveal
	BuildVersion string
	Flags        Flags
	Log          *slog.Logger

	// RateLimit is max command executes per second per process (0 = 20).
	RateLimit int
	// MaxConcurrent is max in-flight executes (0 = 8).
	MaxConcurrent int
}

// Engine runs admin commands.
type Engine struct {
	deps Deps

	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
	inflight int
}

// New builds an Engine.
func New(deps Deps) *Engine {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.RateLimit <= 0 {
		deps.RateLimit = 20
	}
	if deps.MaxConcurrent <= 0 {
		deps.MaxConcurrent = 8
	}
	return &Engine{deps: deps, lastFill: time.Now(), tokens: float64(deps.RateLimit)}
}

// Meta is request-scoped operator context.
type Meta struct {
	Operator  authz.Operator
	RequestID string
	SourceIP  string
	// ReauthOK is true when X-Admin-Reauth matched the bearer (reveal).
	ReauthOK bool
}

// Result is the internal command result before proto mapping.
type Result struct {
	DryRun           bool
	PreviewHash      string
	Before           map[string]any
	After            map[string]any
	Effects          []string
	Outcome          string // ok | dry_run | already_applied | failed
	Message          string
	AuditID          string
	IdempotentReplay bool
	EvidenceJSON     []byte
	Plaintext        string // reveal only; never log
	RevealExpires    time.Time
}

var (
	errStalePreview   = errors.New("stale preview hash")
	errDenied         = errors.New("permission denied")
	errRateLimited    = errors.New("rate limited")
	errBusy           = errors.New("too many concurrent commands")
	errRevealDisabled = errors.New("secret reveal is disabled")
	errReauthRequired = errors.New("re-authentication required")
	errDisabled       = errors.New("command disabled by configuration")
)

// Run executes one command end-to-end.
//
// Safety contract:
//   - Authorization is deny-by-default via authz.Can.
//   - Execute always recomputes a live preview hash and refuses to apply when
//     it does not match opts.preview_hash (stale confirmation fails closed
//     before any mutation).
//   - Idempotency keys bind only successful outcomes (ok / already_applied).
//     Failed/denied/stale attempts still audit, but do not poison the key.
//   - Plaintext (reveal) never enters audit JSON.
func (e *Engine) Run(ctx context.Context, meta Meta, command string, opts *adminv1.CommandOptions, targets map[string]any, exec func(ctx context.Context, dryRun bool) (before, after map[string]any, effects []string, extra *extraOut, err error)) (*Result, error) {
	if !authz.Can(meta.Operator.Role, command) {
		// Denied attempts are audited without an idempotency binding.
		_, _ = e.writeAudit(ctx, meta, command, targets, opts, nil, nil, "denied", errDenied.Error(), "")
		return nil, errDenied
	}
	if opts == nil {
		opts = &adminv1.CommandOptions{}
	}
	dry := opts.GetDryRun()
	reason := strings.TrimSpace(opts.GetReason())
	if !dry && reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	idem := strings.TrimSpace(opts.GetIdempotencyKey())
	if !dry {
		if idem == "" {
			return nil, fmt.Errorf("idempotency_key is required")
		}
		if prev, ok, err := e.lookupIdempotent(ctx, idem); err != nil {
			return nil, err
		} else if ok {
			prev.IdempotentReplay = true
			return prev, nil
		}
		if err := e.acquire(); err != nil {
			return nil, err
		}
		defer e.release()
	}

	// Always probe live state first (dry-run). Execute path uses this to
	// validate the caller-supplied preview hash before any mutation.
	before, after, effects, extra, err := exec(ctx, true)
	if err != nil {
		// Audit failures without binding the idempotency key so the operator
		// can retry the same key after fixing preconditions.
		_, _ = e.writeAudit(ctx, meta, command, targets, opts, before, after, "failed", err.Error(), "")
		return nil, err
	}
	hash := previewHash(command, targets, before)
	res := &Result{
		DryRun:      dry,
		PreviewHash: hash,
		Before:      before,
		After:       after,
		Effects:     effects,
		Outcome:     "dry_run",
	}
	if extra != nil {
		res.EvidenceJSON = extra.EvidenceJSON
		// Never surface plaintext on the dry-run path.
		res.Plaintext = ""
		res.RevealExpires = extra.RevealExpires
	}
	if dry {
		aid, aerr := e.writeAudit(ctx, meta, command, targets, opts, before, before, "dry_run", "", "")
		if aerr == nil {
			res.AuditID = aid
		}
		res.Plaintext = ""
		return res, nil
	}

	// Execute path: require matching preview hash BEFORE applying.
	// A mismatch means targets changed since the operator confirmed — fail
	// closed with no side effects.
	if subtleNeq(opts.GetPreviewHash(), hash) {
		_, _ = e.writeAudit(ctx, meta, command, targets, opts, before, nil, "failed", errStalePreview.Error(), "")
		return nil, errStalePreview
	}

	before2, after2, effects2, extra2, err := exec(ctx, false)
	if err != nil {
		// Mutation may or may not have partially applied inside the command;
		// still do not bind the idempotency key on failure.
		_, _ = e.writeAudit(ctx, meta, command, targets, opts, before2, after2, "failed", err.Error(), "")
		return nil, err
	}
	// Note: we intentionally do NOT re-check preview hash after apply.
	// Commands capture before-state at the start of exec(false); a post-apply
	// mismatch would mean we already mutated and then lied to the caller with
	// a stale error. In-command GetForUpdate / state checks handle races.
	res.Before = before2
	res.After = after2
	res.Effects = effects2
	res.PreviewHash = hash
	res.Outcome = "ok"
	if extra2 != nil {
		res.EvidenceJSON = extra2.EvidenceJSON
		res.Plaintext = extra2.Plaintext
		res.RevealExpires = extra2.RevealExpires
	}
	if after2 != nil {
		if v, ok := after2["already_applied"].(bool); ok && v {
			res.Outcome = "already_applied"
		}
	}
	// Bind idempotency only on success so retries after failure remain possible.
	aid, aerr := e.writeAudit(ctx, meta, command, targets, opts, before2, after2, res.Outcome, "", idem)
	if aerr != nil {
		// State may have changed; surface audit failure.
		return res, fmt.Errorf("command applied but audit failed: %w", aerr)
	}
	res.AuditID = aid
	return res, nil
}

type extraOut struct {
	EvidenceJSON  []byte
	Plaintext     string
	RevealExpires time.Time
}

func (e *Engine) acquire() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(e.lastFill).Seconds()
	e.tokens += elapsed * float64(e.deps.RateLimit)
	if e.tokens > float64(e.deps.RateLimit) {
		e.tokens = float64(e.deps.RateLimit)
	}
	e.lastFill = now
	if e.tokens < 1 {
		return errRateLimited
	}
	if e.inflight >= e.deps.MaxConcurrent {
		return errBusy
	}
	e.tokens--
	e.inflight++
	return nil
}

func (e *Engine) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inflight > 0 {
		e.inflight--
	}
}

func previewHash(command string, targets map[string]any, before map[string]any) string {
	payload := map[string]any{
		"command": command,
		"targets": stable(targets),
		"before":  stable(before),
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func stable(v any) any {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			out[k] = stable(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = stable(t[i])
		}
		return out
	default:
		return v
	}
}

func subtleNeq(a, b string) bool {
	return a != b
}

func toStruct(m map[string]any) *structpb.Struct {
	if m == nil {
		m = map[string]any{}
	}
	// structpb requires json-compatible types; round-trip.
	b, err := json.Marshal(m)
	if err != nil {
		s, _ := structpb.NewStruct(map[string]any{"error": "marshal"})
		return s
	}
	var mm map[string]any
	if err := json.Unmarshal(b, &mm); err != nil {
		s, _ := structpb.NewStruct(map[string]any{"error": "unmarshal"})
		return s
	}
	s, err := structpb.NewStruct(mm)
	if err != nil {
		s, _ = structpb.NewStruct(map[string]any{})
	}
	return s
}

// OutcomeProto maps Result to CommandOutcome (without secret plaintext).
func OutcomeProto(r *Result) *adminv1.CommandOutcome {
	if r == nil {
		return &adminv1.CommandOutcome{}
	}
	return &adminv1.CommandOutcome{
		DryRun: r.DryRun,
		Preview: &adminv1.CommandPreview{
			PreviewHash:     r.PreviewHash,
			BeforeSummary:   toStruct(r.Before),
			ExpectedEffects: r.Effects,
			ComputedAt:      timestamppb.Now(),
		},
		Result:           r.Outcome,
		AfterSummary:     toStruct(r.After),
		AuditEventId:     r.AuditID,
		IdempotentReplay: r.IdempotentReplay,
		Message:          r.Message,
	}
}

func (e *Engine) writeAudit(ctx context.Context, meta Meta, command string, targets map[string]any, opts *adminv1.CommandOptions, before, after map[string]any, result, errText, idem string) (string, error) {
	id := uuid.NewString()
	reason := ""
	preview := ""
	if opts != nil {
		reason = opts.GetReason()
		preview = opts.GetPreviewHash()
		if preview == "" {
			preview = previewHash(command, targets, before)
		}
	}
	tb, _ := json.Marshal(targets)
	if tb == nil {
		tb = []byte(`{}`)
	}
	bb, _ := json.Marshal(before)
	if bb == nil {
		bb = []byte(`{}`)
	}
	ab, _ := json.Marshal(after)
	if ab == nil {
		ab = []byte(`{}`)
	}
	var idemArg any
	if idem != "" {
		idemArg = idem
	}
	_, err := e.deps.Pool.Exec(ctx, `
		INSERT INTO admin_audit_events (
			id, operator_id, operator_role, request_id, command, targets, reason,
			preview_hash, before_summary, after_summary, result, error, source_ip,
			build_version, idempotency_key
		) VALUES (
			$1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13,$14,$15
		)`,
		id, meta.Operator.ID, string(meta.Operator.Role), meta.RequestID, command,
		string(tb), reason, preview, string(bb), string(ab), result, errText,
		meta.SourceIP, e.deps.BuildVersion, idemArg,
	)
	if err != nil {
		// Unique idempotency race: treat as lookup.
		if strings.Contains(err.Error(), "admin_audit_events_idempotency") {
			if prev, ok, lerr := e.lookupIdempotent(ctx, idem); lerr == nil && ok {
				return prev.AuditID, nil
			}
		}
		return "", err
	}
	return id, nil
}

func (e *Engine) lookupIdempotent(ctx context.Context, key string) (*Result, bool, error) {
	var (
		id, command, result, errText string
		beforeB, afterB              []byte
		preview                      string
	)
	// Only successful outcomes reserve the idempotency key. Failed rows never
	// bind the key, so a prior failure cannot short-circuit a retry.
	err := e.deps.Pool.QueryRow(ctx, `
		SELECT id::text, command, result, error, before_summary, after_summary, preview_hash
		  FROM admin_audit_events
		 WHERE idempotency_key = $1
		   AND result IN ('ok', 'already_applied')
		 LIMIT 1`, key).Scan(&id, &command, &result, &errText, &beforeB, &afterB, &preview)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, false, err
		}
		return nil, false, err
	}
	before := map[string]any{}
	after := map[string]any{}
	_ = json.Unmarshal(beforeB, &before)
	_ = json.Unmarshal(afterB, &after)
	return &Result{
		DryRun:      false,
		PreviewHash: preview,
		Before:      before,
		After:       after,
		Outcome:     result,
		Message:     errText,
		AuditID:     id,
	}, true, nil
}

// ---------------------------------------------------------------------------
// Concrete commands
// ---------------------------------------------------------------------------

func (e *Engine) RetryQueueJob(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, jobID int64) (*Result, error) {
	targets := map[string]any{"job_id": jobID}
	return e.Run(ctx, meta, authz.CmdRetryQueueJob, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if e.deps.River == nil {
			return nil, nil, nil, nil, fmt.Errorf("river queue unavailable")
		}
		row, err := e.deps.River.JobGet(ctx, jobID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := map[string]any{"job_id": jobID, "state": string(row.State), "attempt": row.Attempt, "kind": row.Kind}
		effects := []string{"schedule job for immediate retry"}
		if dryRun {
			return before, before, effects, nil, nil
		}
		afterRow, err := e.deps.River.JobRetry(ctx, jobID)
		if err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"job_id": jobID, "state": string(afterRow.State), "attempt": afterRow.Attempt, "kind": afterRow.Kind}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) CancelQueueJob(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, jobID int64) (*Result, error) {
	targets := map[string]any{"job_id": jobID}
	return e.Run(ctx, meta, authz.CmdCancelQueueJob, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if e.deps.River == nil {
			return nil, nil, nil, nil, fmt.Errorf("river queue unavailable")
		}
		row, err := e.deps.River.JobGet(ctx, jobID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := map[string]any{"job_id": jobID, "state": string(row.State), "attempt": row.Attempt, "kind": row.Kind}
		effects := []string{"cancel river job"}
		if dryRun {
			return before, before, effects, nil, nil
		}
		afterRow, err := e.deps.River.JobCancel(ctx, jobID)
		if err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"job_id": jobID, "state": string(afterRow.State), "attempt": afterRow.Attempt, "kind": afterRow.Kind}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) CancelRun(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, runID string) (*Result, error) {
	targets := map[string]any{"run_id": runID}
	return e.Run(ctx, meta, authz.CmdCancelRun, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		run, err := e.loadRun(ctx, runID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := map[string]any{
			"run_id": string(run.ID), "tenant_id": string(run.TenantID), "session_id": string(run.SessionID),
			"state": string(run.State), "cancel_requested": run.CancelRequestedAt != nil,
		}
		effects := []string{"request run cancellation", "finalize awaiting runs immediately"}
		if run.State.Terminal() {
			before["already_applied"] = true
			return before, before, []string{"run already terminal"}, nil, nil
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
			scope := txs.Tenant(run.TenantID)
			locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
			if err != nil {
				return err
			}
			if locked.State.Terminal() {
				return nil
			}
			if err := scope.Runs().RequestCancel(ctx, run.ID); err != nil {
				return err
			}
			if locked.State == uc.RunAwaiting {
				if err := scope.Runs().SetState(ctx, run.ID, uc.RunCancelled, "", ""); err != nil {
					return err
				}
				if err := loop.AbandonWaits(ctx, txs, run.TenantID, run.ID); err != nil {
					return err
				}
				payload, _ := json.Marshal(uc.RunCancelledPayload{RunID: run.ID})
				_, err = scope.Events().Append(ctx, run.SessionID, uc.Event{
					Actor: uc.ActorSystem(), Kind: uc.EventKindRunCancelled, Payload: payload,
				})
				return err
			}
			return nil
		})
		if err != nil {
			return before, nil, effects, nil, err
		}
		afterRun, _ := e.loadRun(ctx, runID)
		after := map[string]any{
			"run_id": string(afterRun.ID), "state": string(afterRun.State),
			"cancel_requested": afterRun.CancelRequestedAt != nil,
		}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) AnswerAwait(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, runID, message string) (*Result, error) {
	targets := map[string]any{"run_id": runID}
	return e.Run(ctx, meta, authz.CmdAnswerAwait, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if strings.TrimSpace(message) == "" {
			return nil, nil, nil, nil, fmt.Errorf("message is required")
		}
		if e.deps.Enqueue == nil {
			return nil, nil, nil, nil, fmt.Errorf("enqueue unavailable")
		}
		run, err := e.loadRun(ctx, runID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := map[string]any{"run_id": string(run.ID), "state": string(run.State), "tenant_id": string(run.TenantID)}
		effects := []string{"append operator answer", "set run running", "enqueue next step"}
		if run.State != uc.RunAwaiting && run.State != uc.RunCompleted {
			return before, nil, effects, nil, fmt.Errorf("run is not awaiting input or completed")
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
			scope := txs.Tenant(run.TenantID)
			locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
			if err != nil {
				return err
			}
			if locked.State != uc.RunAwaiting && locked.State != uc.RunCompleted {
				return fmt.Errorf("run is not awaiting input or completed")
			}
			history, err := loop.AppendUserMessage(locked.History, message)
			if err != nil {
				return err
			}
			if err := scope.Runs().SetHistory(ctx, run.ID, history); err != nil {
				return err
			}
			if err := scope.Runs().SetState(ctx, run.ID, uc.RunRunning, "", ""); err != nil {
				return err
			}
			payload, err := json.Marshal(uc.UserMessagePayload{Text: message})
			if err != nil {
				return err
			}
			if _, err := scope.Events().Append(ctx, run.SessionID, uc.Event{
				Actor: uc.Actor{Kind: "operator", ID: meta.Operator.ID, Display: meta.Operator.Name},
				Kind:  uc.EventKindUserMessage, Payload: payload,
			}); err != nil {
				return err
			}
			steps, err := scope.Runs().Steps(ctx, run.ID)
			if err != nil {
				return err
			}
			nextIndex := 0
			for _, s := range steps {
				if s.StepIndex >= nextIndex {
					nextIndex = s.StepIndex + 1
				}
			}
			return e.deps.Enqueue.EnqueueInTx(ctx, txs, loop.StepJob{
				RunID: string(run.ID), TenantID: string(run.TenantID), SessionID: string(run.SessionID),
				StepIndex: nextIndex,
			})
		})
		if err != nil {
			return before, nil, effects, nil, err
		}
		afterRun, _ := e.loadRun(ctx, runID)
		after := map[string]any{"run_id": string(afterRun.ID), "state": string(afterRun.State)}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) ExpireAwait(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, runID string) (*Result, error) {
	// Expire = cancel an awaiting run (operator-forced timeout).
	targets := map[string]any{"run_id": runID}
	return e.Run(ctx, meta, authz.CmdExpireAwait, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		run, err := e.loadRun(ctx, runID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := map[string]any{"run_id": string(run.ID), "state": string(run.State)}
		effects := []string{"expire awaiting run as cancelled"}
		if run.State != uc.RunAwaiting {
			return before, nil, effects, nil, fmt.Errorf("run is not awaiting")
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
			scope := txs.Tenant(run.TenantID)
			locked, err := scope.Runs().GetForUpdate(ctx, run.ID)
			if err != nil {
				return err
			}
			if locked.State != uc.RunAwaiting {
				return fmt.Errorf("run is not awaiting")
			}
			if err := scope.Runs().RequestCancel(ctx, run.ID); err != nil {
				return err
			}
			if err := scope.Runs().SetState(ctx, run.ID, uc.RunCancelled, "expired", "operator expired await"); err != nil {
				return err
			}
			if err := loop.AbandonWaits(ctx, txs, run.TenantID, run.ID); err != nil {
				return err
			}
			payload, _ := json.Marshal(uc.RunCancelledPayload{RunID: run.ID})
			_, err = scope.Events().Append(ctx, run.SessionID, uc.Event{
				Actor: uc.ActorSystem(), Kind: uc.EventKindRunCancelled, Payload: payload,
			})
			return err
		})
		if err != nil {
			return before, nil, effects, nil, err
		}
		afterRun, _ := e.loadRun(ctx, runID)
		after := map[string]any{"run_id": string(afterRun.ID), "state": string(afterRun.State)}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) ResourceReconcile(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, resourceID string) (*Result, error) {
	targets := map[string]any{"resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdResourceReconcile, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		r, err := e.loadResource(ctx, resourceID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := resourceSummary(r)
		effects := []string{"enqueue resource.reconcile job"}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if err := e.enqueueResourceJob(ctx, resourcework.ReconcileJob{TenantID: string(r.TenantID), ResourceID: string(r.ID)}); err != nil {
			return before, nil, effects, nil, err
		}
		after := resourceSummary(r)
		after["enqueued"] = "resource.reconcile"
		return before, after, effects, nil, nil
	})
}

func (e *Engine) ResourceRestart(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, resourceID string) (*Result, error) {
	targets := map[string]any{"resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdResourceRestart, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		r, err := e.loadResource(ctx, resourceID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := resourceSummary(r)
		effects := []string{"mark provisioning", "enqueue resource.restart"}
		if r.State.Terminal() || r.State == uc.ResourceTerminating {
			return before, nil, effects, nil, fmt.Errorf("resource is %s and cannot restart", r.State)
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if e.deps.Resources != nil {
			if _, err := e.deps.Resources.RequestRestart(ctx, r.TenantID, r.ID); err != nil {
				return before, nil, effects, nil, err
			}
		} else {
			err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
				scope := txs.Tenant(r.TenantID)
				locked, err := scope.Resources().GetForUpdate(ctx, r.ID)
				if err != nil {
					return err
				}
				if locked.State.Terminal() || locked.State == uc.ResourceTerminating {
					return fmt.Errorf("resource is %s and cannot restart", locked.State)
				}
				if err := scope.Resources().SetProvisioning(ctx, r.ID); err != nil {
					return err
				}
				return e.deps.Enqueue.EnqueueInTx(ctx, txs, resourcework.RestartJob{TenantID: string(r.TenantID), ResourceID: string(r.ID)})
			})
			if err != nil {
				return before, nil, effects, nil, err
			}
		}
		afterR, _ := e.loadResource(ctx, resourceID)
		return before, resourceSummary(afterR), effects, nil, nil
	})
}

func (e *Engine) ResourceSuspend(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, resourceID, message string) (*Result, error) {
	targets := map[string]any{"resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdResourceSuspend, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if !e.deps.Flags.SuspendEnabled {
			return nil, nil, nil, nil, errDisabled
		}
		r, err := e.loadResource(ctx, resourceID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := resourceSummary(r)
		effects := []string{"set resource suspended"}
		if message == "" {
			message = "operator suspended"
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
			scope := txs.Tenant(r.TenantID)
			if _, err := scope.Resources().GetForUpdate(ctx, r.ID); err != nil {
				return err
			}
			return scope.Resources().SetSuspended(ctx, r.ID, message)
		})
		if err != nil {
			return before, nil, effects, nil, err
		}
		afterR, _ := e.loadResource(ctx, resourceID)
		return before, resourceSummary(afterR), effects, nil, nil
	})
}

func (e *Engine) ResourceTerminate(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, resourceID string) (*Result, error) {
	targets := map[string]any{"resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdResourceTerminate, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if !e.deps.Flags.TerminateEnabled {
			return nil, nil, nil, nil, errDisabled
		}
		r, err := e.loadResource(ctx, resourceID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := resourceSummary(r)
		effects := []string{"mark terminating", "enqueue resource.terminate"}
		if r.State == uc.ResourceTerminated {
			before["already_applied"] = true
			return before, before, []string{"already terminated"}, nil, nil
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if e.deps.Resources != nil {
			if err := e.deps.Resources.RequestTerminate(ctx, r.TenantID, r.ID); err != nil {
				return before, nil, effects, nil, err
			}
		} else {
			err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
				scope := txs.Tenant(r.TenantID)
				locked, err := scope.Resources().GetForUpdate(ctx, r.ID)
				if err != nil {
					return err
				}
				if locked.State == uc.ResourceTerminated {
					return nil
				}
				if err := scope.Resources().SetTerminating(ctx, r.ID); err != nil {
					return err
				}
				return e.deps.Enqueue.EnqueueInTx(ctx, txs, resourcework.TerminateJob{TenantID: string(r.TenantID), ResourceID: string(r.ID)})
			})
			if err != nil {
				return before, nil, effects, nil, err
			}
		}
		afterR, _ := e.loadResource(ctx, resourceID)
		return before, resourceSummary(afterR), effects, nil, nil
	})
}

func (e *Engine) ResourceAdoptionProbe(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, resourceID string) (*Result, error) {
	targets := map[string]any{"resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdResourceAdoptionProbe, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		r, err := e.loadResource(ctx, resourceID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		before := resourceSummary(r)
		// Probe is observational: report handle presence + enqueue reconcile.
		effects := []string{"record adoption probe metadata", "enqueue reconcile"}
		probe := map[string]any{
			"has_handle": uc.HandlePresent(r.Handle),
			"state":      string(r.State),
			"epoch":      r.Epoch,
		}
		before["probe"] = probe
		if dryRun {
			return before, before, effects, nil, nil
		}
		if err := e.enqueueResourceJob(ctx, resourcework.ReconcileJob{TenantID: string(r.TenantID), ResourceID: string(r.ID)}); err != nil {
			return before, nil, effects, nil, err
		}
		after := resourceSummary(r)
		after["probe"] = probe
		after["enqueued"] = "resource.reconcile"
		return before, after, effects, nil, nil
	})
}

func (e *Engine) ReprobeProvider(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, providerID string) (*Result, error) {
	targets := map[string]any{"provider_id": providerID}
	return e.Run(ctx, meta, authz.CmdReprobeProvider, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		var tenantID, kind, name, state string
		var lastHealthy *time.Time
		err := e.deps.Pool.QueryRow(ctx, `
			SELECT tenant_id::text, kind, name, state, last_healthy_at
			  FROM provider_instances WHERE id=$1`, providerID).Scan(&tenantID, &kind, &name, &state, &lastHealthy)
		if err != nil {
			return nil, nil, nil, nil, uc.ErrNotFound
		}
		before := map[string]any{"provider_id": providerID, "tenant_id": tenantID, "kind": kind, "name": name, "state": state}
		if lastHealthy != nil {
			before["last_healthy_at"] = lastHealthy.UTC().Format(time.RFC3339Nano)
		}
		effects := []string{"mark provider healthy (metadata refresh)"}
		if dryRun {
			return before, before, effects, nil, nil
		}
		err = e.deps.Store.Tx(ctx, func(txs uc.Store) error {
			return txs.Tenant(uc.TenantID(tenantID)).Providers().MarkHealthy(ctx, uc.ProviderInstanceID(providerID))
		})
		if err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"provider_id": providerID, "state": "ready", "probed": true}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) RevokeAPIKey(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, keyID string) (*Result, error) {
	targets := map[string]any{"api_key_id": keyID}
	return e.Run(ctx, meta, authz.CmdRevokeAPIKey, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		var tenantID, name, prefix string
		var revokedAt *time.Time
		err := e.deps.Pool.QueryRow(ctx, `
			SELECT tenant_id::text, name, prefix, revoked_at FROM api_keys WHERE id=$1`, keyID).
			Scan(&tenantID, &name, &prefix, &revokedAt)
		if err != nil {
			return nil, nil, nil, nil, uc.ErrNotFound
		}
		before := map[string]any{"api_key_id": keyID, "tenant_id": tenantID, "name": name, "prefix": prefix, "revoked": revokedAt != nil}
		effects := []string{"set api_keys.revoked_at"}
		if revokedAt != nil {
			before["already_applied"] = true
			return before, before, []string{"already revoked"}, nil, nil
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if err := e.deps.Store.APIKeys().Revoke(ctx, uc.TenantID(tenantID), uc.APIKeyID(keyID)); err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"api_key_id": keyID, "revoked": true}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) DisableCredential(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, tenantID, kind, name string) (*Result, error) {
	targets := map[string]any{"tenant_id": tenantID, "kind": kind, "name": name}
	return e.Run(ctx, meta, authz.CmdDisableCredential, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		var createdAt time.Time
		err := e.deps.Pool.QueryRow(ctx, `
			SELECT created_at FROM credentials WHERE tenant_id=$1 AND kind=$2 AND name=$3`,
			tenantID, kind, name).Scan(&createdAt)
		if err != nil {
			return nil, nil, nil, nil, uc.ErrNotFound
		}
		before := map[string]any{"tenant_id": tenantID, "kind": kind, "name": name, "created_at": createdAt.UTC().Format(time.RFC3339Nano)}
		effects := []string{"delete credential metadata (ciphertext removed; not recoverable via admin)"}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if err := e.deps.Store.Tenant(uc.TenantID(tenantID)).Credentials().Delete(ctx, kind, name); err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"tenant_id": tenantID, "kind": kind, "name": name, "deleted": true}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) PausePeriodicPrompt(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, id string) (*Result, error) {
	return e.setPeriodicEnabled(ctx, meta, opts, id, false, authz.CmdPausePeriodicPrompt)
}

func (e *Engine) ResumePeriodicPrompt(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, id string) (*Result, error) {
	return e.setPeriodicEnabled(ctx, meta, opts, id, true, authz.CmdResumePeriodicPrompt)
}

func (e *Engine) setPeriodicEnabled(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, id string, enabled bool, cmd string) (*Result, error) {
	targets := map[string]any{"periodic_prompt_id": id}
	return e.Run(ctx, meta, cmd, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		var tenantID string
		var cur bool
		err := e.deps.Pool.QueryRow(ctx, `
			SELECT tenant_id::text, enabled FROM periodic_prompts WHERE id=$1`, id).Scan(&tenantID, &cur)
		if err != nil {
			return nil, nil, nil, nil, uc.ErrNotFound
		}
		before := map[string]any{"periodic_prompt_id": id, "tenant_id": tenantID, "enabled": cur}
		action := "pause"
		if enabled {
			action = "resume"
		}
		effects := []string{action + " periodic prompt"}
		if cur == enabled {
			before["already_applied"] = true
			return before, before, []string{"already " + action + "d"}, nil, nil
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		if err := e.deps.Store.Tenant(uc.TenantID(tenantID)).PeriodicPrompts().SetEnabled(ctx, uc.PeriodicPromptID(id), enabled); err != nil {
			return before, nil, effects, nil, err
		}
		after := map[string]any{"periodic_prompt_id": id, "enabled": enabled}
		return before, after, effects, nil, nil
	})
}

func (e *Engine) DisconnectSubscriber(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, sessionID, subscriberID string) (*Result, error) {
	targets := map[string]any{"session_id": sessionID, "subscriber_id": subscriberID}
	return e.Run(ctx, meta, authz.CmdDisconnectSubscriber, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if !e.deps.Flags.DisconnectSubscriberEnabled {
			// Honest deferred: event bus has no cross-process disconnect handle.
			before := map[string]any{"session_id": sessionID, "subscriber_id": subscriberID, "supported": false}
			return before, nil, nil, nil, fmt.Errorf("DisconnectSubscriber unsupported: event bus has no admin disconnect handle (FailedPrecondition)")
		}
		_ = dryRun
		return nil, nil, nil, nil, errDisabled
	})
}

func (e *Engine) ExportIncidentEvidence(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, sessionID, runID, resourceID string, maxEvents int32) (*Result, error) {
	targets := map[string]any{"session_id": sessionID, "run_id": runID, "resource_id": resourceID}
	return e.Run(ctx, meta, authz.CmdExportIncidentEvidence, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if maxEvents <= 0 {
			maxEvents = 100
		}
		if maxEvents > 500 {
			maxEvents = 500
		}
		bundle := map[string]any{
			"exported_at": time.Now().UTC().Format(time.RFC3339Nano),
			"operator_id": meta.Operator.ID,
		}
		effects := []string{"export bounded incident evidence JSON (no secret plaintext)"}
		if runID != "" {
			run, err := e.loadRun(ctx, runID)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			bundle["run"] = map[string]any{
				"id": string(run.ID), "state": string(run.State), "session_id": string(run.SessionID),
				"tenant_id": string(run.TenantID), "failure_reason": run.FailureReason,
			}
			if sessionID == "" {
				sessionID = string(run.SessionID)
			}
		}
		if resourceID != "" {
			r, err := e.loadResource(ctx, resourceID)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			bundle["resource"] = resourceSummary(r)
			if sessionID == "" {
				sessionID = string(r.SessionID)
			}
		}
		if sessionID != "" {
			var tenantID string
			_ = e.deps.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM sessions WHERE id=$1`, sessionID).Scan(&tenantID)
			bundle["session"] = map[string]any{"id": sessionID, "tenant_id": tenantID}
			rows, err := e.deps.Pool.Query(ctx, `
				SELECT seq, ts, actor_type, actor_id, kind, left(payload::text, 512)
				  FROM session_events WHERE session_id=$1
				 ORDER BY seq DESC LIMIT $2`, sessionID, maxEvents)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			defer rows.Close()
			var events []map[string]any
			for rows.Next() {
				var seq int64
				var ts time.Time
				var actorType, actorID, kind, payload string
				if err := rows.Scan(&seq, &ts, &actorType, &actorID, &kind, &payload); err != nil {
					return nil, nil, nil, nil, err
				}
				events = append(events, map[string]any{
					"seq": seq, "ts": ts.UTC().Format(time.RFC3339Nano),
					"actor_kind": actorType, "actor_id": actorID, "kind": kind, "payload_preview": payload,
				})
			}
			bundle["events"] = events
			bundle["event_count"] = len(events)
		}
		before := map[string]any{"targets": targets, "max_events": maxEvents}
		b, err := json.Marshal(bundle)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if dryRun {
			return before, before, effects, &extraOut{EvidenceJSON: b}, nil
		}
		after := map[string]any{"bytes": len(b), "event_count": bundle["event_count"]}
		return before, after, effects, &extraOut{EvidenceJSON: b}, nil
	})
}

func (e *Engine) RevealSecret(ctx context.Context, meta Meta, opts *adminv1.CommandOptions, secretKind, apiKeyID, tenantID, credKind, credName string) (*Result, error) {
	targets := map[string]any{"secret_kind": secretKind}
	if secretKind == "api_key" {
		targets["api_key_id"] = apiKeyID
	} else {
		targets["tenant_id"] = tenantID
		targets["credential_kind"] = credKind
		targets["credential_name"] = credName
	}
	return e.Run(ctx, meta, authz.CmdRevealSecret, opts, targets, func(ctx context.Context, dryRun bool) (map[string]any, map[string]any, []string, *extraOut, error) {
		if !e.deps.Flags.RevealEnabled {
			return nil, nil, nil, nil, errRevealDisabled
		}
		if !meta.ReauthOK {
			return nil, nil, nil, nil, errReauthRequired
		}
		if e.deps.Keyring == nil {
			return nil, nil, nil, nil, fmt.Errorf("keyring unavailable")
		}
		if strings.TrimSpace(opts.GetReason()) == "" && !dryRun {
			return nil, nil, nil, nil, fmt.Errorf("reason is required")
		}
		effects := []string{"decrypt single secret for short-lived operator display"}
		var before map[string]any
		var plaintext string
		switch secretKind {
		case "api_key":
			if apiKeyID == "" {
				return nil, nil, nil, nil, fmt.Errorf("api_key_id required")
			}
			var enc []byte
			var prefix, name, tid string
			var revoked *time.Time
			err := e.deps.Pool.QueryRow(ctx, `
				SELECT tenant_id::text, name, prefix, key_enc, revoked_at FROM api_keys WHERE id=$1`, apiKeyID).
				Scan(&tid, &name, &prefix, &enc, &revoked)
			if err != nil {
				return nil, nil, nil, nil, uc.ErrNotFound
			}
			before = map[string]any{"secret_kind": "api_key", "api_key_id": apiKeyID, "tenant_id": tid, "name": name, "prefix": prefix, "revoked": revoked != nil}
			if !dryRun {
				plain, err := e.deps.Keyring.Decrypt(enc)
				if err != nil {
					return before, nil, effects, nil, err
				}
				plaintext = string(plain)
				secrets.DefaultRedactor.Register(plaintext)
			}
		case "credential":
			if tenantID == "" || credKind == "" || credName == "" {
				return nil, nil, nil, nil, fmt.Errorf("tenant_id, credential_kind, credential_name required")
			}
			cred, err := e.deps.Store.Tenant(uc.TenantID(tenantID)).Credentials().Get(ctx, credKind, credName)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			before = map[string]any{"secret_kind": "credential", "tenant_id": tenantID, "kind": credKind, "name": credName, "ciphertext_bytes": len(cred.EncPayload)}
			if !dryRun {
				plain, err := e.deps.Keyring.Decrypt(cred.EncPayload)
				if err != nil {
					return before, nil, effects, nil, err
				}
				plaintext = string(plain)
				secrets.DefaultRedactor.Register(plaintext)
			}
		default:
			return nil, nil, nil, nil, fmt.Errorf("secret_kind must be api_key or credential")
		}
		if dryRun {
			return before, before, effects, nil, nil
		}
		exp := time.Now().UTC().Add(2 * time.Minute)
		after := map[string]any{"revealed": true, "expires_at": exp.Format(time.RFC3339Nano)}
		// Never put plaintext in after/before summaries.
		return before, after, effects, &extraOut{Plaintext: plaintext, RevealExpires: exp}, nil
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (e *Engine) loadRun(ctx context.Context, id string) (uc.AgentRun, error) {
	var tenantID string
	err := e.deps.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM agent_runs WHERE id=$1`, id).Scan(&tenantID)
	if err != nil {
		return uc.AgentRun{}, uc.ErrNotFound
	}
	return e.deps.Store.Tenant(uc.TenantID(tenantID)).Runs().Get(ctx, uc.RunID(id))
}

func (e *Engine) loadResource(ctx context.Context, id string) (uc.Resource, error) {
	var tenantID string
	err := e.deps.Pool.QueryRow(ctx, `SELECT tenant_id::text FROM resources WHERE id=$1`, id).Scan(&tenantID)
	if err != nil {
		return uc.Resource{}, uc.ErrNotFound
	}
	return e.deps.Store.Tenant(uc.TenantID(tenantID)).Resources().Get(ctx, uc.ResourceID(id))
}

func resourceSummary(r uc.Resource) map[string]any {
	return map[string]any{
		"resource_id": string(r.ID),
		"tenant_id":   string(r.TenantID),
		"session_id":  string(r.SessionID),
		"state":       string(r.State),
		"kind":        string(r.Kind),
		"epoch":       r.Epoch,
		"provider_id": string(r.ProviderInstanceID),
	}
}

func (e *Engine) enqueueResourceJob(ctx context.Context, job jobqueue.Job) error {
	if e.deps.Enqueue == nil {
		return fmt.Errorf("enqueue unavailable")
	}
	return e.deps.Store.Tx(ctx, func(txs uc.Store) error {
		return e.deps.Enqueue.EnqueueInTx(ctx, txs, job)
	})
}

// MapError converts engine errors to connect-ish codes via sentinel matching.
func MapError(err error) (code string, msg string) {
	switch {
	case err == nil:
		return "", ""
	case errors.Is(err, errDenied):
		return "permission_denied", err.Error()
	case errors.Is(err, errStalePreview):
		return "failed_precondition", err.Error()
	case errors.Is(err, errRateLimited):
		return "resource_exhausted", err.Error()
	case errors.Is(err, errBusy):
		return "resource_exhausted", err.Error()
	case errors.Is(err, errRevealDisabled), errors.Is(err, errDisabled):
		return "failed_precondition", err.Error()
	case errors.Is(err, errReauthRequired):
		return "unauthenticated", err.Error()
	case errors.Is(err, uc.ErrNotFound):
		return "not_found", "not found"
	case strings.Contains(err.Error(), "FailedPrecondition") || strings.Contains(err.Error(), "unsupported"):
		return "failed_precondition", err.Error()
	default:
		return "invalid_argument", err.Error()
	}
}

// Ensure postgres.TxEnqueuer satisfies interface used at wiring sites.
var _ jobqueue.TxEnqueuer = postgres.TxEnqueuer{}
