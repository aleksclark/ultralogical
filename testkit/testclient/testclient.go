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
