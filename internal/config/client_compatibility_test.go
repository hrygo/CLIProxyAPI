package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClientCodexHistoricalProgrammaticFields(t *testing.T) {
	for _, cfg := range []*Config{
		{Codex: CodexConfig{OptimizeMultiAgentV2: true}},
		{SDKConfig: SDKConfig{CodexOptimizeMultiAgentV2: true}},
		{SDKConfig: SDKConfig{Client: ClientConfig{Codex: CodexClientConfig{OptimizeMultiAgentV2: true}}}},
	} {
		if !cfg.CodexMultiAgentV2Enabled() || !cfg.ForAPIKey().CodexMultiAgentV2Enabled() {
			t.Fatal("historical programmatic client setting was ignored")
		}
		for _, encoding := range []struct {
			marshal   func(any) ([]byte, error)
			unmarshal func([]byte, any) error
		}{{json.Marshal, json.Unmarshal}, {yaml.Marshal, yaml.Unmarshal}} {
			data, err := encoding.marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var restored Config
			if err := encoding.unmarshal(data, &restored); err != nil || !restored.Client.Codex.OptimizeMultiAgentV2 {
				t.Fatalf("canonical snapshot lost the effective setting: %v", err)
			}
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("config-version: 8\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := SaveConfigPreserveComments(path, cfg, true); err != nil {
			t.Fatal(err)
		}
		restored, err := LoadConfig(path)
		if err != nil || !restored.Client.Codex.OptimizeMultiAgentV2 {
			t.Fatalf("save lost the effective setting: %v", err)
		}
	}
	if (*SDKConfig)(nil).CodexMultiAgentV2Enabled() || (*Config)(nil).CodexMultiAgentV2Enabled() {
		t.Fatal("nil configuration enabled optimization")
	}
}

func TestClientCodexProgrammaticCompatibilitySnapshotsAndScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{name: "disabled"},
		{name: "canonical", cfg: Config{SDKConfig: SDKConfig{Client: ClientConfig{Codex: CodexClientConfig{OptimizeMultiAgentV2: true}}}}, want: true},
		{name: "legacy_sdk", cfg: Config{SDKConfig: SDKConfig{CodexOptimizeMultiAgentV2: true}}, want: true},
		{name: "legacy_codex", cfg: Config{Codex: CodexConfig{OptimizeMultiAgentV2: true}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Client.Codex.EnableApplyPatch = true
			cfg.CodexHeaderDefaults.UserAgent = "oauth-agent"
			cfg.OAuthOnlyFields = map[string]bool{"codex-header-defaults.user-agent": true}
			if cfg.CodexMultiAgentV2Enabled() != tc.want {
				t.Fatalf("effective setting = %t, want %t", cfg.CodexMultiAgentV2Enabled(), tc.want)
			}
			api := cfg.ForAPIKey()
			if api == &cfg || api.CodexMultiAgentV2Enabled() != tc.want || api.CodexHeaderDefaults.UserAgent != "" || cfg.CodexHeaderDefaults.UserAgent != "oauth-agent" {
				t.Fatal("API-key scoping changed the setting or shared the original scope")
			}
			for _, encoding := range []struct {
				name      string
				marshal   func(any) ([]byte, error)
				unmarshal func([]byte, any) error
			}{
				{name: "json", marshal: json.Marshal, unmarshal: json.Unmarshal},
				{name: "yaml", marshal: yaml.Marshal, unmarshal: yaml.Unmarshal},
			} {
				t.Run(encoding.name, func(t *testing.T) {
					data, err := encoding.marshal(&cfg)
					if err != nil {
						t.Fatal(err)
					}
					var restored Config
					if err := encoding.unmarshal(data, &restored); err != nil {
						t.Fatal(err)
					}
					if restored.Client.Codex.OptimizeMultiAgentV2 != tc.want || !restored.Client.Codex.EnableApplyPatch {
						t.Fatalf("snapshot changed client settings: %s", data)
					}
					if strings.Count(string(data), "optimize-multi-agent-v2") != 1 || strings.Contains(string(data), "CodexOptimizeMultiAgentV2") {
						t.Fatalf("snapshot did not use a single canonical field: %s", data)
					}
				})
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("config-version: 8\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := SaveConfigPreserveComments(path, &cfg, true); err != nil {
				t.Fatal(err)
			}
			restored, err := LoadConfig(path)
			if err != nil || restored.Client.Codex.OptimizeMultiAgentV2 != tc.want || !restored.Client.Codex.EnableApplyPatch {
				t.Fatalf("saved client settings changed: %v", err)
			}
			if cfg.Client.Codex.OptimizeMultiAgentV2 != tc.cfg.Client.Codex.OptimizeMultiAgentV2 || cfg.CodexOptimizeMultiAgentV2 != tc.cfg.CodexOptimizeMultiAgentV2 || cfg.Codex.OptimizeMultiAgentV2 != tc.cfg.Codex.OptimizeMultiAgentV2 {
				t.Fatal("snapshot or save mutated the programmatic configuration")
			}
		})
	}
}
