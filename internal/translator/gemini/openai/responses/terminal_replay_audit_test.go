package responses

import "testing"

func TestReplayAuditSingleUsageCompletesOnceWithContent(t *testing.T) {
	usageFrame := `data: {` + terminalUsageObject + `,"responseId":"terminal"}`
	for _, tc := range []struct {
		name      string
		frames    []string
		wantUsage bool
	}{
		{"before_finish", []string{terminalContentFrame, usageFrame, terminalFrame("STOP", ""), terminalDoneFrame}, true},
		{"on_finish", []string{terminalContentFrame, terminalFrame("STOP", ","+terminalUsageObject), terminalDoneFrame}, true},
		{"after_finish", []string{terminalContentFrame, terminalFrame("STOP", ""), usageFrame, terminalDoneFrame}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := observeTerminalStream(t, tc.frames...)
			terminal := requireSingleTerminal(t, observed, "response.completed")
			if len(observed.ItemDone) != 1 || observed.TextDones != 1 {
				t.Fatalf("closures: items=%d texts=%d", len(observed.ItemDone), observed.TextDones)
			}
			if got := terminal.Get("response.output.0.content.0.text").String(); got != "answer" {
				t.Fatalf("output text = %q, want answer", got)
			}
			usage := terminal.Get("response.usage")
			if !tc.wantUsage {
				if usage.Exists() {
					t.Fatalf("finish without observed usage fabricated usage: %s", usage.Raw)
				}
			} else if !usage.Exists() || usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("output_tokens_details.reasoning_tokens").Int() != 50 || usage.Get("total_tokens").Int() != 160 {
				t.Fatalf("usage = %s", usage.Raw)
			}
			requireStrictlyIncreasing(t, observed)
		})
	}
}

func TestReplayAuditPartialUsageBeforeFinishRetainsLaterFields(t *testing.T) {
	observed := observeTerminalStream(t, terminalContentFrame,
		`data: {"usageMetadata":{"promptTokenCount":100},"responseId":"terminal"}`,
		`data: {"usageMetadata":{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160},"responseId":"terminal"}`,
		terminalFrame("STOP", ""), terminalDoneFrame)
	terminal := requireSingleTerminal(t, observed, "response.completed")
	usage := terminal.Get("response.usage")
	if !usage.Exists() || usage.Get("input_tokens").Int() != 100 || usage.Get("output_tokens").Int() != 60 || usage.Get("output_tokens_details.reasoning_tokens").Int() != 50 || usage.Get("total_tokens").Int() != 160 {
		t.Fatalf("usage = %s, want input=100 output=60 reasoning=50 total=160", usage.Raw)
	}
	if len(observed.ItemDone) != 1 || observed.TextDones != 1 || terminal.Get("response.output.0.content.0.text").String() != "answer" {
		t.Fatalf("partial usage lost content or duplicated closures: %s", terminal.Raw)
	}
	requireStrictlyIncreasing(t, observed)
}

func TestReplayAuditPartialUsageAfterFinishCannotChangeTerminal(t *testing.T) {
	first := `data: {"usageMetadata":{"promptTokenCount":100},"responseId":"terminal"}`
	later := `data: {"usageMetadata":{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160},"responseId":"terminal"}`
	finish := terminalFrame("STOP", "")
	before := observeTerminalStream(t, terminalContentFrame, first, finish)
	after := observeTerminalStream(t, terminalContentFrame, first, finish, later, terminalDoneFrame)
	beforeTerminal := requireSingleTerminal(t, before, "response.completed")
	afterTerminal := requireSingleTerminal(t, after, "response.completed")
	beforeUsage := beforeTerminal.Get("response.usage")
	afterUsage := afterTerminal.Get("response.usage")
	// This compares observed snapshots; unobserved counters are not measured zeros.
	if !beforeUsage.Exists() || beforeUsage.Get("input_tokens").Int() != 100 || beforeUsage.Raw != afterUsage.Raw {
		t.Fatalf("usage changed after finish: before=%s after=%s", beforeUsage.Raw, afterUsage.Raw)
	}
	if len(after.ItemDone) != 1 || after.TextDones != 1 || afterTerminal.Get("response.output.0.content.0.text").String() != "answer" {
		t.Fatalf("late usage changed content or closures: %s", afterTerminal.Raw)
	}
	requireStrictlyIncreasing(t, after)
}
