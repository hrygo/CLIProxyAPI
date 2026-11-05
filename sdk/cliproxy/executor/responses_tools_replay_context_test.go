package executor

import (
	"context"
	"testing"
)

func TestResponsesToolsReplayCheckerContext(t *testing.T) {
	if ResponsesToolsReplayCheckerFromContext(context.Background()) != nil || ResponsesToolsReplayCheckerFromContext(nil) != nil {
		t.Fatal("unexpected checker")
	}
	called := false
	want := WithResponsesToolsReplayChecker(context.Background(), func(payload []byte, model, provider, authID string) bool {
		called = string(payload) == "request" && model == "model" && provider == "codex" && authID == "auth"
		return called
	})
	checker := ResponsesToolsReplayCheckerFromContext(want)
	if checker == nil || !checker([]byte("request"), "model", "codex", "auth") || !called {
		t.Fatal("checker did not retain context or arguments")
	}
}
