package test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestResponsesToolsLiteNativeDiscoveryExecutor(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"review-model","input":[{"type":"additional_tools","id":"decl_tools","role":"developer","tools":[{"type":"tool_search","execution":"client"},{"type":"custom","name":"apply_patch","format":{"type":"text"}}]},{"type":"tool_search_call","id":"tsc_search","call_id":"search","arguments":{"query":"budget"}},{"type":"tool_search_output","id":"tso_result","call_id":"search","tools":[{"type":"namespace","name":"mcp__bench","tools":[{"type":"function","name":"ledger_budget_lookup","parameters":{"type":"object","properties":{"project":{"type":"string"}},"required":["project"]}}]}]}]}`)
			contract := responsestools.ParseContract(body)
			alias := ""
			for _, id := range contract.Identities {
				if id.Name == "ledger_budget_lookup" {
					alias = contract.AliasByID[id]
				}
			}
			if alias == "" {
				t.Fatal("missing activation alias")
			}
			response := `data: {"type":"response.completed","response":{"id":"resp_budget","status":"completed","output":[{"type":"function_call","id":"fc_budget","call_id":"budget","name":"` + alias + `","arguments":"{\"project\":\"alpha\"}"}]}}` + "\n\n"
			server, received := newResponsesToolsProviderServer(t, response)
			cfg := &config.Config{}
			cfg.NormalizeResponsesToolsConfig()
			mgr := auth.NewManager(nil, nil, nil)
			mgr.SetConfig(cfg)
			mgr.SetRetryConfig(0, 0, 0)
			mgr.RegisterExecutor(runtimeexecutor.NewMetaExecutor(cfg))
			registerResponsesToolsProvider(t, mgr, "lite-native-"+name, "meta", "review-model", server.URL)
			req := executor.Request{Model: "review-model", Payload: body}
			opts := executor.Options{SourceFormat: translator.FormatOpenAIResponse, OriginalRequest: body, Stream: stream}
			var clientBody []byte
			if stream {
				result, err := mgr.ExecuteStream(context.Background(), []string{"meta"}, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					clientBody = append(clientBody, chunk.Payload...)
				}
			} else {
				result, err := mgr.Execute(context.Background(), []string{"meta"}, req, opts)
				if err != nil {
					t.Fatal(err)
				}
				clientBody = result.Payload
			}
			wire := received.get()
			if gjson.GetBytes(wire, "input.#").Int() != 2 || gjson.GetBytes(wire, "input.0.id").String() != "tsc_search" || gjson.GetBytes(wire, "input.1.id").String() != "tso_result" {
				t.Fatalf("Lite declaration input leaked or history changed: %s", wire)
			}
			if gjson.GetBytes(wire, "tools.#(name=="+alias+")").Get("type").String() != "function" {
				t.Fatalf("discovery missing in upstream declarations: %s", wire)
			}
			// Both buffered and SSE responses must restore the namespaced client identity.
			var outputs []map[string]any
			if stream {
				for _, frame := range splitSSEData(clientBody) {
					var v map[string]any
					if json.Unmarshal(frame, &v) == nil {
						if r, ok := v["response"].(map[string]any); ok {
							if items, ok := r["output"].([]any); ok {
								for _, raw := range items {
									if item, ok := raw.(map[string]any); ok {
										outputs = append(outputs, item)
									}
								}
							}
						}
					}
				}
			} else {
				var v struct {
					Output []map[string]any `json:"output"`
				}
				if err := json.Unmarshal(clientBody, &v); err != nil {
					t.Fatal(err)
				}
				outputs = v.Output
			}
			if len(outputs) != 1 || outputs[0]["namespace"] != "mcp__bench" || outputs[0]["name"] != "ledger_budget_lookup" || outputs[0]["call_id"] != "budget" {
				t.Fatalf("client identity lost: %s", clientBody)
			}
		})
	}
}

func splitSSEData(body []byte) [][]byte {
	var out [][]byte
	for _, line := range bytes.Split(body, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			out = append(out, bytes.TrimSpace(line[5:]))
		}
	}
	return out
}
