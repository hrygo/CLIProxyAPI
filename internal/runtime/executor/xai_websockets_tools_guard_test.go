package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestXAIWebsocketFinalToolGuardRejectsBeforeDialAndUnlocks(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer upstream.Close()
	exec := NewXAIWebsocketsExecutor(&config.Config{})
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	ctx := core.WithWireContract(context.Background(), &core.WireContract{RequiredFunctionNames: []string{"must_survive"}})
	ctx = core.WithDownstreamWebsocket(ctx)
	credential := &auth.Auth{ID: "xai-guard", Provider: "xai", Attributes: map[string]string{"api_key": "test-key", "base_url": upstream.URL, "websockets": "true"}}
	_, err := exec.ExecuteStream(ctx, credential, core.Request{Model: "grok-test", Payload: []byte(`{"model":"grok-test","input":[]}`)}, core.Options{SourceFormat: translator.FromString("openai-response"), Metadata: map[string]any{core.ExecutionSessionMetadataKey: t.Name()}})
	var status interface{ StatusCode() int }
	if !errors.As(err, &status) || status.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("error = %v, want 422", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("upstream requests = %d, want none", got)
	}
	session := exec.getOrCreateSession(t.Name())
	if session == nil || !session.reqMu.TryLock() {
		t.Fatal("guard left session locked")
	}
	session.reqMu.Unlock()
}
