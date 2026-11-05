package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	codexclaude "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/codex/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// prepareClaudeDeferredRequest runs after route/auth selection and before all
// three executor entries. Native Anthropic requests retain their beta fields;
// non-Anthropic routes receive ordinary function declarations and result text.
// No response adapter or retained stream state is required for this bridge.
func (m *Manager) prepareClaudeDeferredRequest(ctx context.Context, auth *Auth, provider string, toFormat sdktranslator.Format,
	req cliproxyexecutor.Request, opts cliproxyexecutor.Options,
) (context.Context, cliproxyexecutor.Request, cliproxyexecutor.Options, error) {
	if m == nil || opts.SourceFormat != sdktranslator.FormatClaude || toFormat == "" || toFormat == sdktranslator.FormatClaude {
		return ctx, req, opts, nil
	}
	state := m.responsesToolsSnapshot()
	state.mu.Lock()
	policy, enabled := state.policy, state.enabled
	state.mu.Unlock()
	if !enabled {
		return ctx, req, opts, nil
	}
	body := responsesToolsPayload(req, opts)
	deferred := bytes.Contains(body, []byte(`"defer_loading"`)) || bytes.Contains(body, []byte(`"tool_reference"`)) ||
		bytes.Contains(body, []byte(`\u`))
	// A bare search declaration changes no bytes, but a route that disables
	// client search must reject it all the same.
	if !deferred && !bytes.Contains(body, []byte(`"ToolSearch"`)) {
		return ctx, req, opts, nil
	}
	route := responsesToolsRoute(auth, provider, req.Model, toFormat)
	route.AuthKind = normalizeAuthKindForPolicy(route.AuthKind)
	matched, err := responsestools.EffectivePolicy(policy, route)
	if err == nil && matched.ClientSearch == responsestools.ClientSearchDisabled &&
		responsestools.ClaudeDeclaresSearch(body) {
		err = &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUnsupportedProtocol,
			Err: errors.New("client search is disabled for this route")}
	}
	var wire []byte
	if err == nil {
		wire, err = responsestools.PrepareClaudeDeferredTools(body, policy.Limits)
	}
	if err != nil {
		responsestools.ReportPreparationFailure(ctx, err)
		return ctx, req, opts, err
	}
	if normalized, normalizedOpts, changed := repairedResponsesToolsRequest(req, opts, body, wire); changed {
		contract, errContract := claudeDeferredWireContract(wire, toFormat, policy.Limits.MaxActiveToolBytes)
		if errContract != nil {
			responsestools.ReportPreparationFailure(ctx, errContract)
			return ctx, req, opts, errContract
		}
		if ctx == nil {
			ctx = context.Background()
		}
		return withResponsesToolsContract(ctx, contract), normalized, normalizedOpts, nil
	}
	return ctx, req, opts, nil
}

// claudeDeferredWireContract binds the active declarations to their actual
// translator identities without translating the request again or retaining
// schemas and history in the context.
func claudeDeferredWireContract(body []byte, target sdktranslator.Format, maximum int) (*cliproxyexecutor.WireContract, error) {
	var root struct {
		Tools []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonAmbiguousIdentity, Err: errors.New("cannot bind active Anthropic tool identities")}
	}
	var codexNames map[string]string
	if target == sdktranslator.FormatCodex {
		codexNames = codexclaude.ClaudeToolNameMap(body)
	}
	var sanitizedNames map[string]string
	if target == sdktranslator.FormatAntigravity || target == sdktranslator.FormatGemini {
		sanitizedNames = util.SanitizedFunctionNameMap(body)
	}
	contract := &cliproxyexecutor.WireContract{MaxActiveToolBytes: maximum, ExactFunctionNames: true}
	seen := make(map[string]struct{}, len(root.Tools))
	for _, tool := range root.Tools {
		name := tool.Name
		switch target {
		case sdktranslator.FormatCodex:
			if tool.Type == "web_search_20250305" || tool.Type == "web_search_20260209" {
				continue
			}
			name = codexNames[name]
		case sdktranslator.FormatGemini, sdktranslator.FormatAntigravity:
			name = util.MapSanitizedFunctionName(sanitizedNames, name)
		}
		if _, duplicate := seen[name]; name == "" || duplicate {
			return nil, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonAmbiguousIdentity, Err: errors.New("active Anthropic tool identities collide after translation")}
		}
		seen[name] = struct{}{}
		contract.RequiredFunctionNames = append(contract.RequiredFunctionNames, name)
	}
	return contract, nil
}
