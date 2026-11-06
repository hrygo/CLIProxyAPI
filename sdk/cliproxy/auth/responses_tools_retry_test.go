package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	responsestools "github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

type responsesToolsRetryCapture struct {
	authID   string
	body     []byte
	original []byte
	guarded  bool
	active   int
}

type responsesToolsRetryExecutor struct {
	fakeResponsesExecutor
	manager  *Manager
	captures []responsesToolsRetryCapture
}

func (f *responsesToolsRetryExecutor) capture(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) {
	active, _ := f.manager.responsesToolsSnapshot().limiter.Usage()
	f.captures = append(f.captures, responsesToolsRetryCapture{
		authID: auth.ID, body: bytes.Clone(req.Payload), original: bytes.Clone(opts.OriginalRequest),
		guarded: cliproxyexecutor.WireContractFromContext(ctx) != nil, active: active,
	})
}

func (f *responsesToolsRetryExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	f.capture(ctx, auth, req, opts)
	if auth.ID == "retry-route-a" {
		return cliproxyexecutor.Response{}, &Error{Code: "upstream_unavailable", HTTPStatus: http.StatusServiceUnavailable, Retryable: true}
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"id":"resp_b","status":"completed","output":[{"type":"custom_tool_call","id":"ctc_b","call_id":"call_b","name":"patcher","input":"new patch"}]}`)}, nil
}

func (f *responsesToolsRetryExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	f.capture(ctx, auth, req, opts)
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	if auth.ID == "retry-route-a" {
		// Exercise failure through the actual bootstrap/attempt cleanup path.
		chunks <- cliproxyexecutor.StreamChunk{Err: &Error{Code: "upstream_unavailable", HTTPStatus: http.StatusServiceUnavailable, Retryable: true}}
	} else {
		chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_b\",\"status\":\"completed\",\"output\":[{\"type\":\"custom_tool_call\",\"id\":\"ctc_b\",\"call_id\":\"call_b\",\"name\":\"patcher\",\"input\":\"new patch\"}]}}\n\n")}
	}
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
}

func responsesToolsRetryManager(t *testing.T) (*Manager, *responsesToolsRetryExecutor) {
	t.Helper()
	enabled := true
	cfg := &internalconfig.Config{}
	cfg.ResponsesTools.Enabled = &enabled
	for _, route := range []struct{ endpoint, custom, refs string }{
		{"https://route-a.invalid", "function", "inline"},
		{"https://route-b.invalid", "native", "preserve"},
	} {
		cfg.ResponsesTools.Routes = append(cfg.ResponsesTools.Routes, internalconfig.ResponsesToolsRoute{
			Match:        internalconfig.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "gpt-5.6-sol", UpstreamFormat: "codex", BaseURL: route.endpoint},
			ClientSearch: "native", CustomTools: route.custom, CustomGrammar: "reject",
			Schema: internalconfig.ResponsesToolsSchema{LocalRefs: route.refs},
		})
	}
	cfg.NormalizeResponsesToolsConfig()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.SetConfig(cfg)
	manager.SetRetryConfig(0, 0, 2)
	executor := &responsesToolsRetryExecutor{fakeResponsesExecutor: fakeResponsesExecutor{provider: "codex", toFormat: sdktranslator.FormatCodex}, manager: manager}
	manager.RegisterExecutor(executor)
	for _, route := range []struct{ id, endpoint, priority string }{
		{"retry-route-a", "https://route-a.invalid", "100"},
		{"retry-route-b", "https://route-b.invalid", "0"},
	} {
		registry.GetGlobalRegistry().RegisterClient(route.id, "codex", []*registry.ModelInfo{{ID: "gpt-5.6-sol"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(route.id) })
		if _, err := manager.Register(context.Background(), &Auth{
			ID: route.id, Provider: "codex", Status: StatusActive,
			Attributes: map[string]string{"auth_kind": "oauth", "base_url": route.endpoint, "priority": route.priority},
			Metadata:   map[string]any{"disable_cooling": true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return manager, executor
}

func TestResponsesToolsRetryAcrossAliasAndSchemaPolicies(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			manager, executor := responsesToolsRetryManager(t)
			body := []byte(`{"tools":[{"type":"custom","name":"patcher"},{"type":"function","name":"read","parameters":{"type":"object","$defs":{"Path":{"type":"string"}},"properties":{"path":{"$ref":"#/$defs/Path"}}}}],"input":[{"type":"custom_tool_call","id":"ctc_old","call_id":"call_old","name":"patcher","input":"old patch"},{"type":"custom_tool_call_output","id":"ctco_old","call_id":"call_old","output":"applied"}]}`)
			before := bytes.Clone(body)
			req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: body}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex, OriginalRequest: body, Stream: stream}
			var result []byte
			if stream {
				output, err := manager.ExecuteStream(context.Background(), []string{"codex"}, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				for chunk := range output.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					result = append(result, chunk.Payload...)
				}
			} else {
				output, err := manager.Execute(context.Background(), []string{"codex"}, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				result = output.Payload
			}
			if !bytes.Equal(body, before) {
				t.Fatal("retry modified the caller's original payload")
			}
			if len(executor.captures) != 2 || executor.captures[0].authID != "retry-route-a" || executor.captures[1].authID != "retry-route-b" {
				t.Fatalf("route attempts = %+v, want A then B", executor.captures)
			}
			a, b := executor.captures[0], executor.captures[1]
			if !a.guarded || a.active != 1 || b.guarded || b.active != 0 {
				t.Fatalf("attempt state crossed routes: A guarded/active=%v/%d, B=%v/%d", a.guarded, a.active, b.guarded, b.active)
			}
			var decoded map[string]any
			if err := json.Unmarshal(a.body, &decoded); err != nil {
				t.Fatal(err)
			}
			tools := decoded["tools"].([]any)
			custom := tools[0].(map[string]any)
			if custom["type"] != "function" || !strings.HasPrefix(custom["name"].(string), responsestools.CustomAliasPrefix) || bytes.Contains(a.body, []byte(`"$ref"`)) || !bytes.Equal(a.body, a.original) {
				t.Fatalf("route A did not receive its alias/inline policy: %s", a.body)
			}
			if !bytes.Equal(b.body, before) || !bytes.Equal(b.original, before) {
				t.Fatalf("route B inherited A's rewritten schema/history: %s", b.body)
			}
			if !bytes.Contains(result, []byte(`"custom_tool_call"`)) || !bytes.Contains(result, []byte(`"call_b"`)) || !bytes.Contains(result, []byte(`"patcher"`)) {
				t.Fatalf("route B's response identity was not retained: %s", result)
			}
			if active, retained := manager.responsesToolsSnapshot().limiter.Usage(); active != 0 || retained != 0 {
				t.Fatalf("retry retained attempt resources: %d/%d", active, retained)
			}
		})
	}
}

func TestResponsesToolsOpaqueHistoryStopsBeforeRouteRotation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		manager, executor := responsesToolsRetryManager(t)
		body := []byte(`{"previous_response_id":"resp_unknown","tools":[{"type":"custom","name":"patcher"}],"input":[]}`)
		req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: body}
		opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex, OriginalRequest: body, Stream: stream}
		var err error
		if stream {
			_, err = manager.ExecuteStream(context.Background(), []string{"codex"}, req, opts)
		} else {
			_, err = manager.Execute(context.Background(), []string{"codex"}, req, opts)
		}
		var status interface{ StatusCode() int }
		if !errors.As(err, &status) || status.StatusCode() != http.StatusUnprocessableEntity || !strings.Contains(err.Error(), "opaque_history") {
			t.Fatalf("stream=%v: error = %v, want opaque_history/422", stream, err)
		}
		if len(executor.captures) != 0 {
			t.Fatalf("stream=%v: local rejection dispatched to %d routes", stream, len(executor.captures))
		}
		if active, retained := manager.responsesToolsSnapshot().limiter.Usage(); active != 0 || retained != 0 {
			t.Fatalf("rejected request retained attempt resources: %d/%d", active, retained)
		}
	}
}
