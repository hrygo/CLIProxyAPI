package responses

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func terminalIncompleteCallFrame(name, path, finish string) string {
	return fmt.Sprintf(`data: {"responseId":"resp_incomplete_tool","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":%q,"partialArgs":[{"jsonPath":%q,"stringValue":"SECRET_PARTIAL_VALUE","willContinue":true}],"willContinue":true}}]}%s}]}`, name, path, finish)
}

func TestTerminalIncompleteStreamingToolFails(t *testing.T) {
	tools := []struct {
		name, request, function, path, itemID string
	}{
		{"function", `{"tools":[{"type":"function","name":"get_weather"}]}`, "get_weather", "$.city", "fc_call_a"},
		{"apply_patch", geminiPatchRequest, "functions__apply_patch", "$.input", "ctc_call_a"},
	}
	for _, tool := range tools {
		for _, state := range []string{"active", "replaced"} {
			for _, marker := range []string{"STOP", "MAX_TOKENS", "DONE"} {
				t.Run(tool.name+"/"+state+"/"+marker, func(t *testing.T) {
					var param any
					translate := func(frame string) []gjson.Result {
						var events []gjson.Result
						for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(frame), &param) {
							_, event := parseSSEEvent(t, chunk)
							events = append(events, event)
						}
						return events
					}
					var before []gjson.Result
					before = append(before, translate(terminalIncompleteCallFrame(tool.function, tool.path, ""))...)
					if state == "replaced" {
						before = append(before, translate(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_b","name":"other_tool","partialArgs":[{"jsonPath":"$.value","stringValue":"safe"}],"willContinue":false}}]}}]}`)...)
					}
					for _, event := range before {
						if event.Get("type").String() == "response.failed" || event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
							t.Fatalf("terminal before finish: %s", event.Raw)
						}
					}
					finish := `[DONE]`
					if marker != "DONE" {
						finish = `data: {"candidates":[{"finishReason":"` + marker + `"}]}`
					}
					terminal := translate(finish)
					all := append(before, terminal...)
					failures := 0
					for _, event := range all {
						kind := event.Get("type").String()
						if kind == "response.completed" || kind == "response.incomplete" {
							t.Fatalf("unfinished call reported success: %s", event.Raw)
						}
						if (kind == "response.function_call_arguments.done" || kind == "response.custom_tool_call_input.done") && event.Get("item_id").String() == tool.itemID {
							t.Fatalf("unfinished call reported done: %s", event.Raw)
						}
						if kind == "response.output_item.done" && event.Get("item.call_id").String() == "call_a" {
							t.Fatalf("unfinished call item reported done: %s", event.Raw)
						}
						if kind == "response.failed" {
							failures++
							if event.Get("response.id").String() != "resp_incomplete_tool" || event.Get("response.status").String() != "failed" || event.Get("response.error.type").String() != "server_error" || event.Get("response.error.code").String() != "invalid_tool_arguments" {
								t.Fatalf("incorrect failure envelope: %s", event.Raw)
							}
							if strings.Contains(event.Raw, "SECRET_PARTIAL_VALUE") || strings.Contains(strings.ToLower(event.Get("response.error.message").String()), "apply_patch") || event.Get("response.error.message").String() == "" {
								t.Fatalf("failure message leaked arguments or misidentified tool: %s", event.Raw)
							}
						}
					}
					if failures != 1 {
						t.Fatalf("finish emitted %d failure events, want 1: %v", failures, terminal)
					}
					if st, ok := param.(interface{ ToolInputError() error }); !ok || st.ToolInputError() == nil {
						t.Fatal("missing retained tool input failure")
					}
					if st, ok := param.(interface{ ToolInputErrorIsGeneric() bool }); !ok || st.ToolInputErrorIsGeneric() != (tool.name == "function") {
						t.Fatal("failed tool kind was not retained for executor")
					}
					for _, later := range []string{finish, `[DONE]`, terminalIncompleteCallFrame(tool.function, tool.path, `,"finishReason":"STOP"`)} {
						if events := translate(later); len(events) != 0 {
							t.Fatalf("events after failure: %v", events)
						}
					}
				})
			}
		}
	}
}

func TestTerminalFinalizeOrdinaryToolWithPatchEnabled(t *testing.T) {
	request := []byte(`{"tools":[{"type":"custom","name":"apply_patch"},{"type":"function","name":"get_weather"}]}`)
	var param any
	frame := terminalIncompleteCallFrame("get_weather", "$.city", "")
	ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", request, nil, []byte(frame), &param)
	state, ok := param.(interface {
		FinalizeToolInput() [][]byte
		ToolInputErrorIsGeneric() bool
	})
	if !ok {
		t.Fatal("missing stream finalizer or failure classification")
	}
	chunks := state.FinalizeToolInput()
	if len(chunks) != 1 {
		t.Fatalf("finalizer emitted %d events, want one failure", len(chunks))
	}
	kind, event := parseSSEEvent(t, chunks[0])
	if kind != "response.failed" || !state.ToolInputErrorIsGeneric() || strings.Contains(strings.ToLower(event.Get("response.error.message").String()), "apply_patch") {
		t.Fatalf("ordinary tool was reported as patch failure: %s", event.Raw)
	}
	if later := state.FinalizeToolInput(); len(later) != 0 {
		t.Fatalf("finalizer repeated terminal: %s", later)
	}
}

func TestTerminalMalformedStreamingFunctionFailsAtFinish(t *testing.T) {
	var param any
	first := `data: {"responseId":"malformed","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":"get_weather","partialArgs":"SECRET_PARTIAL_VALUE","willContinue":true}}]}}]}`
	finish := `data: {"candidates":[{"finishReason":"STOP"}]}`
	var failures int
	for _, frame := range []string{first, finish} {
		for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", nil, nil, []byte(frame), &param) {
			kind, event := parseSSEEvent(t, chunk)
			if kind == "response.completed" || kind == "response.function_call_arguments.done" {
				t.Fatalf("malformed arguments reported success: %s", event.Raw)
			}
			if kind == "response.failed" {
				failures++
				if strings.Contains(event.Raw, "SECRET_PARTIAL_VALUE") || event.Get("response.error.code").String() != "invalid_tool_arguments" {
					t.Fatalf("unsafe or invalid failure: %s", event.Raw)
				}
			}
		}
	}
	state, ok := param.(interface{ ToolInputErrorIsGeneric() bool })
	if failures != 1 || !ok || !state.ToolInputErrorIsGeneric() {
		t.Fatalf("failures=%d, generic classification=%v", failures, ok && state.ToolInputErrorIsGeneric())
	}
}

func TestTerminalPatchValidationFailureRemainsPatchSpecific(t *testing.T) {
	var param any
	frame := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"functions__apply_patch","args":{"input":1}}}]}}]}`
	chunks := ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(geminiPatchRequest), nil, []byte(frame), &param)
	var failures int
	for _, chunk := range chunks {
		kind, _ := parseSSEEvent(t, chunk)
		if kind == "response.failed" {
			failures++
		}
	}
	state, ok := param.(interface{ ToolInputErrorIsGeneric() bool })
	if failures != 1 || !ok || state.ToolInputErrorIsGeneric() {
		t.Fatalf("failures=%d, patch classification=%v", failures, ok && !state.ToolInputErrorIsGeneric())
	}
}

func TestTerminalInterleavedPartialArgumentCannotCloseOtherPath(t *testing.T) {
	frames := []string{
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","partial_args":[{"json_path":"$.a","string_value":"one","will_continue":true},{"json_path":"$.b","string_value":"two","will_continue":true}],"will_continue":true}}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"partial_args":[{"json_path":"$.a","string_value":"done"}],"will_continue":false}}]},"finishReason":"STOP"}]}`,
	}
	var param any
	var failures int
	for _, frame := range frames {
		for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", nil, nil, []byte(frame), &param) {
			kind, event := parseSSEEvent(t, chunk)
			if kind == "response.completed" || kind == "response.function_call_arguments.done" {
				t.Fatalf("unclosed path reported success: %s", event.Raw)
			}
			if kind == "response.failed" {
				failures++
			}
		}
	}
	if failures != 1 {
		t.Fatalf("failures=%d, want one", failures)
	}
}

func TestTerminalIncompleteStreamingToolFailsOnSameFrame(t *testing.T) {
	for _, tool := range []struct{ name, request, function, path string }{
		{"function", `{"tools":[{"type":"function","name":"get_weather"}]}`, "get_weather", "$.city"},
		{"apply_patch", geminiPatchRequest, "functions__apply_patch", "$.input"},
	} {
		t.Run(tool.name, func(t *testing.T) {
			var param any
			chunks := ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(terminalIncompleteCallFrame(tool.function, tool.path, `,"finishReason":"STOP"`)), &param)
			var failures int
			for _, chunk := range chunks {
				name, data := parseSSEEvent(t, chunk)
				if name == "response.failed" {
					failures++
				}
				if name == "response.completed" || name == "response.incomplete" || name == "response.function_call_arguments.done" || name == "response.custom_tool_call_input.done" || (name == "response.output_item.done" && data.Get("item.call_id").String() == "call_a") {
					t.Fatalf("unfinished call reported success: %s", data.Raw)
				}
			}
			if failures != 1 {
				t.Fatalf("same-frame finish emitted %d failure events", failures)
			}
		})
	}
}

func TestTerminalCompleteStreamingToolStillSucceeds(t *testing.T) {
	for _, tool := range []struct{ name, request, function, path, done, field, want string }{
		{"function", `{"tools":[{"type":"function","name":"get_weather"}]}`, "get_weather", "$.city", "response.function_call_arguments.done", "arguments", `{"city":"SECRET_PARTIAL_VALUErest"}`},
		{"apply_patch", geminiPatchRequest, "functions__apply_patch", "$.input", "response.custom_tool_call_input.done", "input", "SECRET_PARTIAL_VALUErest"},
	} {
		t.Run(tool.name, func(t *testing.T) {
			var param any
			frames := []string{
				terminalIncompleteCallFrame(tool.function, tool.path, ""),
				fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","partialArgs":[{"jsonPath":%q,"stringValue":"rest"}],"willContinue":false}}]},"finishReason":"STOP"}]}`, tool.path),
			}
			var completed, done int
			for _, frame := range frames {
				for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(frame), &param) {
					name, data := parseSSEEvent(t, chunk)
					if name == "response.failed" {
						t.Fatalf("valid continuation failed: %s", data.Raw)
					}
					if name == tool.done {
						done++
						if got := data.Get(tool.field).String(); got != tool.want {
							t.Fatalf("completed input = %q, want %q", got, tool.want)
						}
					}
					if name == "response.completed" {
						completed++
					}
				}
			}
			if done != 1 || completed != 1 {
				t.Fatalf("done=%d completed=%d, want 1 each", done, completed)
			}
		})
	}
}

func TestTerminalUnclosedPartialArgumentFails(t *testing.T) {
	for _, tool := range []struct{ name, request, function, path string }{
		{"function", `{"tools":[{"type":"function","name":"get_weather"}]}`, "get_weather", "$.city"},
		{"apply_patch", geminiPatchRequest, "functions__apply_patch", "$.input"},
	} {
		for _, mode := range []string{"on_finish", "before_finish", "same_frame"} {
			t.Run(tool.name+"/"+mode, func(t *testing.T) {
				first := terminalIncompleteCallFrame(tool.function, tool.path, "")
				finalCall := `data: {"candidates":[{"content":{"parts":[{"functionCall":{}}]}}]}`
				finish := `data: {"candidates":[{"finishReason":"STOP"}]}`
				frames := []string{first, finalCall, finish}
				if mode == "on_finish" {
					frames = []string{first, `data: {"candidates":[{"content":{"parts":[{"functionCall":{}}]},"finishReason":"STOP"}]}`}
				} else if mode == "same_frame" {
					first = strings.Replace(terminalIncompleteCallFrame(tool.function, tool.path, `,"finishReason":"STOP"`), `}],"willContinue":true`, `}],"willContinue":false`, 1)
					frames = []string{first}
				}
				var param any
				failures := 0
				for _, frame := range frames {
					for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(frame), &param) {
						kind, data := parseSSEEvent(t, chunk)
						switch kind {
						case "response.completed", "response.incomplete", "response.function_call_arguments.done", "response.custom_tool_call_input.done":
							t.Fatalf("unclosed argument reported success: %s", data.Raw)
						case "response.output_item.done":
							if data.Get("item.call_id").String() == "call_a" {
								t.Fatalf("unclosed tool call reported done: %s", data.Raw)
							}
						case "response.failed":
							failures++
							if data.Get("response.error.code").String() != "invalid_tool_arguments" || strings.Contains(data.Raw, "SECRET_PARTIAL_VALUE") {
								t.Fatalf("invalid or unsafe failure: %s", data.Raw)
							}
						}
					}
				}
				if failures != 1 {
					t.Fatalf("got %d failures, want exactly one", failures)
				}
				if later := ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(`[DONE]`), &param); len(later) != 0 {
					t.Fatalf("terminal reopened: %s", later)
				}
			})
		}
	}
}

func TestTerminalPartialArgumentExplicitClosuresSucceed(t *testing.T) {
	for _, tool := range []struct{ name, request, function, path, quotedPath, done, field, want string }{
		{"function", `{"tools":[{"type":"function","name":"get_weather"}]}`, "get_weather", "$.city", `$['city']`, "response.function_call_arguments.done", "arguments", `{"city":"SECRET_PARTIAL_VALUErest"}`},
		{"apply_patch", geminiPatchRequest, "functions__apply_patch", "$.input", `$['input']`, "response.custom_tool_call_input.done", "input", "SECRET_PARTIAL_VALUErest"},
	} {
		for _, mode := range []string{"same_frame", "alternate_path", "explicit_end"} {
			t.Run(tool.name+"/"+mode, func(t *testing.T) {
				first := terminalIncompleteCallFrame(tool.function, tool.quotedPath, "")
				last := fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"partialArgs":[{"jsonPath":%q,"stringValue":"rest"}],"willContinue":false}}]},"finishReason":"STOP"}]}`, tool.path)
				want := tool.want
				frames := []string{first, last}
				if mode == "same_frame" {
					frames = []string{fmt.Sprintf(`data: {"responseId":"resp_incomplete_tool","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":%q,"partialArgs":[{"jsonPath":%q,"stringValue":"SECRET_PARTIAL_VALUE","willContinue":true},{"jsonPath":%q,"stringValue":"rest"}],"willContinue":false}}]},"finishReason":"STOP"}]}`, tool.function, tool.quotedPath, tool.path)}
				} else if mode == "explicit_end" {
					frames[1] = fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"partialArgs":[{"jsonPath":%q}],"willContinue":false}}]},"finishReason":"STOP"}]}`, tool.path)
					want = strings.Replace(want, "rest", "", 1)
				}
				var param any
				var done, completed int
				for _, frame := range frames {
					for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini", []byte(tool.request), nil, []byte(frame), &param) {
						kind, data := parseSSEEvent(t, chunk)
						if kind == "response.failed" {
							t.Fatalf("closed argument rejected: %s", data.Raw)
						}
						if kind == tool.done {
							done++
							if got := data.Get(tool.field).String(); got != want {
								t.Fatalf("input = %q, want %q", got, want)
							}
						}
						if kind == "response.completed" {
							completed++
						}
					}
				}
				if done != 1 || completed != 1 {
					t.Fatalf("done=%d completed=%d, want one each", done, completed)
				}
			})
		}
	}
}
