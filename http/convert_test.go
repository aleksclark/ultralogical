package http

import (
	"encoding/json"
	"testing"

	uc "github.com/aleksclark/ultracore"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Stored event payloads are decoded into their proto message with unknown
// fields discarded, because the log must survive additive schema growth. The
// cost of that tolerance is that a domain payload whose JSON names merely look
// similar to the proto reaches clients with those fields silently empty — a
// failure with no error anywhere.
//
// This test pins each payload's wire shape: every field the domain type
// produces must be a field the proto actually has.
func TestDomainPayloadsMatchProtoFields(t *testing.T) {
	cases := []struct {
		kind    string
		payload any
	}{
		{uc.EventKindUserMessage, uc.UserMessagePayload{Text: "hi"}},
		{uc.EventKindAnnotation, uc.AnnotationPayload{Text: "note"}},
		{uc.EventKindRunStarted, uc.RunStartedPayload{RunID: "r", Prompt: "p"}},
		{uc.EventKindStepStarted, uc.StepStartedPayload{RunID: "r", StepIndex: 1, Attempt: 2}},
		{uc.EventKindTextDelta, uc.TextDeltaPayload{RunID: "r", StepIndex: 1, Attempt: 1, DeltaIndex: 2, Text: "t"}},
		{uc.EventKindReasoningDelta, uc.ReasoningDeltaPayload{RunID: "r", Text: "t"}},
		{uc.EventKindToolCallStart, uc.ToolCallStartedPayload{RunID: "r", ToolCallID: "c", Name: "bash", Input: "{}"}},
		{uc.EventKindToolResult, uc.ToolResultPayload{RunID: "r", ToolCallID: "c", Name: "bash", Content: "out", IsError: true}},
		{uc.EventKindStepFinished, uc.StepFinishedPayload{RunID: "r", TokensIn: 1, TokensOut: 2, FinishReason: "stop"}},
		{uc.EventKindRunAwaiting, uc.RunAwaitingPayload{RunID: "r", Question: uc.Question{Text: "q", Choices: []string{"a"}}}},
		{uc.EventKindRunCompleted, uc.RunCompletedPayload{RunID: "r", FinalText: "done"}},
		{uc.EventKindRunFailed, uc.RunFailedPayload{RunID: "r", Reason: "internal", Message: "m"}},
		{uc.EventKindRunCancelled, uc.RunCancelledPayload{RunID: "r"}},
		{uc.EventKindResourceReady, uc.ResourceEventPayload{ResourceID: "e", Name: "main", ProviderInstanceID: "p", Endpoint: "http://x", Message: "m", Epoch: 2}},
		{uc.EventKindExecPreviewRan, uc.ExecPreviewRanPayload{ResourceID: "e", Command: "echo", Output: "hi", IsError: false}},
		{uc.EventKindRunSpawned, uc.RunSpawnedPayload{ParentRunID: "p", ChildRunID: "c"}},
		{uc.EventKindMemorySet, uc.NewMemoryEventPayload("a.b", uc.ActorAgent(uc.RunID("r")), []byte(`{"v":1}`))},
		{uc.EventKindMemoryDeleted, uc.NewMemoryEventPayload("a.b", uc.Actor{Kind: uc.ActorKindService, ID: "u"}, nil)},
		{uc.EventKindPermissionDenied, uc.PermissionDeniedPayload{RunID: "r", Tool: "bash", Reason: "denied"}},
		{uc.EventKindHistoryCompacted, uc.HistoryCompactedPayload{RunID: "r", CoveredMessages: 3, SummaryTokens: 9}},
		{uc.EventKindModelFallback, uc.ModelFallbackPayload{RunID: "r", From: "a", To: "b", Reason: "x"}},
		{uc.EventKindHookFired, uc.HookFiredPayload{Hook: "h", RunID: "r"}},
		{uc.EventKindPeriodicPromptFired, uc.PeriodicPromptFiredPayload{RunID: "r", Prompt: "p"}},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			// Strict decoding: any field the proto does not know is an error
			// here, even though production tolerates it.
			converted, err := payloadFromDomain(tc.kind, raw)
			if err != nil {
				t.Fatalf("payload does not convert: %v (payload %s)", err, raw)
			}
			message := variantMessage(t, converted)
			if err := (protojson.UnmarshalOptions{}).Unmarshal(raw, message); err != nil {
				t.Fatalf("domain JSON has fields the proto does not: %v\npayload: %s", err, raw)
			}

			// Every non-empty domain field must survive the round trip. A
			// dropped field is exactly the failure this test exists to catch.
			// protojson emits camelCase, so compare against the proto's own
			// field names rather than against the JSON casing.
			roundTripped, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]any
			if err := json.Unmarshal(raw, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(roundTripped, &after); err != nil {
				t.Fatal(err)
			}
			for field, value := range before {
				if isZeroish(value) {
					continue
				}
				if _, ok := after[field]; !ok {
					t.Errorf("field %q was dropped converting to proto; clients would see it empty", field)
				}
			}
		})
	}
}

// variantMessage returns the concrete message inside a payload oneof.
func variantMessage(t *testing.T, payload proto.Message) proto.Message {
	t.Helper()
	reflected := payload.ProtoReflect()
	var found protoreflect.Message
	reflected.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() == protoreflect.MessageKind {
			found = v.Message()
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("converted payload has no variant set")
	}
	return found.Interface()
}

// isZeroish reports whether a JSON value is a zero value, which protojson
// legitimately omits from its output.
func isZeroish(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case bool:
		return !value
	case float64:
		return value == 0
	case string:
		return value == ""
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	default:
		return false
	}
}
