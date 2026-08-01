// Package loop owns the durable agent loop: one queue job per step, message
// history persisted in Postgres, all activity emitted as session events.
// Execution-time state is disposable — any worker can resume any run from
// its persisted envelope.
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"charm.land/fantasy"

	ultra "github.com/aleksclark/ultralogical"
)

// DefaultLoopKind and version identify the v1 loop. Runs are stamped at
// creation so deploys never change in-flight run behavior.
const (
	DefaultLoopKind    = "default"
	DefaultLoopVersion = 1
)

// DefaultSystemPrompt is the v1 system prompt.
const DefaultSystemPrompt = `You are an agent working inside an Ultralogical session.
Be concise. Use the ask_user tool when you need human input; use the
post_event tool to leave notes in the session log.`

// Envelope is the versioned persisted message history.
type Compaction struct {
	AtStep          int    `json:"at_step"`
	CoveredMessages int    `json:"covered_messages"`
	Summary         string `json:"summary"`
}
type Envelope struct {
	V           int               `json:"v"`
	Messages    []fantasy.Message `json:"messages"`
	Compactions []Compaction      `json:"compactions,omitempty"`
}

// DecodeEnvelope parses a run's history.
func DecodeEnvelope(raw json.RawMessage) (Envelope, error) {
	var env Envelope
	if len(raw) == 0 {
		return Envelope{V: 1}, nil
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("loop: decode envelope: %w", err)
	}
	if env.V != 1 && env.V != 2 {
		return Envelope{}, fmt.Errorf("loop: unsupported envelope version %d", env.V)
	}
	return env, nil
}

// Encode serializes the envelope.
func (e Envelope) Encode() (json.RawMessage, error) {
	if len(e.Compactions) > 0 {
		e.V = 2
	} else if e.V == 0 {
		e.V = 1
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("loop: encode envelope: %w", err)
	}
	return b, nil
}

// InitialEnvelope builds a run's starting history from its prompt.
func InitialEnvelope(prompt string) (json.RawMessage, error) {
	return Envelope{V: 1, Messages: []fantasy.Message{fantasy.NewUserMessage(prompt)}}.Encode()
}

// AppendUserMessage adds a user message to a serialized envelope.
func AppendUserMessage(raw json.RawMessage, text string) (json.RawMessage, error) {
	env, err := DecodeEnvelope(raw)
	if err != nil {
		return nil, err
	}
	env.Messages = append(env.Messages, fantasy.NewUserMessage(text))
	return env.Encode()
}

// Definition is one registered loop implementation. SystemPrompt and Tools
// may grow per version; the registry keeps old versions stable.
type Definition struct {
	Kind         string
	Version      int
	SystemPrompt string
}

// Registry resolves (kind, version) to a Definition.
type Registry struct {
	defs map[string]Definition
}

// NewRegistry builds the registry with all known loop versions.
func NewRegistry() *Registry {
	r := &Registry{defs: map[string]Definition{}}
	r.register(Definition{Kind: DefaultLoopKind, Version: DefaultLoopVersion, SystemPrompt: DefaultSystemPrompt})
	return r
}

func (r *Registry) register(d Definition) {
	r.defs[fmt.Sprintf("%s@%d", d.Kind, d.Version)] = d
}

// Resolve returns the definition for a run's stamped loop identity.
func (r *Registry) Resolve(kind string, version int) (Definition, error) {
	d, ok := r.defs[fmt.Sprintf("%s@%d", kind, version)]
	if !ok {
		return Definition{}, fmt.Errorf("loop: unknown loop %s@%d", kind, version)
	}
	return d, nil
}

// stepRecorder captures per-step tool activity the outcome classifier needs.
type stepRecorder struct {
	question *ultra.Question
	// A pending wait recorded by wait_for_agents or run_agent_cohort. The
	// tool-call id is the model's own id for that call: when the wait
	// resolves, the injected result must be correlated to it, or the model
	// sees an answer to a question it never asked.
	waitRunIDs     []ultra.RunID
	waitToolCallID string
	waitTimeout    time.Duration
	waitKind       string
	waitPolicy     string
	toolsCalled    int
}

// newAskUserTool returns the ask_user native tool. Calling it parks the run
// awaiting human input; the answer arrives as the next user message.
func newAskUserTool(rec *stepRecorder) fantasy.AgentTool {
	type input struct {
		Question string   `json:"question" description:"The question to ask the user"`
		Choices  []string `json:"choices,omitempty" description:"Optional fixed choices"`
	}
	return fantasy.NewAgentTool("ask_user",
		"Ask the human a question and pause until they answer. The answer arrives as the next user message.",
		func(_ context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			rec.question = &ultra.Question{Text: in.Question, Choices: in.Choices}
			resp := fantasy.NewTextResponse("Question posed to the user; their answer will arrive as the next user message.")
			resp.StopTurn = true
			return resp, nil
		})
}

// newPostEventTool returns the post_event native tool: an agent-authored
// annotation in the session log.
func newPostEventTool(events ultra.EventStore, session ultra.SessionID, runID ultra.RunID) fantasy.AgentTool {
	type input struct {
		Text string `json:"text" description:"The note to post to the session log"`
	}
	return fantasy.NewAgentTool("post_event",
		"Post a note to the session event log, visible to everyone in the session.",
		func(ctx context.Context, in input, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			payload, err := json.Marshal(ultra.AnnotationPayload{Text: in.Text})
			if err != nil {
				return fantasy.NewTextErrorResponse("failed to encode note"), nil
			}
			if _, err := events.Append(ctx, session, ultra.Event{
				Actor:   ultra.Actor{Type: ultra.ActorAgent, ID: string(runID)},
				Kind:    ultra.EventKindAnnotation,
				Payload: payload,
			}); err != nil {
				return fantasy.NewTextErrorResponse("failed to post note"), nil
			}
			return fantasy.NewTextResponse("posted"), nil
		})
}
