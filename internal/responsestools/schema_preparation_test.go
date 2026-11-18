package responsestools

import (
	"bytes"
	"testing"
)

func TestPrepareOrdinaryFunctionSchemaPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   LocalRefsMode
		schema string
		reject bool
	}{
		{"inline", LocalRefsInline, `{"type":"object","$defs":{"Number":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/Number"}}}`, false},
		{"flatten", LocalRefsFlatten, `{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}`, false},
		{"recursive inline rejected", LocalRefsInline, `{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"function","name":"lookup","parameters":` + tc.schema + `}],"input":[]}`)
			limits := DefaultLimits()
			limiter := NewLimiter(limits)
			prepared, err := Prepare(body, RoutePolicy{ClientSearch: ClientSearchNative, Schema: SchemaPolicy{LocalRefs: tc.mode}}, limits, limiter)
			if prepared.Attempt != nil {
				prepared.Attempt.Close()
			}
			if tc.reject {
				if err == nil {
					t.Fatal("ordinary recursive schema escaped inline rejection")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Attempt == nil || bytes.Equal(body, prepared.Body) {
					t.Fatal("ordinary function bypassed effective schema policy")
				}
				if !bytes.Contains(prepared.Body, []byte(`"name":"lookup"`)) {
					t.Fatal("schema adaptation changed function identity")
				}
			}
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("schema preparation leaked lease: %d/%d", n, b)
			}
		})
	}
}

func TestPrepareOrdinarySchemaPolicyWithEscapedDeclarationKey(t *testing.T) {
	body := []byte(`{"to\u006fls":[{"type":"function","name":"lookup","parameters":{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}}]}`)
	policy := ConventionPolicy(Route{Provider: "meta", UpstreamFormat: "openai-response"})
	prepared, err := Prepare(body, policy, DefaultLimits(), NewLimiter(DefaultLimits()))
	if prepared.Attempt != nil {
		defer prepared.Attempt.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Attempt == nil || bytes.Equal(body, prepared.Body) {
		t.Fatal("a valid escaped JSON declaration key bypassed the effective schema policy")
	}
}
