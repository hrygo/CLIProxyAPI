package helps

import (
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestGeminiStreamPartialUsageUpdatesPreservePresentFields(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			var buffer StreamUsageBuffer
			observe := func(metadata string) {
				payload := `{"usageMetadata":` + metadata + `}`
				if wrapped {
					buffer.ObserveAntigravityStream([]byte(`data: {"response":` + payload + `}`))
				} else {
					buffer.ObserveGeminiStream([]byte("data: " + payload))
				}
			}
			assertTokens := func(input, output, reasoning, cached, total int64) {
				t.Helper()
				detail, ok := buffer.Detail()
				if !ok {
					t.Fatal("no usage observed")
				}
				if detail.InputTokens != input || detail.OutputTokens != output || detail.ReasoningTokens != reasoning ||
					detail.CachedTokens != cached || detail.CacheReadTokens != cached || detail.TotalTokens != total {
					t.Fatalf("usage = %+v, want input=%d output=%d reasoning=%d cached=%d total=%d", detail, input, output, reasoning, cached, total)
				}
				if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.TotalTokens != total {
					t.Fatalf("invalid token breakdown: %+v", detail.TokenBreakdown)
				}
			}

			observe(`{"promptTokenCount":100,"toolUsePromptTokenCount":4,"cachedContentTokenCount":7}`)
			observe(`{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":164}`)
			assertTokens(104, 10, 50, 7, 164)
			detail, _ := buffer.Detail()
			if detail.ToolUsageDetails == nil || detail.ToolUsageDetails.Extra["tool_use_prompt_tokens"] != 4 {
				t.Fatalf("lost tool-use input tokens: %+v", detail.ToolUsageDetails)
			}

			// Snake-case keys and explicit zeros must replace prior observations;
			// absent fields must keep their previous value.
			observe(`{"candidates_token_count":12,"thoughts_token_count":0,"cached_content_token_count":0,"tool_use_prompt_token_count":0,"total_token_count":112}`)
			assertTokens(100, 12, 0, 0, 112)
			detail, _ = buffer.Detail()
			if detail.ToolUsageDetails == nil || detail.ToolUsageDetails.Extra["tool_use_prompt_tokens"] != 0 {
				t.Fatalf("explicit zero did not clear tool-use input tokens: %+v", detail.ToolUsageDetails)
			}
			observe(`{"totalTokenCount":0}`)
			assertTokens(100, 12, 0, 0, 0)
			detail, _ = buffer.Detail()
			if detail.TokenBreakdown.Quality != usage.TokenAccountingQualityInconsistent {
				t.Fatalf("contradictory zero total must be marked inconsistent: %+v", detail.TokenBreakdown)
			}

			// A zero-only snapshot must not be mistaken for absent usage.
			observe(`{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}`)
			assertTokens(0, 0, 0, 0, 0)
		})
	}
}

func TestGeminiStreamPartialUsageKeepsStructuredMetadata(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":10,"trafficType":"ON_DEMAND","promptTokensDetails":[{"modality":"IMAGE","tokenCount":10}]}}`))
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"candidatesTokenCount":2,"totalTokenCount":12}}`))
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 10 || detail.OutputTokens != 2 || detail.TotalTokens != 12 ||
		detail.ImageInputTokens == nil || *detail.ImageInputTokens != 10 || detail.UpstreamServiceTier != "ON_DEMAND" ||
		detail.TokenBreakdown.Quality != usage.TokenAccountingQualityComplete {
		t.Fatalf("lost token or structured usage: %+v", detail)
	}
}

func TestGeminiStreamZeroUsageWithTierReplacesPriorTokens(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"totalTokenCount":110}}`))
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0,"serviceTier":"STANDARD"}}`))
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 0 || detail.OutputTokens != 0 || detail.TotalTokens != 0 || detail.ResponseServiceTier != "STANDARD" {
		t.Fatalf("zero snapshot with tier did not replace prior usage: %+v", detail)
	}
}

func TestGeminiStreamIgnoresNonObjectUsage(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"totalTokenCount":110}}`))
	buffer.ObserveGeminiStream([]byte(`data: {"usageMetadata":42}`))
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 100 || detail.OutputTokens != 10 || detail.TotalTokens != 110 {
		t.Fatalf("non-object usage changed previous usage: %+v", detail)
	}
}
