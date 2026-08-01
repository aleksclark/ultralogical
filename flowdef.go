package ultra

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
)

// FlowDefinitionVersion is the schema version of the definition language this
// package understands. It is carried in rendered output so a stored rendering
// stays interpretable after the language grows.
const FlowDefinitionVersion = 1

// FlowDefinition is the v1 flow definition language: typed parameters,
// declared environments, and an agent topology. It is the whole contract — a
// flow may not reach anything it has not declared.
type FlowDefinition struct {
	// Description is operator documentation, ignored by execution.
	Description string               `json:"description,omitempty"`
	Params      map[string]FlowParam `json:"params,omitempty"`
	Envs        map[string]FlowEnv   `json:"envs,omitempty"`
	Agents      map[string]FlowAgent `json:"agents"`
}

// FlowParam is one typed invocation parameter.
type FlowParam struct {
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// FlowEnv is a declared environment the flow provisions on invocation. Setup
// commands run inside the environment before it is considered ready.
type FlowEnv struct {
	ProviderInstance string            `json:"provider_instance,omitempty"`
	Image            string            `json:"image,omitempty"`
	Workdir          string            `json:"workdir,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Setup            []string          `json:"setup,omitempty"`
	// Readiness selects the gate: "health" (default) waits for the provider
	// and the environment agent to answer; "none" only waits for the record
	// to leave the requested state.
	Readiness string `json:"readiness,omitempty"`
	// Required defaults to true: a required environment that fails to become
	// ready fails the invocation instead of silently starting agents without
	// the resources they declared.
	Required *bool `json:"required,omitempty"`
	// Timeout bounds readiness. Empty means the server default.
	Timeout string `json:"timeout,omitempty"`
}

// FlowAgent is one declared agent. Exactly one of "entry" or "after" makes it
// start automatically; "spawnable" publishes it to the flow's agent catalog so
// a running agent can spawn it by name.
type FlowAgent struct {
	Prompt      string      `json:"prompt"`
	Model       ModelConfig `json:"model,omitempty"`
	Tools       []string    `json:"tools,omitempty"`
	Envs        []string    `json:"envs,omitempty"`
	Entry       bool        `json:"entry,omitempty"`
	After       []string    `json:"after,omitempty"`
	Spawnable   bool        `json:"spawnable,omitempty"`
	MaySpawn    bool        `json:"may_spawn,omitempty"`
	MaxChildren int         `json:"max_children,omitempty"`
	// Timeout bounds the cohort wait a dependent stage installs.
	Timeout string `json:"timeout,omitempty"`
}

// Flow readiness policies.
const (
	FlowReadinessHealth = "health"
	FlowReadinessNone   = "none"
)

// Flow parameter types.
const (
	FlowParamString  = "string"
	FlowParamNumber  = "number"
	FlowParamBoolean = "boolean"
)

// Flow validation codes. They are the stable machine-readable half of a
// validation failure; the message is for humans and may be reworded.
const (
	FlowErrInvalidJSON      = "invalid_json"
	FlowErrUnknownField     = "unknown_field"
	FlowErrDuplicateName    = "duplicate_name"
	FlowErrInvalidName      = "invalid_name"
	FlowErrRequired         = "required"
	FlowErrUnknownParam     = "unknown_param"
	FlowErrUnknownEnv       = "unknown_env"
	FlowErrUnknownAgent     = "unknown_agent"
	FlowErrUnknownTool      = "unknown_tool"
	FlowErrInvalidTemplate  = "invalid_template"
	FlowErrInvalidType      = "invalid_type"
	FlowErrTypeMismatch     = "type_mismatch"
	FlowErrNoEntryAgent     = "no_entry_agent"
	FlowErrCycle            = "cycle"
	FlowErrUnreachableAgent = "unreachable_agent"
	FlowErrInvalidGrant     = "invalid_grant"
	FlowErrConflict         = "conflict"
	FlowErrInvalidDuration  = "invalid_duration"
	FlowErrUnsupported      = "unsupported"
	FlowErrUnknownProvider  = "unknown_provider_instance"
	FlowErrMissingParameter = "missing_parameter"
	FlowErrUnknownParameter = "unknown_parameter"
	FlowErrProviderMismatch = "unsupported_provider_capability"
)

// FlowFieldError is one validation failure addressed by a stable field path.
type FlowFieldError struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements error.
func (e FlowFieldError) Error() string { return e.Path + ": " + e.Code + ": " + e.Message }

// FlowValidationError is the complete, ordered set of validation failures.
// Ordering is stable (by path then code) so clients, the CLI, and tests all
// render the same list.
type FlowValidationError struct {
	Errors []FlowFieldError `json:"errors"`
}

// Error implements error.
func (e *FlowValidationError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, item := range e.Errors {
		parts = append(parts, item.Error())
	}
	return "flow definition is invalid: " + strings.Join(parts, "; ")
}

// Empty reports whether validation found nothing.
func (e *FlowValidationError) Empty() bool { return e == nil || len(e.Errors) == 0 }

// flowErrors accumulates failures and sorts them once.
type flowErrors struct{ items []FlowFieldError }

func (a *flowErrors) add(path, code, format string, args ...any) {
	a.items = append(a.items, FlowFieldError{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
}

func (a *flowErrors) result() *FlowValidationError {
	if len(a.items) == 0 {
		return nil
	}
	sort.Slice(a.items, func(i, j int) bool {
		if a.items[i].Path != a.items[j].Path {
			return a.items[i].Path < a.items[j].Path
		}
		return a.items[i].Code < a.items[j].Code
	})
	return &FlowValidationError{Errors: a.items}
}

// flowNamePattern is the identifier shape params, envs, and agents share.
// Names appear in field paths, grants, and container labels, so they are
// deliberately narrow.
var flowNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// ValidateFlowDefinition decodes and fully validates a definition. It returns
// the decoded definition and a nil error only when the definition is
// executable: unknown fields, unknown references, cycles, duplicate names,
// invalid templates, and ill-formed grants are all rejected here rather than
// at invocation time.
func ValidateFlowDefinition(raw []byte) (FlowDefinition, *FlowValidationError) {
	var acc flowErrors
	var def FlowDefinition

	if len(bytes.TrimSpace(raw)) == 0 {
		acc.add("definition", FlowErrInvalidJSON, "definition is empty")
		return def, acc.result()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&def); err != nil {
		acc.add("definition", flowDecodeCode(err), "%s", flowDecodeMessage(err))
		return def, acc.result()
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		acc.add("definition", FlowErrInvalidJSON, "definition must be a single JSON object")
		return def, acc.result()
	}
	// Go's decoder silently keeps the last of two identical keys, so a
	// definition with two "reviewer" agents would validate as one. Scan the
	// raw bytes to reject it instead.
	for _, dup := range duplicateFlowKeys(raw) {
		acc.add(dup, FlowErrDuplicateName, "declared more than once")
	}

	validateFlowParams(&acc, def)
	validateFlowEnvs(&acc, def)
	validateFlowAgents(&acc, def)
	return def, acc.result()
}

func flowDecodeCode(err error) string {
	if strings.Contains(err.Error(), "unknown field") {
		return FlowErrUnknownField
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return FlowErrInvalidType
	}
	return FlowErrInvalidJSON
}

func flowDecodeMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "value"
		}
		return fmt.Sprintf("%s must be %s, got %s", field, typeErr.Type, typeErr.Value)
	}
	return strings.TrimPrefix(err.Error(), "json: ")
}

func validateFlowParams(acc *flowErrors, def FlowDefinition) {
	for _, name := range sortedKeys(def.Params) {
		param := def.Params[name]
		path := "params." + name
		if !flowNamePattern.MatchString(name) {
			acc.add(path, FlowErrInvalidName, "parameter names must match %s", flowNamePattern)
		}
		switch param.Type {
		case FlowParamString, FlowParamNumber, FlowParamBoolean:
		case "":
			acc.add(path+".type", FlowErrRequired, "parameter type is required")
		default:
			acc.add(path+".type", FlowErrInvalidType, "unsupported parameter type %q; want string, number, or boolean", param.Type)
		}
		if param.Required && param.Default != nil {
			acc.add(path, FlowErrConflict, "a required parameter cannot also declare a default")
		}
		if param.Default != nil && !flowValueMatches(param.Type, param.Default) {
			acc.add(path+".default", FlowErrTypeMismatch, "default is not a %s", param.Type)
		}
	}
}

func validateFlowEnvs(acc *flowErrors, def FlowDefinition) {
	for _, name := range sortedKeys(def.Envs) {
		env := def.Envs[name]
		path := "envs." + name
		if !flowNamePattern.MatchString(name) {
			acc.add(path, FlowErrInvalidName, "environment names must match %s", flowNamePattern)
		}
		switch env.Readiness {
		case "", FlowReadinessHealth, FlowReadinessNone:
		default:
			acc.add(path+".readiness", FlowErrUnsupported, "unsupported readiness policy %q; want health or none", env.Readiness)
		}
		if env.Timeout != "" {
			if d, err := time.ParseDuration(env.Timeout); err != nil || d <= 0 {
				acc.add(path+".timeout", FlowErrInvalidDuration, "timeout must be a positive Go duration")
			}
		}
		// Setup commands need a place to run: an environment that declares
		// them but no readiness gate could never have executed them before an
		// agent looked for their result.
		if len(env.Setup) > 0 && env.Readiness == FlowReadinessNone {
			acc.add(path+".setup", FlowErrProviderMismatch, "setup commands require the health readiness policy")
		}
		checkTemplate(acc, def, path+".image", env.Image)
		checkTemplate(acc, def, path+".workdir", env.Workdir)
		for _, key := range sortedKeys(env.Env) {
			checkTemplate(acc, def, path+".env."+key, env.Env[key])
		}
		for _, key := range sortedKeys(env.Metadata) {
			checkTemplate(acc, def, path+".metadata."+key, env.Metadata[key])
		}
		for i, command := range env.Setup {
			checkTemplate(acc, def, fmt.Sprintf("%s.setup[%d]", path, i), command)
		}
	}
}

func validateFlowAgents(acc *flowErrors, def FlowDefinition) {
	if len(def.Agents) == 0 {
		acc.add("agents", FlowErrRequired, "at least one agent is required")
		return
	}
	canonical := stringSet(CanonicalTools())
	entries := 0
	for _, name := range sortedKeys(def.Agents) {
		agent := def.Agents[name]
		path := "agents." + name
		if !flowNamePattern.MatchString(name) {
			acc.add(path, FlowErrInvalidName, "agent names must match %s", flowNamePattern)
		}
		if strings.TrimSpace(agent.Prompt) == "" {
			acc.add(path+".prompt", FlowErrRequired, "prompt is required")
		}
		checkTemplate(acc, def, path+".prompt", agent.Prompt)
		if agent.Entry {
			entries++
		}
		if agent.Entry && len(agent.After) > 0 {
			acc.add(path+".after", FlowErrConflict, "an entry agent cannot depend on another agent")
		}
		if !agent.Entry && len(agent.After) == 0 && !agent.Spawnable {
			acc.add(path, FlowErrUnreachableAgent, "agent is never started: declare entry, after, or spawnable")
		}
		for i, dep := range agent.After {
			depPath := fmt.Sprintf("%s.after[%d]", path, i)
			target, ok := def.Agents[dep]
			switch {
			case !ok:
				acc.add(depPath, FlowErrUnknownAgent, "unknown agent %q", dep)
			case dep == name:
				acc.add(depPath, FlowErrCycle, "agent cannot depend on itself")
			case !target.Entry && len(target.After) == 0:
				acc.add(depPath, FlowErrUnreachableAgent, "agent %q is never started by the flow", dep)
			}
		}
		for i, ref := range agent.Envs {
			if _, ok := def.Envs[ref]; !ok {
				acc.add(fmt.Sprintf("%s.envs[%d]", path, i), FlowErrUnknownEnv, "unknown environment %q", ref)
			}
		}
		for i, tool := range agent.Tools {
			if tool == "*" || canonical[tool] {
				continue
			}
			acc.add(fmt.Sprintf("%s.tools[%d]", path, i), FlowErrUnknownTool, "unknown tool %q", tool)
		}
		validateAgentGrants(acc, path, agent)
		if agent.Timeout != "" {
			if d, err := time.ParseDuration(agent.Timeout); err != nil || d <= 0 {
				acc.add(path+".timeout", FlowErrInvalidDuration, "timeout must be a positive Go duration")
			}
		}
		if agent.Model.Provider != "" && !flowKnownProvider(agent.Model.Provider) {
			acc.add(path+".model.provider", FlowErrUnsupported, "unsupported provider %q", agent.Model.Provider)
		}
		if len(agent.Model.Fallbacks) > 0 && agent.Model.Provider == "" {
			acc.add(path+".model.fallbacks", FlowErrConflict, "fallbacks require a primary model")
		}
	}
	if entries == 0 {
		acc.add("agents", FlowErrNoEntryAgent, "at least one entry agent is required")
	}
	for _, cycle := range flowCycles(def) {
		acc.add("agents."+cycle+".after", FlowErrCycle, "dependency cycle through agent %q", cycle)
	}
}

// validateAgentGrants rejects authority a flow could never legally hand out:
// the flow definition is a ceiling under the same lattice runs use, so a
// declaration that exceeds root authority is a definition bug, not a runtime
// denial.
func validateAgentGrants(acc *flowErrors, path string, agent FlowAgent) {
	if agent.MaxChildren < 0 {
		acc.add(path+".max_children", FlowErrInvalidGrant, "max_children cannot be negative")
	}
	root := RootGrants()
	if agent.MaxChildren > root.MaxChildren {
		acc.add(path+".max_children", FlowErrInvalidGrant, "max_children exceeds the platform ceiling of %d", root.MaxChildren)
	}
	tools := stringSet(agent.Tools)
	if agent.MaySpawn {
		if !tools["*"] && !tools["spawn_agent"] {
			acc.add(path+".may_spawn", FlowErrInvalidGrant, "may_spawn requires the spawn_agent tool")
		}
		if agent.MaxChildren == 0 {
			acc.add(path+".max_children", FlowErrInvalidGrant, "may_spawn requires a positive max_children")
		}
	}
	if !agent.MaySpawn && (tools["spawn_agent"] || tools["run_agent_cohort"]) {
		acc.add(path+".tools", FlowErrInvalidGrant, "spawn tools require may_spawn")
	}
	if !agent.MaySpawn && agent.MaxChildren > 0 {
		acc.add(path+".max_children", FlowErrInvalidGrant, "max_children requires may_spawn")
	}
}

func flowKnownProvider(name string) bool {
	switch name {
	case "openai", "anthropic", "bedrock":
		return true
	}
	return false
}

// flowCycles returns the agents that participate in a dependency cycle. Names
// are reported sorted so the error list is stable.
func flowCycles(def FlowDefinition) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	inCycle := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		color[name] = grey
		for _, dep := range def.Agents[name].After {
			if _, ok := def.Agents[dep]; !ok || dep == name {
				continue
			}
			switch color[dep] {
			case white:
				visit(dep)
			case grey:
				inCycle[dep] = true
				inCycle[name] = true
			}
			if inCycle[dep] {
				inCycle[name] = true
			}
		}
		color[name] = black
	}
	for _, name := range sortedKeys(def.Agents) {
		if color[name] == white {
			visit(name)
		}
	}
	out := make([]string, 0, len(inCycle))
	for name := range inCycle {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// checkTemplate rejects a template that cannot parse and a template that
// references a parameter the flow never declared. Both are definition errors:
// discovering them at invocation time would mean a stored flow that can never
// run.
func checkTemplate(acc *flowErrors, def FlowDefinition, path, text string) {
	if text == "" {
		return
	}
	trees, err := parse.Parse(path, text, "{{", "}}")
	if err != nil {
		acc.add(path, FlowErrInvalidTemplate, "%s", flowTemplateMessage(err))
		return
	}
	for _, tree := range trees {
		if tree.Root == nil {
			continue
		}
		for _, field := range templateFields(tree.Root) {
			if _, ok := def.Params[field]; !ok {
				acc.add(path, FlowErrUnknownParam, "template references undeclared parameter %q", field)
			}
		}
	}
}

func flowTemplateMessage(err error) string {
	message := err.Error()
	if index := strings.Index(message, ": "); index >= 0 {
		message = message[index+2:]
	}
	return "invalid template: " + message
}

// templateFields collects the top-level parameter names a template reads.
// Bodies of range and with are skipped because they rebind dot; their pipelines
// are still inspected, which is where a parameter reference appears.
func templateFields(node parse.Node) []string {
	var out []string
	var walk func(parse.Node, bool)
	walk = func(n parse.Node, root bool) {
		switch value := n.(type) {
		case nil:
			return
		case *parse.ListNode:
			if value == nil {
				return
			}
			for _, child := range value.Nodes {
				walk(child, root)
			}
		case *parse.ActionNode:
			walk(value.Pipe, root)
		case *parse.PipeNode:
			if value == nil {
				return
			}
			for _, cmd := range value.Cmds {
				walk(cmd, root)
			}
		case *parse.CommandNode:
			for _, arg := range value.Args {
				walk(arg, root)
			}
		case *parse.FieldNode:
			if root && len(value.Ident) > 0 {
				out = append(out, value.Ident[0])
			}
		case *parse.IfNode:
			walk(value.Pipe, root)
			walk(value.List, root)
			walk(value.ElseList, root)
		case *parse.RangeNode:
			walk(value.Pipe, root)
			walk(value.List, false)
			walk(value.ElseList, false)
		case *parse.WithNode:
			walk(value.Pipe, root)
			walk(value.List, false)
			walk(value.ElseList, false)
		}
	}
	walk(node, true)
	sort.Strings(out)
	return dedupeStrings(out)
}

// duplicateFlowKeys finds keys declared twice inside the objects whose names
// matter: the parameter, environment, and agent catalogs, plus the top level.
func duplicateFlowKeys(raw []byte) []string {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	var out []string
	out = append(out, duplicateKeysIn(raw, "")...)
	for _, section := range []string{"params", "envs", "agents"} {
		body, ok := generic[section]
		if !ok {
			continue
		}
		out = append(out, duplicateKeysIn(body, section+".")...)
	}
	sort.Strings(out)
	return dedupeStrings(out)
}

// duplicateKeysIn scans one JSON object's tokens for repeated keys. The
// standard decoder cannot report them, so the raw token stream is the only
// place a duplicate is still visible.
func duplicateKeysIn(raw []byte, prefix string) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return out
		}
		name, ok := key.(string)
		if !ok {
			return out
		}
		if seen[name] {
			out = append(out, prefix+name)
		}
		seen[name] = true
		if err := skipJSONValue(decoder); err != nil {
			return out
		}
	}
	return out
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	depth := 1
	if delim != '{' && delim != '[' {
		return nil
	}
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if d, ok := token.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// ResolveFlowParams merges supplied parameters with declared defaults and
// rejects missing, unknown, and mistyped values with the same field paths
// definition validation uses.
func ResolveFlowParams(def FlowDefinition, supplied map[string]any) (map[string]any, *FlowValidationError) {
	var acc flowErrors
	out := map[string]any{}
	for _, name := range sortedKeys(supplied) {
		if _, ok := def.Params[name]; !ok {
			acc.add("params."+name, FlowErrUnknownParameter, "flow declares no parameter %q", name)
		}
	}
	for _, name := range sortedKeys(def.Params) {
		param := def.Params[name]
		value, ok := supplied[name]
		switch {
		case ok:
			if !flowValueMatches(param.Type, value) {
				acc.add("params."+name, FlowErrTypeMismatch, "value is not a %s", param.Type)
				continue
			}
			out[name] = flowNormalize(param.Type, value)
		case param.Default != nil:
			out[name] = flowNormalize(param.Type, param.Default)
		case param.Required:
			acc.add("params."+name, FlowErrMissingParameter, "parameter is required")
		default:
			out[name] = flowZero(param.Type)
		}
	}
	if err := acc.result(); err != nil {
		return nil, err
	}
	return out, nil
}

func flowValueMatches(kind string, value any) bool {
	switch kind {
	case FlowParamString:
		_, ok := value.(string)
		return ok
	case FlowParamNumber:
		switch value.(type) {
		case float64, float32, int, int32, int64, json.Number:
			return true
		}
		return false
	case FlowParamBoolean:
		_, ok := value.(bool)
		return ok
	}
	return false
}

func flowNormalize(kind string, value any) any {
	if kind != FlowParamNumber {
		return value
	}
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case float32:
		return float64(typed)
	}
	return value
}

func flowZero(kind string) any {
	switch kind {
	case FlowParamNumber:
		return float64(0)
	case FlowParamBoolean:
		return false
	}
	return ""
}

// RenderedFlow is the frozen rendering one invocation uses. It is persisted so
// a later flow version can never change what an in-flight or replayed
// invocation did.
type RenderedFlow struct {
	SchemaVersion int             `json:"schema_version"`
	Params        map[string]any  `json:"params"`
	Envs          []RenderedEnv   `json:"envs"`
	Agents        []RenderedAgent `json:"agents"`
}

// RenderedEnv is one environment declaration with every template resolved.
type RenderedEnv struct {
	Name             string   `json:"name"`
	ProviderInstance string   `json:"provider_instance"`
	Spec             EnvSpec  `json:"spec"`
	Setup            []string `json:"setup,omitempty"`
	Readiness        string   `json:"readiness"`
	Required         bool     `json:"required"`
	Timeout          string   `json:"timeout,omitempty"`
}

// RenderedAgent is one agent declaration with its prompt resolved, its stage
// computed, and its authority expressed as grants. EnvNames stays symbolic
// because environment ids only exist once the invocation provisions them; the
// invocation writes the concrete ids onto the run it creates.
type RenderedAgent struct {
	Name        string      `json:"name"`
	Prompt      string      `json:"prompt"`
	Model       ModelConfig `json:"model"`
	Grants      Grants      `json:"grants"`
	EnvNames    []string    `json:"env_names,omitempty"`
	Entry       bool        `json:"entry"`
	Spawnable   bool        `json:"spawnable"`
	After       []string    `json:"after,omitempty"`
	Stage       int         `json:"stage"`
	Timeout     string      `json:"timeout,omitempty"`
	Description string      `json:"description,omitempty"`
}

// FindAgent returns a rendered agent by flow-declared name.
func (r RenderedFlow) FindAgent(name string) (RenderedAgent, bool) {
	for _, agent := range r.Agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return RenderedAgent{}, false
}

// FindEnv returns a rendered environment by flow-declared name.
func (r RenderedFlow) FindEnv(name string) (RenderedEnv, bool) {
	for _, env := range r.Envs {
		if env.Name == name {
			return env, true
		}
	}
	return RenderedEnv{}, false
}

// Stages returns the agent stages in execution order: index 0 is the declared
// roots, and each later stage waits for the previous ones.
func (r RenderedFlow) Stages() [][]RenderedAgent {
	highest := -1
	for _, agent := range r.Agents {
		if agent.Spawnable && !agent.Entry && len(agent.After) == 0 {
			continue
		}
		if agent.Stage > highest {
			highest = agent.Stage
		}
	}
	if highest < 0 {
		return nil
	}
	out := make([][]RenderedAgent, highest+1)
	for _, agent := range r.Agents {
		if agent.Spawnable && !agent.Entry && len(agent.After) == 0 {
			continue
		}
		out[agent.Stage] = append(out[agent.Stage], agent)
	}
	return out
}

// RenderFlow resolves parameters and produces the frozen rendering an
// invocation executes. It is deterministic: names are traversed in sorted
// order and nothing depends on map iteration or wall time, so the same
// definition and parameters always yield byte-identical output.
func RenderFlow(def FlowDefinition, supplied map[string]any) (RenderedFlow, *FlowValidationError) {
	params, verr := ResolveFlowParams(def, supplied)
	if verr != nil {
		return RenderedFlow{}, verr
	}
	var acc flowErrors
	out := RenderedFlow{SchemaVersion: FlowDefinitionVersion, Params: params}

	for _, name := range sortedKeys(def.Envs) {
		env := def.Envs[name]
		path := "envs." + name
		rendered := RenderedEnv{
			Name:             name,
			ProviderInstance: env.ProviderInstance,
			Readiness:        env.Readiness,
			Required:         env.Required == nil || *env.Required,
			Timeout:          env.Timeout,
			Spec: EnvSpec{
				Name:     name,
				Image:    renderTemplate(&acc, path+".image", env.Image, params),
				Workdir:  renderTemplate(&acc, path+".workdir", env.Workdir, params),
				Env:      map[string]string{},
				Metadata: map[string]string{},
			},
		}
		if rendered.ProviderInstance == "" {
			rendered.ProviderInstance = "default"
		}
		if rendered.Readiness == "" {
			rendered.Readiness = FlowReadinessHealth
		}
		for _, key := range sortedKeys(env.Env) {
			rendered.Spec.Env[key] = renderTemplate(&acc, path+".env."+key, env.Env[key], params)
		}
		for _, key := range sortedKeys(env.Metadata) {
			rendered.Spec.Metadata[key] = renderTemplate(&acc, path+".metadata."+key, env.Metadata[key], params)
		}
		for i, command := range env.Setup {
			rendered.Setup = append(rendered.Setup,
				renderTemplate(&acc, fmt.Sprintf("%s.setup[%d]", path, i), command, params))
		}
		out.Envs = append(out.Envs, rendered)
	}

	stages := flowStages(def)
	for _, name := range sortedKeys(def.Agents) {
		agent := def.Agents[name]
		rendered := RenderedAgent{
			Name:      name,
			Prompt:    renderTemplate(&acc, "agents."+name+".prompt", agent.Prompt, params),
			Model:     agent.Model,
			Entry:     agent.Entry,
			Spawnable: agent.Spawnable,
			After:     append([]string(nil), agent.After...),
			Stage:     stages[name],
			Timeout:   agent.Timeout,
			EnvNames:  append([]string(nil), agent.Envs...),
			Grants: Grants{
				Tools:       append([]string(nil), agent.Tools...),
				MaySpawn:    agent.MaySpawn,
				MaxChildren: agent.MaxChildren,
			},
		}
		if rendered.Grants.Tools == nil {
			rendered.Grants.Tools = []string{}
		}
		out.Agents = append(out.Agents, rendered)
	}
	if err := acc.result(); err != nil {
		return RenderedFlow{}, err
	}
	return out, nil
}

// flowStages computes each agent's execution stage as the longest dependency
// path to a root, so an agent never starts before anything it declared.
func flowStages(def FlowDefinition) map[string]int {
	stages := map[string]int{}
	var resolve func(string, map[string]bool) int
	resolve = func(name string, path map[string]bool) int {
		if stage, ok := stages[name]; ok {
			return stage
		}
		if path[name] {
			return 0
		}
		path[name] = true
		defer delete(path, name)
		stage := 0
		for _, dep := range def.Agents[name].After {
			if _, ok := def.Agents[dep]; !ok {
				continue
			}
			if candidate := resolve(dep, path) + 1; candidate > stage {
				stage = candidate
			}
		}
		stages[name] = stage
		return stage
	}
	for _, name := range sortedKeys(def.Agents) {
		resolve(name, map[string]bool{})
	}
	return stages
}

func renderTemplate(acc *flowErrors, path, text string, params map[string]any) string {
	if text == "" {
		return ""
	}
	tmpl, err := template.New(path).Option("missingkey=error").Parse(text)
	if err != nil {
		acc.add(path, FlowErrInvalidTemplate, "%s", flowTemplateMessage(err))
		return ""
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		acc.add(path, FlowErrInvalidTemplate, "%s", flowTemplateMessage(err))
		return ""
	}
	return buf.String()
}

// sortedKeys returns a map's keys in ascending order, which is how every flow
// traversal stays deterministic.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	var last string
	for i, value := range values {
		if i > 0 && value == last {
			continue
		}
		last = value
		out = append(out, value)
	}
	return out
}
