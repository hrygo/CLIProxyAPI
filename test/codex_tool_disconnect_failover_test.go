package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestCodexToolDisconnectDispatchBoundary(t *testing.T) {
	for _, buffering := range []bool{false, true} {
		for _, boundary := range []string{"before_output", "partial_arguments", "completed_call", "completed_response"} {
			t.Run(fmt.Sprintf("buffering_%v/%s", buffering, boundary), func(t *testing.T) {
				const model = "gpt-6-astra"
				var mu sync.Mutex
				var attempts []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					account := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					mu.Lock()
					attempts = append(attempts, account)
					mu.Unlock()
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					if account == "account-a" && boundary == "before_output" {
						w.(http.Flusher).Flush()
						return
					}
					item := map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call_fixture", "name": "record_nonce", "arguments": `{"nonce":"fixture"}`, "status": "completed"}
					emit := func(event map[string]any) {
						body, err := json.Marshal(event)
						if err != nil {
							t.Error(err)
							return
						}
						_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
					}
					emit(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "model": model}})
					emit(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call_fixture", "name": "record_nonce", "arguments": "", "status": "in_progress"}})
					if account == "account-a" && boundary == "partial_arguments" {
						emit(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_fixture", "delta": `{"nonce":`})
						w.(http.Flusher).Flush()
						return
					}
					emit(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_fixture", "delta": item["arguments"]})
					emit(map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "item_id": "fc_fixture", "arguments": item["arguments"]})
					emit(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
					if account != "account-a" || boundary == "completed_response" {
						emit(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "model": model, "status": "completed", "output": []any{item}}})
					}
					w.(http.Flusher).Flush()
				}))
				defer server.Close()

				cfg := &config.Config{}
				cfg.Codex.StreamBootstrapBuffering = buffering
				manager := cliproxyauth.NewManager(nil, &cliproxyauth.RoundRobinSelector{}, nil)
				manager.SetRetryConfig(0, 0, 2)
				manager.SetConfig(cfg)
				manager.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
				for _, route := range []struct{ id, key, priority string }{
					{"codex-tool-boundary-a", "account-a", "100"},
					{"codex-tool-boundary-b", "account-b", "0"},
				} {
					registry.GetGlobalRegistry().RegisterClient(route.id, "codex", []*registry.ModelInfo{{ID: model}})
					t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(route.id) })
					if _, err := manager.Register(context.Background(), &cliproxyauth.Auth{
						ID: route.id, Provider: "codex", Status: cliproxyauth.StatusActive,
						Attributes: map[string]string{"priority": route.priority, "base_url": server.URL, "api_key": route.key},
						Metadata:   map[string]any{"disable_cooling": true},
					}); err != nil {
						t.Fatal(err)
					}
				}
				req := cliproxyexecutor.Request{Model: model, Payload: []byte(fmt.Sprintf(`{"model":%q,"input":"Record the fixture nonce once.","tools":[{"type":"function","name":"record_nonce","parameters":{"type":"object","properties":{"nonce":{"type":"string"}},"required":["nonce"],"additionalProperties":false}}],"stream":true}`, model))}
				opts := cliproxyexecutor.Options{Stream: true, SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: req.Payload}
				output, err := manager.ExecuteStream(context.Background(), []string{"codex"}, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				var terminalErr error
				var body strings.Builder
				for chunk := range output.Chunks {
					if chunk.Err != nil {
						terminalErr = chunk.Err
					}
					body.Write(chunk.Payload)
					// SDK chunks are complete records; the HTTP handler adds framing.
					if len(chunk.Payload) != 0 {
						body.WriteByte('\n')
					}
				}
				completed := 0
				dispatches := 0
				var recorded []string
				for _, line := range strings.Split(body.String(), "\n") {
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					var event struct {
						Type string `json:"type"`
						Item struct {
							Type, ID, Name, Arguments string
							CallID                    string `json:"call_id"`
						} `json:"item"`
					}
					if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
						t.Fatalf("invalid downstream SSE data: %s: %v", line, err)
					}
					if event.Type == "response.completed" {
						completed++
					}
					if event.Type == "response.output_item.done" && event.Item.Type == "function_call" {
						if event.Item.ID != "fc_fixture" || event.Item.CallID != "call_fixture" || event.Item.Name != "record_nonce" {
							t.Fatalf("tool identity changed: %+v", event.Item)
						}
						var arguments map[string]any
						if err := json.Unmarshal([]byte(event.Item.Arguments), &arguments); err != nil || len(arguments) != 1 || arguments["nonce"] != "fixture" {
							t.Fatalf("invalid arguments reached the fixture dispatcher: %s", event.Item.Arguments)
						}
						// This fake client executes only validated complete call items.
						dispatches++
						recorded = append(recorded, arguments["nonce"].(string))
					}
				}
				wantDispatches := 1
				if boundary == "partial_arguments" {
					wantDispatches = 0
				}
				wantCompleted := 0
				if boundary == "before_output" || boundary == "completed_response" {
					wantCompleted = 1
				}
				if dispatches != wantDispatches || len(recorded) != wantDispatches || completed != wantCompleted || (terminalErr == nil) != (wantCompleted == 1) {
					t.Fatalf("dispatches/records/completed/error = %d/%v/%d/%v, want %d/%d/%d", dispatches, recorded, completed, terminalErr, wantDispatches, wantDispatches, wantCompleted)
				}
				mu.Lock()
				gotAttempts := append([]string(nil), attempts...)
				mu.Unlock()
				wantAttempts := "account-a"
				if boundary == "before_output" {
					wantAttempts = "account-a,account-b"
				}
				if strings.Join(gotAttempts, ",") != wantAttempts {
					t.Fatalf("attempts = %v, want %s", gotAttempts, wantAttempts)
				}
			})
		}
	}
}
