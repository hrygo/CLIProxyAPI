package responsestools

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const nativeLiteSearchRequest = `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"tool_search","execution":"client"}]},{"role":"user","content":"find budget"}]}`

func nativeSearchAliasItem(arguments string) map[string]any {
	return map[string]any{
		"type": "function_call", "id": "fc_native_search", "call_id": "search_1",
		"namespace": "tool_search", "name": "tool_search_tool", "arguments": arguments,
	}
}

func assertNativeSearchItem(t *testing.T, item map[string]any, complete bool) {
	t.Helper()
	if item["type"] != "tool_search_call" || item["execution"] != "client" ||
		item["call_id"] != "search_1" || !strings.HasPrefix(stringField(item, "id"), "tsc_") {
		t.Fatalf("search identity was not restored: %v", item)
	}
	if _, exists := item["namespace"]; exists {
		t.Fatalf("native alias namespace leaked: %v", item)
	}
	if _, exists := item["name"]; exists {
		t.Fatalf("native alias name leaked: %v", item)
	}
	arguments, ok := item["arguments"].(map[string]any)
	if !ok || (complete && arguments["query"] != "budget") {
		t.Fatalf("expected object search arguments: %v", item)
	}
}

func TestNativeLiteSearchBufferedAlias(t *testing.T) {
	body := []byte(nativeLiteSearchRequest)
	limiter := NewLimiter(DefaultLimits())
	prepared, err := Prepare(body, RoutePolicy{ClientSearch: ClientSearchNative}, DefaultLimits(), limiter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, prepared.Body) {
		t.Fatal("response-only compatibility must preserve native request bytes")
	}
	if prepared.Attempt == nil {
		t.Fatal("native search response adapter was not retained")
	}
	defer prepared.Attempt.Close()
	response := frameJSON(t, map[string]any{"output": []any{nativeSearchAliasItem(`{"query":"budget"}`)}})
	restored, err := prepared.Attempt.RewriteResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(restored, &decoded); err != nil {
		t.Fatal(err)
	}
	assertNativeSearchItem(t, decoded["output"].([]any)[0].(map[string]any), true)
	prepared.Attempt.Close()
	if count, size := limiter.Usage(); count != 0 || size != 0 {
		t.Fatalf("response-only attempt leaked: %d/%d", count, size)
	}
}

func TestNativeLiteSearchFragmentedStreamAlias(t *testing.T) {
	contract := ParseContract([]byte(nativeLiteSearchRequest))
	feed := NewStreamFeed(contract, nil, &Lease{}, DefaultLimits())
	events := []map[string]any{
		{"type": eventOutputItemAdded, "output_index": 0, "item": nativeSearchAliasItem("")},
		{"type": eventFunctionCallArgsDelta, "item_id": "fc_native_search", "delta": `{"query":"bud`},
		{"type": eventFunctionCallArgsDelta, "item_id": "fc_native_search", "delta": `get"}`},
		{"type": eventFunctionCallArgsDone, "item_id": "fc_native_search", "arguments": `{"query":"budget"}`},
		{"type": eventOutputItemDone, "output_index": 0, "item": nativeSearchAliasItem(`{"query":"budget"}`)},
		{"type": eventResponseCompleted, "response": map[string]any{"output": []any{nativeSearchAliasItem(`{"query":"budget"}`)}}},
	}
	var output [][]byte
	for _, event := range events {
		frames, err := feed.Feed(frameJSON(t, event))
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, frames...)
	}
	tail, err := feed.Finish()
	if err != nil {
		t.Fatal(err)
	}
	output = append(output, tail...)
	if len(output) != 3 {
		t.Fatalf("expected added, done and completed, got %d", len(output))
	}
	var clientID string
	for index, frame := range output {
		var event map[string]any
		if err := json.Unmarshal(frame, &event); err != nil {
			t.Fatal(err)
		}
		item, _ := event["item"].(map[string]any)
		if event["type"] == eventResponseCompleted {
			item = event["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
		}
		assertNativeSearchItem(t, item, index > 0)
		if index == 0 {
			clientID = stringField(item, "id")
		} else if stringField(item, "id") != clientID {
			t.Fatalf("stream changed client item id: %v", item)
		}
	}
}

func TestNativeSearchAliasDoesNotStealBusinessTools(t *testing.T) {
	cases := []struct {
		name string
		body string
		ns   string
	}{
		{"server search", `{"tools":[{"type":"tool_search","execution":"server"}]}`, "tool_search"},
		{"no search", `{"tools":[{"type":"function","name":"tool_search_tool","parameters":{"type":"object"}}]}`, "tool_search"},
		{"declared business identity", `{"tools":[{"type":"tool_search"},{"type":"namespace","name":"tool_search","tools":[{"type":"function","name":"tool_search_tool","parameters":{"type":"object"}}]}]}`, "tool_search"},
		{"different namespace", nativeLiteSearchRequest, "business"},
		{"no namespace", nativeLiteSearchRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := ParseContract([]byte(tc.body))
			item := nativeSearchAliasItem(`{"query":"budget"}`)
			item["namespace"] = tc.ns
			changed, err := rewriteResponseItemChecked(item, contract, nil)
			if err != nil {
				t.Fatal(err)
			}
			if item["type"] != "function_call" || changed {
				t.Fatalf("ordinary business call was rewritten: %v", item)
			}
		})
	}
}

const nativeDiscoveryRequest = `{"tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"tool_search_call","id":"tsc_search_1","call_id":"search_1","execution":"client","arguments":{"query":"budget"}},{"type":"tool_search_output","id":"tso_search_1","call_id":"search_1","tools":[{"type":"namespace","name":"mcp__bench","tools":[{"type":"function","name":"ledger_budget_lookup","parameters":{"type":"object","properties":{"project":{"type":"string"}},"required":["project"]}}]}]}]}`

func TestNativePortableSearchActivatesDiscovery(t *testing.T) {
	policy := ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"})
	prepared, err := Prepare([]byte(nativeDiscoveryRequest), policy, DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil {
		t.Fatal("missing portable search attempt")
	}
	defer prepared.Attempt.Close()
	value, _ := decodeValue(prepared.Body)
	root := value.(map[string]any)
	tools := root["tools"].([]any)
	if len(tools) != 2 || stringField(tools[0].(map[string]any), "type") != "tool_search" {
		t.Fatalf("native search must remain native while its discovery activates: %s", prepared.Body)
	}
	activated := tools[1].(map[string]any)
	if stringField(activated, "type") != "function" {
		t.Fatalf("discovery was not activated: %v", activated)
	}
	input := root["input"].([]any)
	if stringField(input[0].(map[string]any), "type") != "tool_search_call" ||
		stringField(input[1].(map[string]any), "type") != "tool_search_output" {
		t.Fatal("native search history was converted")
	}
	response := frameJSON(t, map[string]any{"output": []any{map[string]any{
		"type": "function_call", "id": "fc_budget", "call_id": "budget_1",
		"name": activated["name"], "arguments": `{"project":"alpha"}`,
	}}})
	restored, err := prepared.Attempt.RewriteResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	value, _ = decodeValue(restored)
	item := value.(map[string]any)["output"].([]any)[0].(map[string]any)
	if item["namespace"] != "mcp__bench" || item["name"] != "ledger_budget_lookup" || item["call_id"] != "budget_1" {
		t.Fatalf("discovered tool identity was not restored: %v", item)
	}
}

func TestNativeOrdinarySearchDoesNotPromoteDiscovery(t *testing.T) {
	body := []byte(nativeDiscoveryRequest)
	prepared, err := Prepare(body, RoutePolicy{ClientSearch: ClientSearchNative}, DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Attempt.Close()
	if !bytes.Equal(body, prepared.Body) {
		t.Fatal("ordinary native search request changed")
	}
}

func TestNativePortableSearchRejectsConflictingDiscovery(t *testing.T) {
	body := strings.Replace(nativeDiscoveryRequest, `"tools":[{"type":"namespace"`, `"tools":[{"type":"function","name":"clash","parameters":{"type":"object"}},{"type":"function","name":"clash","parameters":{"type":"string"}},{"type":"namespace"`, 1)
	policy := ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"})
	prepared, err := Prepare([]byte(body), policy, DefaultLimits(), NewLimiter(DefaultLimits()))
	if prepared.Attempt != nil {
		prepared.Attempt.Close()
	}
	if err == nil {
		t.Fatal("conflicting discovery was accepted")
	}
}

func TestNativePortableSearchDiscoveryHonorsBudget(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxActiveToolBytes = 80
	limiter := NewLimiter(limits)
	policy := ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"})
	prepared, err := Prepare([]byte(nativeDiscoveryRequest), policy, limits, limiter)
	if prepared.Attempt != nil {
		prepared.Attempt.Close()
	}
	if err == nil {
		t.Fatal("oversized latest discovery was accepted")
	}
	if count, size := limiter.Usage(); count != 0 || size != 0 {
		t.Fatalf("rejected discovery leaked its lease: %d/%d", count, size)
	}
}

func TestNativeSearchStreamRejectsChangedNamespace(t *testing.T) {
	feed := NewStreamFeed(ParseContract([]byte(nativeLiteSearchRequest)), nil, &Lease{}, DefaultLimits())
	if _, err := feed.Feed(frameJSON(t, map[string]any{
		"type": eventOutputItemAdded, "item": nativeSearchAliasItem(""),
	})); err != nil {
		t.Fatal(err)
	}
	item := nativeSearchAliasItem(`{"query":"budget"}`)
	item["namespace"] = "business"
	if _, err := feed.Feed(frameJSON(t, map[string]any{
		"type": eventOutputItemDone, "item": item,
	})); err == nil {
		t.Fatal("changed native search identity was accepted")
	}
}
