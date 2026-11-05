package responses

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestResponsesFinishWithoutUsageIsImmediate(t *testing.T) {
	for _, tt := range []struct {
		finish, event, status, detail string
	}{
		{"STOP", "response.completed", "completed", ""},
		{"MAX_TOKENS", "response.incomplete", "incomplete", "max_output_tokens"},
	} {
		t.Run(tt.finish, func(t *testing.T) {
			var state any
			translate := func(frame string) []gjson.Result {
				var events []gjson.Result
				for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini-3.7-flash", nil, nil, []byte(frame), &state) {
					_, event := parseSSEEvent(t, chunk)
					events = append(events, event)
				}
				return events
			}

			for _, event := range translate(terminalContentFrame) {
				if event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
					t.Fatalf("content before finish emitted terminal: %s", event.Raw)
				}
			}
			finish := `data: {"candidates":[{"content":{"parts":[{"text":" tail"}]},"finishReason":"` + tt.finish + `"}]}`
			var terminal gjson.Result
			count := 0
			for _, event := range translate(finish) {
				if event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
					terminal = event
					count++
				}
			}
			if count != 1 || terminal.Get("type").String() != tt.event || terminal.Get("response.status").String() != tt.status {
				t.Fatalf("finish emitted %d terminals, got %s; want %s/%s", count, terminal.Raw, tt.event, tt.status)
			}
			if got := terminal.Get("response.output.0.content.0.text").String(); got != "answer tail" {
				t.Fatalf("terminal text = %q, want finish-frame content", got)
			}
			if got := terminal.Get("response.incomplete_details.reason").String(); got != tt.detail {
				t.Fatalf("incomplete reason = %q, want %q", got, tt.detail)
			}
			if usage := terminal.Get("response.usage"); usage.Exists() {
				t.Fatalf("finish without observed usage fabricated usage: %s", usage.Raw)
			}

			for _, frame := range []string{
				`data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"totalTokenCount":110}}`,
				finish,
				terminalDoneFrame,
			} {
				if later := translate(frame); len(later) != 0 {
					t.Fatalf("frame after terminal emitted %d events: %v", len(later), later)
				}
			}
		})
	}
}

func TestResponsesFinishKeepsUsageObservedBeforeOrOnFinish(t *testing.T) {
	for _, tt := range []struct {
		name, first, finish string
	}{
		{
			name:   "before_finish",
			first:  `data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}}`,
			finish: terminalFrame("STOP", ""),
		},
		{
			name:   "on_finish",
			first:  terminalContentFrame,
			finish: terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":4,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			terminal := requireSingleTerminal(t, observeTerminalStream(t, tt.first, tt.finish), "response.completed")
			usage := terminal.Get("response.usage")
			if !usage.Exists() || usage.Get("input_tokens").Int() != 104 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 164 {
				t.Fatalf("usage = %s, want input=104 output=60 total=164", usage.Raw)
			}
		})
	}
}

func TestResponsesFinishWithoutUsagePreservesSameFrameGrounding(t *testing.T) {
	const model = "gemini-timely-grounding"
	registerTestWebSearchModel(t, "client-timely-grounding", "antigravity", model, true)
	request := []byte(`{"model":"gemini-timely-grounding","input":"search","tools":[{"type":"web_search"}]}`)
	frames := []string{
		`data: {"responseId":"timely-grounding","candidates":[{"content":{"parts":[{"text":"Search result"}]}}]}`,
		`data: {"candidates":[{"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["search"],"groundingChunks":[{"web":{"uri":"https://example.com/source","title":"Source"}}],"groundingSupports":[{"groundingChunkIndices":[0],"segment":{"startIndex":0,"endIndex":6}}]}}]}`,
	}
	var state any
	var terminal gjson.Result
	for _, frame := range frames {
		for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), model, request, request, []byte(frame), &state) {
			name, data := parseSSEEvent(t, chunk)
			if name == "response.completed" {
				terminal = data
			}
		}
	}
	if !terminal.Exists() {
		t.Fatal("finish without usage did not emit response.completed")
	}
	if terminal.Get("response.usage").Exists() {
		t.Fatalf("finish without observed usage fabricated usage: %s", terminal.Raw)
	}
	for _, item := range terminal.Get("response.output").Array() {
		if item.Get("type").String() == "message" {
			if got := item.Get("content.0.annotations.0.url").String(); got != "https://example.com/source" {
				t.Fatalf("same-frame grounding citation = %q: %s", got, item.Raw)
			}
			return
		}
	}
	t.Fatalf("terminal is missing the message: %s", terminal.Raw)
}
