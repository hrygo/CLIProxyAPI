package openai

import (
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
	"strings"
)

func responsesWebsocketExecutorInput(
	input <-chan cliproxyexecutor.WebsocketInput,
	toolsReplayTurn bool,
) <-chan cliproxyexecutor.WebsocketInput {
	if toolsReplayTurn {
		return nil
	}
	return input
}

func isResponsesWebsocketToolsReplayControlFrame(payload []byte) bool {
	return strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.steer"
}

// responsesWebsocketRequiresToolsReplay reports whether one downstream turn
// needs request or response adaptation, or a local preparation rejection.
// Such turns run as tools-replay (HTTP replay normalization) instead of
// native duplex passthrough, so request, history, response, and stream
// recovery share one mapping. Unchanged requests and a disabled feature keep
// the native path; unmatched routes still use the HTTP convention policy.
func (h *OpenAIResponsesAPIHandler) responsesWebsocketRequiresToolsReplay(payload []byte) bool {
	if h == nil || h.AuthManager == nil || !h.AuthManager.ResponsesToolsEnabled() {
		return false
	}
	return h.responsesWebsocketRequiresToolsReplayForRoute(
		payload,
		"",
		strings.TrimSpace(gjson.GetBytes(payload, "model").String()),
	)
}

func (h *OpenAIResponsesAPIHandler) responsesWebsocketRequiresToolsReplayForRoute(payload []byte, provider, upstreamModel string) bool {
	if h == nil || h.AuthManager == nil {
		return false
	}
	return h.AuthManager.ResponsesToolsRequiresReplayForRoute(payload, provider, upstreamModel)
}

func (h *OpenAIResponsesAPIHandler) responsesWebsocketRequiresToolsReplayForClientModel(payload []byte, modelID, provider, authID string) bool {
	if h == nil || h.AuthManager == nil {
		return false
	}
	return h.AuthManager.ResponsesToolsRequiresReplayForClientModel(payload, modelID, provider, authID)
}

func (h *OpenAIResponsesAPIHandler) responsesWebsocketResolveToolMode(
	payload []byte,
	modelID string,
	provider string,
	upstreamModel string,
	authID string,
	nativePassthrough bool,
) (bool, bool) {
	var toolsReplay bool
	switch {
	case strings.TrimSpace(authID) != "" && strings.TrimSpace(modelID) != "":
		toolsReplay = h.responsesWebsocketRequiresToolsReplayForClientModel(payload, modelID, provider, authID)
	case strings.TrimSpace(upstreamModel) != "":
		toolsReplay = h.responsesWebsocketRequiresToolsReplayForRoute(payload, provider, upstreamModel)
	case strings.TrimSpace(modelID) != "":
		toolsReplay = h.responsesWebsocketRequiresToolsReplayForClientModel(payload, modelID, provider, authID)
	default:
		toolsReplay = h.responsesWebsocketRequiresToolsReplay(payload)
	}
	if toolsReplay {
		nativePassthrough = false
	}
	return nativePassthrough, toolsReplay
}
