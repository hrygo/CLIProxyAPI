package claude

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeToolResultsRespectGLMAndOtherRoutes(t *testing.T) {
	input := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"lookup","name":"ledger_lookup","input":{"project":"alpha"}},
			{"type":"tool_use","id":"confirm","name":"ledger_confirm","input":{"project":"alpha"}}]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"lookup","content":"actual lookup receipt"},
			{"type":"tool_result","tool_use_id":"confirm","content":"actual confirmation receipt"}]}]}`)
	for _, tc := range []struct {
		model  string
		strict bool
	}{
		{model: "glm-5.3-flash", strict: true},
		{model: " GLM-5.3-FLASH ", strict: true},
		{model: "gemini-2.5-pro"},
		{model: "gpt-5.5"},
	} {
		for _, stream := range []bool{false, true} {
			for _, compat := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/compat=%t", tc.model, stream, compat), func(t *testing.T) {
					var output []byte
					var err error
					if compat {
						output, err = ConvertClaudeRequestToOpenAIWithCompat(tc.model, input, stream)
					} else {
						output, err = ConvertClaudeRequestToOpenAI(tc.model, input, stream)
					}
					if err != nil {
						t.Fatal(err)
					}
					messages := gjson.GetBytes(output, "messages").Array()
					if len(messages) != 3 {
						t.Fatalf("messages = %d, want 3", len(messages))
					}
					for index, name := range []string{"ledger_lookup", "ledger_confirm"} {
						call := messages[0].Get(fmt.Sprintf("tool_calls.%d", index))
						result := messages[index+1]
						if call.Get("function.name").String() != name || result.Get("tool_call_id").String() != call.Get("id").String() || result.Get("role").String() != "tool" {
							t.Fatalf("tool identity changed: %s", result.Raw)
						}
						if tc.strict {
							if result.Get("name").Exists() || len(result.Map()) != 3 {
								t.Fatalf("GLM result contains unsupported fields: %s", result.Raw)
							}
						} else if result.Get("name").String() != name {
							t.Fatalf("other route lost its tool name: %s", result.Raw)
						}
						want := "actual lookup receipt"
						if index == 1 {
							want = "actual confirmation receipt"
						}
						if result.Get("content").String() != want {
							t.Fatalf("tool result content changed: %s", result.Raw)
						}
					}
				})
			}
		}
	}
}
