package responsestools

import (
	"encoding/hex"
	"strings"
)

const CapabilitySearchUnknown ClientSearchMode = "unknown"

type CapabilitySupport string

const (
	SupportDeclared    CapabilitySupport = "declared"
	SupportUnsupported CapabilitySupport = "unsupported"
	SupportUnknown     CapabilitySupport = "unknown"
)

type EvidenceState string

const (
	EvidenceUnknown               EvidenceState = "unknown"
	EvidenceProtocolVerified      EvidenceState = "protocol-verified"
	EvidenceBoundedLive           EvidenceState = "bounded-live"
	EvidenceTaskBenchmarked       EvidenceState = "task-benchmarked"
	EvidenceOperationallyVerified EvidenceState = "operationally-verified"
	EvidenceRevalidationRequired  EvidenceState = "revalidation-required"
	EvidenceFailed                EvidenceState = "failed"
)

const (
	LossCustomGrammarConstraints   TransformationLoss = "custom_grammar_constraints_described"
	LossRecursiveSchemaWidening    TransformationLoss = "recursive_schema_widened"
	LossOptionalSchemaWidening     TransformationLoss = "optional_schema_widened"
	LossCustomDeclarationsStripped TransformationLoss = "custom_declarations_stripped"
)

// CapabilityProfile describes policy, not empirical certification. Potential
// losses apply only when the corresponding input actually needs adaptation.
type CapabilityProfile struct {
	ClientSearch            ClientSearchMode             `json:"client_search"`
	Activation              string                       `json:"activation"`
	AdditionalToolPositions string                       `json:"additional_tool_positions"`
	PotentialLosses         []TransformationLoss         `json:"potential_losses,omitempty"`
	Transports              map[string]CapabilitySupport `json:"transports"`
	Evidence                EvidenceState                `json:"evidence"`
}

// CapabilityProfileForRoute consumes an effective policy. It never changes
// routing based on availability errors or automatically certifies a transport.
func CapabilityProfileForRoute(route Route, policy RoutePolicy) CapabilityProfile {
	profile := CapabilityProfile{
		ClientSearch: CapabilitySearchUnknown, Activation: "unknown", AdditionalToolPositions: "unknown", Evidence: EvidenceUnknown,
		Transports: map[string]CapabilitySupport{"http-buffered": SupportUnknown, "http-sse": SupportUnknown, "websocket-replay": SupportUnknown, "websocket-duplex": SupportUnknown},
	}
	switch policy.ClientSearch {
	case ClientSearchBridge:
		profile.ClientSearch = ClientSearchBridge
		profile.Activation = "cumulative-budgeted"
	case ClientSearchNative:
		profile.ClientSearch = ClientSearchNative
		profile.Activation = "upstream-owned"
		profile.AdditionalToolPositions = "upstream-owned"
		if policy.CustomTools == CustomToolsFunction {
			profile.Activation = "cumulative-budgeted"
			profile.AdditionalToolPositions = "whole-request"
			profile.PotentialLosses = append(profile.PotentialLosses, LossAdditionalToolsPosition)
		}
	case ClientSearchDisabled:
		profile.ClientSearch = ClientSearchDisabled
		profile.Activation = "disabled"
	}
	if profile.CatalogSearchSupported() && strings.TrimSpace(route.UpstreamFormat) != "" {
		profile.Transports["http-buffered"] = SupportDeclared
		profile.Transports["http-sse"] = SupportDeclared
	} else if profile.ClientSearch == ClientSearchDisabled {
		profile.Transports["http-buffered"] = SupportUnsupported
		profile.Transports["http-sse"] = SupportUnsupported
	}
	if policy.CustomTools == CustomToolsFunction && policy.CustomGrammar == CustomGrammarDescribe {
		profile.PotentialLosses = append(profile.PotentialLosses, LossCustomGrammarConstraints)
	}
	if policy.Schema.LocalRefs == LocalRefsFlatten {
		profile.PotentialLosses = append(profile.PotentialLosses, LossRecursiveSchemaWidening)
	}
	if policy.Schema.CompletesSearchSchemas() {
		profile.PotentialLosses = append(profile.PotentialLosses, LossOptionalSchemaWidening)
	}
	if policy.CustomTools == CustomToolsStrip {
		profile.PotentialLosses = append(profile.PotentialLosses, LossCustomDeclarationsStripped)
	}
	return profile
}

// CatalogSearchSupported preserves the preexisting policy-based catalog
// projection. Missing empirical evidence must not silently force eager tools.
func (profile CapabilityProfile) CatalogSearchSupported() bool {
	return profile.ClientSearch == ClientSearchBridge || profile.ClientSearch == ClientSearchNative
}

// CertificationScope binds one observation to the exact components tested.
// EndpointID is an opaque local identifier, never an address or auth identity.
type CertificationScope struct {
	GatewayRevision  string `json:"gateway_revision"`
	PolicyGeneration string `json:"policy_generation"`
	Client           string `json:"client"`
	ClientVersion    string `json:"client_version"`
	ClientRevision   string `json:"client_revision"`
	ClientMode       string `json:"client_mode"`
	SourceFormat     string `json:"source_format"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	UpstreamFormat   string `json:"upstream_format"`
	EndpointID       string `json:"endpoint_id"`
	Transport        string `json:"transport"`
	CatalogHash      string `json:"catalog_hash"`
	FixtureHash      string `json:"fixture_hash"`
	PermissionHash   string `json:"permission_hash"`
	RegionID         string `json:"region_id"`
}

type CertificationRecord struct {
	Scope          CertificationScope `json:"scope"`
	Status         EvidenceState      `json:"status"`
	ArtifactID     string             `json:"artifact_id"`
	ArtifactSHA256 string             `json:"artifact_sha256"`
}

// StateForScope checks whether an externally verified artifact can still be
// used at this scope. It does not authenticate or validate the artifact itself;
// the acceptance harness must do that before constructing this record.
func (record CertificationRecord) StateForScope(current CertificationScope) EvidenceState {
	switch record.Status {
	case EvidenceProtocolVerified, EvidenceBoundedLive, EvidenceTaskBenchmarked, EvidenceOperationallyVerified, EvidenceFailed:
	default:
		return EvidenceUnknown
	}
	artifactID := strings.TrimSpace(record.ArtifactID)
	digest, err := hex.DecodeString(record.ArtifactSHA256)
	if artifactID == "" || strings.EqualFold(artifactID, "unknown") || err != nil || len(digest) != 32 || record.ArtifactSHA256 != strings.ToLower(record.ArtifactSHA256) || !record.Scope.known() || !current.known() {
		return EvidenceUnknown
	}
	if record.Scope != current {
		return EvidenceRevalidationRequired
	}
	return record.Status
}

func (scope CertificationScope) known() bool {
	for _, value := range []string{scope.GatewayRevision, scope.PolicyGeneration, scope.Client, scope.ClientVersion, scope.ClientRevision, scope.ClientMode, scope.SourceFormat, scope.Provider, scope.Model, scope.UpstreamFormat, scope.EndpointID, scope.Transport, scope.CatalogHash, scope.FixtureHash, scope.PermissionHash, scope.RegionID} {
		value = strings.TrimSpace(value)
		if value == "" || strings.EqualFold(value, "unknown") {
			return false
		}
	}
	return true
}
