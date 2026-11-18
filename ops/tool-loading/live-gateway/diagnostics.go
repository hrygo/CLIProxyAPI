package main

import (
	"encoding/json"
	"io"
)

type candidateFailureError struct {
	stage  string
	reason string
	cause  error
}

func (*candidateFailureError) Error() string   { return "isolated candidate failed" }
func (e *candidateFailureError) Unwrap() error { return e.cause }

func candidateFailure(stage, reason string, cause error) error {
	if cause == nil {
		return nil
	}
	return &candidateFailureError{stage: stage, reason: reason, cause: cause}
}

type candidateFailureDiagnostic struct {
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}

var candidateFailureReasons = map[string]map[string]bool{
	"arguments":     {"invalid_arguments": true, "invalid_profile": true, "credential_binding_invalid": true, "unsafe_environment": true},
	"configuration": {"configuration_unavailable": true, "configuration_invalid": true, "policy_invalid": true, "configuration_changed": true},
	"credentials": {"binding_source_unavailable": true, "binding_source_invalid": true, "binding_source_changed": true,
		"binding_source_missing": true, "binding_content_mismatch": true, "binding_required": true, "profile_mismatch": true, "snapshot_unavailable": true},
	"startup":  {"startup_failed": true},
	"evidence": {"evidence_failed": true},
	"unknown":  {"candidate_failed": true},
}

// Only local, typed categories leave the child boundary. Wrapped or joined
// causes remain available in memory, but no error text or identity is exported.
func writeCandidateFailure(output io.Writer, err error) error {
	unknown := candidateFailureDiagnostic{Stage: "unknown", Reason: "candidate_failed"}
	var failures []candidateFailureDiagnostic
	seen := make(map[candidateFailureDiagnostic]bool)
	appendFailure := func(value candidateFailureDiagnostic) {
		if !seen[value] && len(failures) < 8 {
			seen[value] = true
			failures = append(failures, value)
		}
	}
	var walk func(error, int)
	walk = func(cause error, depth int) {
		if cause == nil {
			return
		}
		if depth >= 32 || len(failures) >= 8 {
			if len(failures) == 8 {
				failures[7] = unknown
			} else {
				appendFailure(unknown)
			}
			return
		}
		if classified, ok := cause.(*candidateFailureError); ok {
			value := candidateFailureDiagnostic{Stage: classified.stage, Reason: classified.reason}
			if !candidateFailureReasons[value.Stage][value.Reason] {
				value = unknown
			}
			appendFailure(value)
			return
		}
		switch wrapped := cause.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				walk(child, depth+1)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap(), depth+1)
		default:
			appendFailure(unknown)
		}
	}
	walk(err, 0)
	if len(failures) == 0 {
		failures = []candidateFailureDiagnostic{unknown}
	}
	return json.NewEncoder(output).Encode(struct {
		Version  int                          `json:"version"`
		Scope    string                       `json:"scope"`
		Failures []candidateFailureDiagnostic `json:"failures"`
	}{1, "isolated-candidate-error", failures})
}
