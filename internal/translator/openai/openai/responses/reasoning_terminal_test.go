package responses

import (
	"context"
	"fmt"
	"testing"
)

func TestReasoningOnlyExplicitFinishEmitsTerminal(t *testing.T) {
	for _, tc := range []struct{ reason, event, status string }{
		{"stop", "response.completed", "completed"},
		{"length", "response.incomplete", "incomplete"},
		{"max_tokens", "response.incomplete", "incomplete"},
		{"content_filter", "response.incomplete", "incomplete"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			request := []byte(`{"model":"glm-5.3-flash"}`)
			lines := []string{
				`data: {"id":"reasoning_only","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"checking tools"},"finish_reason":null}]}`,
				fmt.Sprintf(`data: {"id":"reasoning_only","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, tc.reason),
				`data: {"id":"reasoning_only","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`,
				`data: [DONE]`,
				`data: [DONE]`,
			}
			var state any
			terminalCount := 0
			for i, line := range lines {
				for _, chunk := range ConvertOpenAIChatCompletionsResponseToOpenAIResponses(context.Background(), "glm-5.3-flash", request, request, []byte(line), &state) {
					event, data := parseOpenAIResponsesSSEEvent(t, chunk)
					if event != tc.event {
						continue
					}
					terminalCount++
					if i != 3 {
						t.Fatalf("terminal emitted before DONE or duplicated: %d", i)
					}
					if data.Get("response.status").String() != tc.status || data.Get("response.output.0.type").String() != "reasoning" || data.Get("response.usage.input_tokens").Int() != 11 {
						t.Fatalf("terminal lost output/status/usage: %s", chunk)
					}
				}
			}
			if terminalCount != 1 {
				t.Fatalf("expected one %s, got %d", tc.event, terminalCount)
			}
		})
	}
}
