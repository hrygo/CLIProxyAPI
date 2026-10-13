package auth

import (
	"bytes"
	"context"
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// prepareClaudeDeferredRequest runs after route/auth selection and before all
// three executor entries. Native Anthropic requests retain their beta fields;
// non-Anthropic routes receive ordinary function declarations and result text.
// No response adapter or retained stream state is required for this bridge.
func (m *Manager) prepareClaudeDeferredRequest(ctx context.Context, auth *Auth, provider string, toFormat sdktranslator.Format,
	req cliproxyexecutor.Request, opts cliproxyexecutor.Options,
) (cliproxyexecutor.Request, cliproxyexecutor.Options, error) {
	if m == nil || opts.SourceFormat != sdktranslator.FormatClaude || toFormat == "" || toFormat == sdktranslator.FormatClaude {
		return req, opts, nil
	}
	state := m.responsesToolsSnapshot()
	state.mu.Lock()
	policy, enabled := state.policy, state.enabled
	state.mu.Unlock()
	if !enabled {
		return req, opts, nil
	}
	body := responsesToolsPayload(req, opts)
	if !bytes.Contains(body, []byte(`"defer_loading"`)) && !bytes.Contains(body, []byte(`"tool_reference"`)) {
		return req, opts, nil
	}
	route := responsesToolsRoute(auth, provider, req.Model, toFormat)
	route.AuthKind = normalizeAuthKindForPolicy(route.AuthKind)
	matched, err := responsestools.EffectivePolicy(policy, route)
	var wire []byte
	if err == nil {
		wire, err = responsestools.PrepareClaudeDeferredTools(body, policy.Limits)
	}
	if err == nil && !bytes.Equal(body, wire) && matched.ClientSearch == responsestools.ClientSearchDisabled {
		err = &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUnsupportedProtocol,
			Err: errors.New("client search is disabled for this route")}
	}
	if err != nil {
		responsestools.ReportPreparationFailure(ctx, err)
		return req, opts, err
	}
	if normalized, normalizedOpts, changed := repairedResponsesToolsRequest(req, opts, body, wire); changed {
		return normalized, normalizedOpts, nil
	}
	return req, opts, nil
}
