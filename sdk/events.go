package sdk

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
)

// Subscription is a live event stream. When reconnect is enabled (default),
// drops resume from the last observed seq with no gaps or duplicates.
type Subscription struct {
	client    *Client
	sessionID string
	fromSeq   int64
	lastSeq   int64
	reconnect bool
	stream    *connect.ServerStreamForClient[corev1.SubscribeResponse]
	cancel    context.CancelFunc
	parent    context.Context
}

// SubscribeOptions configure Subscribe.
type SubscribeOptions struct {
	// FromSeq delivers events with seq greater than this value. Zero replays.
	FromSeq int64
	// Reconnect resumes from last seen seq after stream errors. Default true.
	// Set to false only for tests that assert stream termination.
	Reconnect *bool
}

// Subscribe opens an event stream for a session.
func (c *Client) Subscribe(ctx context.Context, sessionID string, opts SubscribeOptions) (*Subscription, error) {
	reconnect := true
	if opts.Reconnect != nil {
		reconnect = *opts.Reconnect
	}
	s := &Subscription{
		client:    c,
		sessionID: sessionID,
		fromSeq:   opts.FromSeq,
		lastSeq:   opts.FromSeq,
		reconnect: reconnect,
		parent:    ctx,
	}
	if err := s.open(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Subscription) open() error {
	ctx, cancel := context.WithCancel(s.parent)
	stream, err := s.client.Events.Subscribe(ctx, connect.NewRequest(&corev1.SubscribeRequest{
		SessionId: s.sessionID,
		FromSeq:   s.lastSeq,
	}))
	if err != nil {
		cancel()
		return err
	}
	s.cancel = cancel
	s.stream = stream
	return nil
}

// Close terminates the subscription.
func (s *Subscription) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.stream != nil {
		_ = s.stream.Close()
	}
}

// LastSeq is the highest seq successfully delivered to the caller.
func (s *Subscription) LastSeq() int64 { return s.lastSeq }

// Next returns the next event, reconnecting transparently on stream errors
// when reconnect is enabled. Keepalive frames are skipped.
func (s *Subscription) Next() (*corev1.SessionEvent, error) {
	for {
		if err := s.parent.Err(); err != nil {
			return nil, err
		}
		if s.stream == nil {
			if err := s.open(); err != nil {
				if s.parent.Err() != nil {
					return nil, s.parent.Err()
				}
				if !s.reconnect {
					return nil, err
				}
				select {
				case <-s.parent.Done():
					return nil, s.parent.Err()
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
		}
		for s.stream.Receive() {
			if ev := s.stream.Msg().GetEvent(); ev != nil {
				s.lastSeq = ev.GetSeq()
				return ev, nil
			}
		}
		err := s.stream.Err()
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.stream.Close()
		s.stream = nil
		if !s.reconnect {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("stream closed")
		}
		if s.parent.Err() != nil {
			if err != nil {
				return nil, err
			}
			return nil, s.parent.Err()
		}
		select {
		case <-s.parent.Done():
			return nil, s.parent.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// AppendUserMessage appends a user message and returns its seq.
func (c *Client) AppendUserMessage(ctx context.Context, sessionID, text string) (int64, error) {
	resp, err := c.Events.Append(ctx, connect.NewRequest(&corev1.AppendRequest{
		SessionId: sessionID,
		Payload: &corev1.EventPayload{
			Payload: &corev1.EventPayload_UserMessage{UserMessage: &corev1.UserMessage{Text: text}},
		},
	}))
	if err != nil {
		return 0, err
	}
	return resp.Msg.GetSeq(), nil
}

// GetEvents reads a range of events (non-streaming).
func (c *Client) GetEvents(ctx context.Context, sessionID string, fromSeq, toSeq int64, pageSize int32) ([]*corev1.SessionEvent, error) {
	var out []*corev1.SessionEvent
	token := ""
	for {
		resp, err := c.Events.Get(ctx, connect.NewRequest(&corev1.GetRequest{
			SessionId: sessionID,
			FromSeq:   fromSeq,
			ToSeq:     toSeq,
			PageSize:  pageSize,
			PageToken: token,
		}))
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Msg.GetEvents()...)
		token = resp.Msg.GetNextPageToken()
		if token == "" {
			return out, nil
		}
	}
}

// EventKind returns a short string naming the payload variant of an event.
func EventKind(ev *corev1.SessionEvent) string {
	switch ev.GetPayload().GetPayload().(type) {
	case *corev1.EventPayload_UserMessage:
		return "user_message"
	case *corev1.EventPayload_Annotation:
		return "annotation"
	case *corev1.EventPayload_RunStarted:
		return "run_started"
	case *corev1.EventPayload_StepStarted:
		return "step_started"
	case *corev1.EventPayload_TextDelta:
		return "text_delta"
	case *corev1.EventPayload_ReasoningDelta:
		return "reasoning_delta"
	case *corev1.EventPayload_ToolCallStarted:
		return "tool_call_started"
	case *corev1.EventPayload_ToolResult:
		return "tool_result"
	case *corev1.EventPayload_StepFinished:
		return "step_finished"
	case *corev1.EventPayload_RunAwaiting:
		return "run_awaiting"
	case *corev1.EventPayload_RunCompleted:
		return "run_completed"
	case *corev1.EventPayload_RunFailed:
		return "run_failed"
	case *corev1.EventPayload_RunCancelled:
		return "run_cancelled"
	case *corev1.EventPayload_ResourceRequested:
		return "resource_requested"
	case *corev1.EventPayload_ResourceProvisioning:
		return "resource_provisioning"
	case *corev1.EventPayload_ResourceReady:
		return "resource_ready"
	case *corev1.EventPayload_ResourceFailed:
		return "resource_failed"
	case *corev1.EventPayload_ResourceSuspended:
		return "resource_suspended"
	case *corev1.EventPayload_ResourceTerminating:
		return "resource_terminating"
	case *corev1.EventPayload_ResourceTerminated:
		return "resource_terminated"
	case *corev1.EventPayload_ExecPreviewRan:
		return "exec_preview_ran"
	case *corev1.EventPayload_RunSpawned:
		return "run_spawned"
	case *corev1.EventPayload_MemorySet:
		return "memory_set"
	case *corev1.EventPayload_MemoryDeleted:
		return "memory_deleted"
	case *corev1.EventPayload_PermissionDenied:
		return "permission_denied"
	case *corev1.EventPayload_HistoryCompacted:
		return "history_compacted"
	case *corev1.EventPayload_ModelFallback:
		return "model_fallback"
	case *corev1.EventPayload_HookFired:
		return "hook_fired"
	case *corev1.EventPayload_PeriodicPromptFired:
		return "periodic_prompt_fired"
	case *corev1.EventPayload_SessionLabelsChanged:
		return "session_labels_changed"
	default:
		return "unknown"
	}
}
