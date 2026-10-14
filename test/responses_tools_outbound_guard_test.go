package test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestNativeMultipleOutputGuardPreservesPreparedHistory(t *testing.T) {
	body := []byte(`{"model":"review-model","tools":[{"type":"custom","name":"exec"},{"type":"function","name":"ping","parameters":{"type":"object"}}],"input":[{"type":"custom_tool_call","id":"ctc_1","call_id":"c1","name":"exec","input":"notify('notice')"},{"type":"custom_tool_call_output","id":"ctco_1","call_id":"c1","output":"notice"},{"type":"custom_tool_call","id":"ctc_2","call_id":"c2","name":"exec","input":"text('side')"},{"type":"custom_tool_call_output","id":"ctco_2","call_id":"c2","output":"side"},{"type":"custom_tool_call_output","id":"ctco_3","call_id":"c1","output":[{"type":"input_text","text":"final"},9007199254740993]},{"type":"function_call","id":"fc_3","call_id":"c3","name":"ping","arguments":""},{"type":"function_call_output","id":"fco_4","call_id":"c3","output":"pong"}]}`)
	for _, mutation := range []string{"none", "drop", "duplicate output", "duplicate call", "reorder", "payload", "numeric payload", "result metadata", "item identity", "call identity", "call arguments", "missing call arguments", "structured call arguments", "unrelated call arguments"} {
		for _, stream := range []bool{false, true} {
			name := mutation + "/execute"
			if stream {
				name = mutation + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				server, received := newResponsesToolsProviderServer(t, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_native\",\"status\":\"completed\",\"output\":[]}}\n\n")
				policy := responsestools.RoutePolicy{
					Match:        responsestools.RouteMatch{Provider: "codex", AuthKind: "api-key", UpstreamModel: "review-model", UpstreamFormat: "codex", BaseURL: server.URL},
					ClientSearch: responsestools.ClientSearchNative, CustomTools: responsestools.CustomToolsFunction,
					CustomOutputReplay: responsestools.CustomOutputReplayMultiple,
				}
				cfg := &config.Config{}
				cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{
					Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "api-key", UpstreamModel: "review-model", UpstreamFormat: "codex", BaseURL: server.URL},
					ClientSearch: "native", CustomTools: "function", CustomOutputReplay: "multiple",
				}}
				if mutation != "none" {
					limits := responsestools.DefaultLimits()
					prepared, err := responsestools.Prepare(body, policy, limits, responsestools.NewLimiter(limits))
					if err != nil {
						t.Fatal(err)
					}
					prepared.Attempt.Close()
					decoder := json.NewDecoder(strings.NewReader(string(prepared.Body)))
					decoder.UseNumber()
					var root map[string]any
					if err := decoder.Decode(&root); err != nil {
						t.Fatal(err)
					}
					items := root["input"].([]any)
					switch mutation {
					case "drop":
						items = items[:4]
					case "duplicate output":
						items = append(items, items[4])
					case "duplicate call":
						items = append(items, items[0])
					case "reorder":
						items[1], items[4] = items[4], items[1]
					case "payload":
						items[1].(map[string]any)["output"] = "altered result"
					case "numeric payload":
						items[4].(map[string]any)["output"].([]any)[1] = json.Number("9007199254740992")
					case "result metadata":
						items[4].(map[string]any)["status"] = "incomplete"
					case "item identity":
						items[4].(map[string]any)["id"] = "fco_changed"
					case "call identity":
						items[4].(map[string]any)["call_id"] = "c2"
					case "call arguments":
						items[0].(map[string]any)["arguments"] = `{"input":"text('altered execution')"}`
					case "missing call arguments":
						delete(items[0].(map[string]any), "arguments")
					case "structured call arguments":
						items[0].(map[string]any)["arguments"] = map[string]any{"input": "notify('notice')"}
					case "unrelated call arguments":
						items[5].(map[string]any)["arguments"] = `{"changed":true}`
					}
					cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: map[string]any{"input": items}}}
				}
				cfg.NormalizeResponsesToolsConfig()
				if err := cfg.ValidateResponsesToolsConfig(); err != nil {
					t.Fatal(err)
				}
				manager := auth.NewManager(nil, nil, nil)
				manager.SetConfig(cfg)
				manager.SetRetryConfig(0, 0, 0)
				manager.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
				registerResponsesToolsProvider(t, manager, "native-multiple-guard", "codex", "review-model", server.URL)
				req := executor.Request{Model: "review-model", Payload: body}
				opts := executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: body, Stream: stream}
				var err error
				if stream {
					var result *executor.StreamResult
					result, err = manager.ExecuteStream(context.Background(), []string{"codex"}, req, opts)
					if result != nil {
						for chunk := range result.Chunks {
							if chunk.Err != nil {
								err = chunk.Err
							}
						}
					}
				} else {
					_, err = manager.Execute(context.Background(), []string{"codex"}, req, opts)
				}
				if mutation == "none" {
					if err != nil || len(received.get()) == 0 {
						t.Fatalf("legitimate native multiple outputs rejected at final send: %v", err)
					}
					if arguments := gjson.GetBytes(received.get(), "input.5.arguments").String(); arguments != "{}" {
						t.Fatalf("blank function arguments were not legitimately normalized: %q", arguments)
					}
				} else {
					var rejection interface{ StatusCode() int }
					if !errors.As(err, &rejection) || rejection.StatusCode() != 422 || len(received.get()) != 0 {
						t.Fatalf("mutated history dispatched or incorrectly rejected: %v", err)
					}
				}
			})
		}
	}
}

func TestResponsesToolsNativeDiscoveryCustomLoop(t *testing.T) {
	alias := responsestools.CustomAliasFor("", "apply_patch")
	server, received := newResponsesToolsProviderServer(t, `data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","id":"fc_patch","name":"`+alias+`","call_id":"p1","arguments":"{\"input\":\"patch text\"}"}]}}`+"\n\n")
	cfg := &config.Config{}
	cfg.NormalizeResponsesToolsConfig()
	mgr := auth.NewManager(nil, nil, nil)
	mgr.RegisterExecutor(runtimeexecutor.NewMetaExecutor(cfg))
	mgr.SetConfig(cfg)
	mgr.SetRetryConfig(0, 0, 0)
	registerResponsesToolsProvider(t, mgr, "native-meta-loop", "meta", "review-model", server.URL)
	body := []byte(`{"model":"review-model","tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"tool_search_call","id":"fc_search","call_id":"s1","arguments":{"query":"patch"}},{"type":"tool_search_output","id":"fco_search","call_id":"s1","tools":[{"type":"custom","name":"apply_patch","format":{"type":"text"}}]}]}`)
	response, err := mgr.Execute(context.Background(), []string{"meta"}, executor.Request{Model: "review-model", Payload: body},
		executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: body})
	if err != nil {
		t.Fatal(err)
	}
	wire := received.get()
	if gjson.GetBytes(wire, "input.1.tools.0.name").String() != alias ||
		gjson.GetBytes(wire, "input.1.tools.0.type").String() != "function" {
		t.Fatalf("native discovery did not send a portable custom declaration: %s", wire)
	}
	if gjson.GetBytes(wire, "input.0.id").String() != "tsc_search" ||
		gjson.GetBytes(wire, "input.1.id").String() != "tso_search" {
		t.Fatalf("native search history was not repaired before adaptation: %s", wire)
	}
	call := gjson.GetBytes(response.Payload, "output.0")
	if call.Get("type").String() != "custom_tool_call" || call.Get("id").String() != "ctc_patch" ||
		call.Get("name").String() != "apply_patch" || call.Get("input").String() != "patch text" ||
		call.Get("call_id").String() != "p1" {
		t.Fatalf("native discovery response lost custom identity: %s", response.Payload)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	var replayCall map[string]any
	if err := json.Unmarshal([]byte(call.Raw), &replayCall); err != nil {
		t.Fatal(err)
	}
	root["input"] = append(root["input"].([]any), replayCall, map[string]any{
		"type": "custom_tool_call_output", "id": "ctco_result", "call_id": "p1", "output": "applied",
	})
	followup, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Execute(context.Background(), []string{"meta"}, executor.Request{Model: "review-model", Payload: followup},
		executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: followup}); err != nil {
		t.Fatal(err)
	}
	wire = received.get()
	if gjson.GetBytes(wire, "input.2.name").String() != alias ||
		gjson.GetBytes(wire, "input.2.id").String() != "fc_patch" ||
		gjson.GetBytes(wire, "input.3.id").String() != "fco_result" ||
		gjson.GetBytes(wire, "input.3.call_id").String() != "p1" {
		t.Fatalf("custom result replay lost the native discovery contract: %s", wire)
	}
}

func TestResponsesToolsPostRuleGuardCoverage(t *testing.T) {
	for _, provider := range []string{"xai", "meta", "codex"} {
		for _, stream := range []bool{false, true} {
			name := provider + "/execute"
			if stream {
				name = provider + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				server, received := newResponsesToolsProviderServer(t, responsesToolsCodexStream)
				cfg := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: map[string]any{"tools": []any{}}}}}}
				if provider == "codex" {
					cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{Match: config.ResponsesToolsMatch{Provider: "codex", AuthKind: "api-key", UpstreamModel: "review-model", UpstreamFormat: "codex", BaseURL: server.URL}, ClientSearch: "bridge", CustomTools: "function"}}
				}
				cfg.NormalizeResponsesToolsConfig()
				if err := cfg.ValidateResponsesToolsConfig(); err != nil {
					t.Fatal(err)
				}
				mgr := auth.NewManager(nil, nil, nil)
				if provider == "xai" {
					mgr.RegisterExecutor(runtimeexecutor.NewXAIExecutor(cfg))
				} else if provider == "meta" {
					mgr.RegisterExecutor(runtimeexecutor.NewMetaExecutor(cfg))
				} else {
					mgr.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
				}
				mgr.SetRetryConfig(0, 0, 0)
				mgr.SetConfig(cfg)
				registerResponsesToolsProvider(t, mgr, "strict-review-"+provider, provider, "review-model", server.URL)
				body := []byte(`{"model":"review-model","tools":[{"type":"tool_search","execution":"client"},{"type":"custom","name":"apply_patch"}],"input":[]}`)
				req := executor.Request{Model: "review-model", Payload: body}
				opts := executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: body, Stream: stream}
				var err error
				if stream {
					var result *executor.StreamResult
					result, err = mgr.ExecuteStream(context.Background(), []string{provider}, req, opts)
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
				if len(received.get()) > 0 {
					t.Fatalf("post rule removed negotiated tools but request was sent: err=%v body=%s", err, received.get())
				}
				var localError interface{ StatusCode() int }
				if !errors.As(err, &localError) || localError.StatusCode() != 422 {
					t.Fatalf("expected local 422 outbound contract error: %v", err)
				}
			})
		}
	}
}

func TestResponsesToolsNativeDiscoveryPostRuleGuard(t *testing.T) {
	for _, discovered := range []struct {
		name string
		tool string
	}{
		{"function", `{"type":"function","name":"read","parameters":{"type":"object"}}`},
		{"namespace", `{"type":"namespace","name":"fs","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`},
	} {
		for _, stream := range []bool{false, true} {
			name := discovered.name + "/execute"
			if stream {
				name = discovered.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				server, received := newResponsesToolsProviderServer(t, responsesToolsCodexStream)
				alias := responsestools.CustomAliasFor("", "apply_patch")
				cfg := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{
					Models: []config.PayloadModelRule{{Name: "review-model"}},
					// Discovery is now also activated in top-level tools. Remove
					// both copies to test loss of the negotiated declaration.
					Params: map[string]any{"tools": []any{
						map[string]any{"type": "tool_search", "execution": "client"},
						map[string]any{"type": "function", "name": alias, "parameters": map[string]any{"type": "object"}},
					}, "input.1.tools": []any{map[string]any{
						"type": "function", "name": alias,
						"parameters": map[string]any{"type": "object"},
					}}},
				}}}}
				cfg.NormalizeResponsesToolsConfig()
				if err := cfg.ValidateResponsesToolsConfig(); err != nil {
					t.Fatal(err)
				}
				mgr := auth.NewManager(nil, nil, nil)
				mgr.RegisterExecutor(runtimeexecutor.NewMetaExecutor(cfg))
				mgr.SetConfig(cfg)
				mgr.SetRetryConfig(0, 0, 0)
				registerResponsesToolsProvider(t, mgr, "native-discovery-guard", "meta", "review-model", server.URL)
				body := []byte(`{"model":"review-model","tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"tool_search_call","id":"tsc_s","call_id":"s1","arguments":{"query":"tools"}},{"type":"tool_search_output","id":"tso_s","call_id":"s1","tools":[` +
					discovered.tool + `,{"type":"custom","name":"apply_patch","format":{"type":"text"}}]}]}`)
				req := executor.Request{Model: "review-model", Payload: body}
				opts := executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: body, Stream: stream}
				var err error
				if stream {
					var result *executor.StreamResult
					result, err = mgr.ExecuteStream(context.Background(), []string{"meta"}, req, opts)
					if result != nil {
						for chunk := range result.Chunks {
							if chunk.Err != nil {
								err = chunk.Err
							}
						}
					}
				} else {
					_, err = mgr.Execute(context.Background(), []string{"meta"}, req, opts)
				}
				if len(received.get()) > 0 {
					t.Fatalf("post rule deleted a discovered function but request was sent: err=%v body=%s", err, received.get())
				}
				var compat interface {
					error
					StatusCode() int
					IsRequestScoped() bool
				}
				if !errors.As(err, &compat) || compat.StatusCode() != 422 ||
					!compat.IsRequestScoped() ||
					!strings.Contains(compat.Error(), "removed required function tool") {
					t.Fatalf("expected local 422 for a missing discovered function: %v", err)
				}
			})
		}
	}
}
