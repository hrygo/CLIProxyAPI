package openai

import (
	"context"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func toolsReplayTestHandler(enabled bool) *OpenAIResponsesAPIHandler {
	cfg := &internalconfig.Config{}
	if enabled {
		on := true
		cfg.ResponsesTools.Enabled = &on
		cfg.ResponsesTools.Routes = []internalconfig.ResponsesToolsRoute{{
			Match: internalconfig.ResponsesToolsMatch{
				Provider: "codex", AuthKind: "oauth",
				UpstreamModel: "gpt-5.6-sol", UpstreamFormat: "codex",
			},
			ClientSearch: "bridge", CustomTools: "inherit",
		}}
	} else {
		// Absent configuration keeps the convention on, so the emergency gate
		// has to be requested explicitly.
		off := false
		cfg.ResponsesTools.Enabled = &off
	}
	cfg.NormalizeResponsesToolsConfig()
	mgr := coreauth.NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	return NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, mgr))
}

// toolsReplayTestHandlerConventions builds a handler with no explicit route
// table, so every route follows the convention policy.
func toolsReplayTestHandlerConventions() *OpenAIResponsesAPIHandler {
	cfg := &internalconfig.Config{}
	cfg.NormalizeResponsesToolsConfig()
	mgr := coreauth.NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	return NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, mgr))
}

func TestResponsesWebsocketRequiresToolsReplay(t *testing.T) {
	h := toolsReplayTestHandler(true)
	searchTurn := []byte("{\"type\": \"response.create\", \"tools\": [{\"type\": \"tool_search\"}], \"input\": []}")
	if !h.responsesWebsocketRequiresToolsReplay(searchTurn) {
		t.Fatalf("search turn must force tools-replay when enabled")
	}
	plainTurn := []byte("{\"type\": \"response.create\", \"input\": \"hello\"}")
	if h.responsesWebsocketRequiresToolsReplay(plainTurn) {
		t.Fatalf("plain turn must not force replay")
	}
	off := toolsReplayTestHandler(false)
	if off.responsesWebsocketRequiresToolsReplay(searchTurn) {
		t.Fatalf("disabled feature must never force replay")
	}
	if !h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "gemini", "gpt-5.6-sol") {
		t.Fatalf("an unmatched non-native route still follows the HTTP bridge convention")
	}
	if h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "codex", "other-model") {
		t.Fatalf("a different model route must not force tools-replay")
	}
	if !h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "codex", "gpt-5.6-sol") {
		t.Fatalf("matching provider and model route must force tools-replay")
	}
	bare := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
	if bare.responsesWebsocketRequiresToolsReplay(searchTurn) {
		t.Fatalf("without manager must not force replay")
	}
}

func TestResponsesWebsocketReplayIncludesActualSchemaAndLitePreparation(t *testing.T) {
	h := toolsReplayTestHandlerConventions()
	for _, tc := range []struct {
		name, provider, body string
		replay               bool
	}{
		{"ordinary recursive", "meta", `{"type":"response.create","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"properties":{"node":{"$ref":"#/$defs/Node"}}}}],"input":[]}`, true},
		{"lite eager", "meta", `{"type":"response.create","input":[{"type":"additional_tools","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]}`, true},
		{"lite response-only", "codex", `{"type":"response.create","input":[{"type":"additional_tools","tools":[{"type":"tool_search"}]}]}`, true},
		{"plain function", "meta", `{"type":"response.create","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"input":[]}`, false},
		{"eager namespace identity", "meta", `{"type":"response.create","tools":[{"type":"namespace","name":"fs","tools":[{"type":"function","name":"read"}]}],"input":[]}`, true},
		{"native search", "codex", `{"type":"response.create","tools":[{"type":"tool_search"}],"input":[]}`, false},
		{"request-only repair", "codex", `{"type":"response.create","input":[{"type":"function_call","id":"ctc_x","call_id":"c1","name":"lookup","arguments":"{}"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.responsesWebsocketRequiresToolsReplayForRoute([]byte(tc.body), tc.provider, "review-model"); got != tc.replay {
				t.Fatalf("replay=%v want=%v", got, tc.replay)
			}
		})
	}
}

func TestResponsesWebsocketNativeRouteKeepsPassthroughByDefault(t *testing.T) {
	h := toolsReplayTestHandlerConventions()
	searchTurn := []byte(`{"type":"response.create","tools":[{"type":"tool_search"}],"input":[]}`)
	if h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "codex", "gpt-5.6-sol") {
		t.Fatal("a native Responses route needs no rewriting and must not be forced into replay")
	}
	if h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "meta", "muse") {
		t.Fatal("meta speaks the native Responses format")
	}
	if !h.responsesWebsocketRequiresToolsReplayForRoute(searchTurn, "gemini", "gemini-3.8-flash") {
		t.Fatal("a non-native route takes the bridge and therefore needs replay")
	}
}

func TestResponsesWebsocketExecutorInputIsNotPassedToToolsReplay(t *testing.T) {
	input := make(chan cliproxyexecutor.WebsocketInput)
	if got := responsesWebsocketExecutorInput(input, true); got != nil {
		t.Fatal("tools-replay turn exposed the raw duplex input channel to the executor")
	}
	if got := responsesWebsocketExecutorInput(input, false); got != input {
		t.Fatal("non-replay turn must preserve its duplex input channel")
	}
}

// Unadapted metadata preserves duplex. A portable native namespace needs the
// same response-only identity adapter as HTTP, even with unchanged declarations;
// it must use replay rather than skip recovery through raw duplex passthrough.
func TestResponsesWebsocketPassThroughTurnKeepsNativePassthrough(t *testing.T) {
	h := toolsReplayTestHandlerConventions()
	cases := []struct {
		name    string
		payload string
	}{
		{
			name:    "namespace only",
			payload: `{"type":"response.create","tools":[{"type":"namespace","name":"fs","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}],"input":[]}`,
		},
		{
			name:    "eager function only",
			payload: `{"type":"response.create","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],"input":[]}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, provider := range []string{"xai", "codex", "meta", "gemini"} {
				payload := []byte(testCase.payload)
				wantReplay := provider == "meta" && testCase.name == "namespace only"
				if got := h.responsesWebsocketRequiresToolsReplayForRoute(payload, provider, "review-model"); got != wantReplay {
					t.Errorf("provider %q: replay=%v want=%v", provider, got, wantReplay)
				}
				if wantReplay {
					native, replay := h.responsesWebsocketResolveToolMode(payload, "", provider, "review-model", "", true)
					if native || !replay {
						t.Error("portable namespace bypassed response identity repair in duplex mode")
					}
				}
			}
		})
	}
}

// The counterpart guard: tightening the pass-through case must not let a turn
// the core protocol really rewrites slip into native duplex passthrough, where
// the executor would reject the wire contract it carries.
func TestResponsesWebsocketToolProtocolTurnStillForcesReplay(t *testing.T) {
	h := toolsReplayTestHandlerConventions()
	cases := []struct {
		name    string
		payload string
		custom  bool
	}{
		{
			name:    "tool_search declaration",
			payload: `{"type":"response.create","tools":[{"type":"tool_search"}],"input":[]}`,
		},
		{
			name:    "tool_search history",
			payload: `{"type":"response.create","input":[{"type":"tool_search_call","call_id":"s","arguments":{}},{"type":"tool_search_output","call_id":"s","tools":[]}]}`,
		},
		{
			name:    "custom declaration",
			payload: `{"type":"response.create","tools":[{"type":"custom","name":"apply_patch"}],"input":[]}`,
			custom:  true,
		},
		{
			name:    "custom history",
			payload: `{"type":"response.create","input":[{"type":"custom_tool_call","call_id":"c","name":"apply_patch","input":"x"},{"type":"custom_tool_call_output","call_id":"c","output":"ok"}]}`,
			custom:  true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.custom {
				if !h.responsesWebsocketRequiresToolsReplayForRoute([]byte(testCase.payload), "meta", "muse") {
					t.Error("portable custom conversion must force replay")
				}
				for _, provider := range []string{"xai", "gemini"} {
					if h.responsesWebsocketRequiresToolsReplayForRoute([]byte(testCase.payload), provider, "review-model") {
						t.Errorf("custom inherit does not adapt the request on %s", provider)
					}
				}
				return
			}
			// xai strips the built-in and needs the bridge, so it must replay.
			if !h.responsesWebsocketRequiresToolsReplayForRoute([]byte(testCase.payload), "xai", "review-model") {
				t.Error("a rewritten turn on the xai route must force tools-replay")
			}
			// A non-native provider must keep replaying as well.
			if !h.responsesWebsocketRequiresToolsReplayForRoute([]byte(testCase.payload), "gemini", "gemini-3.8-flash") {
				t.Error("a rewritten turn on a non-native route must force tools-replay")
			}
		})
	}
}

func TestResponsesWebsocketInitialToolTurnUsesReplayBeforeNativePassthrough(t *testing.T) {
	h := toolsReplayTestHandler(true)
	searchTurn := []byte(`{"type":"response.create","model":"gpt-5.6-sol","tools":[{"type":"tool_search"}],"input":[]}`)

	nativePassthrough, toolsReplay := h.responsesWebsocketResolveToolMode(
		searchTurn,
		"gpt-5.6-sol",
		"codex",
		"gpt-5.6-sol",
		"",
		false,
	)
	if nativePassthrough {
		t.Fatal("a bridged first turn must not use native websocket passthrough")
	}
	if !toolsReplay {
		t.Fatal("a bridged first turn must select tools-replay even before native passthrough is established")
	}
	if got := responsesWebsocketExecutorInput(make(chan cliproxyexecutor.WebsocketInput), toolsReplay); got != nil {
		t.Fatal("the first bridged turn must not expose duplex input to the executor")
	}
}

func TestIsResponsesWebsocketToolsReplayControlFrame(t *testing.T) {
	if !isResponsesWebsocketToolsReplayControlFrame([]byte(`{"type":"response.steer","input":"continue"}`)) {
		t.Fatal("response.steer must be rejected in tools-replay mode")
	}
	if isResponsesWebsocketToolsReplayControlFrame([]byte(`{"type":"response.create","input":[]}`)) {
		t.Fatal("ordinary response.create must remain allowed")
	}
}

func TestResponsesWebsocketToolModeUsesPinnedCredentialBeforePartialHints(t *testing.T) {
	cfg := &internalconfig.Config{}
	for _, tc := range []struct{ endpoint, mode string }{{"https://native.invalid", "native"}, {"https://bridge.invalid", "bridge"}} {
		cfg.ResponsesTools.Routes = append(cfg.ResponsesTools.Routes, internalconfig.ResponsesToolsRoute{
			Match: internalconfig.ResponsesToolsMatch{
				Provider: "codex", AuthKind: "oauth", UpstreamModel: "gpt-5.6-sol", UpstreamFormat: "codex", BaseURL: tc.endpoint,
			}, ClientSearch: tc.mode, CustomTools: "inherit",
		})
	}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatal(err)
	}
	mgr := coreauth.NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	executor := &websocketProviderCaptureExecutor{provider: "codex"}
	mgr.RegisterExecutor(executor)
	const authID = "ws-tools-pinned-native"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(authID, "codex", []*registry.ModelInfo{{ID: "gpt-5.6-sol"}})
	t.Cleanup(func() { reg.UnregisterClient(authID) })
	if _, err := mgr.Register(context.Background(), &coreauth.Auth{ID: authID, Provider: "codex", Attributes: map[string]string{
		"auth_kind": "oauth", "base_url": "https://native.invalid",
	}}); err != nil {
		t.Fatal(err)
	}
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, mgr))
	payload := []byte(`{"type":"response.create","tools":[{"type":"tool_search"}],"input":[]}`)
	if !h.responsesWebsocketRequiresToolsReplayForRoute(payload, "codex", "gpt-5.6-sol") {
		t.Fatal("partial hints must retain the possible bridge route")
	}
	native, replay := h.responsesWebsocketResolveToolMode(payload, "gpt-5.6-sol", "codex", "gpt-5.6-sol", authID, true)
	if !native || replay {
		t.Fatalf("pinned native credential lost duplex: native=%v replay=%v", native, replay)
	}
	if executor.streamCalls != 0 {
		t.Fatal("routing probe dispatched upstream execution")
	}
}
