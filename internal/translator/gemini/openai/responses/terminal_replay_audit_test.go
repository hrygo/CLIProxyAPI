package responses

import "testing"

func TestReplayAuditSingleUsageTailCompletesOnceWithContent(t *testing.T) {
	observed := observeTerminalStream(t, terminalContentFrame, terminalFrame("STOP", ""),
		`data: {`+terminalUsageObject+`,"responseId":"terminal"}`, terminalDoneFrame)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	if len(observed.ItemDone) != 1 || observed.TextDones != 1 {
		t.Fatalf("closures: items=%d texts=%d", len(observed.ItemDone), observed.TextDones)
	}
	if got := terminal.Get("response.output.0.content.0.text").String(); got != "answer" {
		t.Fatalf("output text = %q, want answer", got)
	}
	usage := terminal.Get("response.usage")
	if usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 160 {
		t.Fatalf("usage = %s", usage.Raw)
	}
	requireStrictlyIncreasing(t, observed)
}

func TestReplayAuditPartialUsageTailsRetainLaterFields(t *testing.T) {
	observed := observeTerminalStream(t, terminalContentFrame, terminalFrame("STOP", ""),
		`data: {"usageMetadata":{"promptTokenCount":100},"responseId":"terminal"}`,
		`data: {"usageMetadata":{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160},"responseId":"terminal"}`,
		terminalDoneFrame)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	usage := terminal.Get("response.usage")
	if usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("total_tokens").Int() != 160 {
		t.Fatalf("usage = %s, want input=100 output=60 total=160", usage.Raw)
	}
}
