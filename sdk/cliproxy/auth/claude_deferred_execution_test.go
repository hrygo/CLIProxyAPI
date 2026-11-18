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

func TestClaudeDeferredLossyUnicodeNeverReachesExecutor(t *testing.T) {
	for _, entry := range []string{"execute", "count", "stream"} {
		t.Run(entry, func(t *testing.T) {
			mgr := responsesToolsTestManager()
			exec := &fakeResponsesExecutor{provider: "codex", toFormat: sdktranslator.FormatCodex}
			body := []byte(strings.ReplaceAll(claudeClientSearchRequest, "lookup", `look\ud800up`))
			req := cliproxyexecutor.Request{Model: "fixture", Payload: body}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: body}
			auth := &Auth{ID: "fixture", Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
			var err error
			switch entry {
			case "execute":
				_, err = mgr.responsesToolsCallWithExecutor(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
						return exec.Execute(ctx, auth, req, opts)
					})
			case "count":
				_, err = mgr.responsesToolsCountCall(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
						return exec.CountTokens(ctx, auth, req, opts)
					})
			case "stream":
				_, _, err = mgr.responsesToolsStreamCall(context.Background(), auth, "codex", exec, req, opts,
					func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
						return exec.ExecuteStream(ctx, auth, req, opts)
					})
			}
			if !responsestools.IsRequestScopedError(err) || len(exec.wire) != 0 {
				t.Fatalf("invalid Unicode reached executor: err=%v calls=%d", err, len(exec.wire))
			}
		})
	}
}

// A client that stops declaring a search function while keeping the reserved
// placeholder has nothing to search for. The turn must reach the executor with
// the surviving declarations instead of failing locally with 422.
const claudePlaceholderOnlyRequest = `{"model":"fixture","max_tokens":64,"messages":[{"role":"user","content":"say OK"}],"tools":[{"name":"Glob","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","description":"Reserved placeholder that keeps deferred tool loading active; never call this tool.","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`

func TestClaudeDeferredPlaceholderOnlyRequestDispatches(t *testing.T) {
	mgr := responsesToolsTestManager()
	exec := &fakeResponsesExecutor{provider: "codex", toFormat: sdktranslator.FormatCodex, response: []byte(`{"content":[]}`)}
	body := []byte(claudePlaceholderOnlyRequest)
	req := cliproxyexecutor.Request{Model: "fixture", Payload: body}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: body}
	auth := &Auth{ID: "fixture", Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
	_, err := mgr.responsesToolsCallWithExecutor(context.Background(), auth, "codex", exec, req, opts,
		func(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
			wire := cliproxyexecutor.WireContractFromContext(ctx)
			if wire == nil || len(wire.RequiredFunctionNames) != 1 || wire.RequiredFunctionNames[0] != "Glob" {
				t.Fatalf("placeholder-only request lost its surviving declaration: %+v", wire)
			}
			return exec.Execute(ctx, auth, req, opts)
		})
	if err != nil {
		t.Fatalf("placeholder-only request rejected: %v", err)
	}
	if len(exec.wire) != 1 || !bytes.Contains(exec.wire[0], []byte(`"Glob"`)) ||
		bytes.Contains(exec.wire[0], []byte("DeferredToolPlaceholder")) || bytes.Contains(exec.wire[0], []byte("defer_loading")) {
		t.Fatalf("deferred declarations reached the executor: %q", exec.wire)
	}
}

// A search declaration on a route that allows client search changes nothing
// locally: there is no discovery to bridge and no disabled policy to apply.
func TestClaudeDeferredBareSearchDeclarationPassesThroughWhenEnabled(t *testing.T) {
	const bareSearch = `{"model":"fixture","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]}`
	mgr := responsesToolsTestManager()
	auth := &Auth{Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
	req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(bareSearch)}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: []byte(bareSearch)}
	ctx, prepared, preparedOpts, err := mgr.prepareClaudeDeferredRequest(context.Background(), auth, "codex", sdktranslator.FormatCodex, req, opts)
	if err != nil {
		t.Fatalf("bare search declaration rejected: %v", err)
	}
	if !bytes.Equal(prepared.Payload, req.Payload) || !bytes.Equal(preparedOpts.OriginalRequest, opts.OriginalRequest) {
		t.Fatal("bare search declaration was rewritten")
	}
	if cliproxyexecutor.WireContractFromContext(ctx) != nil {
		t.Fatal("bare search declaration built a wire contract")
	}
}

// A route that disables client search rejects every request that declares the
// search function, even when the bridge would change nothing. Only a leftover
// placeholder degrades, because it declares no search entry point.
func TestClaudeDeferredDisabledRouteRejectsDeclaredSearch(t *testing.T) {
	const bareSearch = `{"model":"fixture","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"ToolSearch","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]}`
	const ordinary = `{"tools":[{"name":"ordinary","defer_loading":false,"input_schema":{"type":"object"}}],"messages":[]}`
	rejected, unchanged, degraded := "rejected", "unchanged", "degraded"
	for name, tc := range map[string]struct {
		body    string
		outcome string
	}{
		"declared discovery":         {claudeClientSearchRequest, rejected},
		"escaped declared discovery": {strings.ReplaceAll(strings.ReplaceAll(claudeClientSearchRequest, `defer_loading`, `defer_\u006coading`), `tool_reference`, `tool_refer\u0065nce`), rejected},
		"bare declaration":           {bareSearch, rejected},
		"placeholder only":           {claudePlaceholderOnlyRequest, degraded},
		"ordinary false flag":        {ordinary, unchanged},
		"text tool reference":        {`{"messages":[{"role":"user","content":"tool_reference"}]}`, unchanged},
		"text deferral flag":         {`{"messages":[{"role":"user","content":"defer_loading"}]}`, unchanged},
		"text escaped reference":     {`{"messages":[{"role":"user","content":"tool_refer\u0065nce"}]}`, unchanged},
	} {
		t.Run(name, func(t *testing.T) {
			mgr := responsesToolsTestManagerWithClientSearch("disabled")
			auth := &Auth{Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth"}}
			req := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: []byte(tc.body)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, OriginalRequest: []byte(tc.body)}
			ctx, prepared, _, err := mgr.prepareClaudeDeferredRequest(context.Background(), auth, "codex", sdktranslator.FormatCodex, req, opts)
			switch tc.outcome {
			case rejected:
				if !responsestools.IsRequestScopedError(err) || cliproxyexecutor.WireContractFromContext(ctx) != nil {
					t.Fatalf("declared search admitted on a disabled route: %v", err)
				}
			case unchanged:
				if err != nil || !bytes.Equal(prepared.Payload, req.Payload) || cliproxyexecutor.WireContractFromContext(ctx) != nil {
					t.Fatalf("ordinary request rejected or rewritten: %v %s", err, prepared.Payload)
				}
			case degraded:
				if err != nil {
					t.Fatalf("degradable turn rejected: %v", err)
				}
				if bytes.Contains(prepared.Payload, []byte("DeferredToolPlaceholder")) ||
					bytes.Contains(prepared.Payload, []byte("defer_loading")) {
					t.Fatalf("deferred declarations survived degradation: %s", prepared.Payload)
				}
			}
		})
	}
}
