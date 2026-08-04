package testclient_test

import (
	"testing"

	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/testclient"
)

// Kind is what most acceptance tests assert event sequences with. A payload it
// does not recognize reports "unknown", which would let a test claim an event
// sequence is correct while silently ignoring whole event types. Every variant
// the protos define must therefore be mapped.
func TestKindCoversEveryEventVariant(t *testing.T) {
	payload := (&corev1.EventPayload{}).ProtoReflect().Descriptor()
	oneof := payload.Oneofs().ByName("payload")
	if oneof == nil {
		t.Fatal("EventPayload has no payload oneof")
	}
	for i := range oneof.Fields().Len() {
		field := oneof.Fields().Get(i)
		message := corev1.EventPayload{}
		event := &corev1.SessionEvent{Payload: &message}
		// Set this variant to a zero value of its type and ask Kind about it.
		reflected := message.ProtoReflect()
		reflected.Set(field, reflected.NewField(field))
		if got := testclient.Kind(event); got == "unknown" {
			t.Errorf("Kind does not recognize payload variant %q; add it or event assertions will silently skip it",
				field.Name())
		}
	}
}
