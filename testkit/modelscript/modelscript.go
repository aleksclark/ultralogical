// Package modelscript is a scripted OpenAI-compatible chat-completions
// server for deterministic agent-loop testing. It is the only permitted
// substitute for a real component anywhere in the test stack, and it
// substitutes at the network boundary: the worker talks to it over real
// HTTP with real credentials.
//
// Unmatched requests fail loudly (HTTP 500 and a recorded error) rather
// than defaulting — silent drift is the enemy.
package modelscript

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Message is the OpenAI wire shape of a chat message (subset).
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// Text extracts the plain-text content of a message (string or content-part
// array forms).
func (m Message) Text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// ToolCall is the OpenAI wire shape of an assistant tool call.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Request is a captured chat-completions request.
type Request struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	Stream        bool      `json:"stream"`
	Authorization string    `json:"-"`
}

// ToolCallSpec scripts one tool call in a response.
type ToolCallSpec struct {
	Name string
	Args any
}

// Turn scripts one model response. Turns are selected by Match when set;
// otherwise by index (the i-th turn answers the request containing i
// assistant messages, i.e. the i-th model call of the conversation).
type Turn struct {
	// Match selects this turn when it returns true for the request's
	// messages. Matched turns are consumed in script order.
	Match func(messages []Message) bool
	// Text is streamed as assistant content.
	Text string
	// ToolCalls are emitted after the text.
	ToolCalls []ToolCallSpec
	// ChunkSize is the number of bytes per streamed content chunk (default
	// 16).
	ChunkSize int
	// ChunkDelay paces streamed chunks (default 0).
	ChunkDelay time.Duration
	// Status, when non-zero, makes the server respond with this HTTP status
	// and no body (for auth/fallback tests).
	Status int
	// Gate, when non-nil, blocks the response until the channel is closed.
	// Tests use it to hold a run in a known state (for example a parent
	// parked on children) without sleeping and hoping.
	Gate <-chan struct{}
	// Scenario labels which independent scenario a turn belongs to. Turns from
	// two different labelled scenarios must never match one request: that
	// means one scenario's prompt is a substring of another's, and the server
	// refuses rather than guessing.
	Scenario int
	// Sticky keeps a matched turn available for later requests instead of
	// consuming it. A suite that drives several independent scenarios against
	// one server (a browser suite whose specs each start their own run, for
	// example) declares sticky matcher-selected turns so scenario order
	// cannot change which response a prompt receives.
	Sticky bool
}

// Script is an ordered set of turns.
type Script struct {
	Turns []Turn
}

// Server is a scripted chat-completions server.
type Server struct {
	httpSrv *httptest.Server

	mu       sync.Mutex
	script   Script
	requests []Request
	errors   []string
}

// New starts a Server on a random local port.
func New() *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handle)
	mux.HandleFunc("POST /chat/completions", s.handle)
	s.httpSrv = httptest.NewServer(mux)
	return s
}

// URL is the server's base URL (pass as the credential base_url; fantasy's
// openai provider appends /chat/completions).
func (s *Server) URL() string { return s.httpSrv.URL + "/v1" }

// Close shuts the server down.
func (s *Server) Close() { s.httpSrv.Close() }

// SetScript installs a fresh script and resets turn progress and captures.
func (s *Server) SetScript(script Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = script
	s.requests = nil
	s.errors = nil
}

// Requests returns the captured requests so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// Errors returns script errors (unmatched requests etc). Tests should
// assert this is empty.
func (s *Server) Errors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.errors))
	copy(out, s.errors)
	return out
}

func (s *Server) failf(w http.ResponseWriter, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.errors = append(s.errors, msg)
	http.Error(w, msg, http.StatusInternalServerError)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.mu.Lock()
		s.failf(w, "modelscript: bad request body: %v", err)
		s.mu.Unlock()
		return
	}
	req.Authorization = r.Header.Get("Authorization")

	s.mu.Lock()
	s.requests = append(s.requests, req)
	turn, err := s.selectTurn(req)
	s.mu.Unlock()
	if err != nil {
		s.mu.Lock()
		s.failf(w, "%v", err)
		s.mu.Unlock()
		return
	}

	// Gating happens outside the lock so a held turn cannot stall the whole
	// server for other runs.
	if turn.Gate != nil {
		<-turn.Gate
	}
	if turn.Status != 0 {
		w.WriteHeader(turn.Status)
		_, _ = w.Write([]byte(`{"error":{"message":"scripted error"}}`))
		return
	}
	if req.Stream {
		s.respondStream(w, turn)
		return
	}
	s.respondJSON(w, turn)
}

// selectTurn picks the turn for a request. Caller holds the lock.
//
// Matcher-based scripts are selected by conversation depth rather than by
// consumption order: among the turns whose matcher accepts this request, the
// one chosen is the one at the position matching how many assistant messages
// the conversation already contains. That makes a script independent of the
// order scenarios run in (several runs can share one server) while still
// letting one conversation walk through several turns.
func (s *Server) selectTurn(req Request) (Turn, error) {
	hasMatchers := false
	for _, t := range s.script.Turns {
		if t.Match != nil {
			hasMatchers = true
			break
		}
	}
	if !hasMatchers {
		// Index-based selection: the i-th model call gets Turns[i].
		assistants := countAssistants(req.Messages)
		if assistants >= len(s.script.Turns) {
			return Turn{}, fmt.Errorf("modelscript: request for turn %d but script has %d turns", assistants, len(s.script.Turns))
		}
		return s.script.Turns[assistants], nil
	}

	var matching []Turn
	var matchedGroups []int
	for _, t := range s.script.Turns {
		if t.Match == nil || t.Match(req.Messages) {
			matching = append(matching, t)
			matchedGroups = append(matchedGroups, t.group())
		}
	}
	// A prompt that matches turns from two different scenarios is almost
	// always an accident: one scenario's prompt is a substring of another's.
	// Silently choosing one produces a baffling failure far from the cause, so
	// say so here instead.
	if distinct := distinctGroups(matchedGroups); len(distinct) > 1 {
		return Turn{}, fmt.Errorf("modelscript: request matched turns from %d scenarios (%v); make the prompts distinct", len(distinct), distinct)
	}
	if len(matching) == 0 {
		return Turn{}, fmt.Errorf("modelscript: no turn matched request with %d messages", len(req.Messages))
	}
	depth := countAssistants(req.Messages)
	if depth < len(matching) {
		return matching[depth], nil
	}
	// Past the end of the scripted turns, a sticky turn keeps answering; a
	// script without one has run out and should say so loudly.
	last := matching[len(matching)-1]
	if last.Sticky {
		return last, nil
	}
	return Turn{}, fmt.Errorf("modelscript: conversation reached depth %d but only %d turns matched", depth, len(matching))
}

// group reports which scenario a turn belongs to. Turns default to group zero,
// which is treated as "unlabelled" and never conflicts.
func (t Turn) group() int { return t.Scenario }

// distinctGroups lists the labelled scenarios present in a match set.
func distinctGroups(groups []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, g := range groups {
		if g == 0 || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

// countAssistants reports how many model turns a conversation already
// contains, which is its depth.
func countAssistants(messages []Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

// UserContains returns a matcher for the most recent user message.
func UserContains(substr string) func([]Message) bool {
	return func(messages []Message) bool {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				return strings.Contains(messages[i].Text(), substr)
			}
		}
		return false
	}
}

// ToolResultContains returns a matcher for any tool message content.
func ToolResultContains(substr string) func([]Message) bool {
	return func(messages []Message) bool {
		for _, m := range messages {
			if m.Role == "tool" && strings.Contains(m.Text(), substr) {
				return true
			}
		}
		return false
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
