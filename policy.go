package core

// RunPolicy is fixed at run creation and immutable thereafter. It is the
// consumer's mechanical constraint on what a run may do: which tools, which
// resource kinds, and how many children. The core enforces it without knowing
// why the consumer chose those bounds.
type RunPolicy struct {
	// AllowTools is the positive tool allowlist. "*" means every canonical
	// tool. An empty list means no tools.
	AllowTools []string `json:"allow_tools"`
	// DenyTools is evaluated after AllowTools and always wins.
	DenyTools []string `json:"deny_tools,omitempty"`
	// ResourceKinds lists kinds this run may provision. Empty means none;
	// ["*"] means all.
	ResourceKinds []ResourceKind `json:"resource_kinds,omitempty"`
	// MaxChildren caps spawn_agent / run_agent_cohort fan-out for this run.
	// 0 means no spawning.
	MaxChildren int `json:"max_children"`
	// ChildInherit, when true, forces every child to receive this same policy
	// verbatim. When false, a spawn may pass a child policy that must be a
	// subset of the parent.
	ChildInherit bool `json:"child_inherit"`
}

// DefaultRunPolicy is assigned to API-started runs that omit a policy.
// Full tools, all resource kinds, a modest spawn cap, children inherit.
func DefaultRunPolicy() RunPolicy {
	return RunPolicy{
		AllowTools: []string{"*"},
		ResourceKinds: []ResourceKind{"*"},
		MaxChildren:   16,
		ChildInherit:  false,
	}
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

func kindSet(values []ResourceKind) map[ResourceKind]bool {
	out := map[ResourceKind]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// AllowsTool reports whether name is permitted by the allow list and not
// blocked by the deny list.
func (p RunPolicy) AllowsTool(name string) bool {
	deny := stringSet(p.DenyTools)
	if deny[name] || deny["*"] {
		return false
	}
	allow := stringSet(p.AllowTools)
	return allow["*"] || allow[name]
}

// AllowsResourceKind reports whether kind may be provisioned under this policy.
// Empty ResourceKinds means none; ["*"] means all.
func (p RunPolicy) AllowsResourceKind(kind ResourceKind) bool {
	if len(p.ResourceKinds) == 0 {
		return false
	}
	s := kindSet(p.ResourceKinds)
	return s["*"] || s[kind]
}

// effectiveAllow returns the set of tools the policy actually permits:
// allow minus deny. Used for subset checks.
func (p RunPolicy) effectiveAllow() map[string]bool {
	allow := stringSet(p.AllowTools)
	deny := stringSet(p.DenyTools)
	if deny["*"] {
		return map[string]bool{}
	}
	if allow["*"] {
		// Unbounded allow with finite denies cannot be represented as a
		// finite set; callers treat "*" specially via hasStar.
		return map[string]bool{"*": true}
	}
	out := map[string]bool{}
	for t := range allow {
		if !deny[t] {
			out[t] = true
		}
	}
	return out
}

func (p RunPolicy) allowHasStar() bool {
	if stringSet(p.DenyTools)["*"] {
		return false
	}
	return stringSet(p.AllowTools)["*"]
}

// SubsetOf reports whether p is no wider than parent on every axis:
//   - effective allow ⊆ parent's effective allow
//   - resource kinds ⊆ parent's kinds
//   - MaxChildren ≤ parent's MaxChildren
//
// Deny lists only tighten, so a child with extra denies is still a subset.
func (p RunPolicy) IsSubset(parent RunPolicy) bool {
	if p.MaxChildren > parent.MaxChildren {
		return false
	}
	// Tools.
	if p.allowHasStar() && !parent.allowHasStar() {
		return false
	}
	if !p.allowHasStar() {
		parentAllow := parent.effectiveAllow()
		parentStar := parent.allowHasStar()
		parentDeny := stringSet(parent.DenyTools)
		for t := range p.effectiveAllow() {
			if t == "*" {
				continue
			}
			if parentDeny[t] {
				return false
			}
			if !parentStar && !parentAllow[t] {
				return false
			}
		}
	} else {
		// Child has "*": every tool parent denies must also be denied by child.
		childDeny := stringSet(p.DenyTools)
		for _, d := range parent.DenyTools {
			if d == "*" {
				continue
			}
			if !childDeny[d] && !childDeny["*"] {
				return false
			}
		}
	}
	// Resource kinds.
	if !kindsSubset(p.ResourceKinds, parent.ResourceKinds) {
		return false
	}
	return true
}

func kindsSubset(child, parent []ResourceKind) bool {
	if len(child) == 0 {
		return true // none ⊆ anything
	}
	ps := kindSet(parent)
	if ps["*"] {
		return true
	}
	cs := kindSet(child)
	if cs["*"] {
		return false // all ⊈ finite
	}
	for k := range cs {
		if !ps[k] {
			return false
		}
	}
	return true
}

// CanonicalTools is every capability a run can be granted: the native tools
// plus the resource tools Bezalel exposes for dev_env.
//
// It exists so a run can be offered an explicit denial stub for the tools it
// lacks. Simply omitting them would be worse: the agent framework answers an
// unknown tool call by listing every tool that *does* exist, which is an
// existence oracle. A uniform refusal reveals nothing.
//
// provision_env/list_envs/terminate_env are kept as aliases of the
// provision_resource family through E5/E6; both names are grantable.
func CanonicalTools() []string {
	return []string{
		// Native session and orchestration tools.
		"ask_user", "post_event",
		"session_memory_get", "session_memory_list", "session_memory_set", "session_memory_delete",
		"spawn_agent", "wait_for_agents", "run_agent_cohort",
		"provision_resource", "list_resources", "terminate_resource",
		// Aliases kept through E5/E6 migrations.
		"provision_env", "list_envs", "terminate_env",
		// Resource tools served over MCP (dev_env).
		"bash", "view", "write", "edit", "multiedit", "delete", "ls", "glob", "grep",
		"job_output", "job_kill", "download", "fetch", "web_fetch",
		"lsp_diagnostics", "lsp_references", "lsp_restart",
	}
}
