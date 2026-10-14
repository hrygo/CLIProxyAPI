package test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

const claudeDeferredGuardRequest = `{"model":"review-model","max_tokens":128,"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","defer_loading":true,"input_schema":{"type":"object"}},{"name":"lookup","defer_loading":true,"input_schema":{"type":"object","properties":{}}},{"name":"unloaded","defer_loading":true,"input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"ToolSearch","id":"search_1","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"search_1","content":[{"type":"tool_reference","tool_name":"lookup"}]}]}]}`

func TestClaudeDeferredPostRuleGuard(t *testing.T) {
	for _, provider := range []string{"codex", "fixture-chat", "meta", "xai", "gemini"} {
		for _, stream := range []bool{false, true} {
			for _, mutation := range []string{"none", "long-names", "remove-tools", "rename-active", "declaration-budget", "add-unloaded", "duplicate-active", "force-unloaded"} {
				name := provider + "/execute/" + mutation
				if stream {
					name = provider + "/stream/" + mutation
				}
				t.Run(name, func(t *testing.T) {
					response := responsesToolsCodexStream
					if provider == "fixture-chat" {
						response = `{"id":"chat_1","choices":[{"index":0,"delta":{},"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
						if stream {
							response = "data: " + response + "\n\ndata: [DONE]\n\n"
						}
					}
					if provider == "gemini" {
						response = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
						if stream {
							response = "data: " + response + "\n\n"
						}
					}
					server, received := newResponsesToolsProviderServer(t, response)
					cfg := &config.Config{}
					cfg.NormalizeResponsesToolsConfig()
					cfg.ResponsesTools.Limits.MaxActiveToolBytes = 1024
					params := map[string]any{}
					toolPath := "tools.1"
					if provider == "fixture-chat" {
						toolPath += ".function"
					}
					if provider == "gemini" {
						toolPath = "tools.0.functionDeclarations.1"
					}
					switch mutation {
					case "remove-tools":
						params["tools"] = []any{}
					case "rename-active":
						params[toolPath+".name"] = "unloaded"
					case "declaration-budget":
						params[toolPath+".description"] = strings.Repeat("x", 2048)
					case "add-unloaded", "duplicate-active":
						addedName := "unloaded"
						if mutation == "duplicate-active" {
							addedName = "lookup"
						}
						newPath := "tools.2"
						tool := map[string]any{"type": "function", "name": addedName, "parameters": map[string]any{"type": "object"}}
						if provider == "fixture-chat" {
							tool = map[string]any{"type": "function", "function": map[string]any{"name": addedName, "parameters": map[string]any{"type": "object"}}}
						}
						if provider == "gemini" {
							newPath = "tools.0.functionDeclarations.2"
							tool = map[string]any{"name": addedName, "parametersJsonSchema": map[string]any{"type": "object"}}
						}
						params[newPath] = tool
					case "force-unloaded":
						params["tool_choice"] = map[string]any{"type": "function", "name": "unloaded"}
						if provider == "fixture-chat" {
							params["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "unloaded"}}
						}
						if provider == "gemini" {
							delete(params, "tool_choice")
							params["toolConfig.functionCallingConfig.allowedFunctionNames"] = []string{"unloaded"}
						}
					}
					if len(params) > 0 {
						cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: params}}
					}
					mgr := auth.NewManager(nil, nil, nil)
					switch provider {
					case "codex":
						mgr.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
					case "meta":
						mgr.RegisterExecutor(runtimeexecutor.NewMetaExecutor(cfg))
					case "xai":
						mgr.RegisterExecutor(runtimeexecutor.NewXAIExecutor(cfg))
					case "gemini":
						mgr.RegisterExecutor(runtimeexecutor.NewGeminiExecutor(cfg))
					default:
						mgr.RegisterExecutor(runtimeexecutor.NewOpenAICompatExecutor(provider, cfg))
					}
					mgr.SetRetryConfig(0, 0, 0)
					mgr.SetConfig(cfg)
					registerResponsesToolsProvider(t, mgr, "claude-guard-"+provider, provider, "review-model", server.URL)
					body := []byte(claudeDeferredGuardRequest)
					if mutation == "long-names" {
						body = claudeDeferredLongNamesBody(t)
					}
					req := executor.Request{Model: "review-model", Payload: body}
					opts := executor.Options{SourceFormat: translator.FormatClaude, OriginalRequest: body, Stream: stream}
					var err error
					if stream {
						result, errStream := mgr.ExecuteStream(context.Background(), []string{provider}, req, opts)
						err = errStream
						if result != nil {
							for chunk := range result.Chunks {
								if chunk.Err != nil {
									err = chunk.Err
								}
							}
						}
					} else {
						_, err = mgr.Execute(context.Background(), []string{provider}, req, opts)
					}
					// xAI repairs orphaned post-rule choices to auto before the final
					// guard. That valid final request must retain the existing repair.
					repairedChoice := provider == "xai" && mutation == "force-unloaded"
					if mutation == "none" || mutation == "long-names" || repairedChoice {
						if err != nil || !gjson.GetBytes(received.get(), "tools").Exists() {
							t.Fatalf("legitimate discovery did not dispatch: %v", err)
						}
						if strings.Contains(string(received.get()), "unloaded") || strings.Contains(string(received.get()), "DeferredToolPlaceholder") {
							t.Fatal("unloaded declarations leaked into a legitimate request")
						}
						if choice := gjson.GetBytes(received.get(), "tool_choice"); repairedChoice && choice.Exists() && choice.String() != "auto" {
							t.Fatalf("orphaned choice was not repaired: %s", received.get())
						}
						if mutation == "long-names" {
							wire := gjson.ParseBytes(received.get())
							var names []string
							if provider == "gemini" {
								for _, tool := range wire.Get("tools.0.functionDeclarations").Array() {
									names = append(names, tool.Get("name").String())
								}
							} else {
								for _, tool := range wire.Get("tools").Array() {
									path := "name"
									if provider == "fixture-chat" {
										path = "function.name"
									}
									if tool.Get(path).Exists() {
										names = append(names, tool.Get(path).String())
									}
								}
							}
							if len(names) != 3 || names[1] == names[2] {
								t.Fatalf("distinct activated identities collapsed: %v", names)
							}
						}
						return
					}
					if len(received.get()) != 0 {
						t.Fatalf("corrupted deferred declarations were dispatched: mutation=%s err=%v", mutation, err)
					}
					status := 422
					if mutation == "declaration-budget" {
						status = 413
					}
					var localError interface{ StatusCode() int }
					if !errors.As(err, &localError) || localError.StatusCode() != status {
						t.Fatalf("expected local %d, got %v", status, err)
					}
				})
			}
		}
	}
}

func claudeDeferredLongNamesBody(t *testing.T) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(claudeDeferredGuardRequest), &root); err != nil {
		t.Fatal(err)
	}
	names := []string{strings.Repeat("a", 70) + "first", strings.Repeat("a", 70) + "second"}
	tools := root["tools"].([]any)
	tools[2].(map[string]any)["name"] = names[0]
	root["tools"] = append(tools, map[string]any{"name": names[1], "defer_loading": true, "input_schema": map[string]any{"type": "object"}})
	message := root["messages"].([]any)[1].(map[string]any)
	result := message["content"].([]any)[0].(map[string]any)
	result["content"] = []any{map[string]any{"type": "tool_reference", "tool_name": names[0]}, map[string]any{"type": "tool_reference", "tool_name": names[1]}}
	body, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestClaudeDeferredHostedToolChoiceGuard(t *testing.T) {
	for _, transport := range []string{"execute", "stream", "websocket-execute", "websocket-stream"} {
		stream := strings.HasSuffix(transport, "stream")
		useWebsocket := strings.HasPrefix(transport, "websocket-")
		for _, selection := range []string{"forced", "allowed-tools"} {
			for _, declaration := range []string{"missing", "client-declared", "operator-injected", "disabled-client-declared"} {
				name := transport + "/" + selection + "/" + declaration
				t.Run(name, func(t *testing.T) {
					var server *httptest.Server
					var received *responsesToolsRecorder
					var wsRequests *atomic.Int32
					if useWebsocket {
						server, received, wsRequests = newClaudeHostedWebsocketProviderServer(t)
					} else {
						server, received = newResponsesToolsProviderServer(t, responsesToolsCodexStream)
					}
					cfg := &config.Config{SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationPassthrough}}
					cfg.NormalizeResponsesToolsConfig()
					toolType := "web_search"
					body := []byte(claudeDeferredGuardRequest)
					if declaration == "client-declared" || declaration == "disabled-client-declared" {
						var root map[string]any
						if err := json.Unmarshal(body, &root); err != nil {
							t.Fatal(err)
						}
						root["tools"] = append(root["tools"].([]any), map[string]any{"type": "web_search_20250305", "name": "web_search"})
						body, _ = json.Marshal(root)
					}
					if declaration == "operator-injected" {
						cfg.DisableImageGeneration = config.DisableImageGenerationOff
						toolType = "image_generation"
					}
					choice := map[string]any{"type": toolType}
					if selection == "allowed-tools" {
						choice = map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{choice}}
					}
					params := map[string]any{"tool_choice": choice}
					if declaration == "disabled-client-declared" {
						params["tools"] = []any{
							map[string]any{"type": "function", "name": "ToolSearch", "parameters": map[string]any{"type": "object"}},
							map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}},
						}
					}
					cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: params}}
					mgr := auth.NewManager(nil, nil, nil)
					if useWebsocket {
						mgr.RegisterExecutor(runtimeexecutor.NewCodexWebsocketsExecutor(cfg))
					} else {
						mgr.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
					}
					mgr.SetRetryConfig(0, 0, 0)
					mgr.SetConfig(cfg)
					registerResponsesToolsProvider(t, mgr, "claude-hosted-guard", "codex", "review-model", server.URL)
					req := executor.Request{Model: "review-model", Payload: body}
					opts := executor.Options{SourceFormat: translator.FormatClaude, OriginalRequest: body, Stream: stream}
					var err error
					if stream {
						result, errStream := mgr.ExecuteStream(context.Background(), []string{"codex"}, req, opts)
						err = errStream
						if result != nil {
							for chunk := range result.Chunks {
								if chunk.Err != nil {
									err = chunk.Err
								}
							}
						}
					} else {
						_, err = mgr.Execute(context.Background(), []string{"codex"}, req, opts)
					}
					if declaration == "client-declared" || declaration == "operator-injected" {
						if err != nil || len(received.get()) == 0 {
							t.Fatalf("valid hosted choice did not dispatch: %v", err)
						}
						found := false
						for _, tool := range gjson.GetBytes(received.get(), "tools").Array() {
							found = found || tool.Get("type").String() == toolType
						}
						if !found {
							t.Fatalf("valid hosted selection lost its declaration: %s", received.get())
						}
						return
					}
					if len(received.get()) != 0 {
						t.Fatalf("undeclared hosted choice was dispatched: %s", received.get())
					}
					if useWebsocket && wsRequests.Load() != 0 {
						t.Fatalf("invalid hosted choice reached websocket handshake: %d", wsRequests.Load())
					}
					var localError interface{ StatusCode() int }
					if !errors.As(err, &localError) || localError.StatusCode() != 422 {
						t.Fatalf("expected local 422, got %v", err)
					}
				})
			}
		}
	}
}

func newClaudeHostedWebsocketProviderServer(t *testing.T) (*httptest.Server, *responsesToolsRecorder, *atomic.Int32) {
	t.Helper()
	received := new(responsesToolsRecorder)
	requests := new(atomic.Int32)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, payload, errRead := conn.ReadMessage()
		if errRead != nil {
			t.Errorf("read websocket request: %v", errRead)
			return
		}
		received.set(payload)
		completed := []byte(`{"type":"response.completed","response":{"id":"resp_hosted","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
		if errWrite := conn.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
			t.Errorf("write websocket completion: %v", errWrite)
		}
	}))
	t.Cleanup(server.Close)
	return server, received, requests
}
