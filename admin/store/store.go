package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	uc "github.com/aleksclark/ultracore"
	"github.com/aleksclark/ultracore/admin/query"
	adminv1 "github.com/aleksclark/ultracore/gen/go/admin/v1"
)

// AdminStore is the cross-tenant read surface for coreadmin. It uses the pool
// directly and never goes through TenantScope. Secret plaintext is never
// selected: only hashes, ciphertext lengths, and redacted metadata.
type AdminStore struct {
	pool      *pgxpool.Pool
	compiler  *query.Compiler
	registry  *query.Registry
	buildVers string
}

// NewAdminStore wires an admin read store on an existing pool.
func NewAdminStore(pool *pgxpool.Pool, signer *query.Signer, buildVersion string) *AdminStore {
	return &AdminStore{
		pool:      pool,
		compiler:  &query.Compiler{Signer: signer},
		registry:  query.NewRegistry(),
		buildVers: buildVersion,
	}
}

// Registry exposes collection descriptors.
func (a *AdminStore) Registry() *query.Registry { return a.registry }

// Compiler exposes the query compiler (for tests).
func (a *AdminStore) Compiler() *query.Compiler { return a.compiler }

func (a *AdminStore) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, query.DefaultQueryTimeout)
}

// RiverAvailable reports whether river_job exists (after river migrate).
func (a *AdminStore) RiverAvailable(ctx context.Context) bool {
	var exists bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = 'river_job'
		)`).Scan(&exists)
	return err == nil && exists
}

// rowHandler consumes one data projection (columns before trailing sort keys).
// keep=false for the extra has_more probe row.
type rowHandler func(vals []any, keep bool) error

func (a *AdminStore) list(ctx context.Context, name string, req query.Request, nDataCols int, handle rowHandler) (query.PageInfo, error) {
	col, ok := a.registry.Get(name)
	if !ok {
		return query.PageInfo{}, query.ErrUnknownCollection
	}
	if name == "jobs" && !a.RiverAvailable(ctx) {
		return query.PageInfo{}, nil
	}
	compiled, err := a.compiler.Compile(col, req)
	if err != nil {
		return query.PageInfo{}, err
	}
	sql, nSort := injectSortSelect(compiled)
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	rows, err := a.pool.Query(qctx, sql, compiled.Args...)
	if err != nil {
		return query.PageInfo{}, fmt.Errorf("admin/store: list %s: %w", name, err)
	}
	defer rows.Close()

	var lastSort []string
	n := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return query.PageInfo{}, err
		}
		if len(vals) < nDataCols+nSort {
			return query.PageInfo{}, fmt.Errorf("admin/store: list %s: got %d cols, want >= %d", name, len(vals), nDataCols+nSort)
		}
		data := vals[:nDataCols]
		sortRaw := vals[len(vals)-nSort:]
		keep := n < compiled.Limit
		if err := handle(data, keep); err != nil {
			return query.PageInfo{}, err
		}
		if keep {
			sv := make([]string, nSort)
			for i, v := range sortRaw {
				sv[i] = query.FormatSortValue(v)
			}
			lastSort = sv
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return query.PageInfo{}, err
	}
	info := query.PageInfo{HasMore: n > compiled.Limit}
	if info.HasMore && len(lastSort) > 0 {
		cur, err := a.compiler.NextCursor(col, compiled, lastSort)
		if err != nil {
			return query.PageInfo{}, err
		}
		info.NextCursor = cur
	}
	return info, nil
}

func injectSortSelect(c *query.Compiled) (string, int) {
	sql := c.SQL
	const sel = "SELECT "
	if !strings.HasPrefix(sql, sel) || len(c.SortColumns) == 0 {
		return sql, 0
	}
	rest := sql[len(sel):]
	fromIdx := strings.Index(rest, " FROM ")
	if fromIdx < 0 {
		return sql, 0
	}
	selectList := rest[:fromIdx]
	afterFrom := rest[fromIdx:]
	extras := make([]string, len(c.SortColumns))
	for i, col := range c.SortColumns {
		extras[i] = fmt.Sprintf("(%s) AS __sk%d", col, i)
	}
	return sel + selectList + ", " + strings.Join(extras, ", ") + afterFrom, len(extras)
}

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case [16]byte:
		return query.FormatSortValue(t)
	case []byte:
		if len(t) == 16 {
			return query.FormatSortValue(t)
		}
		return string(t)
	default:
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return fmt.Sprint(t)
	}
}

func asTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case *time.Time:
		if t == nil {
			return time.Time{}
		}
		return *t
	default:
		return time.Time{}
	}
}

func asTimePtr(v any) *time.Time {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return nil
		}
		tt := t
		return &tt
	case *time.Time:
		return t
	default:
		return nil
	}
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case int:
		return int64(t)
	case int16:
		return int64(t)
	case float64:
		return int64(t)
	default:
		return 0
	}
}

func asInt32(v any) int32 { return int32(asInt64(v)) }

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	default:
		return false
	}
}

func asStruct(v any) *structpb.Struct {
	switch t := v.(type) {
	case nil:
		s, _ := structpb.NewStruct(map[string]any{})
		return s
	case []byte:
		var m map[string]any
		if err := json.Unmarshal(t, &m); err != nil {
			s, _ := structpb.NewStruct(map[string]any{})
			return s
		}
		s, err := structpb.NewStruct(m)
		if err != nil {
			s, _ = structpb.NewStruct(map[string]any{})
		}
		return s
	case map[string]any:
		s, err := structpb.NewStruct(t)
		if err != nil {
			s, _ = structpb.NewStruct(map[string]any{})
		}
		return s
	default:
		s, _ := structpb.NewStruct(map[string]any{})
		return s
	}
}

func asStringMap(v any) map[string]string {
	switch t := v.(type) {
	case map[string]string:
		if t == nil {
			return map[string]string{}
		}
		return t
	case map[string]any:
		out := make(map[string]string, len(t))
		for k, vv := range t {
			out[k] = asString(vv)
		}
		return out
	default:
		return map[string]string{}
	}
}

func (a *AdminStore) ListTenants(ctx context.Context, req query.Request) ([]*adminv1.TenantSummary, query.PageInfo, error) {
	var items []*adminv1.TenantSummary
	info, err := a.list(ctx, "tenants", req, 6, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.TenantSummary{
			Id: asString(vals[0]), Name: asString(vals[1]), CreatedAt: ts(asTime(vals[2])),
			SessionCount: asInt64(vals[3]), RunCount: asInt64(vals[4]), ApiKeyCount: asInt64(vals[5]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetTenant(ctx context.Context, id string) (*adminv1.TenantDetail, error) {
	items, _, err := a.ListTenants(ctx, query.Request{
		Filters: []query.Filter{{Field: "id", Op: query.OpEq, Value: id}},
		Page:    query.Page{Limit: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, uc.ErrNotFound
	}
	return &adminv1.TenantDetail{Summary: items[0]}, nil
}

func (a *AdminStore) ListAPIKeys(ctx context.Context, req query.Request) ([]*adminv1.APIKeySummary, query.PageInfo, error) {
	var items []*adminv1.APIKeySummary
	info, err := a.list(ctx, "api_keys", req, 9, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.APIKeySummary{
			Id: asString(vals[0]), TenantId: asString(vals[1]), Name: asString(vals[2]),
			Scope: asString(vals[3]), Prefix: asString(vals[4]),
			CreatedAt: ts(asTime(vals[5])), RevokedAt: tsPtr(asTimePtr(vals[6])),
			KeyHashPrefix: asString(vals[7]), HasCiphertext: asBool(vals[8]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetAPIKey(ctx context.Context, id string) (*adminv1.APIKeySummary, error) {
	items, _, err := a.ListAPIKeys(ctx, query.Request{
		Filters: []query.Filter{{Field: "id", Op: query.OpEq, Value: id}},
		Page:    query.Page{Limit: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, uc.ErrNotFound
	}
	return items[0], nil
}

func (a *AdminStore) ListSessions(ctx context.Context, req query.Request) ([]*adminv1.SessionSummary, query.PageInfo, error) {
	var items []*adminv1.SessionSummary
	info, err := a.list(ctx, "sessions", req, 10, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.SessionSummary{
			Id: asString(vals[0]), TenantId: asString(vals[1]), Title: asString(vals[2]),
			Labels: asStringMap(vals[3]), LastSeq: asInt64(vals[4]),
			CreatedAt: ts(asTime(vals[5])), ArchivedAt: tsPtr(asTimePtr(vals[6])),
			EventCount: asInt64(vals[7]), RunCount: asInt64(vals[8]), ResourceCount: asInt64(vals[9]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetSession(ctx context.Context, id string) (*adminv1.SessionDetail, error) {
	items, _, err := a.ListSessions(ctx, query.Request{
		Filters: []query.Filter{{Field: "id", Op: query.OpEq, Value: id}},
		Page:    query.Page{Limit: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, uc.ErrNotFound
	}
	keys := make([]string, 0, len(items[0].Labels))
	for k := range items[0].Labels {
		keys = append(keys, k)
	}
	return &adminv1.SessionDetail{Summary: items[0], LabelKeys: keys}, nil
}

func (a *AdminStore) ListEvents(ctx context.Context, req query.Request) ([]*adminv1.EventSummary, query.PageInfo, error) {
	var items []*adminv1.EventSummary
	info, err := a.list(ctx, "events", req, 10, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.EventSummary{
			SessionId: asString(vals[0]), TenantId: asString(vals[1]), Seq: asInt64(vals[2]),
			Ts: ts(asTime(vals[3])), ActorKind: asString(vals[4]), ActorId: asString(vals[5]),
			ActorDisplay: asString(vals[6]), Kind: asString(vals[7]),
			PayloadBytes: asInt32(vals[8]), PayloadPreview: asString(vals[9]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetEvent(ctx context.Context, sessionID string, seq int64) (*adminv1.EventDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var tenantID, actorKind, actorID, actorDisplay, kind string
	var tsVal time.Time
	var payload []byte
	err := a.pool.QueryRow(qctx, `
		SELECT s.tenant_id::text, e.ts, e.actor_type, e.actor_id, e.actor_display, e.kind, e.payload
		  FROM session_events e
		  JOIN sessions s ON s.id = e.session_id
		 WHERE e.session_id = $1 AND e.seq = $2`, sessionID, seq).
		Scan(&tenantID, &tsVal, &actorKind, &actorID, &actorDisplay, &kind, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get event: %w", err)
	}
	preview := string(payload)
	if len(preview) > query.MaxPreviewBytes {
		preview = preview[:query.MaxPreviewBytes]
	}
	return &adminv1.EventDetail{
		Summary: &adminv1.EventSummary{
			SessionId: sessionID, TenantId: tenantID, Seq: seq, Ts: ts(tsVal),
			ActorKind: actorKind, ActorId: actorID, ActorDisplay: actorDisplay,
			Kind: kind, PayloadBytes: int32(len(payload)), PayloadPreview: preview,
		},
		PayloadJson: payload,
	}, nil
}

func (a *AdminStore) ListRuns(ctx context.Context, req query.Request) ([]*adminv1.RunSummary, query.PageInfo, error) {
	var items []*adminv1.RunSummary
	info, err := a.list(ctx, "runs", req, 21, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.RunSummary{
			Id: asString(vals[0]), SessionId: asString(vals[1]), TenantId: asString(vals[2]),
			State: asString(vals[3]), LoopKind: asString(vals[4]), LoopVersion: asInt32(vals[5]),
			ParentRunId: asString(vals[6]), SpawnKey: asString(vals[7]), CohortId: asString(vals[8]),
			CohortOrdinal: asInt32(vals[9]), FailureReason: asString(vals[10]),
			ActorKind: asString(vals[11]), ActorId: asString(vals[12]), ActorDisplay: asString(vals[13]),
			CreatedAt: ts(asTime(vals[14])), UpdatedAt: ts(asTime(vals[15])),
			CancelRequestedAt: tsPtr(asTimePtr(vals[16])), StepCount: asInt32(vals[17]),
			PromptBytes: asInt32(vals[18]), HistoryBytes: asInt32(vals[19]), HasResult: asBool(vals[20]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetRun(ctx context.Context, id string) (*adminv1.RunDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var sessionID, tenantID, state, loopKind, parent, spawn, cohort, failReason, failMsg, aKind, aID, aDisp, prompt string
	var loopVer, cohortOrd int32
	var modelCfg, policy, result []byte
	var created, updated time.Time
	var cancelAt *time.Time
	var histBytes int
	err := a.pool.QueryRow(qctx, `
		SELECT id::text, session_id::text, tenant_id::text, state, loop_kind, loop_version,
			COALESCE(parent_run_id::text,''), COALESCE(spawn_key,''), COALESCE(cohort_id::text,''), COALESCE(cohort_ordinal,0),
			failure_reason, failure_message, COALESCE(actor_kind,''), COALESCE(actor_id,''), COALESCE(actor_display,''),
			created_at, updated_at, cancel_requested_at, prompt, model_config, grants, result,
			octet_length(history::text)
		  FROM agent_runs WHERE id = $1`, id).
		Scan(&id, &sessionID, &tenantID, &state, &loopKind, &loopVer,
			&parent, &spawn, &cohort, &cohortOrd, &failReason, &failMsg, &aKind, &aID, &aDisp,
			&created, &updated, &cancelAt, &prompt, &modelCfg, &policy, &result, &histBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get run: %w", err)
	}
	return &adminv1.RunDetail{
		Summary: &adminv1.RunSummary{
			Id: id, SessionId: sessionID, TenantId: tenantID, State: state,
			LoopKind: loopKind, LoopVersion: loopVer, ParentRunId: parent,
			SpawnKey: spawn, CohortId: cohort, CohortOrdinal: cohortOrd,
			FailureReason: failReason, ActorKind: aKind, ActorId: aID, ActorDisplay: aDisp,
			CreatedAt: ts(created), UpdatedAt: ts(updated), CancelRequestedAt: tsPtr(cancelAt),
			PromptBytes: int32(len(prompt)), HistoryBytes: int32(histBytes), HasResult: result != nil,
		},
		ModelConfigJson: modelCfg, PolicyJson: policy, ResultJson: result,
		FailureMessage: failMsg, Prompt: prompt,
	}, nil
}

func (a *AdminStore) GetRunHistory(ctx context.Context, id string) (*adminv1.RunHistoryBlob, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var history []byte
	err := a.pool.QueryRow(qctx, `SELECT history FROM agent_runs WHERE id = $1`, id).Scan(&history)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get run history: %w", err)
	}
	return &adminv1.RunHistoryBlob{RunId: id, HistoryJson: history}, nil
}

func (a *AdminStore) ListRunSteps(ctx context.Context, req query.Request) ([]*adminv1.RunStepSummary, query.PageInfo, error) {
	var items []*adminv1.RunStepSummary
	info, err := a.list(ctx, "run_steps", req, 9, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.RunStepSummary{
			RunId: asString(vals[0]), TenantId: asString(vals[1]), SessionId: asString(vals[2]),
			StepIndex: asInt32(vals[3]), Attempt: asInt32(vals[4]),
			TokensIn: asInt64(vals[5]), TokensOut: asInt64(vals[6]),
			FinishReason: asString(vals[7]), CreatedAt: ts(asTime(vals[8])),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) ListResources(ctx context.Context, req query.Request) ([]*adminv1.ResourceSummary, query.PageInfo, error) {
	var items []*adminv1.ResourceSummary
	info, err := a.list(ctx, "resources", req, 17, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.ResourceSummary{
			Id: asString(vals[0]), TenantId: asString(vals[1]), SessionId: asString(vals[2]),
			ProviderInstanceId: asString(vals[3]), Kind: asString(vals[4]), State: asString(vals[5]),
			Endpoint: asString(vals[6]), Epoch: asInt32(vals[7]), FailureMessage: asString(vals[8]),
			CreatedByRunId: asString(vals[9]), CreatedAt: ts(asTime(vals[10])), UpdatedAt: ts(asTime(vals[11])),
			ReadyAt: tsPtr(asTimePtr(vals[12])), TerminatedAt: tsPtr(asTimePtr(vals[13])),
			SpecBytes: asInt32(vals[14]), HandleBytes: asInt32(vals[15]), HasToken: asBool(vals[16]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetResource(ctx context.Context, id string) (*adminv1.ResourceDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var tenantID, sessionID, provID, kind, state, endpoint, fail, createdBy, hashPrefix string
	var epoch int32
	var created, updated time.Time
	var ready, term *time.Time
	var spec, handle []byte
	var hasCT bool
	err := a.pool.QueryRow(qctx, `
		SELECT id::text, tenant_id::text, session_id::text, provider_instance_id::text, kind, state, endpoint, epoch,
			failure_message, COALESCE(created_by_run_id::text,''), created_at, updated_at, ready_at, terminated_at,
			spec, handle, encode(substring(token_hash from 1 for 4), 'hex'),
			(token_enc IS NOT NULL AND length(token_enc) > 0)
		  FROM resources WHERE id = $1`, id).
		Scan(&id, &tenantID, &sessionID, &provID, &kind, &state, &endpoint, &epoch,
			&fail, &createdBy, &created, &updated, &ready, &term, &spec, &handle, &hashPrefix, &hasCT)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get resource: %w", err)
	}
	return &adminv1.ResourceDetail{
		Summary: &adminv1.ResourceSummary{
			Id: id, TenantId: tenantID, SessionId: sessionID, ProviderInstanceId: provID,
			Kind: kind, State: state, Endpoint: endpoint, Epoch: epoch, FailureMessage: fail,
			CreatedByRunId: createdBy, CreatedAt: ts(created), UpdatedAt: ts(updated),
			ReadyAt: tsPtr(ready), TerminatedAt: tsPtr(term),
			SpecBytes: int32(len(spec)), HandleBytes: int32(len(handle)), HasToken: hasCT,
		},
		SpecJson: spec, HandleJson: handle,
		TokenHashPrefix: hashPrefix, HasTokenCiphertext: hasCT,
	}, nil
}

func (a *AdminStore) ListProviders(ctx context.Context, req query.Request) ([]*adminv1.ProviderSummary, query.PageInfo, error) {
	var items []*adminv1.ProviderSummary
	info, err := a.list(ctx, "providers", req, 10, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.ProviderSummary{
			Id: asString(vals[0]), TenantId: asString(vals[1]), Kind: asString(vals[2]),
			Name: asString(vals[3]), State: asString(vals[4]),
			LastHealthyAt: tsPtr(asTimePtr(vals[5])), CreatedAt: ts(asTime(vals[6])),
			ConfigBytes: asInt32(vals[7]), CapabilitiesBytes: asInt32(vals[8]), ResourceCount: asInt64(vals[9]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetProvider(ctx context.Context, id string) (*adminv1.ProviderDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var tenantID, kind, name, state string
	var healthy *time.Time
	var created time.Time
	var cfg, caps []byte
	err := a.pool.QueryRow(qctx, `
		SELECT id::text, tenant_id::text, kind, name, state, last_healthy_at, created_at, config, capabilities
		  FROM provider_instances WHERE id = $1`, id).
		Scan(&id, &tenantID, &kind, &name, &state, &healthy, &created, &cfg, &caps)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get provider: %w", err)
	}
	return &adminv1.ProviderDetail{
		Summary: &adminv1.ProviderSummary{
			Id: id, TenantId: tenantID, Kind: kind, Name: name, State: state,
			LastHealthyAt: tsPtr(healthy), CreatedAt: ts(created),
			ConfigBytes: int32(len(cfg)), CapabilitiesBytes: int32(len(caps)),
		},
		ConfigJson: cfg, CapabilitiesJson: caps,
	}, nil
}

func (a *AdminStore) ListCredentials(ctx context.Context, req query.Request) ([]*adminv1.CredentialSummary, query.PageInfo, error) {
	var items []*adminv1.CredentialSummary
	info, err := a.list(ctx, "credentials", req, 7, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.CredentialSummary{
			TenantId: asString(vals[0]), Kind: asString(vals[1]), Name: asString(vals[2]),
			CreatedAt: ts(asTime(vals[3])), RotatedAt: ts(asTime(vals[4])),
			CiphertextBytes: asInt32(vals[5]), Encrypted: asBool(vals[6]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetCredential(ctx context.Context, tenantID, kind, name string) (*adminv1.CredentialDetail, error) {
	items, _, err := a.ListCredentials(ctx, query.Request{
		Filters: []query.Filter{
			{Field: "tenant_id", Op: query.OpEq, Value: tenantID},
			{Field: "kind", Op: query.OpEq, Value: kind},
			{Field: "name", Op: query.OpEq, Value: name},
		},
		Page: query.Page{Limit: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, uc.ErrNotFound
	}
	return &adminv1.CredentialDetail{Summary: items[0]}, nil
}

func (a *AdminStore) ListPeriodicPrompts(ctx context.Context, req query.Request) ([]*adminv1.PeriodicPromptSummary, query.PageInfo, error) {
	var items []*adminv1.PeriodicPromptSummary
	info, err := a.list(ctx, "periodic_prompts", req, 10, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.PeriodicPromptSummary{
			Id: asString(vals[0]), TenantId: asString(vals[1]), SessionId: asString(vals[2]),
			RunId: asString(vals[3]), Schedule: asString(vals[4]), Enabled: asBool(vals[5]),
			NextAt: ts(asTime(vals[6])), CreatedAt: ts(asTime(vals[7])),
			PromptBytes: asInt32(vals[8]), PromptPreview: asString(vals[9]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetPeriodicPrompt(ctx context.Context, id string) (*adminv1.PeriodicPromptDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var tenantID, sessionID, runID, schedule, prompt string
	var enabled bool
	var nextAt, created time.Time
	err := a.pool.QueryRow(qctx, `
		SELECT id::text, tenant_id::text, session_id::text, COALESCE(run_id::text,''), schedule, enabled, next_at, created_at, prompt
		  FROM periodic_prompts WHERE id = $1`, id).
		Scan(&id, &tenantID, &sessionID, &runID, &schedule, &enabled, &nextAt, &created, &prompt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get periodic prompt: %w", err)
	}
	preview := prompt
	if len(preview) > query.MaxPreviewBytes {
		preview = preview[:query.MaxPreviewBytes]
	}
	return &adminv1.PeriodicPromptDetail{
		Summary: &adminv1.PeriodicPromptSummary{
			Id: id, TenantId: tenantID, SessionId: sessionID, RunId: runID,
			Schedule: schedule, Enabled: enabled, NextAt: ts(nextAt), CreatedAt: ts(created),
			PromptBytes: int32(len(prompt)), PromptPreview: preview,
		},
		Prompt: prompt,
	}, nil
}

func (a *AdminStore) ListMemory(ctx context.Context, req query.Request) ([]*adminv1.MemorySummary, query.PageInfo, error) {
	var items []*adminv1.MemorySummary
	info, err := a.list(ctx, "memory", req, 7, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.MemorySummary{
			SessionId: asString(vals[0]), TenantId: asString(vals[1]), Key: asString(vals[2]),
			UpdatedByKind: asString(vals[3]), UpdatedById: asString(vals[4]),
			UpdatedAt: ts(asTime(vals[5])), ValueBytes: asInt32(vals[6]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetMemory(ctx context.Context, sessionID, key string) (*adminv1.MemoryDetail, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var tenantID, byKind, byID string
	var updated time.Time
	var value []byte
	err := a.pool.QueryRow(qctx, `
		SELECT s.tenant_id::text, m.updated_by_type, m.updated_by_id, m.updated_at, m.value
		  FROM session_memory m JOIN sessions s ON s.id = m.session_id
		 WHERE m.session_id = $1 AND m.key = $2`, sessionID, key).
		Scan(&tenantID, &byKind, &byID, &updated, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get memory: %w", err)
	}
	return &adminv1.MemoryDetail{
		Summary: &adminv1.MemorySummary{
			SessionId: sessionID, TenantId: tenantID, Key: key,
			UpdatedByKind: byKind, UpdatedById: byID, UpdatedAt: ts(updated),
			ValueBytes: int32(len(value)),
		},
		ValueJson: value,
	}, nil
}

func (a *AdminStore) ListWaits(ctx context.Context, req query.Request) ([]*adminv1.WaitSummary, query.PageInfo, error) {
	var items []*adminv1.WaitSummary
	info, err := a.list(ctx, "waits", req, 15, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.WaitSummary{
			Id: asString(vals[0]), ParentRunId: asString(vals[1]), TenantId: asString(vals[2]),
			SessionId: asString(vals[3]), StepIndex: asInt32(vals[4]), ToolCallId: asString(vals[5]),
			Kind: asString(vals[6]), State: asString(vals[7]), TimeoutPolicy: asString(vals[8]),
			Deadline: ts(asTime(vals[9])), CreatedAt: ts(asTime(vals[10])),
			ResolvedAt: tsPtr(asTimePtr(vals[11])), ResumedAt: tsPtr(asTimePtr(vals[12])),
			MemberCount: asInt32(vals[13]), HasResult: asBool(vals[14]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetWait(ctx context.Context, id string) (*adminv1.WaitDetail, error) {
	items, _, err := a.ListWaits(ctx, query.Request{
		Filters: []query.Filter{{Field: "id", Op: query.OpEq, Value: id}},
		Page:    query.Page{Limit: 1},
	})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, uc.ErrNotFound
	}
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var result []byte
	_ = a.pool.QueryRow(qctx, `SELECT result FROM run_waits WHERE id = $1`, id).Scan(&result)
	mrows, err := a.pool.Query(qctx, `SELECT run_id::text, ordinal FROM run_wait_members WHERE wait_id = $1 ORDER BY ordinal`, id)
	if err != nil {
		return nil, fmt.Errorf("admin/store: wait members: %w", err)
	}
	defer mrows.Close()
	var members []*adminv1.WaitMember
	for mrows.Next() {
		var runID string
		var ord int32
		if err := mrows.Scan(&runID, &ord); err != nil {
			return nil, err
		}
		members = append(members, &adminv1.WaitMember{RunId: runID, Ordinal: ord})
	}
	return &adminv1.WaitDetail{Summary: items[0], ResultJson: result, Members: members}, mrows.Err()
}

func (a *AdminStore) ListJobs(ctx context.Context, req query.Request) ([]*adminv1.JobSummary, query.PageInfo, error) {
	if !a.RiverAvailable(ctx) {
		return nil, query.PageInfo{}, nil
	}
	var items []*adminv1.JobSummary
	info, err := a.list(ctx, "jobs", req, 14, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, &adminv1.JobSummary{
			Id: asInt64(vals[0]), Kind: asString(vals[1]), State: asString(vals[2]),
			Attempt: asInt32(vals[3]), MaxAttempts: asInt32(vals[4]),
			Queue: asString(vals[5]), Priority: asString(vals[6]),
			CreatedAt: ts(asTime(vals[7])), ScheduledAt: ts(asTime(vals[8])),
			AttemptedAt: tsPtr(asTimePtr(vals[9])), FinalizedAt: tsPtr(asTimePtr(vals[10])),
			ErrorsPreview: asString(vals[11]), ErrorsCount: asInt32(vals[12]), Tags: asString(vals[13]),
		})
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetJob(ctx context.Context, id int64) (*adminv1.JobDetail, error) {
	if !a.RiverAvailable(ctx) {
		return nil, uc.ErrNotFound
	}
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	var kind, state, queue string
	var priority int16
	var attempt, maxAttempts int32
	var created, scheduled time.Time
	var attempted, finalized *time.Time
	var args, metadata []byte
	var errorsRaw [][]byte
	var tags []string
	err := a.pool.QueryRow(qctx, `
		SELECT id, kind, state::text, attempt::int, max_attempts::int, queue, priority,
			created_at, scheduled_at, attempted_at, finalized_at, args, metadata, errors, tags
		  FROM river_job WHERE id = $1`, id).
		Scan(&id, &kind, &state, &attempt, &maxAttempts, &queue, &priority,
			&created, &scheduled, &attempted, &finalized, &args, &metadata, &errorsRaw, &tags)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uc.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin/store: get job: %w", err)
	}
	var errStrs []string
	for _, e := range errorsRaw {
		errStrs = append(errStrs, string(e))
	}
	preview := ""
	if len(errStrs) > 0 {
		preview = errStrs[0]
		if len(preview) > query.MaxPreviewBytes {
			preview = preview[:query.MaxPreviewBytes]
		}
	}
	tagStr := ""
	if len(tags) > 0 {
		tagStr = strings.Join(tags, ",")
	}
	return &adminv1.JobDetail{
		Summary: &adminv1.JobSummary{
			Id: id, Kind: kind, State: state, Attempt: attempt, MaxAttempts: maxAttempts,
			Queue: queue, Priority: fmt.Sprintf("%d", priority),
			CreatedAt: ts(created), ScheduledAt: ts(scheduled),
			AttemptedAt: tsPtr(attempted), FinalizedAt: tsPtr(finalized),
			ErrorsPreview: preview, ErrorsCount: int32(len(errStrs)), Tags: tagStr,
		},
		ArgsJson: args, MetadataJson: metadata, Errors: errStrs,
	}, nil
}

func (a *AdminStore) GetRuntimeHealth(ctx context.Context) (*adminv1.RuntimeHealth, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	h := &adminv1.RuntimeHealth{
		BuildVersion: a.buildVers,
		ServerTime:   timestamppb.Now(),
		Diagnostics:  map[string]string{},
	}
	_ = a.pool.QueryRow(qctx, `SELECT COALESCE(MAX(version_id),0) FROM goose_db_version`).Scan(&h.SchemaVersion)
	h.RiverSchemaPresent = a.RiverAvailable(qctx)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM tenants`).Scan(&h.TenantCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM sessions`).Scan(&h.SessionCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM session_events`).Scan(&h.EventCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM agent_runs`).Scan(&h.RunCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM resources`).Scan(&h.ResourceCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM provider_instances`).Scan(&h.ProviderCount)
	_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM run_waits WHERE state = 'open'`).Scan(&h.OpenWaitCount)
	if h.RiverSchemaPresent {
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'available'`).Scan(&h.QueueAvailable)
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'running'`).Scan(&h.QueueRunning)
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'scheduled'`).Scan(&h.QueueScheduled)
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'retryable'`).Scan(&h.QueueRetryable)
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'discarded'`).Scan(&h.QueueDiscarded)
		_ = a.pool.QueryRow(qctx, `SELECT count(*) FROM river_job WHERE state = 'completed'`).Scan(&h.QueueCompleted)
	} else {
		h.Diagnostics["river"] = "schema not present; start coreadmin after river migrate or cored"
	}
	return h, nil
}

func (a *AdminStore) GetSessionTimeline(ctx context.Context, sessionID string, page query.Page) ([]*adminv1.TimelineEntry, query.PageInfo, error) {
	limit := int(page.Limit)
	if limit == 0 {
		limit = query.DefaultLimit
	}
	if limit > query.MaxLimit {
		return nil, query.PageInfo{}, query.ErrInvalidLimit
	}
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()

	var exists bool
	if err := a.pool.QueryRow(qctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id = $1)`, sessionID).Scan(&exists); err != nil {
		return nil, query.PageInfo{}, err
	}
	if !exists {
		return nil, query.PageInfo{}, uc.ErrNotFound
	}

	sql := `
		SELECT * FROM (
			SELECT e.ts AS ts, 'event' AS kind, (e.session_id::text || ':' || e.seq::text) AS id,
				e.kind || ' seq=' || e.seq::text AS summary
			  FROM session_events e WHERE e.session_id = $1
			UNION ALL
			SELECT r.created_at, 'run', r.id::text, 'run ' || r.state || ' ' || r.loop_kind
			  FROM agent_runs r WHERE r.session_id = $1
			UNION ALL
			SELECT res.created_at, 'resource', res.id::text, 'resource ' || res.kind || ' ' || res.state
			  FROM resources res WHERE res.session_id = $1
			UNION ALL
			SELECT w.created_at, 'wait', w.id::text, 'wait ' || w.state
			  FROM run_waits w JOIN agent_runs r ON r.id = w.parent_run_id WHERE r.session_id = $1
		) t
		ORDER BY ts DESC, kind ASC, id ASC
		LIMIT $2`
	args := []any{sessionID, limit + 1}
	if page.Cursor != "" && a.compiler.Signer != nil {
		p, err := a.compiler.Signer.Verify(page.Cursor)
		if err != nil {
			return nil, query.PageInfo{}, err
		}
		if p.Collection != "session_timeline" || p.Fingerprint != sessionID || len(p.SortValues) != 3 {
			return nil, query.PageInfo{}, query.ErrCursorMismatch
		}
		sql = `
		SELECT * FROM (
			SELECT e.ts AS ts, 'event' AS kind, (e.session_id::text || ':' || e.seq::text) AS id,
				e.kind || ' seq=' || e.seq::text AS summary
			  FROM session_events e WHERE e.session_id = $1
			UNION ALL
			SELECT r.created_at, 'run', r.id::text, 'run ' || r.state || ' ' || r.loop_kind
			  FROM agent_runs r WHERE r.session_id = $1
			UNION ALL
			SELECT res.created_at, 'resource', res.id::text, 'resource ' || res.kind || ' ' || res.state
			  FROM resources res WHERE res.session_id = $1
			UNION ALL
			SELECT w.created_at, 'wait', w.id::text, 'wait ' || w.state
			  FROM run_waits w JOIN agent_runs r ON r.id = w.parent_run_id WHERE r.session_id = $1
		) t
		WHERE ts < $3::timestamptz
		   OR (ts = $3::timestamptz AND (kind > $4 OR (kind = $4 AND id > $5)))
		ORDER BY ts DESC, kind ASC, id ASC
		LIMIT $2`
		args = []any{sessionID, limit + 1, p.SortValues[0], p.SortValues[1], p.SortValues[2]}
	}

	rows, err := a.pool.Query(qctx, sql, args...)
	if err != nil {
		return nil, query.PageInfo{}, fmt.Errorf("admin/store: timeline: %w", err)
	}
	defer rows.Close()
	var items []*adminv1.TimelineEntry
	var lastSort []string
	n := 0
	for rows.Next() {
		var t time.Time
		var kind, id, summary string
		if err := rows.Scan(&t, &kind, &id, &summary); err != nil {
			return nil, query.PageInfo{}, err
		}
		n++
		if n <= limit {
			items = append(items, &adminv1.TimelineEntry{Ts: ts(t), Kind: kind, Id: id, Summary: summary})
			lastSort = []string{query.FormatSortValue(t), kind, id}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, query.PageInfo{}, err
	}
	info := query.PageInfo{HasMore: n > limit}
	if info.HasMore && a.compiler.Signer != nil {
		cur, err := a.compiler.Signer.Sign(query.CursorPayload{
			Collection: "session_timeline", Fingerprint: sessionID, SortValues: lastSort,
		})
		if err != nil {
			return nil, query.PageInfo{}, err
		}
		info.NextCursor = cur
	}
	return items, info, nil
}

func (a *AdminStore) ListRelated(ctx context.Context, collection, id, relation string, page query.Page) ([]*adminv1.RelatedRef, query.PageInfo, error) {
	limit := int(page.Limit)
	if limit == 0 {
		limit = query.DefaultLimit
	}
	if limit > query.MaxLimit {
		return nil, query.PageInfo{}, query.ErrInvalidLimit
	}
	// ListRelated is a first-page navigation aid. Full collection traversal uses
	// the corresponding List* RPC with an allowlisted filter (e.g. tenant_id /
	// session_id). Cursors are rejected here so callers do not assume keyset
	// semantics that the multi-edge fan-out cannot provide.
	if page.Cursor != "" {
		return nil, query.PageInfo{}, &query.ValidationError{
			Err: query.ErrBadCursor,
			Msg: "ListRelated does not support cursors; use the filtered List* RPC for the target collection",
		}
	}
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()

	type edge struct {
		sql  string
		args []any
	}
	var edges []edge
	switch collection {
	case "tenants":
		if relation == "" || relation == "sessions" {
			edges = append(edges, edge{`SELECT 'sessions', id::text, title FROM sessions WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "api_keys" {
			edges = append(edges, edge{`SELECT 'api_keys', id::text, name FROM api_keys WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "providers" {
			edges = append(edges, edge{`SELECT 'providers', id::text, name FROM provider_instances WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
	case "sessions":
		if relation == "" || relation == "runs" {
			edges = append(edges, edge{`SELECT 'runs', id::text, state || ' ' || loop_kind FROM agent_runs WHERE session_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "resources" {
			edges = append(edges, edge{`SELECT 'resources', id::text, kind || ' ' || state FROM resources WHERE session_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "events" {
			edges = append(edges, edge{`SELECT 'events', session_id::text || ':' || seq::text, kind FROM session_events WHERE session_id = $1 ORDER BY seq DESC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "memory" {
			edges = append(edges, edge{`SELECT 'memory', session_id::text || ':' || key, key FROM session_memory WHERE session_id = $1 ORDER BY updated_at DESC, key ASC LIMIT $2`, []any{id, limit + 1}})
		}
	case "runs":
		if relation == "" || relation == "steps" {
			edges = append(edges, edge{`SELECT 'run_steps', agent_run_id::text || ':' || step_index::text, 'step ' || step_index::text FROM agent_run_steps WHERE agent_run_id = $1 ORDER BY step_index LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "children" {
			edges = append(edges, edge{`SELECT 'runs', id::text, state FROM agent_runs WHERE parent_run_id = $1 ORDER BY created_at ASC, id ASC LIMIT $2`, []any{id, limit + 1}})
		}
		if relation == "" || relation == "waits" {
			edges = append(edges, edge{`SELECT 'waits', id::text, state FROM run_waits WHERE parent_run_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
	case "providers":
		if relation == "" || relation == "resources" {
			edges = append(edges, edge{`SELECT 'resources', id::text, kind || ' ' || state FROM resources WHERE provider_instance_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, []any{id, limit + 1}})
		}
	case "resources":
		if relation == "" || relation == "provider" {
			edges = append(edges, edge{`SELECT 'providers', p.id::text, p.name FROM provider_instances p JOIN resources r ON r.provider_instance_id = p.id WHERE r.id = $1 LIMIT $2`, []any{id, limit + 1}})
		}
	default:
		return nil, query.PageInfo{}, fmt.Errorf("%w: %s", query.ErrUnknownCollection, collection)
	}
	if len(edges) == 0 {
		return nil, query.PageInfo{}, &query.ValidationError{
			Err: query.ErrInvalidField,
			Msg: fmt.Sprintf("unknown relation %q for collection %q", relation, collection),
		}
	}

	var items []*adminv1.RelatedRef
	for _, e := range edges {
		rows, err := a.pool.Query(qctx, e.sql, e.args...)
		if err != nil {
			return nil, query.PageInfo{}, fmt.Errorf("admin/store: related: %w", err)
		}
		for rows.Next() {
			var col, rid, label string
			if err := rows.Scan(&col, &rid, &label); err != nil {
				rows.Close()
				return nil, query.PageInfo{}, err
			}
			items = append(items, &adminv1.RelatedRef{Collection: col, Id: rid, Label: label})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, query.PageInfo{}, err
		}
	}
	info := query.PageInfo{}
	if len(items) > limit {
		items = items[:limit]
		info.HasMore = true
		// No next_cursor: callers continue via filtered List* RPCs.
	}
	return items, info, nil
}

func (a *AdminStore) ListAuditEvents(ctx context.Context, req query.Request) ([]*adminv1.AuditEventSummary, query.PageInfo, error) {
	var items []*adminv1.AuditEventSummary
	info, err := a.list(ctx, "audit_events", req, 16, func(vals []any, keep bool) error {
		if !keep {
			return nil
		}
		items = append(items, auditFromVals(vals))
		return nil
	})
	return items, info, err
}

func (a *AdminStore) GetAuditEvent(ctx context.Context, id string) (*adminv1.AuditEventSummary, error) {
	qctx, cancel := a.withTimeout(ctx)
	defer cancel()
	row := a.pool.QueryRow(qctx, `
		SELECT id::text, ts, operator_id, operator_role, request_id, command,
		       targets, reason, preview_hash, before_summary, after_summary,
		       result, error, source_ip, build_version, COALESCE(idempotency_key,'')
		  FROM admin_audit_events WHERE id=$1`, id)
	vals := make([]any, 16)
	ptrs := make([]any, 16)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, uc.ErrNotFound
		}
		return nil, err
	}
	return auditFromVals(vals), nil
}

func auditFromVals(vals []any) *adminv1.AuditEventSummary {
	return &adminv1.AuditEventSummary{
		Id:             asString(vals[0]),
		Ts:             ts(asTime(vals[1])),
		OperatorId:     asString(vals[2]),
		OperatorRole:   asString(vals[3]),
		RequestId:      asString(vals[4]),
		Command:        asString(vals[5]),
		Targets:        asStruct(vals[6]),
		Reason:         asString(vals[7]),
		PreviewHash:    asString(vals[8]),
		BeforeSummary:  asStruct(vals[9]),
		AfterSummary:   asStruct(vals[10]),
		Result:         asString(vals[11]),
		Error:          asString(vals[12]),
		SourceIp:       asString(vals[13]),
		BuildVersion:   asString(vals[14]),
		IdempotencyKey: asString(vals[15]),
	}
}
