package responsestools

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestReplayRequirementMatchesActualPreparation(t *testing.T) {
	for _, tc := range []struct {
		name, provider, body string
		replay               bool
	}{
		{"plain", "meta", `{"input":"hello"}`, false},
		{"ordinary function", "meta", `{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"input":[]}`, false},
		{"native search", "codex", `{"tools":[{"type":"tool_search"}],"input":[]}`, false},
		{"request-only item repair", "codex", `{"input":[{"type":"function_call","id":"ctc_x","call_id":"c1","name":"lookup","arguments":"{}"}]}`, true},
		{"opaque item repair rejection", "codex", `{"previous_response_id":"resp_1","input":[{"type":"function_call","id":"ctc_x","call_id":"c1","name":"lookup","arguments":"{}"}]}`, true},
		{"portable search no discoveries", "meta", `{"tools":[{"type":"tool_search"}],"input":[]}`, false},
		{"bridged search", "gemini", `{"tools":[{"type":"tool_search"}],"input":[]}`, true},
		{"lite native response repair", "codex", `{"input":[{"type":"additional_tools","tools":[{"type":"tool_search"}]}]}`, true},
		{"lite eager portable lift", "meta", `{"input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]}`, true},
		{"ordinary recursive schema", "meta", `{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}}],"input":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := ConventionPolicy(Route{Provider: tc.provider, UpstreamFormat: "openai-response"})
			body := []byte(tc.body)
			before := bytes.Clone(body)
			if got := RequiresToolReplay(body, policy, DefaultLimits()); got != tc.replay {
				t.Fatalf("requires replay=%v want %v", got, tc.replay)
			}
			if !bytes.Equal(before, body) {
				t.Fatal("routing probe mutated caller payload")
			}
		})
	}
}

func BenchmarkReplayRequirement(b *testing.B) {
	var ordinary, deferred []string
	for i := 0; i < 128; i++ {
		ordinary = append(ordinary, fmt.Sprintf(`{"type":"function","name":"lookup_%d","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}`, i))
		deferred = append(deferred, fmt.Sprintf(`{"type":"function","name":"lookup_%d","defer_loading":true,"parameters":{"type":"object","properties":{"query":{"type":"string"}}}}`, i))
	}
	for _, tc := range []struct {
		name, provider, body string
		want                 bool
	}{
		{"native plain", "codex", `{"input":"hello"}`, false},
		{"native search", "codex", `{"tools":[{"type":"tool_search"}],"input":[]}`, false},
		{"portable ordinary 128", "meta", `{"tools":[` + strings.Join(ordinary, ",") + `],"input":[]}`, false},
		{"bridge deferred 128", "gemini", `{"tools":[{"type":"tool_search"},` + strings.Join(deferred, ",") + `],"input":[]}`, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			payload := []byte(tc.body)
			policy := ConventionPolicy(Route{Provider: tc.provider, UpstreamFormat: "openai-response"})
			if got := RequiresToolReplay(payload, policy, DefaultLimits()); got != tc.want {
				b.Fatalf("probe=%v want %v", got, tc.want)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				RequiresToolReplay(payload, policy, DefaultLimits())
			}
		})
	}
}
