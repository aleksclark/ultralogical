package ultra_test

import (
	"encoding/json"
	"strings"
	"testing"

	ultra "github.com/aleksclark/ultralogical"
)

// codesFor collects the (path, code) pairs a definition produced, which is the
// stable half of a validation failure. Messages are for humans and may be
// reworded without breaking a client.
func codesFor(t *testing.T, raw string) map[string]string {
	t.Helper()
	_, verr := ultra.ValidateFlowDefinition([]byte(raw))
	if verr == nil {
		t.Fatalf("definition validated but should not have: %s", raw)
	}
	out := map[string]string{}
	for _, item := range verr.Errors {
		out[item.Path] = item.Code
	}
	return out
}

func TestValidateFlowDefinitionRejectsEveryFailureClass(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		path string
		code string
	}{
		{"malformed json", `{`, "definition", ultra.FlowErrInvalidJSON},
		{"unknown top-level field", `{"agents":{"a":{"prompt":"x","entry":true}},"nope":1}`,
			"definition", ultra.FlowErrUnknownField},
		{"unknown agent field", `{"agents":{"a":{"prompt":"x","entry":true,"bogus":2}}}`,
			"definition", ultra.FlowErrUnknownField},
		{"no agents", `{"agents":{}}`, "agents", ultra.FlowErrRequired},
		{"no entry agent", `{"agents":{"a":{"prompt":"x","spawnable":true}}}`,
			"agents", ultra.FlowErrNoEntryAgent},
		{"template syntax", `{"agents":{"a":{"prompt":"{{","entry":true}}}`,
			"agents.a.prompt", ultra.FlowErrInvalidTemplate},
		{"undeclared parameter", `{"agents":{"a":{"prompt":"hi {{.who}}","entry":true}}}`,
			"agents.a.prompt", ultra.FlowErrUnknownParam},
		{"unknown tool", `{"agents":{"a":{"prompt":"x","entry":true,"tools":["bsh"]}}}`,
			"agents.a.tools[0]", ultra.FlowErrUnknownTool},
		{"dangling env ref", `{"agents":{"a":{"prompt":"x","entry":true,"envs":["main"]}}}`,
			"agents.a.envs[0]", ultra.FlowErrUnknownEnv},
		{"dangling agent ref", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","after":["ghost"]}}}`,
			"agents.b.after[0]", ultra.FlowErrUnknownAgent},
		{"self dependency", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","after":["b"]}}}`,
			"agents.b.after[0]", ultra.FlowErrCycle},
		{"dependency cycle", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","after":["c"]},"c":{"prompt":"z","after":["b"]}}}`,
			"agents.b.after", ultra.FlowErrCycle},
		{"unreachable agent", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y"}}}`,
			"agents.b", ultra.FlowErrUnreachableAgent},
		{"entry with dependency", `{"agents":{"a":{"prompt":"x","entry":true},"b":{"prompt":"y","entry":true,"after":["a"]}}}`,
			"agents.b.after", ultra.FlowErrConflict},
		{"param type", `{"params":{"p":{"type":"blob"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"params.p.type", ultra.FlowErrInvalidType},
		{"param type missing", `{"params":{"p":{}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"params.p.type", ultra.FlowErrRequired},
		{"default type mismatch", `{"params":{"p":{"type":"number","default":"x"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"params.p.default", ultra.FlowErrTypeMismatch},
		{"required with default", `{"params":{"p":{"type":"string","required":true,"default":"x"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"params.p", ultra.FlowErrConflict},
		{"grant exceeds ceiling", `{"agents":{"a":{"prompt":"x","entry":true,"tools":["spawn_agent"],"may_spawn":true,"max_children":99}}}`,
			"agents.a.max_children", ultra.FlowErrInvalidGrant},
		{"spawn tool without may_spawn", `{"agents":{"a":{"prompt":"x","entry":true,"tools":["spawn_agent"]}}}`,
			"agents.a.tools", ultra.FlowErrInvalidGrant},
		{"may_spawn without spawn tool", `{"agents":{"a":{"prompt":"x","entry":true,"may_spawn":true,"max_children":2,"tools":["view"]}}}`,
			"agents.a.may_spawn", ultra.FlowErrInvalidGrant},
		{"bad agent name", `{"agents":{"Bad Name":{"prompt":"x","entry":true}}}`,
			"agents.Bad Name", ultra.FlowErrInvalidName},
		{"bad readiness policy", `{"envs":{"main":{"readiness":"vibes"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"envs.main.readiness", ultra.FlowErrUnsupported},
		{"bad env timeout", `{"envs":{"main":{"timeout":"soon"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"envs.main.timeout", ultra.FlowErrInvalidDuration},
		{"setup without health readiness", `{"envs":{"main":{"readiness":"none","setup":["true"]}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"envs.main.setup", ultra.FlowErrProviderMismatch},
		{"env template references undeclared param", `{"envs":{"main":{"image":"{{.tag}}"}},"agents":{"a":{"prompt":"x","entry":true}}}`,
			"envs.main.image", ultra.FlowErrUnknownParam},
		{"duplicate agent name", `{"agents":{"a":{"prompt":"x","entry":true},"a":{"prompt":"y","entry":true}}}`,
			"agents.a", ultra.FlowErrDuplicateName},
		{"unsupported model provider", `{"agents":{"a":{"prompt":"x","entry":true,"model":{"provider":"vibes","model_id":"m"}}}}`,
			"agents.a.model.provider", ultra.FlowErrUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codesFor(t, tc.raw)
			if code := got[tc.path]; code != tc.code {
				t.Fatalf("want %s at %q, got %v", tc.code, tc.path, got)
			}
		})
	}
}

func TestValidateFlowDefinitionAcceptsAFullFlow(t *testing.T) {
	raw := `{
	  "description": "review with a worker",
	  "params": {"subject": {"type": "string", "required": true},
	             "depth": {"type": "number", "default": 2},
	             "verbose": {"type": "boolean", "default": false}},
	  "envs": {"main": {"provider_instance": "default", "workdir": "/work",
	                    "env": {"SUBJECT": "{{.subject}}"}, "setup": ["true"]}},
	  "agents": {
	    "reviewer": {"prompt": "Review {{.subject}} at depth {{.depth}}", "entry": true,
	                 "envs": ["main"], "tools": ["view", "spawn_agent"],
	                 "may_spawn": true, "max_children": 4},
	    "summarizer": {"prompt": "Summarize {{.subject}}", "after": ["reviewer"], "tools": ["post_event"]},
	    "security": {"prompt": "Audit {{.subject}}", "spawnable": true, "envs": ["main"], "tools": ["view"]}
	  }
	}`
	def, verr := ultra.ValidateFlowDefinition([]byte(raw))
	if verr != nil {
		t.Fatalf("valid definition rejected: %v", verr)
	}
	if len(def.Agents) != 3 || len(def.Envs) != 1 || len(def.Params) != 3 {
		t.Fatalf("decoded shape wrong: %+v", def)
	}
}

func TestRenderFlowIsDeterministic(t *testing.T) {
	raw := `{
	  "params": {"subject": {"type": "string", "required": true}, "depth": {"type": "number", "default": 3}},
	  "envs": {"b": {"env": {"S": "{{.subject}}", "D": "{{.depth}}"}},
	           "a": {"workdir": "/w/{{.subject}}"}},
	  "agents": {"z": {"prompt": "Z {{.subject}}", "after": ["m"], "tools": ["view"]},
	             "m": {"prompt": "M {{.subject}}", "entry": true, "envs": ["a", "b"], "tools": ["view"]}}
	}`
	def, verr := ultra.ValidateFlowDefinition([]byte(raw))
	if verr != nil {
		t.Fatal(verr)
	}
	params := map[string]any{"subject": "db"}
	first, verr := ultra.RenderFlow(def, params)
	if verr != nil {
		t.Fatal(verr)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	// Rendering the same definition and parameters many times must produce
	// byte-identical output: a rendering that depended on map iteration order
	// would make replay non-reproducible.
	for range 25 {
		again, verr := ultra.RenderFlow(def, map[string]any{"subject": "db"})
		if verr != nil {
			t.Fatal(verr)
		}
		againJSON, err := json.Marshal(again)
		if err != nil {
			t.Fatal(err)
		}
		if string(againJSON) != string(firstJSON) {
			t.Fatalf("rendering is not deterministic:\n%s\n%s", firstJSON, againJSON)
		}
	}
	if first.Params["depth"] != float64(3) {
		t.Fatalf("default not applied: %+v", first.Params)
	}
	agent, ok := first.FindAgent("m")
	if !ok || agent.Prompt != "M db" || agent.Stage != 0 {
		t.Fatalf("entry agent rendered wrong: %+v", agent)
	}
	dependent, ok := first.FindAgent("z")
	if !ok || dependent.Stage != 1 {
		t.Fatalf("dependent agent stage wrong: %+v", dependent)
	}
	env, ok := first.FindEnv("b")
	if !ok || env.Spec.Env["S"] != "db" || env.Spec.Env["D"] != "3" {
		t.Fatalf("environment rendered wrong: %+v", env)
	}
	if env.Readiness != ultra.FlowReadinessHealth || !env.Required {
		t.Fatalf("environment defaults wrong: %+v", env)
	}
}

func TestRenderFlowStagesExcludeSpawnableCatalogAgents(t *testing.T) {
	raw := `{"agents":{"root":{"prompt":"go","entry":true},
	                   "worker":{"prompt":"work","spawnable":true},
	                   "after":{"prompt":"later","after":["root"]}}}`
	def, verr := ultra.ValidateFlowDefinition([]byte(raw))
	if verr != nil {
		t.Fatal(verr)
	}
	rendered, verr := ultra.RenderFlow(def, nil)
	if verr != nil {
		t.Fatal(verr)
	}
	stages := rendered.Stages()
	if len(stages) != 2 {
		t.Fatalf("want 2 stages, got %d: %+v", len(stages), stages)
	}
	for _, stage := range stages {
		for _, agent := range stage {
			if agent.Name == "worker" {
				t.Fatal("a spawnable-only catalog agent must not be auto-started")
			}
		}
	}
}

func TestResolveFlowParamsRejectsMissingUnknownAndMistyped(t *testing.T) {
	def, verr := ultra.ValidateFlowDefinition([]byte(
		`{"params":{"subject":{"type":"string","required":true},"depth":{"type":"number","default":1}},
		  "agents":{"a":{"prompt":"x {{.subject}} {{.depth}}","entry":true}}}`))
	if verr != nil {
		t.Fatal(verr)
	}
	cases := []struct {
		name     string
		supplied map[string]any
		path     string
		code     string
	}{
		{"missing required", map[string]any{}, "params.subject", ultra.FlowErrMissingParameter},
		{"unknown supplied", map[string]any{"subject": "s", "extra": 1}, "params.extra", ultra.FlowErrUnknownParameter},
		{"wrong type", map[string]any{"subject": 5}, "params.subject", ultra.FlowErrTypeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, verr := ultra.ResolveFlowParams(def, tc.supplied)
			if verr == nil {
				t.Fatal("params accepted but should not have been")
			}
			found := false
			for _, item := range verr.Errors {
				if item.Path == tc.path && item.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("want %s at %s, got %v", tc.code, tc.path, verr.Errors)
			}
		})
	}
}

func TestFlowValidationErrorsAreOrderedStably(t *testing.T) {
	raw := `{"agents":{"zeta":{"prompt":"{{","entry":true,"tools":["nope"]},
	                   "alpha":{"prompt":"{{","entry":true,"tools":["nope"]}}}`
	var previous string
	for range 10 {
		_, verr := ultra.ValidateFlowDefinition([]byte(raw))
		if verr == nil {
			t.Fatal("expected failures")
		}
		var paths []string
		for _, item := range verr.Errors {
			paths = append(paths, item.Path+"|"+item.Code)
		}
		current := strings.Join(paths, ",")
		if previous != "" && current != previous {
			t.Fatalf("error order is unstable:\n%s\n%s", previous, current)
		}
		previous = current
	}
	if !strings.Contains(previous, "agents.alpha.prompt") {
		t.Fatalf("expected sorted paths, got %s", previous)
	}
	if strings.Index(previous, "agents.alpha") > strings.Index(previous, "agents.zeta") {
		t.Fatalf("paths are not sorted: %s", previous)
	}
}
