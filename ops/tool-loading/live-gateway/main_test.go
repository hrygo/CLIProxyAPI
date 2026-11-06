package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/acceptance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestIsolateConfigDisablesHostedImageGenerationWithoutMutatingSource(t *testing.T) {
	source := &config.Config{}
	before := source.DisableImageGeneration
	isolated := isolateConfig(source, t.TempDir(), 8123)
	if isolated.DisableImageGeneration != config.DisableImageGenerationAll {
		t.Fatal("performance fixtures must not permit provider-hosted image generation")
	}
	if source.DisableImageGeneration != before {
		t.Fatal("isolation mutated the operator configuration")
	}
}

func TestCloseEvidenceWritesPrivateFinalAdmissionWithoutOverwriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	journal, err := acceptance.NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := closeEvidence(journal, dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "admission.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state acceptance.HTTPAdmissionDiagnostics
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || !state.JournalClosed || state.JournalFailed || state.ActiveBodies != 0 || state.MaxAttempts != 2 {
		t.Fatalf("invalid final diagnostic: %+v", state)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("diagnostic is not private")
	}
	if closeEvidence(journal, dir) == nil {
		t.Fatal("overwrote existing admission evidence")
	}
	unchanged, _ := os.ReadFile(path)
	if string(data) != string(unchanged) {
		t.Fatal("admission evidence replaced")
	}
}

func TestSnapshotStoreNeverWritesOrMutatesSource(t *testing.T) {
	source := &coreauth.Auth{ID: "fixture-record", Provider: "codex", Metadata: map[string]any{"access_token": "synthetic-only"}}
	store := newSnapshotStore([]*coreauth.Auth{source}, "codex")
	first, err := store.List(context.Background())
	if err != nil || len(first) != 1 || first[0] == source || first[0].Storage != nil {
		t.Fatal("snapshot did not isolate source")
	}
	first[0].Metadata["access_token"] = "changed"
	second, _ := store.List(context.Background())
	if second[0].Metadata["access_token"] != "synthetic-only" || source.Metadata["access_token"] != "synthetic-only" {
		t.Fatal("snapshot metadata shared across loads")
	}
	if path, err := store.Save(context.Background(), first[0]); err != nil || path != "" {
		t.Fatal("ephemeral save must not publish a path")
	}
	if store.Delete(context.Background(), source.ID) == nil {
		t.Fatal("snapshot delete allowed")
	}
	if other, _ := newSnapshotStore([]*coreauth.Auth{source}, "gemini").List(context.Background()); len(other) != 0 {
		t.Fatal("unrelated credential admitted")
	}
}

func TestIsolateConfigPreservesSourceAndDisablesExternalState(t *testing.T) {
	source := &config.Config{Host: "0.0.0.0", Port: 8317, AuthDir: "/operator/auths", SaveCooldownStatus: true}
	source.APIKeys = []string{"operator-secret"}
	source.Plugins.Enabled, source.Home.Enabled, source.Discovery.Enabled = true, true, true
	isolated := isolateConfig(source, "/private/fixture", 18419)
	if source.AuthDir != "/operator/auths" || source.APIKeys[0] != "operator-secret" || !source.Plugins.Enabled {
		t.Fatal("operator config mutated")
	}
	if isolated.Host != "127.0.0.1" || isolated.Port != 18419 || isolated.AuthDir != "/private/fixture/auths" ||
		isolated.APIKeys[0] != "local-acceptance-placeholder" || isolated.Plugins.Enabled || isolated.Home.Enabled || isolated.Discovery.Enabled || isolated.SaveCooldownStatus {
		t.Fatal("candidate isolation incomplete")
	}
}

func TestActualRouteGuardRejectsMismatchBeforeHTTP(t *testing.T) {
	profile := liveProfile{Executor: "codex", UpstreamModel: "fixture-model", Contract: "openai-responses-v1"}
	observer := guardObserver(profile, func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		t.Fatal("wrong executor admitted to capture/dispatch")
		return nil, nil
	}, func(string) bool { return false }, nil)
	for _, trial := range []struct{ provider, model string }{{"wrong-provider", "fixture-model"}, {"codex", "wrong-model"}, {"codex", "fixture-model"}} {
		t.Run(trial.provider+"/"+trial.model, func(t *testing.T) {
			ctx := helps.WithHTTPAttemptObserver(context.Background(), observer)
			reporter := helps.NewUsageReporter(ctx, trial.provider, trial.model, &coreauth.Auth{ID: "fixture-record"})
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/v1", strings.NewReader("{}"))
			client := reporter.TrackHTTPClient(&http.Client{Transport: forbiddenTransport{}})
			if _, err := client.Do(req); err == nil {
				t.Fatal("route or frozen credential mismatch accepted")
			}
		})
	}
}

func TestProfilesUseSupportedExplicitUsageContracts(t *testing.T) {
	for provider, contract := range map[string]string{"codex": "openai-responses-v1", "meta": "openai-responses-v1", "gemini": "gemini-generate-v1", "claude": "anthropic-messages-v1", "openai-compatible-fixture": "openai-chat-v1"} {
		profile := liveProfile{ClientModel: "fixture", UpstreamModel: "fixture-model", Executor: provider, Contract: contract}
		if err := profile.validate(); err != nil {
			t.Fatalf("valid explicit profile rejected: %s", provider)
		}
		profile.Contract = "unknown"
		if profile.validate() == nil {
			t.Fatal("unknown usage contract accepted")
		}
	}
	if (liveProfile{ClientModel: "fixture", UpstreamModel: "fixture-model", Executor: "unknown", Contract: "openai-chat-v1"}).validate() == nil {
		t.Fatal("unknown executor inferred as Chat")
	}
}

func TestSnapshotCannotRefreshSharedOAuthCredentials(t *testing.T) {
	source := &coreauth.Auth{ID: "fixture-record", Provider: "codex", Metadata: map[string]any{
		"access_token": "synthetic-access", "refresh_token": "synthetic-refresh",
		"token": map[string]any{"refreshToken": "synthetic-nested-refresh"}}}
	store := newSnapshotStore([]*coreauth.Auth{source}, "codex")
	items, _ := store.List(context.Background())
	item := items[0]
	if item.NextRefreshAfter.Year() != 9999 || item.Metadata["refresh_token"] != nil || item.Metadata["token"].(map[string]any)["refreshToken"] != nil {
		t.Fatal("candidate can refresh a shared OAuth snapshot")
	}
	if item.Metadata["access_token"] != "synthetic-access" || source.Metadata["refresh_token"] != "synthetic-refresh" || source.Metadata["token"].(map[string]any)["refreshToken"] != "synthetic-nested-refresh" {
		t.Fatal("access credential or source snapshot changed")
	}
	manager := coreauth.NewManager(store, &coreauth.RoundRobinSelector{}, nil)
	if err := manager.Load(context.Background()); err != nil {
		t.Fatal("snapshot manager load failed")
	}
	loaded := manager.List()
	if len(loaded) != 1 || loaded[0].NextRefreshAfter.Year() != 9999 || loaded[0].Metadata["refresh_token"] != nil {
		t.Fatal("SDK load restored shared refresh capability")
	}
}

type forbiddenTransport struct{}

func TestAcceptanceFrontendIncludesMessagesWithLocalAPIKeyOnly(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		if !acceptanceInferencePath(path) {
			t.Fatalf("isolated frontend missing %s", path)
		}
	}
	for _, path := range []string{"/v1/messages/count_tokens", "/v1/messages/other", "/v0/management/config", "/v1/messages/"} {
		if acceptanceInferencePath(path) {
			t.Fatalf("unrequested frontend admitted %s", path)
		}
	}
	for _, trial := range []struct {
		bearer, key string
		valid       bool
	}{
		{"Bearer local-acceptance-placeholder", "", true},
		{"", "local-acceptance-placeholder", true},
		{"Bearer local-acceptance-placeholder", "local-acceptance-placeholder", true},
		{"Bearer wrong", "local-acceptance-placeholder", false},
		{"Bearer local-acceptance-placeholder", "wrong", false},
		{"", "", false},
	} {
		if acceptanceCredentialMatches(trial.bearer, trial.key) != trial.valid {
			t.Fatal("local credential boundary mismatch")
		}
	}
}

func (forbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("forbidden test dispatch")
}
