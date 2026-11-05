package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeGeminiCollidingToolNamesStayConsistent(t *testing.T) {
	first := strings.Repeat("a", 70) + "first"
	second := strings.Repeat("a", 70) + "second"
	request := map[string]any{
		"tools":       []any{map[string]any{"name": first, "input_schema": map[string]any{"type": "object"}}, map[string]any{"name": second, "input_schema": map[string]any{"type": "object"}}},
		"tool_choice": map[string]any{"type": "tool", "name": second},
		"messages": []any{
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "call_1", "name": second, "input": map[string]any{}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "ok"}}},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ConvertClaudeRequestToGemini("gemini", body, false)
	if err != nil {
		t.Fatal(err)
	}
	one := gjson.GetBytes(got, "tools.0.functionDeclarations.0.name").String()
	two := gjson.GetBytes(got, "tools.0.functionDeclarations.1.name").String()
	if one == "" || two == "" || one == two {
		t.Fatalf("names collapsed: %s", got)
	}
	for _, path := range []string{"toolConfig.functionCallingConfig.allowedFunctionNames.0", "contents.0.parts.0.functionCall.name", "contents.1.parts.0.functionResponse.name"} {
		if name := gjson.GetBytes(got, path).String(); name != two {
			t.Errorf("%s = %q, want %q", path, name, two)
		}
	}
	response, err := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": two, "args": map[string]any{}}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var state any
	result := ConvertGeminiResponseToClaudeNonStream(context.Background(), "gemini", body, got, response, &state)
	if name := gjson.GetBytes(result, "content.0.name").String(); name != second {
		t.Fatalf("response name = %q, want %q", name, second)
	}
	state = nil
	stream := ConvertGeminiResponseToClaude(context.Background(), "gemini", body, got, response, &state)
	if !strings.Contains(string(stream[0]), `"name":"`+second+`"`) {
		t.Fatalf("stream did not restore the tool name: %s", stream)
	}
}
