package openai

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	multiagentv2 "github.com/router-for-me/CLIProxyAPI/v8/internal/client/codex/optimize-multi-agent-v2"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestResponsesPreparesMultiAgentToolsForHistoricalSDKConfig(t *testing.T) {
	payload := []byte(`{"tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"spawn_agent","description":"Spawns an agent.","parameters":{"properties":{"message":{"type":"string","encrypted":true}}}}]}]}`)
	for _, tc := range []struct {
		name, userAgent string
		cfg             *config.SDKConfig
		want            bool
	}{
		{name: "nil", userAgent: "codex-tui/0.154.0"},
		{name: "disabled", cfg: &config.SDKConfig{}, userAgent: "codex-tui/0.154.0"},
		{name: "legacy_sdk", cfg: &config.SDKConfig{CodexOptimizeMultiAgentV2: true}, userAgent: "codex-tui/0.154.0", want: true},
		{name: "canonical", cfg: &config.SDKConfig{Client: config.ClientConfig{Codex: config.CodexClientConfig{OptimizeMultiAgentV2: true}}}, userAgent: "codex-tui/0.154.0", want: true},
		{name: "unrelated_client", cfg: &config.SDKConfig{CodexOptimizeMultiAgentV2: true}, userAgent: "curl/8.7.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", tc.userAgent)
			handler := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(tc.cfg, nil))
			got := handler.prepareCodexMultiAgentV2Tools(c, payload)
			value, prepared := c.Get(multiagentv2.CodexMultiAgentV2ToolsPreparedContextKey)
			if tc.want {
				if !prepared || value != true || gjson.GetBytes(got, "tools.0.tools.0.parameters.properties.message.encrypted").Exists() {
					t.Fatalf("enabled configuration did not prepare tool: prepared=%v payload=%s", value, got)
				}
			} else if prepared || !bytes.Equal(got, payload) {
				t.Fatalf("disabled or unrelated request changed: prepared=%v payload=%s", value, got)
			}
		})
	}
}
