package responsestools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPrepareEscapedCustomMetadataKeepsGuards(t *testing.T) {
	const declaration = `{"type":"\u0063ustom","name":"exec"}`
	const call = `{"type":"\u0063ustom_tool_call","call_id":"c1","name":"exec","input":"ok"}`
	const result = `{"type":"\u0063ustom_tool_call_output","call_id":"c1","output":"done"}`
	for _, tc := range []struct {
		name   string
		mode   CustomToolsMode
		input  string
		reason Reason
	}{
		{"reject declaration", CustomToolsReject, "", ReasonUnsupportedProtocol},
		{"multiple outputs", CustomToolsFunction, call + "," + result + "," + result, ReasonUnsupportedProtocol},
		{"orphan output", CustomToolsFunction, result, ReasonInvalidCustomInput},
		{"unpaired input", CustomToolsFunction, strings.Replace(call, `"input":"ok"`, `"input":"\ud800"`, 1) + "," + result, ReasonInvalidCustomInput},
		{"invalid UTF-8 input", CustomToolsFunction, strings.Replace(call, "ok", string([]byte{0xff}), 1) + "," + result, ReasonInvalidCustomInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"tools":[` + declaration + `],"input":[` + tc.input + `]}`)
			policy := RoutePolicy{ClientSearch: ClientSearchNative, CustomTools: tc.mode, CustomGrammar: CustomGrammarDescribe}
			limits := DefaultLimits()
			limiter := NewLimiter(limits)
			prepared, err := Prepare(body, policy, limits, limiter)
			if prepared.Attempt != nil {
				prepared.Attempt.Close()
			}
			var compat *ToolCompatibilityError
			if !errors.As(err, &compat) || compat.Reason != tc.reason || len(prepared.Body) != 0 {
				t.Fatalf("escaped metadata bypassed %s: err=%v", tc.reason, err)
			}
			if n, b := limiter.Usage(); n != 0 || b != 0 {
				t.Fatalf("rejected request retained a lease: %d/%d", n, b)
			}
		})
	}
}

func TestCustomBufferedEscapedTypeRestoresIdentity(t *testing.T) {
	policy := RoutePolicy{ClientSearch: ClientSearchNative, CustomTools: CustomToolsFunction, CustomGrammar: CustomGrammarDescribe}
	limits := DefaultLimits()
	prepared, err := Prepare([]byte(`{"tools":[{"type":"custom","name":"exec"}],"input":[]}`), policy, limits, NewLimiter(limits))
	if err != nil || prepared.Attempt == nil {
		t.Fatalf("prepare control: %v", err)
	}
	defer prepared.Attempt.Close()
	alias, _ := prepared.Attempt.bridge.Alias(ToolIdentity{Name: "exec", Kind: ToolKindCustom})
	body := fmt.Sprintf(`{"output":[{"type":"function\u005fcall","id":"fc_escaped","name":%q,"call_id":"c1","arguments":"{\"input\":\"ok\"}"}]}`, alias)
	restored, err := prepared.Attempt.RewriteResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := decodeValue(restored)
	item := value.(map[string]any)["output"].([]any)[0].(map[string]any)
	if item["type"] != "custom_tool_call" || item["name"] != "exec" || item["input"] != "ok" || item["call_id"] != "c1" {
		t.Fatalf("escaped response leaked a bridge identity: %s", restored)
	}
}

func TestOrdinaryCustomMetadataIsNotProtocol(t *testing.T) {
	for index, body := range []string{
		`{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"metadata":{"const":{"type":"custom"}}}}}],"input":[]}`,
		`{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"input":[{"type":"function_call","name":"lookup","call_id":"c1","arguments":{"metadata":{"type":"custom_tool_call"}}}]}`,
	} {
		for _, escaped := range []bool{false, true} {
			t.Run(fmt.Sprintf("metadata_%d/escaped_%t", index, escaped), func(t *testing.T) {
				original := []byte(body)
				if escaped {
					original = []byte(strings.ReplaceAll(body, "custom", `\u0063ustom`))
				}
				policy := RoutePolicy{ClientSearch: ClientSearchNative, CustomTools: CustomToolsReject}
				prepared, err := Prepare(original, policy, DefaultLimits(), NewLimiter(DefaultLimits()))
				if prepared.Attempt != nil {
					prepared.Attempt.Close()
				}
				if err != nil || prepared.Attempt != nil || string(prepared.Body) != string(original) {
					t.Fatalf("ordinary metadata mistaken for custom protocol: escaped=%v err=%v", escaped, err)
				}
				if PayloadUsesToolProtocol(original) {
					t.Fatal("ordinary metadata selected managed protocol replay")
				}
			})
		}
	}
}

func TestCustomBridgeRejectsUnicodeLossAtJSONBoundary(t *testing.T) {
	policy := RoutePolicy{ClientSearch: ClientSearchNative, CustomTools: CustomToolsFunction, CustomGrammar: CustomGrammarDescribe}
	for _, bad := range []string{`\ud800`, string([]byte{0xff})} {
		request := `{"tools":[{"type":"custom","name":"apply_patch"}],"input":[{"type":"custom_tool_call","name":"apply_patch","call_id":"old","input":"` + bad + `"}]}`
		if prepared, err := Prepare([]byte(request), policy, DefaultLimits(), NewLimiter(DefaultLimits())); err == nil {
			if prepared.Attempt != nil {
				prepared.Attempt.Close()
			}
			t.Fatal("request decoding silently changed custom history")
		}
		prepared, err := Prepare([]byte(`{"tools":[{"type":"custom","name":"apply_patch"}],"input":[]}`), policy, DefaultLimits(), NewLimiter(DefaultLimits()))
		if err != nil || prepared.Attempt == nil {
			t.Fatalf("prepare control: %v", err)
		}
		alias, _ := prepared.Attempt.bridge.Alias(ToolIdentity{Name: "apply_patch", Kind: ToolKindCustom})
		body := fmt.Sprintf(`{"output":[{"type":"function_call","id":"fc_boundary","name":%q,"call_id":"c_boundary","arguments":"{\"input\":\"%s\"}"}]}`, alias, bad)
		if _, err := prepared.Attempt.RewriteResponse([]byte(body)); err == nil {
			t.Fatal("buffered response decoding silently changed custom input")
		}
		if _, err := prepared.Attempt.Feed(frameJSON(t, map[string]any{
			"type": eventOutputItemAdded,
			"item": map[string]any{"type": "function_call", "id": "fc_boundary", "name": alias, "call_id": "c_boundary"},
		})); err != nil {
			t.Fatalf("stream identity control: %v", err)
		}
		frame := fmt.Sprintf(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_boundary","name":%q,"call_id":"c_boundary","arguments":"{\"input\":\"%s\"}"}}`, alias, bad)
		if _, err := prepared.Attempt.Feed([]byte(frame)); err == nil {
			t.Fatal("stream JSON decoding silently changed custom input")
		}
		prepared.Attempt.Close()
	}
}

func TestCustomPatchBridgeDescriptionAndIdentity(t *testing.T) {
	value, _ := decodeValue([]byte(`{
		"tools":[
			{"type":"function","name":"apply_patch","description":"ordinary function","parameters":{"type":"object"}},
			{"type":"namespace","name":"fs","tools":[
				{"type":"custom","name":"apply_patch","description":"Patch files. This is a FREEFORM tool, so do not wrap the patch in JSON.",
				"format":{"type":"grammar","syntax":"lark","definition":"start: \"*** Begin Patch\" \"*** Environment ID:\" \"*** End Patch\""}}
			]}
		],"input":[]
	}`))
	bridge, changed, err := RewriteCustomDeclarations(value, CustomGrammarDescribe)
	if err != nil || !changed {
		t.Fatalf("rewrite: changed=%v err=%v", changed, err)
	}
	tools := value.(map[string]any)["tools"].([]any)
	ordinary := tools[0].(map[string]any)
	if ordinary["name"] != "apply_patch" || ordinary["description"] != "ordinary function" {
		t.Fatalf("ordinary function was rewritten: %v", ordinary)
	}
	tool := tools[1].(map[string]any)["tools"].([]any)[0].(map[string]any)
	description := tool["description"].(string)
	for _, required := range []string{"JSON object", "*** Begin Patch", "*** End Patch", "*** Environment ID:", "Original custom tool format"} {
		if !strings.Contains(description, required) {
			t.Errorf("bridged description omitted %q: %s", required, description)
		}
	}
	if strings.Contains(description, "do not wrap the patch in JSON") {
		t.Fatal("bridged tool retains contradictory freeform instructions")
	}
	identity, ok := bridge.ResolveWireAlias(tool["name"].(string))
	if !ok || identity != (ToolIdentity{Namespace: "fs", Name: "apply_patch", Kind: ToolKindCustom}) {
		t.Fatalf("bridge lost namespace or custom identity: %+v", identity)
	}
}

func TestCustomArgumentsUnicodeFidelity(t *testing.T) {
	for _, tc := range []struct {
		name, arguments, want string
		valid                 bool
	}{
		{"paired", `{"input":"\ud83d\ude00"}`, "😀", true},
		{"bmp", `{"input":"\u4e2d\u6587\r\n"}`, "中文\r\n", true},
		{"escaped_literal", `{"input":"\\ud800"}`, `\ud800`, true},
		{"literal_replacement", `{"input":"�"}`, "�", true},
		{"high_only", `{"input":"\ud800"}`, "", false},
		{"low_only", `{"input":"\udc00"}`, "", false},
		{"wrong_pair", `{"input":"\ud800\u0041"}`, "", false},
		{"interrupted_pair", `{"input":"\ud800x\udc00"}`, "", false},
		{"invalid_utf8", "{\"input\":\"" + string([]byte{0xff}) + "\"}", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := UnpackCustomArguments(tc.arguments)
			if tc.valid {
				if err != nil || got != tc.want {
					t.Fatalf("input changed: got=%q want=%q err=%v", got, tc.want, err)
				}
			} else if err == nil {
				t.Fatalf("malformed Unicode silently became %q", got)
			}
		})
	}
}

func TestCustomStreamSplitUnicodeAndMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name, tail string
		valid      bool
	}{
		{"paired", `\ude00"}`, true},
		{"unpaired", `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feed := streamTestFeed(t)
			alias, _ := feed.bridge.Alias(ToolIdentity{Name: "apply_patch", Kind: ToolKindCustom})
			if _, err := feed.Feed(frameJSON(t, map[string]any{
				"type": eventOutputItemAdded,
				"item": map[string]any{"type": "function_call", "id": "fc_unicode", "name": alias, "call_id": "c_unicode"},
			})); err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{`{"input":"\ud83d`, tc.tail} {
				out, err := feed.Feed(frameJSON(t, map[string]any{
					"type": eventFunctionCallArgsDelta, "item_id": "fc_unicode", "delta": fragment,
				}))
				if err != nil || len(out) != 0 {
					t.Fatalf("partial executable input must stay buffered: out=%s err=%v", out, err)
				}
			}
			out, err := feed.Feed(frameJSON(t, map[string]any{
				"type": eventOutputItemDone,
				"item": map[string]any{"type": "function_call", "id": "fc_unicode", "name": alias, "call_id": "c_unicode"},
			}))
			if !tc.valid {
				if err == nil || len(out) != 0 {
					t.Fatalf("invalid input must fail before custom input events: out=%s err=%v", out, err)
				}
				return
			}
			if err != nil || len(out) != 3 {
				t.Fatalf("valid split input did not finish: out=%s err=%v", out, err)
			}
			var event map[string]any
			if err := json.Unmarshal(out[2], &event); err != nil {
				t.Fatal(err)
			}
			item := event["item"].(map[string]any)
			if item["name"] != "apply_patch" || item["input"] != "😀" || item["call_id"] != "c_unicode" {
				t.Fatalf("custom output lost identity/input: %v", item)
			}
		})
	}
}
