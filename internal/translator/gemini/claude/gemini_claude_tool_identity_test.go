package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeGeminiCollidingToolIdentitiesRoundTrip(t *testing.T) {
	for _, names := range [][]string{
		{strings.Repeat("a", 70) + "first", strings.Repeat("a", 70) + "second"},
		{"mcp/server/read", "mcp_server_read"},
		{"_lookup", "lookup"},
		{"Read", "read"},
	} {
		t.Run(names[0], func(t *testing.T) {
			root := map[string]any{"tools": []any{}, "messages": []any{}, "tool_choice": map[string]any{"type": "tool", "name": names[1]}}
			for i, name := range names {
				root["tools"] = append(root["tools"].([]any), map[string]any{"name": name, "input_schema": map[string]any{"type": "object"}})
				root["messages"] = append(root["messages"].([]any),
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "name": name, "id": fmt.Sprintf("call_%d", i), "input": map[string]any{}}}},
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("call_%d", i), "content": "ok"}}})
			}
			original, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, compat := range []bool{false, true} {
				var request []byte
				if compat {
					request = ConvertClaudeRequestToGeminiWithCompat("gemini-test", original, false)
				} else {
					request = ConvertClaudeRequestToGemini("gemini-test", original, false)
				}
				declared := gjson.GetBytes(request, "tools.0.functionDeclarations").Array()
				if len(declared) != 2 || declared[0].Get("name").String() == declared[1].Get("name").String() {
					t.Fatalf("distinct tool names collapsed (compat=%v): %s", compat, request)
				}
				contents := gjson.GetBytes(request, "contents").Array()
				for i := range names {
					wireName := declared[i].Get("name").String()
					if len(wireName) > 64 || contents[2*i].Get("parts.0.functionCall.name").String() != wireName ||
						contents[2*i+1].Get("parts.0.functionResponse.name").String() != wireName {
						t.Fatalf("declaration/history identity differs: %s", request)
					}
					if i == 1 && gjson.GetBytes(request, "toolConfig.functionCallingConfig.allowedFunctionNames.0").String() != wireName {
						t.Fatalf("forced choice names a different tool: %s", request)
					}
					payload, err := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": wireName, "args": map[string]any{}}}}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1}})
					if err != nil {
						t.Fatal(err)
					}
					buffered := ConvertGeminiResponseToClaudeNonStream(context.Background(), "gemini-test", original, request, payload, nil)
					if gjson.GetBytes(buffered, "content.0.name").String() != names[i] {
						t.Fatalf("buffered response restored wrong tool: %s", buffered)
					}
					var param any
					stream := bytes.Join(ConvertGeminiResponseToClaude(context.Background(), "gemini-test", original, request, payload, &param), nil)
					if !bytes.Contains(stream, []byte(`"name":"`+names[i]+`"`)) {
						t.Fatalf("stream response restored wrong tool: %s", stream)
					}
				}
			}
		})
	}
}
