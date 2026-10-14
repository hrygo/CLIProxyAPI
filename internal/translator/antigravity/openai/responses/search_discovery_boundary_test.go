package responses

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	"github.com/tidwall/gjson"
)

func TestSearchDiscoveryBoundaryPreservesClientSelection(t *testing.T) {
	for _, name := range []string{"collection_01_read", "ledger_budget_lookup"} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"tool_search_call","id":"tsc_search","call_id":"search","arguments":{"query":"budget"}},{"type":"tool_search_output","id":"tso_search","call_id":"search","tools":[{"type":"namespace","name":"mcp__bench","tools":[{"type":"function","name":"` + name + `","description":"Authoritative fixture lookup","parameters":{"type":"object","properties":{"project":{"type":"string"}},"required":["project"]}}]}]}]}`)
			policy := responsestools.ConventionPolicy(responsestools.Route{Provider: "antigravity", UpstreamFormat: "antigravity"})
			prepared, err := responsestools.Prepare(body, policy, responsestools.DefaultLimits(), responsestools.NewLimiter(responsestools.DefaultLimits()))
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Attempt == nil {
				t.Fatal("missing search bridge")
			}
			defer prepared.Attempt.Close()
			wire := ConvertOpenAIResponsesRequestToAntigravity("gemini-3.5-flash-lite", prepared.Body, true)
			declarations := gjson.GetBytes(wire, "request.tools.0.functionDeclarations").Array()
			if len(declarations) != 2 {
				t.Fatalf("expected search and one discovered tool: %s", wire)
			}
			selected := declarations[1]
			if selected.Get("description").String() != "Authoritative fixture lookup" || selected.Get("parametersJsonSchema.properties.project.type").String() != "string" {
				t.Fatalf("discovered schema lost: %s", wire)
			}
			contract := responsestools.ParseContract(body)
			var alias string
			for _, id := range contract.Identities {
				if id.Name == name {
					alias = contract.AliasByID[id]
				}
			}
			if selected.Get("name").String() != alias {
				t.Fatalf("selection changed: got %s want %s", selected.Raw, alias)
			}
			foundCall, foundResult := false, false
			for _, content := range gjson.GetBytes(wire, "request.contents").Array() {
				for _, part := range content.Get("parts").Array() {
					if part.Get("functionCall.args.query").String() == "budget" {
						foundCall = true
					}
					if part.Get("functionResponse").Exists() {
						foundResult = true
					}
				}
			}
			if !foundCall || !foundResult {
				t.Fatalf("search call/result disappeared: %s", wire)
			}
		})
	}
}
