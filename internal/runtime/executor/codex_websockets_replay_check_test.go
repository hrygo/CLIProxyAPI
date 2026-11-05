package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexDuplexLaterToolTurnRequiresReplay(t *testing.T) {
	for _, tc := range []struct {
		name, turnType string
		finalizer      bool
	}{
		{"create_interceptor", "response.create", false},
		{"steer_interceptor", "response.steer", false},
		{"create_finalizer", "response.create", true},
	} {
		t.Run(tc.name, func(t *testing.T) { testCodexDuplexLaterToolTurnRequiresReplay(t, tc.turnType, tc.finalizer) })
	}
}

func testCodexDuplexLaterToolTurnRequiresReplay(t *testing.T, turnType string, finalizer bool) {
	var requests atomic.Int32
	done := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}
		requests.Add(1)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`))
		if _, _, err = conn.ReadMessage(); err == nil {
			requests.Add(1)
		}
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := make(chan core.WebsocketInput, 1)
	ctx = core.WithWebsocketInput(core.WithDownstreamWebsocket(ctx), input)
	checked := make(chan bool, 1)
	var checks atomic.Int32
	ctx = core.WithResponsesToolsReplayChecker(ctx, func(payload []byte, model, provider, authID string) bool {
		if finalizer && checks.Add(1) == 1 {
			return false
		}
		if gjson.GetBytes(payload, "tools.0.name").String() != "rewritten" {
			return false
		}
		checked <- model == "gpt-test" && provider == "codex" && authID == "A"
		return true
	})
	cfg := &config.Config{Codex: config.CodexConfig{ResponseSteering: true}}
	if finalizer {
		cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-test"}}, Params: map[string]any{"tools": []any{map[string]any{"type": "function", "name": "rewritten", "parameters": map[string]any{"type": "object"}}}}}}
	}
	exec := NewCodexWebsocketsExecutor(cfg)
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	credential := &auth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test-key", "base_url": upstream.URL, "websockets": "true"}}
	result, err := exec.ExecuteStream(ctx, credential, core.Request{Model: "gpt-test", Payload: []byte(`{"model":"gpt-test","input":[]}`)}, core.Options{SourceFormat: translator.FromString("codex"), Metadata: map[string]any{core.ExecutionSessionMetadataKey: t.Name()}, RequestAfterAuthInterceptor: func(_ context.Context, _ core.RequestAfterAuthInterceptRequest) core.RequestAfterAuthInterceptResponse {
		if finalizer {
			return core.RequestAfterAuthInterceptResponse{}
		}
		return core.RequestAfterAuthInterceptResponse{Body: []byte(`{"type":"` + turnType + `","input":[],"tools":[{"type":"function","name":"rewritten","parameters":{"type":"object"}}]}`)}
	}})
	if err != nil {
		t.Fatal(err)
	}
	var streamErr error
	for chunk := range result.Chunks {
		if gjson.GetBytes(chunk.Payload, "type").String() == "response.created" {
			input <- core.WebsocketInput{Payload: []byte(`{"type":"` + turnType + `","input":[]}`)}
		}
		if chunk.Err != nil {
			streamErr = chunk.Err
			break
		}
	}
	if !core.IsUpstreamWebsocketReplayRequired(streamErr) {
		t.Fatalf("error = %v, want replay-required signal", streamErr)
	}
	select {
	case valid := <-checked:
		if !valid {
			t.Fatal("checker received wrong payload or route")
		}
	case <-ctx.Done():
		t.Fatal("replay checker not called")
	}
	cancel()
	exec.CloseExecutionSession(t.Name())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("upstream did not close")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("forwarded %d frames; want only initial frame", got)
	}
}

func TestCodexDuplexInitialFinalizerToolRequiresReplay(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer upstream.Close()
	cfg := &config.Config{Codex: config.CodexConfig{ResponseSteering: true}}
	cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-test"}}, Params: map[string]any{"tools": []any{map[string]any{"type": "function", "name": "rewritten", "parameters": map[string]any{"type": "object"}}}}}}
	exec := NewCodexWebsocketsExecutor(cfg)
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	ctx := core.WithWebsocketInput(core.WithDownstreamWebsocket(context.Background()), make(chan core.WebsocketInput))
	ctx = core.WithResponsesToolsReplayChecker(ctx, func(payload []byte, model, provider, authID string) bool {
		return gjson.GetBytes(payload, "tools.0.name").String() == "rewritten" && model == "gpt-test" && provider == "codex" && authID == "A"
	})
	credential := &auth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test-key", "base_url": upstream.URL, "websockets": "true"}}
	_, err := exec.ExecuteStream(ctx, credential, core.Request{Model: "gpt-test", Payload: []byte(`{"model":"gpt-test","input":[]}`)}, core.Options{SourceFormat: translator.FromString("codex"), Metadata: map[string]any{core.ExecutionSessionMetadataKey: t.Name()}})
	if !core.IsUpstreamWebsocketReplayRequired(err) {
		t.Fatalf("error = %v, want replay-required", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("upstream requests = %d, want none", n)
	}
}
