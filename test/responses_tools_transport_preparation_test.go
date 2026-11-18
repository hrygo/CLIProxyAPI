package test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	openaihandlers "github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
)

// Both transports cross the real Manager, executor, translator and outbound
// guard before the mock upstream captures the resulting declarations.
func TestResponsesToolsHTTPAndWebsocketUseSamePreparation(t *testing.T) {
	const recursive = `{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}`
	for _, tc := range []struct {
		name, declarations, custom, localRefs string
		search, replay                        bool
	}{
		{"ordinary_recursive", `"tools":[{"type":"function","name":"lookup","parameters":` + recursive + `}],"input":[]`, "inherit", "flatten", false, true},
		{"lite_eager", `"input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]`, "function", "preserve", false, true},
		{"lite_response_only", `"input":[{"type":"additional_tools","tools":[{"type":"tool_search"}]}]`, "inherit", "preserve", true, true},
		{"native_search", `"tools":[{"type":"tool_search"}],"input":[]`, "inherit", "preserve", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := "transport-preparation-" + tc.name
			h, _ := newResponsesToolsIntegrationHarnessWithExpectations(t, model,
				[]string{responsesToolsSSECompletedMessage("done"), responsesToolsSSECompletedMessage("done")}, "inherit", []bool{!tc.search, !tc.search})
			cfg := &config.Config{}
			cfg.CodexResponseSteering = true
			cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{
				Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: model, UpstreamFormat: "codex"},
				ClientSearch: "native", CustomTools: tc.custom, Schema: config.ResponsesToolsSchema{LocalRefs: tc.localRefs},
			}}
			cfg.NormalizeResponsesToolsConfig()
			if err := cfg.ValidateResponsesToolsConfig(); err != nil {
				t.Fatal(err)
			}
			h.manager.SetConfig(cfg)
			h.manager.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
			h.handler = openaihandlers.NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, h.manager))
			httpBody := fmt.Sprintf(`{"model":%q,%s}`, model, tc.declarations)
			recorder := h.postResponses(t, httpBody)
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "done") {
				t.Fatalf("HTTP status=%d response=%s", recorder.Code, recorder.Body.String())
			}
			conn := openResponsesIntegrationWebsocket(t, h.handler)
			events := postResponsesIntegrationWebsocketTurn(t, conn, fmt.Sprintf(`{"type":"response.create","model":%q,%s}`, model, tc.declarations))
			if completed := completedWebsocketEvent(t, events); !strings.Contains(fmt.Sprint(completed), "done") {
				t.Fatalf("WS completion missing result: %v", completed)
			}
			h.mu.Lock()
			bodies := append([]string(nil), h.wireBodies...)
			h.mu.Unlock()
			if len(bodies) != 2 {
				t.Fatalf("dispatches=%d want 2", len(bodies))
			}
			var httpWire, wsWire map[string]any
			if err := json.Unmarshal([]byte(bodies[0]), &httpWire); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(bodies[1]), &wsWire); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(httpWire["tools"], wsWire["tools"]) || !reflect.DeepEqual(httpWire["input"], wsWire["input"]) {
				t.Fatalf("HTTP/WS declaration or history mismatch: HTTP=%s WS=%s", bodies[0], bodies[1])
			}
			if tc.name == "ordinary_recursive" {
				tools := httpWire["tools"].([]any)
				schema := tools[0].(map[string]any)["parameters"].(map[string]any)
				node := schema["$defs"].(map[string]any)["Node"].(map[string]any)
				next := node["properties"].(map[string]any)["next"].(map[string]any)
				if _, exists := next["$ref"]; exists {
					t.Fatal("recursive edge reached the upstream")
				}
			}
			if tc.name == "lite_eager" {
				functions := 0
				for _, raw := range httpWire["tools"].([]any) {
					tool := raw.(map[string]any)
					if tool["type"] == "function" {
						functions++
						if tool["name"] != "lookup" {
							t.Fatalf("unexpected function identity: %v", tool)
						}
					}
				}
				if functions != 1 || strings.Contains(bodies[0], "additional_tools") {
					t.Fatalf("Lite eager declaration was not lifted: %s", bodies[0])
				}
			}
			if tc.replay {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer","input":"continue"}`)); err != nil {
					t.Fatal(err)
				}
				_, payload, err := conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				var rejected map[string]any
				if err := json.Unmarshal(payload, &rejected); err != nil || rejected["type"] != "error" || !strings.Contains(string(payload), "live steering is not supported") {
					t.Fatalf("replay steering rejection=%s err=%v", payload, err)
				}
				if got := h.nextTurn.Load(); got != 2 {
					t.Fatalf("rejected steering dispatched upstream: %d", got)
				}
			}
		})
	}
}
