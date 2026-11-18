package responsestools

import (
	"reflect"
	"strings"
	"testing"
)

func TestCapabilityProfileSeparatesPolicyAndEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		policy     RoutePolicy
		search     ClientSearchMode
		activation string
		positions  string
		losses     []TransformationLoss
	}{
		{"native", RoutePolicy{ClientSearch: ClientSearchNative}, ClientSearchNative, "upstream-owned", "upstream-owned", nil},
		{"bridge", RoutePolicy{ClientSearch: ClientSearchBridge}, ClientSearchBridge, "cumulative-budgeted", "unknown", nil},
		{"disabled", RoutePolicy{ClientSearch: ClientSearchDisabled}, ClientSearchDisabled, "disabled", "unknown", nil},
		{"unknown", RoutePolicy{}, CapabilitySearchUnknown, "unknown", "unknown", nil},
		{"lite portable", RoutePolicy{ClientSearch: ClientSearchNative, CustomTools: CustomToolsFunction, CustomGrammar: CustomGrammarDescribe, Schema: SchemaPolicy{LocalRefs: LocalRefsFlatten, SearchRequired: SearchRequiredComplete}}, ClientSearchNative, "cumulative-budgeted", "whole-request", []TransformationLoss{LossAdditionalToolsPosition, LossCustomGrammarConstraints, LossRecursiveSchemaWidening, LossOptionalSchemaWidening}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := CapabilityProfileForRoute(Route{Provider: "fixture", UpstreamFormat: "openai-response"}, tc.policy)
			if profile.ClientSearch != tc.search || profile.Activation != tc.activation || profile.AdditionalToolPositions != tc.positions || !reflect.DeepEqual(profile.PotentialLosses, tc.losses) {
				t.Fatalf("wrong profile: %+v", profile)
			}
			if profile.Evidence != EvidenceUnknown {
				t.Fatal("policy promoted itself into certified evidence")
			}
			if profile.Transports["websocket-replay"] != SupportUnknown || profile.Transports["websocket-duplex"] != SupportUnknown {
				t.Fatal("HTTP policy certified WebSocket behavior")
			}
			if got := profile.CatalogSearchSupported(); got != (tc.search == ClientSearchNative || tc.search == ClientSearchBridge) {
				t.Fatal("catalog projection changed")
			}
		})
	}
}

func TestCertificationScopeInvalidation(t *testing.T) {
	scope := CertificationScope{
		GatewayRevision: "revision-1", PolicyGeneration: "generation-1", Client: "codex", ClientVersion: "1", ClientRevision: "build-1", ClientMode: "tool-search", SourceFormat: "codex",
		Provider: "fixture", Model: "model-1", UpstreamFormat: "openai-response", EndpointID: "ep_0123456789abcdef0123456789abcdef",
		Transport: "http-sse", CatalogHash: "catalog-1", FixtureHash: "fixture-1", PermissionHash: "permission-1", RegionID: "region-1",
	}
	record := CertificationRecord{Scope: scope, Status: EvidenceBoundedLive, ArtifactID: "run-1", ArtifactSHA256: strings.Repeat("1", 64)}
	if record.StateForScope(scope) != EvidenceBoundedLive {
		t.Fatal("matching bounded evidence lost")
	}
	for _, change := range []func(*CertificationScope){
		func(s *CertificationScope) { s.GatewayRevision = "revision-2" },
		func(s *CertificationScope) { s.PolicyGeneration = "generation-2" },
		func(s *CertificationScope) { s.ClientVersion = "2" },
		func(s *CertificationScope) { s.ClientRevision = "build-2" },
		func(s *CertificationScope) { s.ClientMode = "code-mode" },
		func(s *CertificationScope) { s.SourceFormat = "openai-chat" },
		func(s *CertificationScope) { s.Provider = "other" },
		func(s *CertificationScope) { s.Model = "model-2" },
		func(s *CertificationScope) { s.UpstreamFormat = "codex" },
		func(s *CertificationScope) { s.EndpointID = "ep_fedcba9876543210fedcba9876543210" },
		func(s *CertificationScope) { s.Transport = "websocket-replay" },
		func(s *CertificationScope) { s.CatalogHash = "catalog-2" },
		func(s *CertificationScope) { s.FixtureHash = "fixture-2" },
		func(s *CertificationScope) { s.PermissionHash = "permission-2" },
		func(s *CertificationScope) { s.RegionID = "region-2" },
	} {
		current := scope
		change(&current)
		if record.StateForScope(current) != EvidenceRevalidationRequired {
			t.Fatalf("changed scope reused certificate: %+v", current)
		}
	}
	unknown := scope
	unknown.ClientVersion = "unknown"
	record.Scope = unknown
	if record.StateForScope(unknown) != EvidenceUnknown {
		t.Fatal("unknown scope certified")
	}
	record.Scope = scope
	record.ArtifactID = ""
	if record.StateForScope(scope) != EvidenceUnknown {
		t.Fatal("missing artifact certified")
	}
	for _, artifact := range []string{"unknown", "UNKNOWN", "   "} {
		record.ArtifactID = artifact
		if record.StateForScope(scope) != EvidenceUnknown {
			t.Fatalf("unknown artifact certified: %q", artifact)
		}
	}
	record.ArtifactID = "run-1"
	for _, digest := range []string{"", "unknown", "digest", strings.Repeat("z", 64)} {
		record.ArtifactSHA256 = digest
		if record.StateForScope(scope) != EvidenceUnknown {
			t.Fatalf("unverifiable digest certified: %q", digest)
		}
	}
}
