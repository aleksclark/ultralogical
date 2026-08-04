// Package testclient wraps the ultracore Go SDK — the same artifact real
// consumers use — with helpers for the functional suite. It is deliberately
// the only way tests talk to cored: if the public API can't do it, the test
// can't do it.
package testclient

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/gen/go/core/v1/corev1connect"
	"github.com/aleksclark/ultracore/sdk"
)

// Client is an authenticated API client for one tenant key. Embedded service
// clients and helpers come from the SDK so the entire functional suite
// exercises the consumer surface.
type Client struct {
	*sdk.Client
	// Agents is an alias for Runs retained for existing e2e call sites.
	Agents corev1connect.RunServiceClient
}

// New builds a Client against baseURL, authenticating every request with the
// given bearer token.
func New(baseURL, token string) *Client {
	return NewWithActor(baseURL, token, "service/test")
}

// NewWithActor builds a Client that sends the given X-Core-Actor header.
func NewWithActor(baseURL, token, actor string) *Client {
	c := sdk.New(sdk.Options{BaseURL: baseURL, APIKey: token, Actor: actor})
	return &Client{Client: c, Agents: c.Runs}
}

// Subscription is a live event stream with collection helpers.
type Subscription struct {
	inner *sdk.Subscription
}

// Subscribe opens an event stream from fromSeq. Reconnect is disabled so tests
// can assert mid-stream auth failure (A3.3) without silent resume.
func (c *Client) Subscribe(ctx context.Context, sessionID string, fromSeq int64) (*Subscription, error) {
	reconnect := false
	s, err := c.Client.Subscribe(ctx, sessionID, sdk.SubscribeOptions{FromSeq: fromSeq, Reconnect: &reconnect})
	if err != nil {
		return nil, err
	}
	return &Subscription{inner: s}, nil
}

// SubscribeResume opens a reconnecting subscription (SDK default).
func (c *Client) SubscribeResume(ctx context.Context, sessionID string, fromSeq int64) (*Subscription, error) {
	s, err := c.Client.Subscribe(ctx, sessionID, sdk.SubscribeOptions{FromSeq: fromSeq})
	if err != nil {
		return nil, err
	}
	return &Subscription{inner: s}, nil
}

// Close terminates the subscription.
func (s *Subscription) Close() { s.inner.Close() }

// Next returns the next event.
func (s *Subscription) Next() (*corev1.SessionEvent, error) { return s.inner.Next() }

// LastSeq is the highest seq successfully delivered.
func (s *Subscription) LastSeq() int64 { return s.inner.LastSeq() }

// Collect receives exactly n events or fails the test after timeout.
func (s *Subscription) Collect(t *testing.T, n int, timeout time.Duration) []*corev1.SessionEvent {
	t.Helper()
	var out []*corev1.SessionEvent
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

// CollectUntil receives events until match returns true (inclusive).
func (s *Subscription) CollectUntil(t *testing.T, timeout time.Duration, match func(*corev1.SessionEvent) bool) []*corev1.SessionEvent {
	t.Helper()
	var out []*corev1.SessionEvent
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

// Kind returns a short string naming the payload variant of an event.
func Kind(ev *corev1.SessionEvent) string { return sdk.EventKind(ev) }

// StartRun starts an agent run with the default model config.
func (c *Client) StartRun(ctx context.Context, sessionID, prompt string) (*corev1.AgentRun, int64, error) {
	return c.Client.StartRun(ctx, sessionID, prompt, nil, nil)
}

// AwaitRunState polls GetRun until the run reaches the wanted state.
func (c *Client) AwaitRunState(t *testing.T, runID string, want corev1.RunState, timeout time.Duration) *corev1.AgentRun {
	t.Helper()
	run, err := c.AwaitRun(context.Background(), runID, sdk.AwaitRunOptions{
		Timeout: timeout,
		States:  []corev1.RunState{want},
	})
	if err != nil {
		t.Fatalf("run %s never reached %v within %s: %v", runID, want, timeout, err)
	}
	return run
}

// PromptRun is retained as an alias for AnswerRun for existing e2e call sites
// that use the helper (not the generated client).
func (c *Client) PromptRun(ctx context.Context, runID, message string) (int64, error) {
	return c.AnswerRun(ctx, runID, message)
}

// Ensure connect is referenced for callers constructing requests directly.
var _ = connect.CodeOf
