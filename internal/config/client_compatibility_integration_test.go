package config_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	gjson "github.com/MoeForge/moego/ojson"
	multiagentv2 "github.com/router-for-me/CLIProxyAPI/v8/internal/client/codex/optimize-multi-agent-v2"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestHistoricalMultiAgentConfigurationAffectsRequests(t *testing.T) {
	message := []byte(`{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"hello"}]}]}`)
	tools := []byte(`{"tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","description":"Spawns an agent.","parameters":{"properties":{"message":{"type":"string","encrypted":true}}}}]}]}`)
	for _, tc := range []struct {
		name, agent string
		cfg         *config.Config
		want        bool
	}{
		{name: "nil", agent: "codex-tui/0.154.0"},
		{name: "disabled", cfg: &config.Config{}, agent: "codex-tui/0.154.0"},
		{name: "legacy_sdk", cfg: &config.Config{SDKConfig: config.SDKConfig{CodexOptimizeMultiAgentV2: true}}, agent: "codex-tui/0.154.0", want: true},
		{name: "legacy_codex", cfg: &config.Config{Codex: config.CodexConfig{OptimizeMultiAgentV2: true}}, agent: "codex-tui/0.154.0", want: true},
		{name: "canonical", cfg: &config.Config{SDKConfig: config.SDKConfig{Client: config.ClientConfig{Codex: config.CodexClientConfig{OptimizeMultiAgentV2: true}}}}, agent: "codex-tui/0.154.0", want: true},
		{name: "unrelated_client", cfg: &config.Config{Codex: config.CodexConfig{OptimizeMultiAgentV2: true}}, agent: "curl/8.7.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{"User-Agent": {tc.agent}}
			// Configuration loading folds the historical fields into the canonical
			// client layout; mirror that here for programmatic construction.
			if tc.cfg != nil {
				tc.cfg.Client.Codex.OptimizeMultiAgentV2 = tc.cfg.CodexMultiAgentV2Enabled()
			}
			got := multiagentv2.RewriteCodexMultiAgentV2Input(context.Background(), headers, message, tc.cfg)
			if tc.want {
				if gjson.GetBytes(got, "input.0.type").String() != "message" || gjson.GetBytes(got, "input.0.role").String() != "user" {
					t.Fatalf("legacy or canonical setting did not rewrite agent input: %s", got)
				}
			} else if !bytes.Equal(got, message) {
				t.Fatalf("disabled or unrelated request changed: %s", got)
			}
			got, optimized := multiagentv2.OptimizeCodexMultiAgentV2Request(context.Background(), headers, tools, tc.cfg)
			if optimized != tc.want {
				t.Fatalf("namespace optimized = %t, want %t: %s", optimized, tc.want, got)
			}
			if tc.want {
				if gjson.GetBytes(got, "tools.0.name").String() != "collaboration-optimize" || gjson.GetBytes(got, "tools.0.tools.0.parameters.properties.message.encrypted").Exists() {
					t.Fatalf("legacy or canonical setting did not prepare spawn_agent: %s", got)
				}
			} else if !bytes.Equal(got, tools) {
				t.Fatalf("disabled or unrelated tools changed: %s", got)
			}
		})
	}
}
