package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAntigravityProfileRequiresExplicitCurrentOAuthSnapshot(t *testing.T) {
	p := liveProfile{ClientModel: "fixture", UpstreamModel: "fixture-upstream", Executor: "antigravity", Contract: "antigravity-generate-v1"}
	if p.validate() == nil {
		t.Fatal("implicit Antigravity credential selection accepted")
	}
	if _, err := loadSelectedCredentials(&config.Config{}, p); err == nil {
		t.Fatal("implicit Antigravity OAuth scan accepted")
	}
	p.AuthFiles = []credentialBinding{{strings.Repeat("a", 64), strings.Repeat("b", 64)}}
	if err := p.validate(); err != nil {
		t.Fatalf("explicit wrapped profile rejected: %v", err)
	}
	p.Contract = "gemini-generate-v1"
	if p.validate() == nil {
		t.Fatal("plain Gemini usage contract accepted for wrapped route")
	}
	for _, changed := range []map[string]any{nil, {"project_id": " "}, {"access_token": ""}, {"expired": "invalid"},
		{"expired": time.Now().Add(4 * time.Minute).Format(time.RFC3339)}} {
		raw := map[string]any{"type": "antigravity", "project_id": "fixture-project", "access_token": "synthetic-token", "expired": time.Now().Add(time.Hour).Format(time.RFC3339)}
		for key, value := range changed {
			raw[key] = value
		}
		dir := t.TempDir()
		data, _ := json.Marshal(raw)
		if err := os.WriteFile(filepath.Join(dir, "selected.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		p.Contract = "antigravity-generate-v1"
		p.AuthFiles = []credentialBinding{{fingerprint([]byte("selected.json")), fingerprint(data)}}
		items, err := loadSelectedCredentials(&config.Config{AuthDir: dir}, p)
		if changed == nil {
			if err != nil || len(items) != 1 {
				t.Fatalf("valid current snapshot rejected: %v", err)
			}
		} else if err == nil {
			t.Fatalf("unusable snapshot accepted: fields=%v", changed)
		}
	}
}

func TestAntigravityGuardAdmitsOnlyPinnedCreditsOperation(t *testing.T) {
	profile := liveProfile{Executor: "antigravity", UpstreamModel: "fixture-model", Contract: "antigravity-generate-v1"}
	auth := &coreauth.Auth{ID: "fixture-auth"}
	endpoint, err := antigravityCreditsEndpoint(auth)
	if err != nil {
		t.Fatal(err)
	}
	allowed := 0
	observer := guardObserver(profile, func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		allowed++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}, func(hash string) bool { return hash == fingerprint([]byte(auth.ID)) },
		func(req *http.Request, _ *helps.HTTPAttemptIdentity) bool {
			return req.Method == http.MethodPost && req.URL.String() == endpoint
		})
	ctx := helps.WithHTTPAttemptObserver(context.Background(), observer)
	client := helps.WithAntigravityHTTPClientTrace(&http.Client{Transport: forbiddenTransport{}}, auth, "credits_query")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", strings.NewReader("{}"))
	response, err := client.Do(req)
	if err != nil || allowed != 1 {
		t.Fatalf("bound default credits query rejected: allowed=%d err=%v", allowed, err)
	}
	_ = response.Body.Close()
	for _, trial := range []struct{ operation, address, method string }{
		{"project_discovery", req.URL.String(), http.MethodPost}, {"oauth_refresh", req.URL.String(), http.MethodPost},
		{"credits_query", "https://unrelated.invalid/v1internal:loadCodeAssist", http.MethodPost},
		{"credits_query", req.URL.String() + "?extra=1", http.MethodPost}, {"credits_query", req.URL.String(), http.MethodGet},
	} {
		client = helps.WithAntigravityHTTPClientTrace(&http.Client{Transport: forbiddenTransport{}}, auth, trial.operation)
		req, _ = http.NewRequestWithContext(ctx, trial.method, trial.address, strings.NewReader("{}"))
		if _, err := client.Do(req); err == nil || allowed != 1 {
			t.Fatal("unbound auxiliary operation admitted")
		}
	}
}

func TestAntigravityCreditsEndpointMatchesExecutorResolution(t *testing.T) {
	for _, trial := range []struct {
		auth *coreauth.Auth
		want string
	}{
		{&coreauth.Auth{}, "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"},
		{&coreauth.Auth{Metadata: map[string]any{"base_url": " http://127.0.0.1:1234/prefix/ "}}, "http://127.0.0.1:1234/prefix/v1internal:loadCodeAssist"},
		{&coreauth.Auth{Attributes: map[string]string{"base_url": "https://attribute.invalid/"}, Metadata: map[string]any{"base_url": "https://metadata.invalid"}}, "https://attribute.invalid/v1internal:loadCodeAssist"},
	} {
		actual, err := antigravityCreditsEndpoint(trial.auth)
		if err != nil || actual != trial.want {
			t.Fatalf("endpoint resolution mismatch: %v", err)
		}
	}
	for _, address := range []string{"relative", "ftp://fixture.invalid", "https://user:secret@fixture.invalid", "https://fixture.invalid?extra=1", "https://fixture.invalid?", "https://fixture.invalid#fragment"} {
		if _, err := antigravityCreditsEndpoint(&coreauth.Auth{Metadata: map[string]any{"base_url": address}}); err == nil {
			t.Fatal("ambiguous auxiliary endpoint accepted")
		}
	}
}

func TestAntigravityModelCatalogEndpointUsesFirstRouteAndDailyDefault(t *testing.T) {
	for _, trial := range []struct {
		auth *coreauth.Auth
		want string
	}{
		{&coreauth.Auth{}, "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"},
		{&coreauth.Auth{Attributes: map[string]string{"base_urls": " , https://first.invalid///,https://second.invalid", "base_url": "https://ignored.invalid"}}, "https://first.invalid/v1internal:fetchAvailableModels"},
		{&coreauth.Auth{Metadata: map[string]any{"base_url": "http://127.0.0.1:1234/prefix/"}}, "http://127.0.0.1:1234/prefix/v1internal:fetchAvailableModels"},
	} {
		actual, err := antigravityAuxiliaryEndpoints(trial.auth)
		if err != nil || actual["model_discovery"] != trial.want {
			t.Fatalf("model catalog route mismatch: %v", err)
		}
	}
	if _, err := antigravityAuxiliaryEndpoints(&coreauth.Auth{Attributes: map[string]string{"base_urls": "https://fixture.invalid?query=1"}}); err == nil {
		t.Fatal("ambiguous model catalog route accepted")
	}
}
