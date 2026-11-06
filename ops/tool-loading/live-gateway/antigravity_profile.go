package main

import (
	"errors"
	"net/url"
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// This acceptance adapter mirrors antigravityLoadCodeAssistBaseURL, including
// attribute precedence and the production default (inference defaults to daily).
// Revalidate the adapter when the executor's private endpoint resolver changes.
func antigravityCreditsEndpoint(auth *coreauth.Auth) (string, error) {
	base := strings.TrimSpace(auth.Attributes["base_url"])
	if base == "" {
		value, _ := auth.Metadata["base_url"].(string)
		base = strings.TrimSpace(value)
	}
	if base == "" {
		base = "https://cloudcode-pa.googleapis.com"
	}
	base = strings.TrimSuffix(base, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("invalid pinned Antigravity auxiliary endpoint")
	}
	return base + "/v1internal:loadCodeAssist", nil
}

func antigravityAuxiliaryEndpoints(auth *coreauth.Auth) (map[string]string, error) {
	credits, err := antigravityCreditsEndpoint(auth)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(auth.Attributes["base_url"])
	if base == "" {
		value, _ := auth.Metadata["base_url"].(string)
		base = strings.TrimSpace(value)
	}
	// Model catalogs use the first base_urls entry, then base_url, then daily.
	for _, value := range strings.Split(auth.Attributes["base_urls"], ",") {
		if strings.TrimRight(strings.TrimSpace(value), "/") != "" {
			base = value
			break
		}
	}
	if strings.TrimSpace(base) == "" {
		base = "https://daily-cloudcode-pa.googleapis.com"
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("invalid pinned Antigravity model catalog endpoint")
	}
	return map[string]string{"credits_query": credits, "model_discovery": base + "/v1internal:fetchAvailableModels"}, nil
}
