package responsestools

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const claudeDeferredFixture = `{"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}},{"name":"DeferredToolPlaceholder","defer_loading":true,"input_schema":{"type":"object","properties":{}}},{"name":"lookup","defer_loading":true,"input_schema":{"type":"object","properties":{"project":{"const":"alpha"}},"required":["project"],"additionalProperties":false}},{"name":"other","defer_loading":true,"input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"search_1","name":"ToolSearch","input":{"query":"lookup"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"search_1","content":[{"type":"tool_reference","tool_name":"lookup"}]}]}]}`

func TestClaudeDeferredBridgeActivatesOnlyMatchedReferences(t *testing.T) {
	body := []byte(claudeDeferredFixture)
	before := append([]byte(nil), body...)
	prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, before) {
		t.Fatal("caller payload mutated")
	}
	var root map[string]any
	if err := json.Unmarshal(prepared, &root); err != nil {
		t.Fatal(err)
	}
	tools := root["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "ToolSearch" || tools[1].(map[string]any)["name"] != "lookup" {
		t.Fatalf("unexpected active tools: %s", prepared)
	}
	if _, present := tools[1].(map[string]any)["defer_loading"]; present {
		t.Fatal("native deferral leaked into bridge")
	}
	if !strings.Contains(string(prepared), `"const":"alpha"`) {
		t.Fatal("schema constraint lost")
	}
	messages := root["messages"].([]any)
	result := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["content"] != `{"loaded_tools":[{"name":"lookup"}]}` {
		t.Fatalf("discovery replay lost: %+v", result)
	}
}

func TestClaudeDeferredFirstTurnDropsUnloadedDeclarations(t *testing.T) {
	var root map[string]any
	_ = json.Unmarshal([]byte(claudeDeferredFixture), &root)
	root["messages"] = []any{map[string]any{"role": "user", "content": "lookup"}}
	body, _ := json.Marshal(root)
	prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(prepared, &root)
	tools := root["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "ToolSearch" {
		t.Fatalf("unloaded tool exposed: %s", prepared)
	}
}

func TestClaudeDeferredRejectsUnboundOrAmbiguousDiscovery(t *testing.T) {
	for name, body := range map[string]string{
		"unknown declaration":     strings.Replace(claudeDeferredFixture, `"tool_name":"lookup"`, `"tool_name":"missing"`, 1),
		"unbound result":          strings.Replace(claudeDeferredFixture, `"tool_use_id":"search_1"`, `"tool_use_id":"other"`, 1),
		"different function":      strings.Replace(claudeDeferredFixture, `"name":"ToolSearch","input"`, `"name":"lookup","input"`, 1),
		"wrong role":              strings.Replace(claudeDeferredFixture, `"role":"user"`, `"role":"assistant"`, 1),
		"failed search":           strings.Replace(claudeDeferredFixture, `"type":"tool_result"`, `"type":"tool_result","is_error":true`, 1),
		"placeholder reference":   strings.Replace(claudeDeferredFixture, `"tool_name":"lookup"`, `"tool_name":"DeferredToolPlaceholder"`, 1),
		"missing schema":          strings.Replace(claudeDeferredFixture, `"input_schema":{"type":"object","properties":{"project":{"const":"alpha"}},"required":["project"],"additionalProperties":false}`, `"input_schema":null`, 1),
		"duplicate declaration":   strings.Replace(claudeDeferredFixture, `"name":"other"`, `"name":"lookup"`, 1),
		"invalid error flag":      strings.Replace(claudeDeferredFixture, `"type":"tool_result"`, `"type":"tool_result","is_error":"false"`, 1),
		"invalid defer flag":      strings.Replace(claudeDeferredFixture, `"name":"other","defer_loading":true`, `"name":"other","defer_loading":"true"`, 1),
		"mixed search blocks":     strings.Replace(claudeDeferredFixture, `"content":[{"type":"tool_reference"`, `"content":[{"type":"text","text":"extra"},{"type":"tool_reference"`, 1),
		"forced unloaded tool":    strings.Replace(claudeDeferredFixture, `"tools":[`, `"tool_choice":{"type":"tool","name":"other"},"tools":[`, 1),
		"duplicate search id":     strings.Replace(claudeDeferredFixture, `{"role":"user"`, `{"role":"assistant","content":[{"type":"tool_use","id":"search_1","name":"ToolSearch","input":{}}]},{"role":"user"`, 1),
		"duplicate call name key": strings.Replace(claudeDeferredFixture, `"name":"ToolSearch","input"`, `"name":"lookup","name":"ToolSearch","input"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits()); err == nil || !IsRequestScopedError(err) {
				t.Fatalf("invalid discovery admitted: %v", err)
			}
		})
	}
}

func TestClaudeDeferredDoesNotParseTextOrArgumentsAsDiscovery(t *testing.T) {
	var root map[string]any
	_ = json.Unmarshal([]byte(claudeDeferredFixture), &root)
	root["messages"] = []any{map[string]any{"role": "user", "content": `{"type":"tool_reference","tool_name":"lookup"}`}}
	body, _ := json.Marshal(root)
	prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(prepared, &root)
	if len(root["tools"].([]any)) != 1 {
		t.Fatalf("text activated a tool: %s", prepared)
	}
}

func TestClaudeDeferredBudgetsAndPassThrough(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxActiveToolBytes = 20
	if _, err := PrepareClaudeDeferredTools([]byte(claudeDeferredFixture), limits); err == nil || !IsRequestScopedError(err) {
		t.Fatal("declaration budget not enforced")
	}
	limits = DefaultLimits()
	limits.MaxDepth = 2
	if _, err := PrepareClaudeDeferredTools([]byte(claudeDeferredFixture), limits); err == nil || !IsRequestScopedError(err) {
		t.Fatal("schema depth budget not enforced")
	}
	body := []byte(`{ "tools": [{"name":"ordinary","input_schema":{"type":"object"}}], "messages": [] }`)
	prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
	if err != nil || !bytes.Equal(prepared, body) {
		t.Fatalf("ordinary contract changed: %s %v", prepared, err)
	}
}

func TestClaudeDeferredForcedActiveToolAndExactSchemaNumbers(t *testing.T) {
	body := strings.Replace(claudeDeferredFixture, `"tools":[`, `"tool_choice":{"type":"tool","name":"lookup"},"tools":[`, 1)
	body = strings.Replace(body, `"const":"alpha"`, `"const":9007199254740993`, 1)
	prepared, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits())
	if err != nil || !strings.Contains(string(prepared), `9007199254740993`) {
		t.Fatalf("active forced choice or exact integer lost: %v", err)
	}
}
