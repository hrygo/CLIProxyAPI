package responsestools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func discoveryLifecycleBody(t *testing.T) []byte {
	t.Helper()
	declaration := func(name, description string) map[string]any {
		return map[string]any{"type": "function", "namespace": "fs", "name": name, "description": description, "parameters": map[string]any{"type": "object"}}
	}
	root := map[string]any{
		"tools": []any{map[string]any{"type": "tool_search", "execution": "client"}},
		"input": []any{
			map[string]any{"type": "tool_search_call", "call_id": "s1", "arguments": map[string]any{"query": "a"}},
			map[string]any{"type": "tool_search_output", "call_id": "s1", "tools": []any{declaration("a", strings.Repeat("large schema fixture ", 100))}},
			map[string]any{"type": "function_call", "namespace": "fs", "name": "a", "call_id": "a1", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "a1", "output": "first result"},
			map[string]any{"type": "tool_search_call", "call_id": "s2", "arguments": map[string]any{"query": "b"}},
			map[string]any{"type": "tool_search_output", "call_id": "s2", "tools": []any{declaration("b", "new discovery")}},
			map[string]any{"type": "function_call", "namespace": "fs", "name": "a", "call_id": "a2", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "a2", "output": "second result"},
		},
	}
	body, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDiscoveryLifecycleLatestSchemaPreservesHistory(t *testing.T) {
	body := []byte(`{"tools":[{"type":"tool_search"}],"input":[{"type":"tool_search_call","call_id":"s1","arguments":{}},{"type":"tool_search_output","call_id":"s1","tools":[{"type":"function","name":"a","parameters":{"type":"object","properties":{"old":{"type":"string"}}}}]},{"type":"function_call","name":"a","call_id":"a1","arguments":"{\"old\":\"historical\"}"},{"type":"function_call_output","call_id":"a1","output":"first result"},{"type":"tool_search_call","call_id":"s2","arguments":{}},{"type":"tool_search_output","call_id":"s2","tools":[{"type":"function","name":"a","parameters":{"type":"object","properties":{"new":{"type":"integer"}}}}]}]}`)
	limits := DefaultLimits()
	prepared, err := Prepare(body, RoutePolicy{ClientSearch: ClientSearchBridge}, limits, NewLimiter(limits))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil {
		t.Fatal("missing activation")
	}
	defer prepared.Attempt.Close()
	value, _ := decodeValue(prepared.Body)
	root := value.(map[string]any)
	tools := root["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("same identity activated multiple times: %s", prepared.Body)
	}
	parameters := tools[1].(map[string]any)["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	if properties["new"] == nil || properties["old"] != nil {
		t.Fatal("old schema remains active")
	}
	input := root["input"].([]any)
	historical := input[2].(map[string]any)
	if historical["arguments"] != `{"old":"historical"}` || historical["call_id"] != "a1" || input[3].(map[string]any)["output"] != "first result" {
		t.Fatal("schema update rewrote historical facts")
	}
}

func TestPrepareDiscoveredFunctionChoiceMatchesDeclaration(t *testing.T) {
	t.Parallel()
	for _, route := range []struct {
		name   string
		search ClientSearchMode
		custom CustomToolsMode
	}{
		{"portable native", ClientSearchNative, CustomToolsFunction},
		{"bridge", ClientSearchBridge, CustomToolsFunction},
		{"native passthrough", ClientSearchNative, CustomToolsInherit},
	} {
		for _, allowed := range []bool{false, true} {
			name := route.name + "/forced"
			if allowed {
				name = route.name + "/allowed"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				target := map[string]any{"type": "function", "namespace": "fs", "name": "read"}
				choice := target
				if allowed {
					choice = map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{
						target,
						map[string]any{"type": "tool_search"},
						map[string]any{"type": "function", "name": "eager"},
						map[string]any{"type": "function", "namespace": "fs", "name": "undiscovered"},
					}}
				}
				body, err := json.Marshal(map[string]any{
					"tools": []any{
						map[string]any{"type": "tool_search"},
						map[string]any{"type": "function", "name": "eager", "parameters": map[string]any{"type": "object"}},
						map[string]any{"type": "namespace", "name": "fs", "tools": []any{
							map[string]any{"type": "function", "name": "undiscovered", "defer_loading": true},
						}},
					},
					"tool_choice": choice,
					"input": []any{
						map[string]any{"type": "tool_search_call", "call_id": "s1", "arguments": map[string]any{"query": "read"}},
						map[string]any{"type": "tool_search_output", "call_id": "s1", "tools": []any{
							map[string]any{"type": "function", "namespace": "fs", "name": "read", "parameters": map[string]any{"type": "object"}},
						}},
						map[string]any{"type": "function_call", "namespace": "fs", "name": "read", "call_id": "r1", "arguments": "{}"},
						map[string]any{"type": "function_call_output", "call_id": "r1", "output": "previous result"},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				limits := DefaultLimits()
				limiter := NewLimiter(limits)
				policy := RoutePolicy{ClientSearch: route.search, CustomTools: route.custom, CustomGrammar: CustomGrammarDescribe}
				prepared, err := Prepare(body, policy, limits, limiter)
				if err != nil {
					t.Fatal(err)
				}
				if route.custom == CustomToolsInherit {
					if string(prepared.Body) != string(body) || prepared.Attempt != nil {
						t.Fatal("native declaration and choice were adapted")
					}
					return
				}
				if prepared.Attempt == nil {
					t.Fatal("missing activation attempt")
				}
				defer prepared.Attempt.Close()
				value, ok := decodeValue(prepared.Body)
				if !ok {
					t.Fatal("invalid prepared request")
				}
				root := value.(map[string]any)
				alias := prepared.Attempt.contract.AliasByID[ToolIdentity{Namespace: "fs", Name: "read", Kind: ToolKindFunction}]
				selected := root["tool_choice"].(map[string]any)
				if allowed {
					entries := selected["tools"].([]any)
					if len(entries) != 4 || selected["mode"] != "required" {
						t.Fatalf("allowed choice changed its restriction: %v", selected)
					}
					search := entries[1].(map[string]any)
					if route.search == ClientSearchNative && search["type"] != "tool_search" {
						t.Fatalf("native search choice was bridged: %v", search)
					}
					if entries[2].(map[string]any)["name"] != "eager" || entries[3].(map[string]any)["namespace"] != "fs" || entries[3].(map[string]any)["name"] != "undiscovered" {
						t.Fatal("unactivated choices were rewritten")
					}
					selected = entries[0].(map[string]any)
				}
				if alias == "" || selected["name"] != alias || selected["namespace"] != nil {
					t.Fatalf("choice does not select the active declaration %q: %v", alias, selected)
				}
				found := false
				for _, name := range toolNames(root) {
					found = found || name == alias
				}
				input := root["input"].([]any)
				if !found || input[2].(map[string]any)["name"] != alias || input[3].(map[string]any)["output"] != "previous result" {
					t.Fatal("declaration, choice and replay disagree")
				}
				response, err := json.Marshal(map[string]any{"output": []any{
					map[string]any{"type": "function_call", "name": alias, "call_id": "r2", "arguments": "{}"},
				}})
				if err != nil {
					t.Fatal(err)
				}
				restored, err := prepared.Attempt.RewriteResponse(response)
				if err != nil {
					t.Fatal(err)
				}
				value, _ = decodeValue(restored)
				call := value.(map[string]any)["output"].([]any)[0].(map[string]any)
				if call["namespace"] != "fs" || call["name"] != "read" || call["call_id"] != "r2" {
					t.Fatalf("selected tool lost client identity: %s", restored)
				}
				prepared.Attempt.Close()
				if n, b := limiter.Usage(); n != 0 || b != 0 {
					t.Fatalf("lease leaked: %d/%d", n, b)
				}
			})
		}
	}
}

func TestDiscoveryLifecycleCumulativeActivation(t *testing.T) {
	for _, search := range []ClientSearchMode{ClientSearchBridge, ClientSearchNative} {
		t.Run(string(search), func(t *testing.T) {
			body := discoveryLifecycleBody(t)
			policy := RoutePolicy{ClientSearch: search, CustomTools: CustomToolsFunction}
			limits := DefaultLimits()
			limiter := NewLimiter(limits)
			prepared, err := Prepare(body, policy, limits, limiter)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Attempt == nil {
				t.Fatal("missing activation attempt")
			}
			defer prepared.Attempt.Close()
			value, ok := decodeValue(prepared.Body)
			if !ok {
				t.Fatal("invalid prepared request")
			}
			root := value.(map[string]any)
			aliasA := prepared.Attempt.contract.AliasByID[ToolIdentity{Namespace: "fs", Name: "a", Kind: ToolKindFunction}]
			aliasB := prepared.Attempt.contract.AliasByID[ToolIdentity{Namespace: "fs", Name: "b", Kind: ToolKindFunction}]
			if aliasA == "" || aliasB == "" {
				t.Fatal("missing canonical aliases")
			}
			names := make(map[string]bool)
			for _, name := range toolNames(root) {
				names[name] = true
			}
			if !names[aliasA] || !names[aliasB] {
				t.Fatalf("cumulative activation missing: %v", names)
			}
			input := root["input"].([]any)
			if len(input) != 8 {
				t.Fatal("history was evicted")
			}
			for _, index := range []int{2, 6} {
				if stringField(input[index].(map[string]any), "name") != aliasA {
					t.Fatalf("A replay lost identity at %d: %v", index, input[index])
				}
			}
			if search == ClientSearchBridge {
				for _, index := range []int{1, 5} {
					manifest, ok := decodeValue([]byte(stringField(input[index].(map[string]any), "output")))
					if !ok {
						t.Fatal("invalid discovery manifest")
					}
					for _, raw := range manifest.(map[string]any)["tools"].([]any) {
						name := stringField(raw.(map[string]any), "name")
						if !names[name] {
							t.Fatalf("manifest advertises unavailable %q", name)
						}
					}
				}
			}
			response, err := json.Marshal(map[string]any{"output": []any{map[string]any{"type": "function_call", "name": aliasA, "call_id": "a3", "arguments": "{}"}}})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := prepared.Attempt.RewriteResponse(response)
			if err != nil {
				t.Fatal(err)
			}
			value, _ = decodeValue(restored)
			call := value.(map[string]any)["output"].([]any)[0].(map[string]any)
			if call["namespace"] != "fs" || call["name"] != "a" || call["call_id"] != "a3" {
				t.Fatalf("A identity changed: %s", restored)
			}
			fullSize, err := ActiveToolArraysBytes(root)
			if err != nil {
				t.Fatal(err)
			}
			prepared.Attempt.Close()
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("success lease leaked: %d/%d", n, b)
			}
			limits.MaxActiveToolBytes = fullSize - 500
			limiter = NewLimiter(limits)
			partial, err := Prepare(body, policy, limits, limiter)
			if partial.Attempt != nil {
				partial.Attempt.Close()
			}
			if err == nil {
				t.Fatalf("silently accepted old discovery eviction: %s", partial.Body)
			}
			var compat *ToolCompatibilityError
			if !errors.As(err, &compat) || compat.Reason != ReasonDeclarationBudget || compat.StatusCode() != 413 {
				t.Fatalf("wrong cumulative-budget failure: %v", err)
			}
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("rejected attempt leaked lease: %d/%d", n, b)
			}
		})
	}
}
