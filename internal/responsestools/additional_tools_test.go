package responsestools

import (
	"bytes"
	"testing"
)

func TestNativePortableAdditionalTools(t *testing.T) {
	for _, search := range []bool{false, true} {
		t.Run(map[bool]string{false: "eager", true: "search"}[search], func(t *testing.T) {
			searchTool := ""
			if search {
				searchTool = `{"type":"tool_search","execution":"client"},`
			}
			body := []byte(`{"input":[{"type":"additional_tools","id":"decl_1","role":"developer","tools":[` + searchTool + `{"type":"function","name":"lookup","parameters":{"type":"object"}}]},{"type":"message","id":"msg_1","role":"user","content":"budget"}]}`)
			prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), NewLimiter(DefaultLimits()))
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Attempt == nil {
				t.Fatal("additional declarations were not adapted")
			}
			defer prepared.Attempt.Close()
			value, _ := decodeValue(prepared.Body)
			root := value.(map[string]any)
			input := root["input"].([]any)
			if len(input) != 1 || input[0].(map[string]any)["id"] != "msg_1" {
				t.Fatalf("message identity changed: %s", prepared.Body)
			}
			tools := root["tools"].([]any)
			want := 1
			if search {
				want = 2
			}
			if len(tools) != want {
				t.Fatalf("declarations not lifted: %s", prepared.Body)
			}
		})
	}
}

func TestNativeAdditionalToolsInheritPreservesBytes(t *testing.T) {
	body := []byte(nativeLiteSearchRequest)
	prepared, err := Prepare(body, RoutePolicy{ClientSearch: ClientSearchNative}, DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt != nil {
		defer prepared.Attempt.Close()
	}
	if !bytes.Equal(body, prepared.Body) {
		t.Fatal("native inherited declaration format changed")
	}
}

func TestNativePortableAdditionalToolsMergeAndReject(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		reject      bool
	}{
		{"identical", `{"type":"function","name":"lookup","parameters":{"type":"object"}}`, false},
		{"different sibling", `{"type":"function","name":"other","parameters":{"type":"object"}}`, false},
		{"conflicting schema", `{"type":"function","name":"lookup","parameters":{"type":"string"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"namespace","name":"mcp__bench","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}],"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"mcp__bench","tools":[` + tc.extra + `]}]}]}`)
			limiter := NewLimiter(DefaultLimits())
			prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), limiter)
			if tc.reject {
				if err == nil {
					if prepared.Attempt != nil {
						prepared.Attempt.Close()
					}
					t.Fatal("conflicting schema accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Attempt == nil {
					t.Fatal("no declaration adapter")
				}
				prepared.Attempt.Close()
				if bytes.Contains(prepared.Body, []byte("additional_tools")) {
					t.Fatalf("declaration input leaked: %s", prepared.Body)
				}
			}
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("lease leaked: %d/%d", n, b)
			}
		})
	}
}

func TestNativePortableAdditionalToolsValidation(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"type":"additional_tools","tools":{}}]}`,
		`{"tools":{},"input":[{"type":"additional_tools","tools":[]}]}`,
		`{"input":[{"type":"additional_tools","tools":[3]}]}`,
		`{"previous_response_id":"resp_hidden","input":[{"type":"additional_tools","tools":[]}]}`,
	} {
		limiter := NewLimiter(DefaultLimits())
		prepared, err := Prepare([]byte(body), ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), limiter)
		if err == nil {
			if prepared.Attempt != nil {
				prepared.Attempt.Close()
			}
			t.Fatalf("invalid declaration input accepted: %s", body)
		}
		if n, b := limiter.Usage(); n != 0 || b != 0 {
			t.Fatalf("lease leaked: %d/%d", n, b)
		}
	}
}

func TestNativePortableAdditionalToolsBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxActiveToolBytes = 1
	limiter := NewLimiter(limits)
	_, err := Prepare([]byte(nativeLiteSearchRequest), ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), limits, limiter)
	if err == nil {
		t.Fatal("lifted declarations escaped the active declaration budget")
	}
	if n, b := limiter.Usage(); n != 0 || b != 0 {
		t.Fatalf("lease leaked: %d/%d", n, b)
	}
}

func TestNativePortableAdditionalToolsWithDiscovery(t *testing.T) {
	body := []byte(`{"input":[{"type":"additional_tools","tools":[{"type":"tool_search","execution":"client"}]},{"type":"tool_search_call","id":"tsc_search_1","call_id":"search_1","arguments":{"query":"budget"}},{"type":"tool_search_output","id":"tso_search_1","call_id":"search_1","tools":[{"type":"namespace","name":"mcp__bench","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]}]}`)
	prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil {
		t.Fatal("missing attempt")
	}
	defer prepared.Attempt.Close()
	v, _ := decodeValue(prepared.Body)
	root := v.(map[string]any)
	input := root["input"].([]any)
	if len(input) != 2 || stringField(input[0].(map[string]any), "id") != "tsc_search_1" || stringField(input[1].(map[string]any), "id") != "tso_search_1" {
		t.Fatalf("search history changed: %s", prepared.Body)
	}
	tools := root["tools"].([]any)
	if len(tools) != 2 || stringField(tools[1].(map[string]any), "type") != "function" {
		t.Fatalf("discovered tool missing: %s", prepared.Body)
	}
	response := frameJSON(t, map[string]any{"output": []any{map[string]any{"type": "function_call", "id": "fc_lookup", "call_id": "call_lookup", "name": tools[1].(map[string]any)["name"], "arguments": "{}"}}})
	restored, err := prepared.Attempt.RewriteResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	v, _ = decodeValue(restored)
	item := v.(map[string]any)["output"].([]any)[0].(map[string]any)
	if item["namespace"] != "mcp__bench" || item["name"] != "lookup" {
		t.Fatalf("identity lost: %s", restored)
	}
}

func TestNativePortableAdditionalToolsCompletesNamespaceDescription(t *testing.T) {
	body := []byte(`{"input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","description":"","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}]}]}`)
	prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil {
		t.Fatal("missing adapter")
	}
	defer prepared.Attempt.Close()
	v, _ := decodeValue(prepared.Body)
	tool := v.(map[string]any)["tools"].([]any)[0].(map[string]any)
	if tool["description"] != "Tools in the functions namespace." {
		t.Fatalf("empty namespace description violates portable native contract: %s", prepared.Body)
	}
}
