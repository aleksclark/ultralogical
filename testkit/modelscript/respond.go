package modelscript

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// wire shapes for responses

type respMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func turnUsage(t Turn) usage {
	in := 7
	out := max(len(t.Text)/4, 1) + len(t.ToolCalls)*3
	return usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out}
}

func finishReason(t Turn) string {
	if len(t.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func wireToolCalls(t Turn) []ToolCall {
	out := make([]ToolCall, len(t.ToolCalls))
	for i, spec := range t.ToolCalls {
		tc := ToolCall{ID: fmt.Sprintf("call_%d", i), Type: "function"}
		tc.Function.Name = spec.Name
		tc.Function.Arguments = mustJSON(spec.Args)
		out[i] = tc
	}
	return out
}

// respondJSON serves the non-streaming completion shape.
func (s *Server) respondJSON(w http.ResponseWriter, t Turn) {
	content := t.Text
	resp := map[string]any{
		"id":      "chatcmpl-modelscript",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "modelscript",
		"choices": []map[string]any{{
			"index":         0,
			"message":       respMessage{Role: "assistant", Content: &content, ToolCalls: wireToolCalls(t)},
			"finish_reason": finishReason(t),
		}},
		"usage": turnUsage(t),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// respondStream serves the SSE streaming completion shape with configurable
// chunking and pacing.
func (s *Server) respondStream(w http.ResponseWriter, t Turn) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	writeChunk := func(delta map[string]any, finish any) {
		chunk := map[string]any{
			"id":      "chatcmpl-modelscript",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   "modelscript",
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
		b, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Role prelude.
	writeChunk(map[string]any{"role": "assistant"}, nil)

	// Text content, chunked and paced.
	chunkSize := t.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 16
	}
	for start := 0; start < len(t.Text); start += chunkSize {
		end := min(start+chunkSize, len(t.Text))
		writeChunk(map[string]any{"content": t.Text[start:end]}, nil)
		if t.ChunkDelay > 0 {
			time.Sleep(t.ChunkDelay)
		}
	}

	// Tool calls: name first, then arguments as one delta.
	for i, tc := range wireToolCalls(t) {
		writeChunk(map[string]any{"tool_calls": []map[string]any{{
			"index": i,
			"id":    tc.ID,
			"type":  "function",
			"function": map[string]any{
				"name":      tc.Function.Name,
				"arguments": "",
			},
		}}}, nil)
		writeChunk(map[string]any{"tool_calls": []map[string]any{{
			"index": i,
			"function": map[string]any{
				"arguments": tc.Function.Arguments,
			},
		}}}, nil)
	}

	// Finish + usage.
	writeChunk(map[string]any{}, finishReason(t))
	u := turnUsage(t)
	final := map[string]any{
		"id":      "chatcmpl-modelscript",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "modelscript",
		"choices": []map[string]any{},
		"usage":   u,
	}
	b, _ := json.Marshal(final)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
