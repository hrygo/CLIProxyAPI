package helps

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestGeminiStreamInvalidTokenFieldsKeepKnownSnapshot(t *testing.T) {
	for _, invalid := range []string{`null`, `"not a number"`, `"1.5"`, `"1e-1"`, `[]`, `-1`, `1.5`, `9223372036854775808`, `true`} {
		for _, keys := range []struct{ prompt, candidate, thought, total, cached, tool string }{
			{"promptTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount", "cachedContentTokenCount", "toolUsePromptTokenCount"},
			{"prompt_token_count", "candidates_token_count", "thoughts_token_count", "total_token_count", "cached_content_token_count", "tool_use_prompt_token_count"},
		} {
			t.Run(keys.prompt+"/"+invalid, func(t *testing.T) {
				var buffer StreamUsageBuffer
				buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164,"cachedContentTokenCount":7,"toolUsePromptTokenCount":4}}`))
				metadata := `{"` + keys.prompt + `":` + invalid + `,"` + keys.candidate + `":` + invalid + `,"` + keys.thought + `":` + invalid + `,"` + keys.total + `":` + invalid + `,"` + keys.cached + `":` + invalid + `,"` + keys.tool + `":` + invalid + `}`
				buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":` + metadata + `}`))
				detail, ok := buffer.Detail()
				if !ok || detail.InputTokens != 104 || detail.OutputTokens != 10 || detail.ReasoningTokens != 50 || detail.TotalTokens != 164 || detail.CachedTokens != 7 {
					t.Fatalf("invalid fields changed known counts: %+v", detail)
				}
			})
		}
	}
}

func TestGeminiStreamInvalidUnknownCounterIsNotSynthesized(t *testing.T) {
	for _, invalid := range []string{`null`, `"not a number"`, `"1.5"`, `"1e-1"`, `[]`, `-1`, `1.5`, `9223372036854775808`, `true`} {
		t.Run(invalid, func(t *testing.T) {
			var counts geminiStreamTokenCounts
			merged := counts.merge(gjson.Parse(`{"promptTokenCount":` + invalid + `,"candidates_token_count":` + invalid + `}`))
			if counts.prompt.present || counts.candidates.present || merged.Get("promptTokenCount").Exists() || merged.Get("candidates_token_count").Exists() {
				t.Fatalf("unknown invalid counter was treated as measured: %s", merged.Raw)
			}
		})
	}
}

func TestGeminiStreamCamelPrecedenceAndExplicitZero(t *testing.T) {
	var counts geminiStreamTokenCounts
	counts.merge(gjson.Parse(`{"promptTokenCount":100}`))
	merged := counts.merge(gjson.Parse(`{"promptTokenCount":null,"prompt_token_count":99}`))
	if !counts.prompt.present || counts.prompt.value != 100 || merged.Get("promptTokenCount").Int() != 100 || merged.Get("prompt_token_count").Exists() {
		t.Fatalf("invalid preferred camel field accepted snake alias: %s", merged.Raw)
	}
	merged = counts.merge(gjson.Parse(`{"promptTokenCount":0,"prompt_token_count":99}`))
	if counts.prompt.value != 0 || merged.Get("promptTokenCount").Int() != 0 || merged.Get("prompt_token_count").Exists() {
		t.Fatalf("explicit zero failed camel precedence: %s", merged.Raw)
	}
}

func TestGeminiStreamInitialExplicitZeroIsObserved(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`))
	if _, ok := buffer.Detail(); !ok {
		t.Fatal("explicit zero usage was treated as absent")
	}
}

func TestGeminiStreamProtoJSONUsageCountsStayExact(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":"9.007199254740993e15","candidatesTokenCount":"1e2","thoughtsTokenCount":1.0,"totalTokenCount":9007199254741094}}`))
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 9007199254740993 || detail.OutputTokens != 100 || detail.ReasoningTokens != 1 || detail.TotalTokens != 9007199254741094 {
		t.Fatalf("ProtoJSON counts lost precision: %+v", detail)
	}
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"candidatesTokenCount":"0e100","thoughtsTokenCount":0.0}}`))
	detail, ok = buffer.Detail()
	if !ok || detail.InputTokens != 9007199254740993 || detail.OutputTokens != 0 || detail.ReasoningTokens != 0 {
		t.Fatalf("explicit ProtoJSON zero did not replace counts: %+v", detail)
	}
}
