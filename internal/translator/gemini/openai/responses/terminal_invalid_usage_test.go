package responses

import (
	"context"
	"testing"
)

func TestTerminalNonObjectUsageCompletesWithoutInventingCounts(t *testing.T) {
	for _, tc := range []struct {
		name, value string
	}{
		{name: "null", value: `null`},
		{name: "string", value: `"not usage"`},
		{name: "array", value: `[]`},
		{name: "number", value: `42`},
	} {
		for _, usageKey := range []string{"usageMetadata", "cpaUsageMetadata"} {
			for _, priorUsage := range []bool{false, true} {
				name := tc.name + "/" + usageKey + "/no_prior"
				if priorUsage {
					name = tc.name + "/" + usageKey + "/prior_snapshot"
				}
				t.Run(name, func(t *testing.T) {
					var param any
					translate := func(frame string) [][]byte {
						return ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini-3.8-flash", nil, nil, []byte(frame), &param)
					}
					translate(terminalContentFrame)
					if priorUsage {
						translate(`data: {` + terminalUsageObject + `}`)
					}
					chunks := translate(terminalFrame("STOP", `,"`+usageKey+`":`+tc.value))
					terminals := 0
					for _, chunk := range chunks {
						event, data := parseSSEEvent(t, chunk)
						if event != "response.completed" {
							continue
						}
						terminals++
						usage := data.Get("response.usage")
						if !priorUsage && usage.Exists() {
							t.Fatalf("non-object usage invented a usage object: %s", usage.Raw)
						}
						if priorUsage && (usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 160) {
							t.Fatalf("non-object usage changed previous snapshot: %s", usage.Raw)
						}
					}
					if terminals != 1 {
						t.Fatalf("terminal events = %d, want 1 without waiting for EOF", terminals)
					}
				})
			}
		}
	}
}

func TestInvalidUsageBeforeFinishDoesNotBlockValidSnapshot(t *testing.T) {
	for _, invalid := range []string{`null`, `"not usage"`, `[]`, `42`} {
		for _, usageKey := range []string{"usageMetadata", "cpaUsageMetadata"} {
			t.Run(usageKey+"/"+invalid, func(t *testing.T) {
				observed := observeTerminalStream(t,
					terminalContentFrame,
					`data: {"`+usageKey+`":`+invalid+`}`,
					terminalFrame("STOP", ","+terminalUsageObject),
				)
				terminal := requireSingleTerminal(t, observed, "response.completed")
				usage := terminal.Get("response.usage")
				if usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 160 {
					t.Fatalf("terminal usage = %s, want input=100 output=60 total=160", usage.Raw)
				}
			})
		}
	}
}

func TestNonObjectUsageTailCompletesWithoutInventingCounts(t *testing.T) {
	for _, invalid := range []string{`null`, `"not usage"`, `[]`} {
		t.Run(invalid, func(t *testing.T) {
			observed := observeTerminalStream(t, terminalContentFrame, terminalFrame("STOP", ""),
				`data: {"usageMetadata":`+invalid+`}`)
			terminal := requireSingleTerminal(t, observed, "response.completed")
			if usage := terminal.Get("response.usage"); usage.Exists() {
				t.Fatalf("non-object usage tail invented counts: %s", usage.Raw)
			}
		})
	}
}

func TestTerminalInvalidTokenFieldsPreserveKnownCounts(t *testing.T) {
	for _, invalid := range []string{`null`, `"not a number"`, `"1.5"`, `"1e-1"`, `[]`, `-1`, `1.5`, `9223372036854775808`, `true`} {
		t.Run(invalid, func(t *testing.T) {
			observed := observeTerminalStream(t,
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160,"cachedContentTokenCount":7}}`,
				`data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":`+invalid+`,"candidatesTokenCount":`+invalid+`,"thoughtsTokenCount":`+invalid+`,"totalTokenCount":`+invalid+`,"cachedContentTokenCount":`+invalid+`}}`,
			)
			terminal := requireSingleTerminal(t, observed, "response.completed")
			usage := terminal.Get("response.usage")
			if usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 160 || usage.Get("input_tokens_details.cached_tokens").Int() != 7 {
				t.Fatalf("invalid counters overwrote snapshot: %s", usage.Raw)
			}
		})
	}
}

func TestTerminalInvalidUnknownTokenFieldsOmitUsage(t *testing.T) {
	for _, invalid := range []string{`null`, `"not a number"`, `"1.5"`, `"1e-1"`, `[]`, `-1`, `1.5`, `9223372036854775808`, `true`} {
		t.Run(invalid, func(t *testing.T) {
			observed := observeTerminalStream(t, terminalContentFrame,
				`data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":`+invalid+`}}`,
			)
			terminal := requireSingleTerminal(t, observed, "response.completed")
			if usage := terminal.Get("response.usage"); usage.Exists() {
				t.Fatalf("invalid unknown counter produced usage: %s", usage.Raw)
			}
		})
	}
}

func TestInvalidUnknownCounterCanBeResolvedBeforeFinish(t *testing.T) {
	observed := observeTerminalStream(t,
		`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":null}}`,
		terminalFrame("STOP", `,"usageMetadata":{"candidatesTokenCount":0,"totalTokenCount":100}`),
	)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	usage := terminal.Get("response.usage")
	if !usage.Exists() || usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 0 || usage.Get("total_tokens").Int() != 100 {
		t.Fatalf("explicit zero did not resolve previously invalid counter: %s", usage.Raw)
	}
}

func TestTerminalUsageSnakeCaseAndCamelPrecedence(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "native"
		if wrapped {
			name = "wrapped"
		}
		t.Run(name, func(t *testing.T) {
			wrap := func(frame string) string {
				if wrapped {
					return `data: {"response":` + frame + `}`
				}
				return `data: ` + frame
			}
			observed := observeTerminalStream(t,
				wrap(`{"candidates":[{"content":{"parts":[{"text":"answer"}]}}],"usage_metadata":{"prompt_token_count":100,"candidates_token_count":10,"thoughts_token_count":50,"cached_content_token_count":7,"total_token_count":160}}`),
				wrap(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":null,"prompt_token_count":999,"candidatesTokenCount":0,"candidates_token_count":999}}`),
			)
			terminal := requireSingleTerminal(t, observed, "response.completed")
			usage := terminal.Get("response.usage")
			if usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 50 || usage.Get("input_tokens_details.cached_tokens").Int() != 7 || usage.Get("total_tokens").Int() != 160 {
				t.Fatalf("snake fields or camel precedence lost: %s", usage.Raw)
			}
		})
	}
}

func TestTerminalOutputTokenSumOverflowOmitsUsage(t *testing.T) {
	observed := observeTerminalStream(t, terminalContentFrame,
		terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1}`),
	)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	if usage := terminal.Get("response.usage"); usage.Exists() {
		t.Fatalf("overflow produced usage: %s", usage.Raw)
	}
}

func TestTerminalProtoJSONUsageCountsStayExact(t *testing.T) {
	observed := observeTerminalStream(t, terminalContentFrame,
		terminalFrame("STOP", `,"usageMetadata":{"promptTokenCount":9.007199254740993e15,"candidatesTokenCount":"1e2","thoughtsTokenCount":1.0,"totalTokenCount":9007199254741094}`),
	)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	usage := terminal.Get("response.usage")
	if usage.Get("input_tokens").Int() != 9007199254740993 || usage.Get("output_tokens").Int() != 101 || usage.Get("total_tokens").Int() != 9007199254741094 {
		t.Fatalf("ProtoJSON counts lost precision: %s", usage.Raw)
	}
}
