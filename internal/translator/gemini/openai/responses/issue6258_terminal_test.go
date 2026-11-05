package responses

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

const issue6258Usage = `{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160,"cachedContentTokenCount":7}`

func issue6258Translate(t *testing.T, param *any, frame string) []gjson.Result {
	t.Helper()
	var events []gjson.Result
	for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini-3.7-flash", nil, nil, []byte(frame), param) {
		_, event := parseSSEEvent(t, chunk)
		events = append(events, event)
	}
	return events
}

func issue6258Terminal(t *testing.T, events []gjson.Result) gjson.Result {
	t.Helper()
	var terminal gjson.Result
	terminals, created, inProgress := 0, 0, 0
	previous := int64(-1)
	added := map[string]gjson.Result{}
	done := map[string]int{}
	responseID := ""
	for _, event := range events {
		seq := event.Get("sequence_number").Int()
		if seq <= previous {
			t.Errorf("sequence_number=%d after %d", seq, previous)
		}
		previous = seq
		if id := event.Get("response.id").String(); id != "" {
			if responseID != "" && id != responseID {
				t.Errorf("response ID changed: %q -> %q", responseID, id)
			}
			responseID = id
		}
		switch event.Get("type").String() {
		case "response.created":
			created++
		case "response.in_progress":
			inProgress++
		case "response.output_item.added":
			added[event.Get("item.id").String()] = event
		case "response.output_item.done":
			id := event.Get("item.id").String()
			done[id]++
			start, ok := added[id]
			if !ok || start.Get("output_index").Int() != event.Get("output_index").Int() {
				t.Errorf("item identity/index changed: %s", event.Raw)
			}
		case "response.completed", "response.incomplete", "response.failed":
			terminals++
			terminal = event
		}
	}
	if terminals != 1 {
		t.Errorf("terminal count=%d, want exactly 1", terminals)
	}
	if created != 1 || inProgress != 1 {
		t.Errorf("created/in_progress counts=%d/%d, want 1/1", created, inProgress)
	}
	for id := range added {
		if done[id] != 1 {
			t.Errorf("item %s done count=%d, want exactly 1", id, done[id])
		}
	}
	for index, item := range terminal.Get("response.output").Array() {
		start, ok := added[item.Get("id").String()]
		if !ok || start.Get("output_index").Int() != int64(index) {
			t.Errorf("terminal output identity/index changed: %s", item.Raw)
		}
	}
	return terminal.Get("response")
}

func issue6258AssertUsage(t *testing.T, response gjson.Result, input, output, reasoning, total, cached int64) {
	t.Helper()
	for path, want := range map[string]int64{
		"input_tokens": input, "output_tokens": output, "output_tokens_details.reasoning_tokens": reasoning,
		"total_tokens": total, "input_tokens_details.cached_tokens": cached,
	} {
		got := response.Get("usage." + path)
		if !got.Exists() || got.Int() != want {
			t.Errorf("usage.%s=%s, want %d; usage=%s", path, got.Raw, want, response.Get("usage").Raw)
		}
	}
}

func TestIssue6258GeminiResponsesStopUsageTiming(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		for _, usageKey := range []string{"usageMetadata", "cpaUsageMetadata"} {
			for _, timing := range []string{"before_finish", "on_finish", "after_finish"} {
				t.Run(fmt.Sprintf("wrapped=%t/%s/%s", wrapped, usageKey, timing), func(t *testing.T) {
					frame := func(body string) string {
						if wrapped {
							return `data: {"response":` + body + `}`
						}
						return "data: " + body
					}
					var param any
					events := issue6258Translate(t, &param, frame(`{"responseId":"split-stop","candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`))
					usageFrame := frame(`{"` + usageKey + `":` + issue6258Usage + `}`)
					if timing == "before_finish" {
						events = append(events, issue6258Translate(t, &param, usageFrame)...)
					}
					finishBody := `{"candidates":[{"content":{"parts":[{"text":" tail"}]},"finishReason":"STOP"}]`
					if timing == "on_finish" {
						finishBody += `,"` + usageKey + `":` + issue6258Usage
					}
					finish := frame(finishBody + `}`)
					finishEvents := issue6258Translate(t, &param, finish)
					finishTerminals := 0
					for _, event := range finishEvents {
						if event.Get("type").String() == "response.completed" {
							finishTerminals++
						}
					}
					if finishTerminals != 1 {
						t.Errorf("finish emitted %d completed terminals, want 1", finishTerminals)
					}
					events = append(events, finishEvents...)
					for _, later := range []string{usageFrame, finish, "data: [DONE]", "data: [DONE]"} {
						laterEvents := issue6258Translate(t, &param, later)
						if len(laterEvents) != 0 {
							t.Errorf("frame after completion emitted %d events", len(laterEvents))
						}
						events = append(events, laterEvents...)
					}
					response := issue6258Terminal(t, events)
					if response.Get("status").String() != "completed" || response.Get("output.0.content.0.text").String() != "answer tail" {
						t.Errorf("lost finish-frame content or wrong status: %s", response.Raw)
					}
					if timing == "after_finish" {
						if usage := response.Get("usage"); usage.Exists() {
							t.Errorf("finish without observed usage fabricated usage: %s", usage.Raw)
						}
					} else {
						issue6258AssertUsage(t, response, 100, 60, 50, 160, 7)
					}
					for _, event := range events {
						if strings.Contains(event.Raw, "cpaUsageMetadata") {
							t.Errorf("internal usage carrier leaked downstream: %s", event.Raw)
						}
					}
				})
			}
		}
	}
}

func TestIssue6258GeminiResponsesMaxTokens(t *testing.T) {
	for _, mode := range []string{"before_finish", "same_frame", "split_usage", "no_usage_clean_done"} {
		t.Run("stream/"+mode, func(t *testing.T) {
			var param any
			events := issue6258Translate(t, &param, `{"responseId":"max","candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`)
			if mode == "before_finish" {
				events = append(events, issue6258Translate(t, &param, `{"usageMetadata":`+issue6258Usage+`}`)...)
			}
			finish := `{"candidates":[{"finishReason":"MAX_TOKENS"}]`
			if mode == "same_frame" {
				finish += `,"usageMetadata":` + issue6258Usage
			}
			finishEvents := issue6258Translate(t, &param, finish+`}`)
			finishTerminals := 0
			for _, event := range finishEvents {
				if event.Get("type").String() == "response.incomplete" {
					finishTerminals++
				}
			}
			if finishTerminals != 1 {
				t.Errorf("MAX_TOKENS finish emitted %d incomplete terminals, want 1", finishTerminals)
			}
			events = append(events, finishEvents...)
			if mode == "split_usage" {
				later := issue6258Translate(t, &param, `{"usageMetadata":`+issue6258Usage+`}`)
				if len(later) != 0 {
					t.Errorf("usage after MAX_TOKENS emitted %d events", len(later))
				}
				events = append(events, later...)
			}
			for _, frame := range []string{"[DONE]", "[DONE]", finish + `}`} {
				later := issue6258Translate(t, &param, frame)
				if len(later) != 0 {
					t.Errorf("frame after MAX_TOKENS emitted %d events", len(later))
				}
				events = append(events, later...)
			}
			response := issue6258Terminal(t, events)
			for _, event := range events {
				kind := event.Get("type").String()
				if kind == "response.completed" || kind == "response.failed" {
					t.Errorf("MAX_TOKENS emitted %s, want response.incomplete", kind)
				}
			}
			issue6258AssertIncomplete(t, response)
			if mode == "same_frame" || mode == "before_finish" {
				issue6258AssertUsage(t, response, 100, 60, 50, 160, 7)
			} else if usage := response.Get("usage"); usage.Exists() {
				t.Errorf("MAX_TOKENS without observed usage fabricated usage: %s", usage.Raw)
			}
		})
	}
	t.Run("nonstream", func(t *testing.T) {
		raw := `{"responseId":"max","candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":` + issue6258Usage + `}`
		response := gjson.ParseBytes(ConvertGeminiResponseToOpenAIResponsesNonStream(context.Background(), "gemini-3.7-flash", nil, nil, []byte(raw), nil))
		issue6258AssertIncomplete(t, response)
		issue6258AssertUsage(t, response, 100, 60, 50, 160, 7)
	})
}

func issue6258AssertIncomplete(t *testing.T, response gjson.Result) {
	t.Helper()
	if response.Get("status").String() != "incomplete" || response.Get("incomplete_details.reason").String() != "max_output_tokens" {
		t.Errorf("MAX_TOKENS status=%q details=%s, want incomplete/max_output_tokens", response.Get("status").String(), response.Get("incomplete_details").Raw)
	}
	if response.Get("output.0.status").String() != "incomplete" || response.Get("output.0.content.0.text").String() != "partial" {
		t.Errorf("partial message must be preserved and incomplete: %s", response.Get("output").Raw)
	}
}

func TestIssue6258GeminiResponsesUsageSnapshots(t *testing.T) {
	for _, test := range []struct {
		name, finishUsage        string
		output, reasoning, total int64
	}{
		{"absent_preserves", `{"candidatesTokenCount":12,"totalTokenCount":182}`, 62, 50, 182},
		{"explicit_zero_replaces", `{"candidatesTokenCount":12,"thoughtsTokenCount":0,"totalTokenCount":0}`, 12, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var param any
			var events []gjson.Result
			for _, frame := range []string{
				`{"responseId":"snapshots","candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":` + issue6258Usage + `}`,
				`{"usageMetadata":` + issue6258Usage + `}`,
				`{"usageMetadata":{"promptTokenCount":120,"cachedContentTokenCount":0},"cpaUsageMetadata":{"promptTokenCount":999}}`,
				`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":` + test.finishUsage + `}`,
				`[DONE]`,
			} {
				events = append(events, issue6258Translate(t, &param, frame)...)
			}
			issue6258AssertUsage(t, issue6258Terminal(t, events), 120, test.output, test.reasoning, test.total, 0)
		})
	}
}

func TestIssue6258GeminiResponsesMaxTokensOnlyActiveMessageIncomplete(t *testing.T) {
	var param any
	var events []gjson.Result
	for _, frame := range []string{
		`{"responseId":"mixed","candidates":[{"content":{"parts":[{"text":"preface"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{"key":"value"}}}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":"MAX_TOKENS"}],"usageMetadata":` + issue6258Usage + `}`,
		`[DONE]`,
	} {
		events = append(events, issue6258Translate(t, &param, frame)...)
	}
	response := issue6258Terminal(t, events)
	items := response.Get("output").Array()
	if len(items) != 3 {
		t.Fatalf("output count=%d, want preface/tool/active message: %s", len(items), response.Raw)
	}
	for index, want := range []string{"completed", "completed", "incomplete"} {
		if got := items[index].Get("status").String(); got != want {
			t.Errorf("output[%d].status=%q, want %q", index, got, want)
		}
		for _, event := range events {
			if event.Get("type").String() == "response.output_item.done" && event.Get("output_index").Int() == int64(index) && event.Get("item.status").String() != want {
				t.Errorf("output_item.done[%d].status=%q, want %q", index, event.Get("item.status").String(), want)
			}
		}
	}
}

func TestIssue6258GeminiResponsesDoneUsesUsageSnapshot(t *testing.T) {
	for _, usageKey := range []string{"usageMetadata", "cpaUsageMetadata"} {
		t.Run(usageKey, func(t *testing.T) {
			var param any
			var events []gjson.Result
			for _, frame := range []string{
				`{"responseId":"snapshot-done","candidates":[{"content":{"parts":[{"text":"answer"}]}}],"` + usageKey + `":` + issue6258Usage + `}`,
				`{"` + usageKey + `":` + issue6258Usage + `}`,
				`{"candidates":[{"finishReason":"STOP"}]}`,
				`[DONE]`, `[DONE]`,
			} {
				events = append(events, issue6258Translate(t, &param, frame)...)
			}
			issue6258AssertUsage(t, issue6258Terminal(t, events), 100, 60, 50, 160, 7)
		})
	}
}

func TestIssue6258GeminiResponsesSameFrameControl(t *testing.T) {
	var param any
	events := issue6258Translate(t, &param, `{"responseId":"control","candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":`+issue6258Usage+`}`)
	events = append(events, issue6258Translate(t, &param, "[DONE]")...)
	response := issue6258Terminal(t, events)
	issue6258AssertUsage(t, response, 100, 60, 50, 160, 7)
}
