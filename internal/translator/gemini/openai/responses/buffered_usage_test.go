package responses

import (
	"context"
	"testing"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
	"github.com/tidwall/gjson"
)

func TestResponsesUsesBufferedUsageOnlyForOwningFinish(t *testing.T) {
	const finish = `data: {"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":9,"totalTokenCount":109}}`
	snapshots := [][]byte{
		[]byte(`{"usageMetadata":{"candidatesTokenCount":10}}`),
		[]byte(`{"usageMetadata":{"totalTokenCount":110}}`),
	}
	var owner, other any
	ctx := translatorcommon.WithGeminiBufferedUsage(context.Background(), &owner, snapshots)
	convert := func(param *any, frame string) gjson.Result {
		t.Helper()
		var terminal gjson.Result
		for _, chunk := range ConvertGeminiResponseToOpenAIResponses(ctx, "gemini-test", nil, nil, []byte(frame), param) {
			name, event := parseSSEEvent(t, chunk)
			if name == "response.completed" {
				terminal = event
			}
		}
		if !terminal.Exists() {
			t.Fatal("missing completed terminal")
		}
		return terminal.Get("response.usage")
	}
	otherUsage := convert(&other, finish)
	if otherUsage.Get("input_tokens").Int() != 100 || otherUsage.Get("output_tokens").Int() != 9 || otherUsage.Get("total_tokens").Int() != 109 {
		t.Fatalf("buffered usage leaked into another stream: %s", otherUsage.Raw)
	}
	ownerUsage := convert(&owner, finish)
	if ownerUsage.Get("input_tokens").Int() != 100 || ownerUsage.Get("output_tokens").Int() != 10 || ownerUsage.Get("total_tokens").Int() != 110 {
		t.Fatalf("owner did not merge buffered snapshots in order: %s", ownerUsage.Raw)
	}
}

func TestResponsesDoesNotApplyBufferedUsageBeforeFinish(t *testing.T) {
	var state any
	ctx := translatorcommon.WithGeminiBufferedUsage(context.Background(), &state, [][]byte{[]byte(`{"usageMetadata":{"promptTokenCount":100,"totalTokenCount":100}}`)})
	content := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`)
	_ = ConvertGeminiResponseToOpenAIResponses(ctx, "gemini-test", nil, nil, content, &state)
	var terminal gjson.Result
	for _, chunk := range ConvertGeminiResponseToOpenAIResponses(context.Background(), "gemini-test", nil, nil, []byte(terminalFrame("STOP", "")), &state) {
		name, event := parseSSEEvent(t, chunk)
		if name == "response.completed" {
			terminal = event
		}
	}
	if !terminal.Exists() || terminal.Get("response.usage").Exists() {
		t.Fatalf("usage was applied to a frame without a finish: %s", terminal.Raw)
	}
}
