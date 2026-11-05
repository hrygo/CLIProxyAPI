package responses

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestResponsesToolUseInputStreaming(t *testing.T) {
	tests := []struct {
		name      string
		frames    []string
		wantInput int64
		wantTotal int64
	}{
		{
			name:      "camel_case",
			frames:    []string{terminalContentFrame, terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`)},
			wantInput: 104, wantTotal: 164,
		},
		{
			name:      "snake_case_wrapped",
			frames:    []string{terminalContentFrame, `data: {"response":{"candidates":[{"finishReason":"STOP"}],"usage_metadata":{"prompt_token_count":100,"tool_use_prompt_token_count":4,"candidates_token_count":10,"thoughts_token_count":50,"total_token_count":164}}}`},
			wantInput: 104, wantTotal: 164,
		},
		{
			name: "later_snapshot_preserves_omitted_tool_count",
			frames: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4}}`,
				terminalFrame("STOP", `,"usageMetadata":{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`),
			},
			wantInput: 104, wantTotal: 164,
		},
		{
			name: "explicit_zero_replaces_prior_tool_count",
			frames: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4}}`,
				terminalFrame("STOP", `,"usageMetadata":{"tool_use_prompt_token_count":0,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160}`),
			},
			wantInput: 100, wantTotal: 160,
		},
		{
			name: "invalid_update_preserves_known_tool_count",
			frames: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4}}`,
				terminalFrame("STOP", `,"usageMetadata":{"toolUsePromptTokenCount":-1,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`),
			},
			wantInput: 104, wantTotal: 164,
		},
		{
			name: "valid_update_resolves_previously_invalid_tool_count",
			frames: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":null}}`,
				terminalFrame("STOP", `,"usageMetadata":{"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`),
			},
			wantInput: 104, wantTotal: 164,
		},
		{
			name:      "camel_case_takes_precedence",
			frames:    []string{terminalContentFrame, terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"tool_use_prompt_token_count":9,"candidatesTokenCount":10,"totalTokenCount":114}`)},
			wantInput: 104, wantTotal: 114,
		},
		{
			name:      "int64_boundary",
			frames:    []string{terminalContentFrame, terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":0,"toolUsePromptTokenCount":9223372036854775807,"candidatesTokenCount":0,"totalTokenCount":9223372036854775807}`)},
			wantInput: 9223372036854775807, wantTotal: 9223372036854775807,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal := requireSingleTerminal(t, observeTerminalStream(t, tt.frames...), "response.completed")
			usage := terminal.Get("response.usage")
			if !usage.Exists() || usage.Get("input_tokens").Int() != tt.wantInput || usage.Get("total_tokens").Int() != tt.wantTotal {
				t.Fatalf("usage = %s, want input=%d total=%d", usage.Raw, tt.wantInput, tt.wantTotal)
			}
		})
	}
}

func TestResponsesToolUseInputNonStream(t *testing.T) {
	for _, tt := range []struct {
		name      string
		metadata  string
		wantInput int64
	}{
		{"camel_case", `"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`, 104},
		{"snake_case", `"usage_metadata":{"prompt_token_count":100,"tool_use_prompt_token_count":4,"candidates_token_count":10,"thoughts_token_count":50,"total_token_count":164}`, 104},
		{"explicit_zero", `"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":0,"candidatesTokenCount":10,"totalTokenCount":110}`, 100},
		{"internal_carrier", `"cpaUsageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"totalTokenCount":114}`, 104},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],` + tt.metadata + `}`
			result := ConvertGeminiResponseToOpenAIResponsesNonStream(context.Background(), "gemini", nil, nil, []byte(raw), nil)
			usage := gjson.GetBytes(result, "usage")
			if !usage.Exists() || usage.Get("input_tokens").Int() != tt.wantInput {
				t.Fatalf("usage = %s, want input=%d", usage.Raw, tt.wantInput)
			}
		})
	}
}

func TestResponsesToolUseInvalidOrOverflowOmitsUsage(t *testing.T) {
	for _, tt := range []struct {
		name, prompt, tool string
	}{
		{"null", "100", "null"},
		{"negative", "100", "-1"},
		{"fraction", "100", "1.5"},
		{"boolean", "100", "true"},
		{"tool_exceeds_int64", "100", "9223372036854775808"},
		{"sum_overflow", "9223372036854775807", "1"},
	} {
		for _, streaming := range []bool{false, true} {
			mode := "nonstream"
			if streaming {
				mode = "stream"
			}
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				metadata := `"usageMetadata":{"promptTokenCount":` + tt.prompt + `,"toolUsePromptTokenCount":` + tt.tool + `,"candidatesTokenCount":10,"totalTokenCount":110}`
				var usage gjson.Result
				if streaming {
					terminal := requireSingleTerminal(t, observeTerminalStream(t, terminalContentFrame, terminalFrame("STOP", ","+metadata)), "response.completed")
					usage = terminal.Get("response.usage")
				} else {
					raw := `{"candidates":[{"finishReason":"STOP"}],` + metadata + `}`
					result := ConvertGeminiResponseToOpenAIResponsesNonStream(context.Background(), "gemini", nil, nil, []byte(raw), nil)
					usage = gjson.GetBytes(result, "usage")
				}
				if usage.Exists() {
					t.Fatalf("invalid tool-use input produced usage: %s", usage.Raw)
				}
			})
		}
	}
}
