package responsestools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestAdditionalToolPositionSemantics(t *testing.T) {
	message := map[string]any{"type": "message", "role": "user", "content": "private prompt"}
	declaration := map[string]any{"type": "additional_tools", "tools": []any{
		map[string]any{"type": "function", "name": "private_lookup", "parameters": map[string]any{"type": "object"}},
	}}
	for _, tc := range []struct {
		name        string
		input       []any
		indices     []int
		interleaved bool
	}{
		{"initial", []any{declaration, message}, []int{0}, false},
		{"middle", []any{message, declaration, message}, []int{1}, true},
		{"multiple", []any{declaration, message, declaration, message}, []int{0, 2}, true},
		{"trailing", []any{message, declaration}, []int{1}, false},
	} {
		for _, portable := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/native", true: "/portable"}[portable], func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"input": tc.input})
				if err != nil {
					t.Fatal(err)
				}
				policy := RoutePolicy{ClientSearch: ClientSearchNative}
				if portable {
					policy.CustomTools = CustomToolsFunction
				}
				limits := DefaultLimits()
				prepared, err := Prepare(body, policy, limits, NewLimiter(limits))
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Attempt != nil {
					defer prepared.Attempt.Close()
				}
				report := prepared.Transformation
				if !reflect.DeepEqual(report.Positions.DeclarationIndices, tc.indices) || report.Positions.Interleaved != tc.interleaved {
					t.Fatalf("wrong original positions: %+v", report)
				}
				if report.PositionPreserved == portable {
					t.Fatalf("position preservation misreported: %+v", report)
				}
				if portable {
					if !reflect.DeepEqual(report.Losses, []TransformationLoss{LossAdditionalToolsPosition}) {
						t.Fatalf("missing position loss: %+v", report)
					}
					if bytes.Contains(prepared.Body, []byte("additional_tools")) {
						t.Fatal("declarations not lifted")
					}
				} else if !bytes.Equal(prepared.Body, body) || len(report.Losses) != 0 {
					t.Fatal("native passthrough changed request or reported a loss")
				}
				encoded, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(encoded, []byte("private")) {
					t.Fatal("diagnostics contain client content")
				}
			})
		}
	}
}

func TestAdditionalToolPositionIdentityChanges(t *testing.T) {
	for _, tc := range []struct {
		name, tools, extra string
		changed            bool
	}{
		{"same", `{"type":"function","name":"lookup","parameters":{"type":"object"}}`, `{"type":"function","name":"lookup","parameters":{"type":"object"}}`, false},
		{"changed schema", `{"type":"function","name":"lookup","parameters":{"type":"object"}}`, `{"type":"function","name":"lookup","parameters":{"type":"string"}}`, true},
		{"namespace siblings", `{"type":"namespace","name":"one","tools":[{"type":"function","name":"a"}]}`, `{"type":"namespace","name":"one","tools":[{"type":"function","name":"b"}]}`, false},
		{"namespace leaf changed", `{"type":"namespace","name":"one","tools":[{"type":"function","name":"a","parameters":{"type":"object"}}]}`, `{"type":"namespace","name":"one","tools":[{"type":"function","name":"a","parameters":{"type":"string"}}]}`, true},
		{"different namespaces", `{"type":"namespace","name":"one","tools":[{"type":"function","name":"a"}]}`, `{"type":"namespace","name":"two","tools":[{"type":"function","name":"a","parameters":{"type":"string"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, ok := decodeValue([]byte(`{"tools":[` + tc.tools + `],"input":[{"type":"additional_tools","tools":[` + tc.extra + `]}]}`))
			if !ok {
				t.Fatal("invalid fixture")
			}
			analysis := AnalyzeToolPositions(value.(map[string]any))
			if analysis.SameIdentityChanged != tc.changed {
				t.Fatalf("wrong identity change: %+v", analysis)
			}
		})
	}
}
