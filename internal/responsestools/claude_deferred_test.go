package responsestools

import (
	"bytes"
	"encoding/json"
	"errors"
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

// A client keeps the reserved placeholder after it stops declaring a search
// function, for example once every tool is loaded eagerly. There is nothing to
// bridge then, so the turn must still complete with the deferred declarations
// dropped instead of failing with 422.
func TestClaudeDeferredPlaceholderWithoutSearchDegrades(t *testing.T) {
	for name, body := range map[string]string{
		"placeholder only": `{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"say OK"}],"tools":[{"name":"Glob","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","description":"Reserved placeholder that keeps deferred tool loading active; never call this tool.","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`,
		"inactive search":  `{"model":"m","messages":[{"role":"user","content":"say OK"}],"tools":[{"name":"ToolSearch","input_schema":{"type":"object"},"defer_loading":true},{"name":"Glob","input_schema":{"type":"object","properties":{}}},{"name":"lookup","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`,
		"loaded history":   `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"ok"}]}],"tools":[{"name":"Read","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`,
		// The reserved name is dropped on identity, not on its flag.
		"placeholder not deferred": `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Glob","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","input_schema":{"type":"object","properties":{}}},{"name":"other","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits())
			if err != nil {
				t.Fatalf("placeholder-only request rejected: %v", err)
			}
			var root map[string]any
			if err := json.Unmarshal(prepared, &root); err != nil {
				t.Fatal(err)
			}
			tools := root["tools"].([]any)
			if len(tools) != 1 || tools[0].(map[string]any)["name"] == "DeferredToolPlaceholder" {
				t.Fatalf("deferred declarations survived degradation: %s", prepared)
			}
			if bytes.Contains(prepared, []byte(`"defer_loading"`)) {
				t.Fatalf("deferral flag leaked into the bridge payload: %s", prepared)
			}
		})
	}
}

// An explicit false flag is protocol structure, not deferred work: the turn
// carries nothing to bridge and must pass through unchanged.
func TestClaudeDeferredExplicitFalseFlagPassesThrough(t *testing.T) {
	body := []byte(`{"model":"m","messages":[],"tools":[{"name":"Glob","input_schema":{"type":"object"},"defer_loading":false}]}`)
	prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
	if err != nil || !bytes.Equal(prepared, body) {
		t.Fatalf("explicit false flag rewritten or rejected: %v %s", err, prepared)
	}
}

// Without a search function the bridge cannot replay a discovery round: the
// upstream would receive a tool call that no declaration admits.
func TestClaudeDeferredRejectsDiscoveryReplayWithoutSearchDeclaration(t *testing.T) {
	withoutSearch := strings.Replace(claudeDeferredFixture,
		`{"name":"ToolSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}},`, ``, 1)
	var orphan map[string]any
	_ = json.Unmarshal([]byte(withoutSearch), &orphan)
	orphan["messages"] = orphan["messages"].([]any)[1:]
	orphanBody, _ := json.Marshal(orphan)
	for name, body := range map[string]string{
		"search call":      withoutSearch,
		"orphan reference": string(orphanBody),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits()); err == nil || !IsRequestScopedError(err) {
				t.Fatalf("orphan discovery replay admitted: %v", err)
			}
		})
	}
}

// A forced choice cannot survive when its declaration is dropped.
func TestClaudeDeferredRejectsForcedToolWithoutSearchDeclaration(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool","name":"DeferredToolPlaceholder"},"tools":[{"name":"DeferredToolPlaceholder","input_schema":{"type":"object"},"defer_loading":true}]}`
	if _, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits()); err == nil || !IsRequestScopedError(err) {
		t.Fatalf("forced placeholder admitted after degradation: %v", err)
	}
}

// Only block structure is discovery. A tool argument that happens to contain
// the same member name must not turn a degradable turn into a 422.
func TestClaudeDeferredIgnoresArgumentsAsDiscovery(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"tool_reference":"x"}}]}],"tools":[{"name":"DeferredToolPlaceholder","defer_loading":true,"input_schema":{"type":"object","properties":{}}}]}`
	prepared, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits())
	if err != nil {
		t.Fatalf("tool arguments were read as discovery: %v", err)
	}
	if bytes.Contains(prepared, []byte("DeferredToolPlaceholder")) || bytes.Contains(prepared, []byte("defer_loading")) {
		t.Fatalf("deferred declarations survived: %s", prepared)
	}
	if !bytes.Contains(prepared, []byte(`"tool_reference":"x"`)) {
		t.Fatalf("caller arguments were rewritten: %s", prepared)
	}
}

func TestClaudeDeclaresSearch(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"active":              {`{"tools":[{"name":"ToolSearch","input_schema":{"type":"object"}}]}`, true},
		"not deferred":        {`{"tools":[{"name":"ToolSearch","defer_loading":false}]}`, true},
		"deferred":            {`{"tools":[{"name":"ToolSearch","defer_loading":true}]}`, false},
		"other tools":         {`{"tools":[{"name":"Glob","input_schema":{"type":"object"}}]}`, false},
		"placeholder only":    {`{"tools":[{"name":"DeferredToolPlaceholder","defer_loading":true}]}`, false},
		"no tools":            {`{"messages":[]}`, false},
		"invalid json":        {`{`, false},
		"case sensitive name": {`{"tools":[{"name":"toolsearch"}]}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ClaudeDeclaresSearch([]byte(tc.body)); got != tc.want {
				t.Fatalf("ClaudeDeclaresSearch = %v, want %v", got, tc.want)
			}
		})
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

func TestClaudeDeferredOrdinaryProtocolWordsPassThrough(t *testing.T) {
	for name, body := range map[string]string{
		"string reference": ` {"messages":[{"role":"user","content":"tool_reference"}]} `,
		"string flag":      `{"messages":[{"role":"user","content":"defer_loading"}]}`,
		"text block":       `{"tools":[{"name":"ordinary","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":[{"type":"text","text":"tool_reference"}]}]}`,
		"tool name":        `{"tools":[{"name":"tool_reference","input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"ordinary_1","name":"tool_reference","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"ordinary_1","content":"done"}]}]}`,
		"tool input":       `{"tools":[{"name":"ordinary","input_schema":{"type":"object"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"ordinary_1","name":"ordinary","input":{"type":"tool_reference","defer_loading":true}}]}]}`,
		"result text":      `{"tools":[{"name":"ordinary","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"ordinary_1","content":[{"type":"text","text":"tool_reference"}]}]}]}`,
		"schema metadata":  `{"tools":[{"name":"ordinary","input_schema":{"type":"object","properties":{"defer_loading":{"type":"boolean"}},"const":"tool_reference"}}],"messages":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			original := []byte(body)
			prepared, err := PrepareClaudeDeferredTools(original, DefaultLimits())
			if err != nil || !bytes.Equal(prepared, original) || string(original) != body {
				t.Fatalf("ordinary request changed or rejected: %s %v", prepared, err)
			}
		})
	}
}

func TestClaudeDeferredReferencesWithoutFlagsStillValidated(t *testing.T) {
	body := strings.ReplaceAll(claudeDeferredFixture, `,"defer_loading":true`, "")
	prepared, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits())
	if err != nil || bytes.Contains(prepared, []byte(`"type":"tool_reference"`)) {
		t.Fatalf("reference-only discovery was not bridged: %s %v", prepared, err)
	}
	for name, invalid := range map[string]string{
		"missing declarations":   `{"messages":[{"role":"user","content":[{"type":"tool_reference","tool_name":"lookup"}]}]}`,
		"duplicate block type":   strings.Replace(body, `"type":"tool_reference"`, `"type":"tool_reference","type":"text"`, 1),
		"duplicate result type":  strings.Replace(body, `"type":"tool_result"`, `"type":"tool_result","type":"text"`, 1),
		"duplicate content":      strings.Replace(body, `"content":[{"type":"tool_reference","tool_name":"lookup"}]`, `"content":[{"type":"tool_reference","tool_name":"lookup"}],"content":"done"`, 1),
		"duplicate root history": strings.TrimSuffix(body, "}") + `,"messages":[]}`,
		"hidden deferral flag":   `{"tools":[{"name":"ordinary","defer_loading":true,"defer_loading":false,"input_schema":{"type":"object"}}],"messages":[]}`,
		"hidden deferral tools":  `{"tools":[{"name":"ordinary","defer_loading":true,"input_schema":{"type":"object"}}],"tools":[],"messages":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(invalid)) || invalid == body {
				t.Fatal("invalid discovery fixture did not encode the intended mutation")
			}
			if _, err := PrepareClaudeDeferredTools([]byte(invalid), DefaultLimits()); err == nil || !IsRequestScopedError(err) {
				t.Fatalf("invalid reference-only discovery admitted: %v", err)
			}
		})
	}
}

func TestClaudeDeferredEscapedProtocolMarkers(t *testing.T) {
	for name, body := range map[string]string{
		"escaped flag":      `{"tools":[{"name":"ToolSearch","input_schema":{"type":"object"}},{"name":"lookup","defer_\u006coading":true,"input_schema":{"type":"object"}}],"messages":[]}`,
		"escaped reference": strings.ReplaceAll(strings.ReplaceAll(claudeDeferredFixture, `,"defer_loading":true`, ""), `tool_reference`, `tool_refer\u0065nce`),
	} {
		t.Run(name, func(t *testing.T) {
			prepared, err := PrepareClaudeDeferredTools([]byte(body), DefaultLimits())
			if err != nil || bytes.Equal(prepared, []byte(body)) || bytes.Contains(prepared, []byte(`defer_\u006coading`)) || bytes.Contains(prepared, []byte(`tool_refer\u0065nce`)) ||
				bytes.Contains(prepared, []byte(`"defer_loading"`)) || bytes.Contains(prepared, []byte(`"tool_reference"`)) {
				t.Fatalf("escaped discovery state was not bridged: %s %v", prepared, err)
			}
		})
	}
}

func TestClaudeDeferredRejectsLossyUnicode(t *testing.T) {
	for name, replacement := range map[string]string{
		"invalid UTF-8":        "look" + string([]byte{0xff}) + "up",
		"unpaired high UTF-16": `look\ud800up`,
		"unpaired low UTF-16":  `look\udc00up`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(strings.ReplaceAll(claudeDeferredFixture, "lookup", replacement))
			original := append([]byte(nil), body...)
			prepared, err := PrepareClaudeDeferredTools(body, DefaultLimits())
			var compat *ToolCompatibilityError
			if !errors.As(err, &compat) || compat.Reason != ReasonJSONSyntax || compat.StatusCode() != 400 || prepared != nil {
				t.Fatalf("lossy tool identity admitted: %s %v", prepared, err)
			}
			if !bytes.Equal(body, original) {
				t.Fatal("caller payload mutated")
			}
		})
	}
	for name, value := range map[string]string{
		"valid surrogate pair": `\ud83d\ude80`,
		"escaped literal":      `\\ud800`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(strings.Replace(claudeDeferredFixture, `"const":"alpha"`, `"const":"`+value+`"`, 1))
			if _, err := PrepareClaudeDeferredTools(body, DefaultLimits()); err != nil {
				t.Fatalf("lossless Unicode rejected: %v", err)
			}
		})
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

func TestClaudeDeferredBudgetCountsOnlyActiveDeclarations(t *testing.T) {
	for _, discovered := range []bool{false, true} {
		name := "first turn"
		if discovered {
			name = "discovered lookup"
		}
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(claudeDeferredFixture), &root); err != nil {
				t.Fatal(err)
			}
			tools := root["tools"].([]any)
			tools[3].(map[string]any)["description"] = strings.Repeat("x", DefaultLimits().MaxActiveToolBytes)
			wantTools := []any{tools[0]}
			if discovered {
				lookup := make(map[string]any)
				for key, value := range tools[2].(map[string]any) {
					if key != "defer_loading" {
						lookup[key] = value
					}
				}
				wantTools = append(wantTools, lookup)
			} else {
				root["messages"] = []any{map[string]any{"role": "user", "content": "lookup"}}
			}
			body, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(wantTools)
			catalog, _ := json.Marshal(tools)
			if len(catalog) <= DefaultLimits().MaxActiveToolBytes {
				t.Fatal("fixture catalog must exceed the default active declaration budget")
			}
			for _, maximum := range []int{DefaultLimits().MaxActiveToolBytes, len(want)} {
				limits := DefaultLimits()
				limits.MaxActiveToolBytes = maximum
				prepared, errPrepared := PrepareClaudeDeferredTools(body, limits)
				if errPrepared != nil {
					t.Fatalf("unloaded catalog charged to active budget %d: %v", maximum, errPrepared)
				}
				var wire map[string]json.RawMessage
				if err := json.Unmarshal(prepared, &wire); err != nil || !bytes.Equal(wire["tools"], want) {
					t.Fatalf("active declarations changed: %s %v", wire["tools"], err)
				}
			}
			limits := DefaultLimits()
			limits.MaxActiveToolBytes = len(want) - 1
			_, errBudget := PrepareClaudeDeferredTools(body, limits)
			var toolError *ToolCompatibilityError
			if !errors.As(errBudget, &toolError) || toolError.Reason != ReasonDeclarationBudget {
				t.Fatalf("active overflow was not rejected: %v", errBudget)
			}
			t.Logf("catalog declaration bytes=%d, active declaration bytes=%d", len(catalog), len(want))
		})
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
