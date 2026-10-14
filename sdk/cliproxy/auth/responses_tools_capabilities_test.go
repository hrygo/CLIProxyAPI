package auth

import (
	"context"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func capabilitiesTestManager(t *testing.T, routes []internalconfig.ResponsesToolsRoute) *Manager {
	return capabilitiesTestManagerForModels(t, routes, "gpt-5.6-sol", "")
}

func TestClientSearchSupportedCooledCandidateVeto(t *testing.T) {
	blocked := capRoute("disabled", "inherit")
	blocked.Match.UpstreamModel = "blocked-upstream"
	mgr := capabilitiesTestManagerForModels(t, []internalconfig.ResponsesToolsRoute{capRoute("bridge", "inherit"), blocked}, "client-alias", "gpt-5.6-sol")
	registry.GetGlobalRegistry().RegisterClient("cap-cooled", "codex", []*registry.ModelInfo{{ID: "client-alias"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient("cap-cooled") })
	if _, err := mgr.Register(context.Background(), &Auth{
		ID: "cap-cooled", Provider: "codex", Unavailable: true,
		NextRetryAfter: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		Attributes:     map[string]string{"auth_kind": "oauth", homeUpstreamModelAttributeKey: "blocked-upstream"},
	}); err != nil {
		t.Fatal(err)
	}
	if routes := mgr.clientSearchCandidateRoutes("client-alias"); len(routes) != 2 {
		t.Fatalf("cooled route omitted: %+v", routes)
	}
	if supported := mgr.ClientSearchSupported("client-alias"); supported == nil || *supported {
		t.Fatal("cooled disabled candidate did not veto public alias")
	}
}

func TestClientSearchSupportedRegionFailureKeepsDeclaredCapability(t *testing.T) {
	mgr := capabilitiesTestManager(t, nil)
	mgr.mu.Lock()
	auth := mgr.auths["cap-auth"]
	auth.Unavailable = true
	auth.NextRetryAfter = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	auth.StatusMessage = "fixture provider region unavailable"
	mgr.mu.Unlock()
	if supported := mgr.ClientSearchSupported("gpt-5.6-sol"); supported == nil || !*supported {
		t.Fatal("availability observation changed declared capability")
	}
	routes := mgr.clientSearchCandidateRoutes("gpt-5.6-sol")
	if len(routes) != 1 {
		t.Fatal("unavailable route vanished")
	}
	profile := responsestools.CapabilityProfileForRoute(routes[0], responsestools.ConventionPolicy(routes[0]))
	if profile.Evidence != responsestools.EvidenceUnknown {
		t.Fatal("provider availability promoted certification")
	}
}

func TestClientSearchSupportedModelCooldownAndExplicitDisable(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "cooldown", true: "explicit disabled"}[explicit], func(t *testing.T) {
			blocked := capRoute("disabled", "inherit")
			blocked.Match.UpstreamModel = "blocked-upstream"
			mgr := capabilitiesTestManagerForModels(t, []internalconfig.ResponsesToolsRoute{capRoute("bridge", "inherit"), blocked}, "client-alias", "gpt-5.6-sol")
			registry.GetGlobalRegistry().RegisterClient("cap-model-state", "codex", []*registry.ModelInfo{{ID: "client-alias"}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient("cap-model-state") })
			state := &ModelState{Unavailable: true, NextRetryAfter: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}
			if explicit {
				state.Status = StatusDisabled
			}
			if _, err := mgr.Register(context.Background(), &Auth{ID: "cap-model-state", Provider: "codex", Attributes: map[string]string{"auth_kind": "oauth", homeUpstreamModelAttributeKey: "blocked-upstream"}, ModelStates: map[string]*ModelState{"blocked-upstream": state}}); err != nil {
				t.Fatal(err)
			}
			supported := mgr.ClientSearchSupported("client-alias")
			if supported == nil || *supported != explicit {
				t.Fatalf("temporary and explicit disable conflated: explicit=%v supported=%v", explicit, supported)
			}
		})
	}
}

func capabilitiesTestManagerForModels(t *testing.T, routes []internalconfig.ResponsesToolsRoute, clientModel, upstreamModel string) *Manager {
	t.Helper()
	cfg := &internalconfig.Config{}
	enabled := true
	cfg.ResponsesTools.Enabled = &enabled
	cfg.ResponsesTools.Routes = routes
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatalf("config: %v", err)
	}
	mgr := NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("cap-auth", "codex", []*registry.ModelInfo{{ID: clientModel}})
	t.Cleanup(func() { reg.UnregisterClient("cap-auth") })
	attributes := map[string]string{"auth_kind": "oauth"}
	if upstreamModel != "" {
		attributes[homeUpstreamModelAttributeKey] = upstreamModel
	}
	if _, err := mgr.Register(context.Background(), &Auth{
		ID: "cap-auth", Provider: "codex",
		Attributes: attributes,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	mgr.RegisterExecutor(&fakeResponsesExecutor{provider: "codex", toFormat: "codex"})
	return mgr
}

func capRoute(clientSearch, customTools string) internalconfig.ResponsesToolsRoute {
	return internalconfig.ResponsesToolsRoute{
		Match: internalconfig.ResponsesToolsMatch{
			Provider: "codex", AuthKind: "oauth",
			UpstreamModel: "gpt-5.6-sol", UpstreamFormat: "codex",
		},
		ClientSearch: clientSearch, CustomTools: customTools,
	}
}

func TestClientSearchSupportedBridge(t *testing.T) {
	mgr := capabilitiesTestManager(t, []internalconfig.ResponsesToolsRoute{capRoute("bridge", "inherit")})
	supported := mgr.ClientSearchSupported("gpt-5.6-sol")
	if supported == nil || !*supported {
		t.Fatalf("bridge route must advertise support, got %v", supported)
	}
}

func TestClientSearchSupportedDisabledVetoes(t *testing.T) {
	mgr := capabilitiesTestManager(t, []internalconfig.ResponsesToolsRoute{capRoute("disabled", "inherit")})
	supported := mgr.ClientSearchSupported("gpt-5.6-sol")
	if supported == nil || *supported {
		t.Fatalf("disabled route must veto, got %v", supported)
	}
}

func TestClientSearchSupportedUnknownIsNil(t *testing.T) {
	mgr := capabilitiesTestManager(t, []internalconfig.ResponsesToolsRoute{capRoute("bridge", "inherit")})
	if supported := mgr.ClientSearchSupported("no-such-model"); supported != nil {
		t.Fatalf("unknown alias must not promote, got %v", *supported)
	}
}

func TestClientSearchSupportedOffIsNil(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	cfg := &internalconfig.Config{}
	disabled := false
	cfg.ResponsesTools.Enabled = &disabled
	mgr.SetConfig(cfg)
	if supported := mgr.ClientSearchSupported("gpt-5.6-sol"); supported != nil {
		t.Fatalf("disabled feature must stay nil, got %v", *supported)
	}
}

func TestClientSearchSupportedFollowsConventionWithoutRoutes(t *testing.T) {
	mgr := capabilitiesTestManager(t, nil)
	supported := mgr.ClientSearchSupported("gpt-5.6-sol")
	if supported == nil || !*supported {
		t.Fatalf("native upstream must advertise search support by convention, got %v", supported)
	}
}

func TestResponsesToolsReplayFollowsProviderConvention(t *testing.T) {
	mgr := capabilitiesTestManager(t, nil)
	if mgr.ResponsesToolsMayApplyToRoute("codex", "gpt-5.6-sol") {
		t.Fatal("a native Responses provider needs no rewriting and must keep passthrough")
	}
	if !mgr.ResponsesToolsMayApplyToRoute("xai", "grok") {
		t.Fatal("the xai executor strips tool_search, so its turns must take the bridge")
	}
	if !mgr.ResponsesToolsMayApplyToRoute("gemini", "gemini-3.8-flash") {
		t.Fatal("a non-native provider must take the tools bridge and therefore replay")
	}
}

// TestProviderNeedsToolsBridgeMatchesConventionPolicy locks the replay
// decision to the same convention the request path uses, so a turn is never
// sent as native passthrough while the request path still rewrites it.
func TestProviderNeedsToolsBridgeMatchesRequestToFormat(t *testing.T) {
	providers := []string{
		"codex", "xai", "meta", "claude", "gemini", "vertex", "aistudio",
		"kimi", "kimi-ai", "kimi.ai", "kimi.com", "antigravity", "devin",
		"openai-compatible", "unknown-provider", "",
	}
	for _, provider := range providers {
		format := requestToFormat(provider, nil, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
		want := responsestools.ConventionPolicy(responsestools.Route{
			Provider:       provider,
			UpstreamFormat: format.String(),
		}).ClientSearch == responsestools.ClientSearchBridge
		if got := providerNeedsToolsBridge(provider); got != want {
			t.Errorf("provider %q: providerNeedsToolsBridge=%v, convention implies %v", provider, got, want)
		}
	}
}

func TestResponsesToolsEnabledWithoutRoutes(t *testing.T) {
	mgr := capabilitiesTestManager(t, nil)
	if !mgr.ResponsesToolsEnabled() {
		t.Fatal("the convention policy must be active without explicit routes")
	}
}

func TestResponsesToolsMayApplyToClientModelResolvesUpstreamAlias(t *testing.T) {
	mgr := capabilitiesTestManagerForModels(t, []internalconfig.ResponsesToolsRoute{
		capRoute("bridge", "inherit"),
	}, "client-alias", "gpt-5.6-sol")

	if mgr.ResponsesToolsMayApplyToRoute("codex", "client-alias") {
		t.Fatal("public model alias must not be mistaken for the configured upstream model")
	}
	if !mgr.ResponsesToolsMayApplyToClientModel("client-alias", "codex", "cap-auth") {
		t.Fatal("active upstream route behind the client model alias must require tools replay")
	}
	if mgr.ResponsesToolsMayApplyToClientModel("client-alias", "gemini", "cap-auth") {
		t.Fatal("different provider must not require tools replay")
	}
	if mgr.ResponsesToolsMayApplyToClientModel("client-alias", "codex", "other-auth") {
		t.Fatal("different pinned auth must not require tools replay")
	}
}

func TestResponsesToolsReplayProbeUsesResolvedCredentialAndHTTPPreparation(t *testing.T) {
	bridge := capRoute("bridge", "inherit")
	bridge.Match.BaseURL = "https://bridge.invalid"
	native := capRoute("native", "inherit")
	native.Match.BaseURL = "https://native.invalid"
	mgr := capabilitiesTestManagerForModels(t, []internalconfig.ResponsesToolsRoute{bridge, native}, "client-alias", "gpt-5.6-sol")
	mgr.mu.Lock()
	mgr.auths["cap-auth"].Attributes["base_url"] = bridge.Match.BaseURL
	mgr.mu.Unlock()
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("cap-native", "codex", []*registry.ModelInfo{{ID: "client-alias"}})
	t.Cleanup(func() { reg.UnregisterClient("cap-native") })
	if _, err := mgr.Register(context.Background(), &Auth{ID: "cap-native", Provider: "codex", Attributes: map[string]string{
		"auth_kind": "oauth", "base_url": native.Match.BaseURL, homeUpstreamModelAttributeKey: "gpt-5.6-sol",
	}}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"tools":[{"type":"tool_search"}],"input":[]}`)
	for _, tc := range []struct {
		authID string
		want   bool
	}{{"cap-auth", true}, {"cap-native", false}, {"", true}, {"missing-auth", false}} {
		t.Run(tc.authID, func(t *testing.T) {
			got := mgr.ResponsesToolsRequiresReplayForClientModel(payload, "client-alias", "codex", tc.authID)
			if got != tc.want {
				t.Fatalf("replay=%v want %v", got, tc.want)
			}
			if tc.authID == "" || tc.authID == "missing-auth" {
				return
			}
			routes := mgr.responsesToolsCandidateRoutes("client-alias", tc.authID)
			if len(routes) != 1 {
				t.Fatalf("resolved routes=%d", len(routes))
			}
			_, attempt, _, err := mgr.prepareResponsesToolsAttempt(routes[0], payload)
			if attempt != nil {
				defer attempt.Close()
			}
			if httpReplay := attempt != nil || err != nil; got != httpReplay {
				t.Fatalf("WS replay=%v HTTP replay=%v", got, httpReplay)
			}
		})
	}
	if mgr.ResponsesToolsRequiresReplayForClientModel(payload, "client-alias", "gemini", "cap-auth") {
		t.Fatal("provider hint must narrow actual credential candidates")
	}
	if mgr.ResponsesToolsRequiresReplayForClientModel(payload, "gpt-5.6-sol", "codex", "missing-auth") {
		t.Fatal("an absent pinned credential must not borrow an unrelated explicit route")
	}
	if attempts, bytes := mgr.responsesToolsSnapshot().limiter.Usage(); attempts != 0 || bytes != 0 {
		t.Fatalf("probe or HTTP comparison retained shared reservations: attempts=%d bytes=%d", attempts, bytes)
	}
}

func TestResponsesToolsReplayProbePreservesExactModelMatching(t *testing.T) {
	mgr := capabilitiesTestManager(t, []internalconfig.ResponsesToolsRoute{capRoute("bridge", "inherit")})
	payload := []byte(`{"tools":[{"type":"tool_search"}],"input":[]}`)
	if mgr.ResponsesToolsRequiresReplayForRoute(payload, "codex", "Gpt-5.6-sol") {
		t.Fatal("a case-mismatched model must use the native HTTP convention, not another model's bridge rule")
	}
	if !mgr.ResponsesToolsRequiresReplayForRoute(payload, "codex", "gpt-5.6-sol") {
		t.Fatal("the exact model must retain its explicit bridge rule")
	}
}
