package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestSelectedCredentialUsesOnlyBoundFileAndCanonicalSynthesis(t *testing.T) {
	dir := t.TempDir()
	name := "selected.json"
	raw := []byte(`{"type":"codex","access_token":"synthetic-access","refresh_token":"synthetic-refresh","plan_type":"plus","model_aliases":[{"name":"fixture-upstream","alias":"fixture-client"}]}`)
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.json"), []byte("invalid unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	p := liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{fingerprint([]byte(name)), fingerprint(raw)}}}
	auths, err := loadSelectedCredentials(&config.Config{AuthDir: dir}, p)
	if err != nil || len(auths) != 1 || auths[0].ID != name || auths[0].Provider != "codex" || auths[0].Attributes["auth_kind"] != "oauth" {
		t.Fatalf("selected canonical auth unavailable: count=%d error=%v", len(auths), err)
	}
	store := newSnapshotStore(auths, "codex")
	items, _ := store.List(context.Background())
	if items[0].Attributes[coreauth.AttributePath] != "" || items[0].Attributes[coreauth.AttributeSource] != "" || items[0].Metadata["refresh_token"] != nil {
		t.Fatal("snapshot retains persistence or refresh authority")
	}
	after, _ := os.ReadFile(filepath.Join(dir, name))
	if string(after) != string(raw) {
		t.Fatal("source credential was rewritten")
	}
}

func TestCredentialBindingValidationAndFailClosedRead(t *testing.T) {
	for _, values := range [][]string{{""}, {"x:y"}, {strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)}} {
		if _, err := parseCredentialBindings(values); err == nil {
			t.Fatal("invalid or duplicate binding accepted")
		}
	}
	for _, trial := range []struct{ name, raw string }{
		{"wrong-provider", `{"type":"meta","access_token":"synthetic"}`},
		{"disabled", `{"type":"codex","disabled":true,"access_token":"synthetic"}`},
		{"invalid-json", `{"type":`},
	} {
		t.Run(trial.name, func(t *testing.T) {
			dir := t.TempDir()
			name := "selected.json"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(trial.raw), 0600); err != nil {
				t.Fatal(err)
			}
			p := liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{fingerprint([]byte(name)), fingerprint([]byte(trial.raw))}}}
			if _, err := loadSelectedCredentials(&config.Config{AuthDir: dir}, p); err == nil {
				t.Fatal("unusable selection accepted")
			}
			p.AuthFiles[0].ContentSHA256 = strings.Repeat("c", 64)
			if _, err := loadSelectedCredentials(&config.Config{AuthDir: dir}, p); err == nil {
				t.Fatal("changed credential accepted")
			}
		})
	}
}

func TestNativeAPIProfileDoesNotScanOAuthAndNativeOAuthRequiresBinding(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-auth-directory")
	cfg := &config.Config{AuthDir: missing, MetaKey: []config.MetaKey{{APIKey: "synthetic-only"}}}
	if items, err := loadSelectedCredentials(cfg, liveProfile{Executor: "meta"}); err != nil || len(items) != 0 {
		t.Fatal("native API profile attempted OAuth load")
	}
	if _, err := loadSelectedCredentials(cfg, liveProfile{Executor: "codex"}); err == nil {
		t.Fatal("implicit OAuth directory scan allowed")
	}
}

func TestSelectedCredentialRejectsSymlinkAndDetectsLaterChange(t *testing.T) {
	dir := t.TempDir()
	name := "selected.json"
	raw := []byte(`{"type":"codex","access_token":"synthetic-only"}`)
	outside := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
		t.Skip("platform symlink unavailable")
	}
	p := liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{fingerprint([]byte(name)), fingerprint(raw)}}}
	if _, err := loadSelectedCredentials(&config.Config{AuthDir: dir}, p); err == nil {
		t.Fatal("credential symlink accepted")
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySelectedCredentialSources(&config.Config{AuthDir: dir}, p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"type":"codex","access_token":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySelectedCredentialSources(&config.Config{AuthDir: dir}, p); err == nil {
		t.Fatal("source change was hidden")
	}
}

func TestSnapshotCannotExchangeSharedMetaDCA(t *testing.T) {
	source := &coreauth.Auth{ID: "synthetic", Provider: "meta", Metadata: map[string]any{"dca_token": "dca:synthetic", "access_token": "dca:synthetic"}, Attributes: map[string]string{"dca_token": "dca:synthetic"}}
	items, _ := newSnapshotStore([]*coreauth.Auth{source}, "meta").List(context.Background())
	if items[0].Metadata["dca_token"] != nil || items[0].Metadata["access_token"] != nil || items[0].Attributes["dca_token"] != "" {
		t.Fatal("snapshot can exchange shared DCA")
	}
	if source.Metadata["dca_token"] != "dca:synthetic" {
		t.Fatal("source DCA mutated")
	}
}

func TestSelectedCredentialBudgetsAndMissingSource(t *testing.T) {
	if _, err := parseCredentialBindings(make([]string, 9)); err == nil {
		t.Fatal("credential count budget ignored")
	}
	for _, trial := range []struct {
		name string
		raw  []byte
		omit bool
	}{
		{"selected.txt", []byte(`{"type":"codex"}`), false},
		{"selected.json", []byte(strings.Repeat("x", (1<<20)+1)), false},
		{"missing.json", []byte(`{"type":"codex"}`), true},
	} {
		t.Run(trial.name, func(t *testing.T) {
			dir := t.TempDir()
			if !trial.omit {
				if err := os.WriteFile(filepath.Join(dir, trial.name), trial.raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			p := liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{fingerprint([]byte(trial.name)), fingerprint(trial.raw)}}}
			if _, _, err := selectedCredentialPayloads(&config.Config{AuthDir: dir}, p); err == nil {
				t.Fatal("unbounded, missing, or non-JSON credential source accepted")
			}
		})
	}
}

func TestSelectedOAuthProfileCannotMixConfiguredAPIAccounts(t *testing.T) {
	source := &config.Config{CodexKey: []config.CodexKey{{APIKey: "synthetic-only"}}}
	isolated := isolateConfig(source, t.TempDir(), 18419)
	isolateSelectedAPIKeys(isolated, liveProfile{Executor: "codex", AuthFiles: []credentialBinding{{"selected", "bound"}}})
	if len(isolated.CodexKey) != 0 || len(source.CodexKey) != 1 {
		t.Fatal("explicit OAuth profile mixed API accounts or mutated operator config")
	}
}
