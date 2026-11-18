package responsestools

import (
	"bytes"
	"testing"
)

func TestNativePortableEagerNamespaceKeepsRequestAndRestoresResponse(t *testing.T) {
	body := []byte(`{"tools":[{"type":"namespace","name":"mcp__fixture","tools":[{"type":"function","name":"ledger_budget_lookup","parameters":{"type":"object"}}]}],"input":[]}`)
	limiter := NewLimiter(DefaultLimits())
	prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), limiter)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil {
		t.Fatal("portable eager namespace has no response identity adapter")
	}
	defer prepared.Attempt.Close()
	if !bytes.Equal(prepared.Body, body) || !prepared.Attempt.ResponseOnly() {
		t.Fatal("native eager request changed instead of retaining a response-only adapter")
	}
	response := []byte(`{"id":"resp","status":"completed","output":[{"type":"function_call","id":"fc_one","call_id":"call_one","name":"mcp__fixture.ledger_budget_lookup","arguments":"{}"}]}`)
	restored, err := prepared.Attempt.RewriteResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := decodeValue(restored)
	item := value.(map[string]any)["output"].([]any)[0].(map[string]any)
	if item["name"] != "ledger_budget_lookup" || item["namespace"] != "mcp__fixture" || item["call_id"] != "call_one" || item["arguments"] != "{}" {
		t.Fatalf("declared identity was not restored without changing call semantics: %s", restored)
	}
	prepared.Attempt.Close()
	if attempts, state := limiter.Usage(); attempts != 0 || state != 0 {
		t.Fatal("native response identity lease leaked")
	}
}

func TestNativeEagerNamespaceRepairPreservesTransparentAndPlainRoutes(t *testing.T) {
	for _, trial := range []struct {
		provider string
		body     string
	}{
		{"codex", `{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"read"}]}],"input":[]}`},
		{"meta", `{"tools":[{"type":"function","name":"read"}],"input":[]}`},
		{"meta", `{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"read","defer_loading":true}]}],"input":[]}`},
	} {
		prepared, err := Prepare([]byte(trial.body), ConventionPolicy(Route{Provider: trial.provider, UpstreamFormat: "openai-response"}), DefaultLimits(), NewLimiter(DefaultLimits()))
		if err != nil || prepared.Attempt != nil || string(prepared.Body) != trial.body {
			t.Fatalf("transparent or plain route changed: provider=%s error=%v", trial.provider, err)
		}
	}
}

func TestNativePortableEagerNamespaceStreamRestoresCompletedItem(t *testing.T) {
	body := []byte(`{"tools":[{"type":"namespace","name":"ns","tools":[{"type":"function","name":"read"}]}],"input":[]}`)
	prepared, err := Prepare(body, ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"}), DefaultLimits(), NewLimiter(DefaultLimits()))
	if err != nil || prepared.Attempt == nil {
		t.Fatalf("native eager stream has no response adapter: %v", err)
	}
	defer prepared.Attempt.Close()
	response := []byte(`data: {"type":"response.completed","response":{"id":"resp","status":"completed","output":[{"type":"function_call","id":"fc_one","call_id":"call_one","name":"ns.read","arguments":"{}"}]}}` + "\n\n")
	outputs, err := prepared.Attempt.Feed(response)
	if err != nil {
		t.Fatal(err)
	}
	var observed bool
	for _, output := range outputs {
		value, ok := decodeValue(output)
		if !ok {
			continue
		}
		event, _ := value.(map[string]any)
		if event["type"] != "response.completed" {
			continue
		}
		item := event["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
		observed = item["name"] == "read" && item["namespace"] == "ns" && item["call_id"] == "call_one"
	}
	if !observed {
		t.Fatalf("terminal stream identity not restored: %q", outputs)
	}
}
