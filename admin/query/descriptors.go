package query

// Registry holds all admin collection descriptors.
type Registry struct {
	byName map[string]Collection
	order  []string
}

// NewRegistry builds the default admin collection set.
func NewRegistry() *Registry {
	cols := []Collection{
		tenantsCollection(),
		apiKeysCollection(),
		sessionsCollection(),
		eventsCollection(),
		runsCollection(),
		runStepsCollection(),
		resourcesCollection(),
		providersCollection(),
		credentialsCollection(),
		periodicPromptsCollection(),
		memoryCollection(),
		waitsCollection(),
		jobsCollection(),
		auditEventsCollection(),
	}
	r := &Registry{byName: make(map[string]Collection, len(cols))}
	for _, c := range cols {
		r.byName[c.Name] = c
		r.order = append(r.order, c.Name)
	}
	return r
}

// Get returns a collection by name.
func (r *Registry) Get(name string) (Collection, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// All returns collections in registration order.
func (r *Registry) All() []Collection {
	out := make([]Collection, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}

func stdStringOps() []Op {
	return []Op{OpEq, OpNe, OpIn, OpNotIn, OpContains, OpPrefix, OpIsNull, OpIsNotNull}
}

func stdUUIDOps() []Op {
	return []Op{OpEq, OpNe, OpIn, OpNotIn, OpIsNull, OpIsNotNull}
}

func stdIntOps() []Op {
	return []Op{OpEq, OpNe, OpLt, OpLte, OpGt, OpGte, OpIn, OpNotIn}
}

func stdTSOps() []Op {
	return []Op{OpEq, OpNe, OpLt, OpLte, OpGt, OpGte, OpIsNull, OpIsNotNull}
}

func stdBoolOps() []Op {
	return []Op{OpEq, OpNe}
}

func tenantsCollection() Collection {
	return Collection{
		Name:        "tenants",
		Description: "Tenants (cross-tenant operator view)",
		From: `tenants t
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM sessions s WHERE s.tenant_id = t.id) sc ON true
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM agent_runs r WHERE r.tenant_id = t.id) rc ON true
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM api_keys k WHERE k.tenant_id = t.id) kc ON true`,
		Select: `t.id::text, t.name, t.created_at, COALESCE(sc.c,0), COALESCE(rc.c,0), COALESCE(kc.c,0)`,
		Fields: []Field{
			{Name: "id", Column: "t.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "name", Column: "t.name", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "created_at", Column: "t.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func apiKeysCollection() Collection {
	return Collection{
		Name:        "api_keys",
		Description: "API key metadata (never raw keys or full secrets)",
		From:        `api_keys k`,
		Select: `k.id::text, k.tenant_id::text, k.name, k.scope, k.prefix, k.created_at, k.revoked_at,
			encode(substring(k.key_hash from 1 for 4), 'hex'), (k.key_enc IS NOT NULL AND length(k.key_enc) > 0)`,
		Fields: []Field{
			{Name: "id", Column: "k.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "k.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "name", Column: "k.name", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "scope", Column: "k.scope", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "prefix", Column: "k.prefix", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "created_at", Column: "k.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "revoked_at", Column: "k.revoked_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func sessionsCollection() Collection {
	return Collection{
		Name:        "sessions",
		Description: "Sessions across all tenants",
		From: `sessions s
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM session_events e WHERE e.session_id = s.id) ec ON true
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM agent_runs r WHERE r.session_id = s.id) rc ON true
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM resources res WHERE res.session_id = s.id) rsc ON true`,
		Select: `s.id::text, s.tenant_id::text, s.title, s.labels, s.last_seq, s.created_at, s.archived_at,
			COALESCE(ec.c,0), COALESCE(rc.c,0), COALESCE(rsc.c,0)`,
		Fields: []Field{
			{Name: "id", Column: "s.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "s.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "title", Column: "s.title", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "last_seq", Column: "s.last_seq", Type: TypeInt, FilterOps: stdIntOps(), Sortable: true},
			{Name: "created_at", Column: "s.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "archived_at", Column: "s.archived_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func eventsCollection() Collection {
	return Collection{
		Name:        "events",
		Description: "Session event log (payload summaries only in list)",
		From: `session_events e
			JOIN sessions s ON s.id = e.session_id`,
		Select: `e.session_id::text, s.tenant_id::text, e.seq, e.ts, e.actor_type, e.actor_id, e.actor_display, e.kind,
			octet_length(e.payload::text)::int,
			left(e.payload::text, 256)`,
		Fields: []Field{
			{Name: "session_id", Column: "e.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "seq", Column: "e.seq", Type: TypeInt, FilterOps: stdIntOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "s.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "ts", Column: "e.ts", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "kind", Column: "e.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "actor_kind", Column: "e.actor_type", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "actor_id", Column: "e.actor_id", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
		},
		DefaultSorts: []Sort{{Field: "ts", Descending: true}},
		HasDetail:    true,
	}
}

func runsCollection() Collection {
	return Collection{
		Name:        "runs",
		Description: "Agent runs across tenants",
		From: `agent_runs r
			LEFT JOIN LATERAL (SELECT count(*)::int AS c FROM agent_run_steps st WHERE st.agent_run_id = r.id) sc ON true`,
		Select: `r.id::text, r.session_id::text, r.tenant_id::text, r.state, r.loop_kind, r.loop_version,
			COALESCE(r.parent_run_id::text,''), COALESCE(r.spawn_key,''), COALESCE(r.cohort_id::text,''), COALESCE(r.cohort_ordinal,0),
			r.failure_reason, COALESCE(r.actor_kind,''), COALESCE(r.actor_id,''), COALESCE(r.actor_display,''),
			r.created_at, r.updated_at, r.cancel_requested_at, COALESCE(sc.c,0),
			octet_length(r.prompt)::int, octet_length(r.history::text)::int, (r.result IS NOT NULL)`,
		Fields: []Field{
			{Name: "id", Column: "r.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "session_id", Column: "r.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "tenant_id", Column: "r.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "state", Column: "r.state", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "loop_kind", Column: "r.loop_kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "parent_run_id", Column: "r.parent_run_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "cohort_id", Column: "r.cohort_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "spawn_key", Column: "r.spawn_key", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "created_at", Column: "r.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "updated_at", Column: "r.updated_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
		HasBlob:      true,
	}
}

func runStepsCollection() Collection {
	return Collection{
		Name:        "run_steps",
		Description: "Per-run step records",
		From: `agent_run_steps st
			JOIN agent_runs r ON r.id = st.agent_run_id`,
		Select: `st.agent_run_id::text, r.tenant_id::text, r.session_id::text, st.step_index, st.attempt,
			st.tokens_in, st.tokens_out, st.finish_reason, st.created_at`,
		Fields: []Field{
			{Name: "run_id", Column: "st.agent_run_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "step_index", Column: "st.step_index", Type: TypeInt, FilterOps: stdIntOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "r.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "session_id", Column: "r.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "created_at", Column: "st.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "finish_reason", Column: "st.finish_reason", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
	}
}

func resourcesCollection() Collection {
	return Collection{
		Name:        "resources",
		Description: "Session resources and lifecycle state",
		From:        `resources res`,
		Select: `res.id::text, res.tenant_id::text, res.session_id::text, res.provider_instance_id::text,
			res.kind, res.state, res.endpoint, res.epoch, res.failure_message, COALESCE(res.created_by_run_id::text,''),
			res.created_at, res.updated_at, res.ready_at, res.terminated_at,
			octet_length(res.spec::text)::int, octet_length(res.handle::text)::int,
			(res.token_enc IS NOT NULL AND length(res.token_enc) > 0)`,
		Fields: []Field{
			{Name: "id", Column: "res.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "res.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "session_id", Column: "res.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "provider_instance_id", Column: "res.provider_instance_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "kind", Column: "res.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "state", Column: "res.state", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "created_at", Column: "res.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "updated_at", Column: "res.updated_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func providersCollection() Collection {
	return Collection{
		Name:        "providers",
		Description: "Provider instance registrations",
		From: `provider_instances p
			LEFT JOIN LATERAL (SELECT count(*)::bigint AS c FROM resources res WHERE res.provider_instance_id = p.id) rc ON true`,
		Select: `p.id::text, p.tenant_id::text, p.kind, p.name, p.state, p.last_healthy_at, p.created_at,
			octet_length(p.config::text)::int, octet_length(p.capabilities::text)::int, COALESCE(rc.c,0)`,
		Fields: []Field{
			{Name: "id", Column: "p.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "p.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "kind", Column: "p.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "name", Column: "p.name", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "state", Column: "p.state", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "created_at", Column: "p.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "last_healthy_at", Column: "p.last_healthy_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func credentialsCollection() Collection {
	return Collection{
		Name:        "credentials",
		Description: "Inference credential metadata (ciphertext only, never plaintext)",
		From:        `credentials c`,
		Select: `c.tenant_id::text, c.kind, c.name, c.created_at, c.rotated_at,
			length(c.enc_payload)::int, true`,
		Fields: []Field{
			{Name: "tenant_id", Column: "c.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "kind", Column: "c.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true, PK: true},
			{Name: "name", Column: "c.name", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true, PK: true},
			{Name: "created_at", Column: "c.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "rotated_at", Column: "c.rotated_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func periodicPromptsCollection() Collection {
	return Collection{
		Name:        "periodic_prompts",
		Description: "Scheduled periodic prompts",
		From:        `periodic_prompts pp`,
		Select: `pp.id::text, pp.tenant_id::text, pp.session_id::text, COALESCE(pp.run_id::text,''),
			pp.schedule, pp.enabled, pp.next_at, pp.created_at,
			octet_length(pp.prompt)::int, left(pp.prompt, 256)`,
		Fields: []Field{
			{Name: "id", Column: "pp.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "tenant_id", Column: "pp.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "session_id", Column: "pp.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "run_id", Column: "pp.run_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "schedule", Column: "pp.schedule", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "enabled", Column: "pp.enabled", Type: TypeBool, FilterOps: stdBoolOps(), Sortable: true},
			{Name: "next_at", Column: "pp.next_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "created_at", Column: "pp.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "next_at", Descending: false}},
		HasDetail:    true,
	}
}

func memoryCollection() Collection {
	return Collection{
		Name:        "memory",
		Description: "Session memory entries",
		From: `session_memory m
			JOIN sessions s ON s.id = m.session_id`,
		Select: `m.session_id::text, s.tenant_id::text, m.key, m.updated_by_type, m.updated_by_id, m.updated_at,
			octet_length(m.value::text)::int`,
		Fields: []Field{
			{Name: "session_id", Column: "m.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "key", Column: "m.key", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true, PK: true},
			{Name: "tenant_id", Column: "s.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "updated_at", Column: "m.updated_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "updated_by_kind", Column: "m.updated_by_type", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "updated_at", Descending: true}},
		HasDetail:    true,
	}
}

func waitsCollection() Collection {
	return Collection{
		Name:        "waits",
		Description: "Run waits / awaits",
		From: `run_waits w
			JOIN agent_runs r ON r.id = w.parent_run_id
			LEFT JOIN LATERAL (SELECT count(*)::int AS c FROM run_wait_members m WHERE m.wait_id = w.id) mc ON true`,
		Select: `w.id::text, w.parent_run_id::text, r.tenant_id::text, r.session_id::text, w.step_index, w.tool_call_id,
			w.kind, w.state, w.timeout_policy, w.deadline, w.created_at, w.resolved_at, w.resumed_at,
			COALESCE(mc.c,0), (w.result IS NOT NULL)`,
		Fields: []Field{
			{Name: "id", Column: "w.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "parent_run_id", Column: "w.parent_run_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "tenant_id", Column: "r.tenant_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "session_id", Column: "r.session_id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true},
			{Name: "state", Column: "w.state", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "kind", Column: "w.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "deadline", Column: "w.deadline", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "created_at", Column: "w.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func jobsCollection() Collection {
	// River schema is created by river migrator. Admin queries tolerate missing
	// table via Store.RiverAvailable check; descriptor still exists for Describe.
	// errors is jsonb[] in River; coalesce to text for preview/count.
	return Collection{
		Name:        "jobs",
		Description: "River job queue rows",
		From:        `river_job j`,
		Select: `j.id, j.kind, j.state::text, j.attempt::int, j.max_attempts::int, j.queue, j.priority::text,
			j.created_at, j.scheduled_at, j.attempted_at, j.finalized_at,
			left(COALESCE(j.errors::text,''), 256), COALESCE(cardinality(j.errors),0),
			COALESCE(array_to_string(j.tags, ','), '')`,
		Fields: []Field{
			{Name: "id", Column: "j.id", Type: TypeInt, FilterOps: stdIntOps(), Sortable: true, PK: true},
			{Name: "kind", Column: "j.kind", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "state", Column: "j.state::text", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "queue", Column: "j.queue", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "attempt", Column: "j.attempt", Type: TypeInt, FilterOps: stdIntOps(), Sortable: true},
			{Name: "created_at", Column: "j.created_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "scheduled_at", Column: "j.scheduled_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "finalized_at", Column: "j.finalized_at", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
		},
		DefaultSorts: []Sort{{Field: "created_at", Descending: true}},
		HasDetail:    true,
	}
}

func auditEventsCollection() Collection {
	return Collection{
		Name:        "audit_events",
		Description: "Immutable operator admin audit events",
		From:        `admin_audit_events a`,
		Select: `a.id::text, a.ts, a.operator_id, a.operator_role, a.request_id, a.command,
			a.targets, a.reason, a.preview_hash, a.before_summary, a.after_summary,
			a.result, a.error, a.source_ip, a.build_version, COALESCE(a.idempotency_key,'')`,
		Fields: []Field{
			{Name: "id", Column: "a.id", Type: TypeUUID, FilterOps: stdUUIDOps(), Sortable: true, PK: true},
			{Name: "ts", Column: "a.ts", Type: TypeTimestamp, FilterOps: stdTSOps(), Sortable: true},
			{Name: "operator_id", Column: "a.operator_id", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "operator_role", Column: "a.operator_role", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "command", Column: "a.command", Type: TypeString, FilterOps: stdStringOps(), Sortable: true, Searchable: true},
			{Name: "result", Column: "a.result", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "request_id", Column: "a.request_id", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "idempotency_key", Column: "a.idempotency_key", Type: TypeString, FilterOps: stdStringOps(), Sortable: true},
			{Name: "reason", Column: "a.reason", Type: TypeString, FilterOps: stdStringOps(), Searchable: true},
		},
		DefaultSorts: []Sort{{Field: "ts", Descending: true}},
		HasDetail:    true,
	}
}
