package auth

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const claudeClientSearchRequest = `{"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","defer_loading":true,"input_schema":{"type":"object","properties":{}}},{"name":"lookup","defer_loading":true,"input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"ToolSearch","id":"search_1","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"search_1","content":[{"type":"tool_reference","tool_name":"lookup"}]}]}]}`

func TestClaudeDeferredPreparationAtAllExecutorEntries(t *testing.T) {
	for _, entry := range []string{"execute", "count", "stream"} {
		t.Run(entry, func(t *testing.T) {
			mgr := responsesToolsTestManager()
			exec := &fakeResponsesExecutor{provider: "codex", toFormat: sdktranslator.FormatCodex, response: []byte(`{"content":[]}`)}
			unloaded := `{"name":"unloaded","defer_loading":true,"input_schema":{"type":"object"},"description":"` +
				strings.Repeat("x", responsestools.DefaultLimits().MaxActiveToolBytes) + `"},`
			original := strings.Replace(claudeClientSearchRequest, `"tools":[`, `"tools":[`+unloaded, 1)
			body := []byte(original)
			req := cliproxyexecutor.Request{Model: "fixture", Payload: body}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: body}
			auth := &Auth{ID: "fixture", Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
			checkContract := func(ctx context.Context) {
				t.Helper()
				wire := cliproxyexecutor.WireContractFromContext(ctx)
				if wire == nil || !wire.ExactFunctionNames || wire.MaxActiveToolBytes != responsestools.DefaultLimits().MaxActiveToolBytes ||
					len(wire.RequiredFunctionNames) != 2 || wire.RequiredFunctionNames[0] != "ToolSearch" || wire.RequiredFunctionNames[1] != "lookup" {
					t.Fatalf("executor entry lost the active declaration contract: %+v", wire)
				}
			}
			var err error
			switch entry {
			case "execute":
				_, err = mgr.responsesToolsCallWithExecutor(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
						checkContract(ctx)
						return exec.Execute(ctx, auth, req, opts)
					})
			case "count":
				_, err = mgr.responsesToolsCountCall(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
						checkContract(ctx)
						return exec.CountTokens(ctx, auth, req, opts)
					})
			case "stream":
				_, _, err = mgr.responsesToolsStreamCall(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
						checkContract(ctx)
						return exec.ExecuteStream(ctx, auth, req, opts)
					})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(exec.wire) != 1 || bytes.Contains(exec.wire[0], []byte("DeferredToolPlaceholder")) || bytes.Contains(exec.wire[0], []byte(`"tool_reference"`)) {
				t.Fatalf("executor received native deferred contract: %q", exec.wire)
			}
			if !bytes.Equal(exec.wire[0], exec.originals[0]) {
				t.Fatal("executor payload views diverged")
			}
			if !bytes.Equal(body, []byte(original)) {
				t.Fatal("original input mutated")
			}
			bad := strings.Replace(original, `"tool_name":"lookup"`, `"tool_name":"missing"`, 1)
			req.Payload, opts.OriginalRequest = []byte(bad), []byte(bad)
			_, _, err = mgr.responsesToolsStreamCall(context.Background(), auth, "codex", exec, req, opts,
				func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
					return exec.ExecuteStream(ctx, auth, req, opts)
				})
			if !responsestools.IsRequestScopedError(err) || len(exec.wire) != 1 {
				t.Fatalf("invalid discovery dispatched: %v", err)
			}
		})
	}
}

func TestClaudeDeferredNativeAndEmergencyGateRemainUntouched(t *testing.T) {
	for _, native := range []bool{true, false} {
		mgr := responsesToolsTestManager()
		format := sdktranslator.FormatClaude
		if !native {
			format = sdktranslator.FormatCodex
			mgr.syncResponsesToolsPolicy(responsestools.Policy{Enabled: false, Limits: responsestools.DefaultLimits()})
		}
		body := []byte(claudeClientSearchRequest)
		req := cliproxyexecutor.Request{Payload: body}
		opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: body}
		_, outReq, outOpts, err := mgr.prepareClaudeDeferredRequest(context.Background(), nil, "fixture", format, req, opts)
		if err != nil || !bytes.Equal(outReq.Payload, body) || !bytes.Equal(outOpts.OriginalRequest, body) {
			t.Fatalf("pass through altered: %v", err)
		}
	}
}

func TestClaudeDeferredDisabledRouteRejectsDiscoveryOnly(t *testing.T) {
	mgr := responsesToolsTestManagerWithClientSearch("disabled")
	auth := &Auth{Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
	for _, test := range []struct {
		body      string
		discovery bool
	}{
		{claudeClientSearchRequest, true},
		{strings.ReplaceAll(strings.ReplaceAll(claudeClientSearchRequest, `defer_loading`, `defer_\u006coading`), `tool_reference`, `tool_refer\u0065nce`), true},
		{`{"tools":[{"name":"ordinary","defer_loading":false,"input_schema":{"type":"object"}}],"messages":[]}`, false},
		{`{"messages":[{"role":"user","content":"tool_reference"}]}`, false},
		{`{"messages":[{"role":"user","content":"defer_loading"}]}`, false},
		{`{"messages":[{"role":"user","content":"tool_refer\u0065nce"}]}`, false},
	} {
		req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(test.body)}
		opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: []byte(test.body)}
		ctx, prepared, preparedOpts, err := mgr.prepareClaudeDeferredRequest(context.Background(), auth, "codex", sdktranslator.FormatCodex, req, opts)
		if test.discovery {
			if !responsestools.IsRequestScopedError(err) {
				t.Fatalf("disabled discovery admitted: %v", err)
			}
		} else if err != nil || !bytes.Equal(prepared.Payload, req.Payload) || !bytes.Equal(preparedOpts.OriginalRequest, req.Payload) ||
			cliproxyexecutor.WireContractFromContext(ctx) != nil {
			t.Fatalf("ordinary request rejected or given a discovery contract: %v", err)
		}
	}
}
