package e2e

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"
	"github.com/aleksclark/ultracore/testkit/harness"
)

func TestA66_PeriodicPromptAPI(t *testing.T) {
	stack := harness.Up(t)
	c := stack.AliceClient()
	sess := createSession(t, c, string(stack.TenantA.ID), "periodic")
	put, err := c.Automation.PutPeriodicPrompt(context.Background(), connect.NewRequest(&corev1.PutPeriodicPromptRequest{SessionId: sess.GetId(), Schedule: "1s", Prompt: "check status"}))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := c.Automation.ListPeriodicPrompts(context.Background(), connect.NewRequest(&corev1.ListPeriodicPromptsRequest{SessionId: sess.GetId()}))
	if err != nil || len(listed.Msg.GetPeriodicPrompts()) != 1 {
		t.Fatalf("list=%v err=%v", listed, err)
	}
	_, err = c.Automation.SetPeriodicPromptEnabled(context.Background(), connect.NewRequest(&corev1.SetPeriodicPromptEnabledRequest{PeriodicPromptId: put.Msg.GetPeriodicPrompt().GetId(), Enabled: false}))
	if err != nil {
		t.Fatal(err)
	}
}
