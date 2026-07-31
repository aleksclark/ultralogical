// Package testclient wraps the generated Go Connect client — the same
// artifact real consumers use — with helpers for the functional suite. It is
// deliberately the only way tests talk to ultrad: if the public API can't do
// it, the test can't do it.
package testclient

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	ultrav1 "github.com/aleksclark/ultralogical/gen/go/ultra/v1"
	"github.com/aleksclark/ultralogical/gen/go/ultra/v1/ultrav1connect"
)

// Client is an authenticated API client for one user.
type Client struct {
	Orgs     ultrav1connect.OrgServiceClient
	Sessions ultrav1connect.SessionServiceClient
	Events   ultrav1connect.EventServiceClient
	Agents   ultrav1connect.AgentServiceClient
	Envs     ultrav1connect.EnvServiceClient
	Billing  ultrav1connect.BillingServiceClient
	Flows    ultrav1connect.FlowServiceClient
}

type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

// New builds a Client against baseURL, authenticating every request with the
// given bearer token.
func New(baseURL, token string) *Client {
	httpClient := &http.Client{
		Transport: &authTransport{token: token, base: http.DefaultTransport},
	}
	return &Client{
		Orgs:     ultrav1connect.NewOrgServiceClient(httpClient, baseURL),
		Sessions: ultrav1connect.NewSessionServiceClient(httpClient, baseURL),
		Events:   ultrav1connect.NewEventServiceClient(httpClient, baseURL),
		Agents:   ultrav1connect.NewAgentServiceClient(httpClient, baseURL),
		Envs:     ultrav1connect.NewEnvServiceClient(httpClient, baseURL),
		Billing:  ultrav1connect.NewBillingServiceClient(httpClient, baseURL),
		Flows:    ultrav1connect.NewFlowServiceClient(httpClient, baseURL),
	}
}

// AppendUserMessage appends a user message and returns its seq.
func (c *Client) AppendUserMessage(ctx context.Context, sessionID, text string) (int64, error) {
	resp, err := c.Events.Append(ctx, connect.NewRequest(&ultrav1.AppendRequest{
		SessionId: sessionID,
		Payload: &ultrav1.EventPayload{
			Payload: &ultrav1.EventPayload_UserMessage{UserMessage: &ultrav1.UserMessage{Text: text}},
		},
	}))
	if err != nil {
		return 0, err
	}
	return resp.Msg.GetSeq(), nil
}

// Subscription is a live event stream with collection helpers.
type Subscription struct {
	stream *connect.ServerStreamForClient[ultrav1.SubscribeResponse]
	cancel context.CancelFunc
}

// Subscribe opens an event stream from fromSeq.
func (c *Client) Subscribe(ctx context.Context, sessionID string, fromSeq int64) (*Subscription, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := c.Events.Subscribe(ctx, connect.NewRequest(&ultrav1.SubscribeRequest{
		SessionId: sessionID,
		FromSeq:   fromSeq,
	}))
	if err != nil {
		cancel()
		return nil, err
	}
	return &Subscription{stream: stream, cancel: cancel}, nil
}

// Close terminates the subscription.
func (s *Subscription) Close() {
	s.cancel()
	_ = s.stream.Close()
}

// Next returns the next event or an error when the stream ends/times out.
// Keepalive frames (responses without an event) are skipped transparently.
func (s *Subscription) Next() (*ultrav1.SessionEvent, error) {
	for s.stream.Receive() {
		if ev := s.stream.Msg().GetEvent(); ev != nil {
			return ev, nil
		}
	}
	if err := s.stream.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("stream closed")
}

// Collect receives exactly n events or fails the test after timeout.
func (s *Subscription) Collect(t *testing.T, n int, timeout time.Duration) []*ultrav1.SessionEvent {
	t.Helper()
	var out []*ultrav1.SessionEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for len(out) < n {
			ev, err := s.Next()
			if err != nil {
				return
			}
			out = append(out, ev)
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
	if len(out) != n {
		t.Fatalf("collected %d events, want %d (timeout %s)", len(out), n, timeout)
	}
	return out
}

// CollectUntil receives events until match returns true (inclusive) or the
// timeout elapses (test failure). Returns everything received.
func (s *Subscription) CollectUntil(t *testing.T, timeout time.Duration, match func(*ultrav1.SessionEvent) bool) []*ultrav1.SessionEvent {
	t.Helper()
	var out []*ultrav1.SessionEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			ev, err := s.Next()
			if err != nil {
				return
			}
			out = append(out, ev)
			if match(ev) {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("CollectUntil: no matching event within %s (%d received)", timeout, len(out))
	}
	if len(out) == 0 || !match(out[len(out)-1]) {
		t.Fatalf("CollectUntil: stream ended before match (%d received)", len(out))
	}
	return out
}

// Kind returns a short string naming the payload variant of an event, for
// compact sequence assertions.
func Kind(ev *ultrav1.SessionEvent) string {
	switch ev.GetPayload().GetPayload().(type) {
	case *ultrav1.EventPayload_UserMessage:
		return "user_message"
	case *ultrav1.EventPayload_Annotation:
		return "annotation"
	case *ultrav1.EventPayload_RunStarted:
		return "run_started"
	case *ultrav1.EventPayload_StepStarted:
		return "step_started"
	case *ultrav1.EventPayload_TextDelta:
		return "text_delta"
	case *ultrav1.EventPayload_ReasoningDelta:
		return "reasoning_delta"
	case *ultrav1.EventPayload_ToolCallStarted:
		return "tool_call_started"
	case *ultrav1.EventPayload_ToolResult:
		return "tool_result"
	case *ultrav1.EventPayload_StepFinished:
		return "step_finished"
	case *ultrav1.EventPayload_RunAwaiting:
		return "run_awaiting"
	case *ultrav1.EventPayload_RunCompleted:
		return "run_completed"
	case *ultrav1.EventPayload_RunFailed:
		return "run_failed"
	case *ultrav1.EventPayload_RunCancelled:
		return "run_cancelled"
	case *ultrav1.EventPayload_EnvRequested:
		return "env_requested"
	case *ultrav1.EventPayload_EnvProvisioning:
		return "env_provisioning"
	case *ultrav1.EventPayload_EnvReady:
		return "env_ready"
	case *ultrav1.EventPayload_EnvFailed:
		return "env_failed"
	case *ultrav1.EventPayload_EnvTerminating:
		return "env_terminating"
	case *ultrav1.EventPayload_EnvTerminated:
		return "env_terminated"
	case *ultrav1.EventPayload_ExecPreviewRan:
		return "exec_preview_ran"
	case *ultrav1.EventPayload_HistoryCompacted:
		return "history_compacted"
	case *ultrav1.EventPayload_ModelFallback:
		return "model_fallback"
	case *ultrav1.EventPayload_HookFired:
		return "hook_fired"
	case *ultrav1.EventPayload_PeriodicPromptFired:
		return "periodic_prompt_fired"
	default:
		return "unknown"
	}
}

// StartRun starts an agent run with the default model config.
func (c *Client) StartRun(ctx context.Context, sessionID, prompt string) (*ultrav1.AgentRun, int64, error) {
	resp, err := c.Agents.StartRun(ctx, connect.NewRequest(&ultrav1.StartRunRequest{
		SessionId: sessionID,
		Prompt:    prompt,
	}))
	if err != nil {
		return nil, 0, err
	}
	return resp.Msg.GetRun(), resp.Msg.GetEventSeq(), nil
}

// AwaitRunState polls GetRun until the run reaches the wanted state or the
// timeout elapses (test failure).
func (c *Client) AwaitRunState(t *testing.T, runID string, want ultrav1.RunState, timeout time.Duration) *ultrav1.AgentRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *ultrav1.AgentRun
	for time.Now().Before(deadline) {
		resp, err := c.Agents.GetRun(context.Background(), connect.NewRequest(&ultrav1.GetRunRequest{RunId: runID}))
		if err == nil {
			last = resp.Msg.GetRun()
			if last.GetState() == want {
				return last
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %v within %s (last: %v)", runID, want, timeout, last.GetState())
	return nil
}
