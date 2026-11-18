package test

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// A real Claude Code client can keep only the reserved placeholder while every
// tool is loaded eagerly. That request carries no discovery round, so the
// bridge must drop the deferred declarations and dispatch instead of rejecting
// the turn with 422.
const claudeDeferredPlaceholderOnlyRequest = `{"model":"placeholder-model","max_tokens":64,"messages":[{"role":"user","content":"say OK"}],"tools":[{"name":"Glob","description":"Fast file pattern matching","input_schema":{"type":"object","properties":{}}},{"name":"DeferredToolPlaceholder","description":"Reserved placeholder that keeps deferred tool loading active; never call this tool.","input_schema":{"type":"object","properties":{}},"defer_loading":true}]}`

func TestClaudeDeferredPlaceholderOnlyRequestReachesUpstream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "execute"
		response := `{"id":"chat_1","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`
		if stream {
			name = "stream"
			response = "data: " + response + "\n\ndata: [DONE]\n\n"
		}
		t.Run(name, func(t *testing.T) {
			server, received := newResponsesToolsProviderServer(t, response)
			cfg := &config.Config{}
			cfg.NormalizeResponsesToolsConfig()
			mgr := auth.NewManager(nil, nil, nil)
			mgr.RegisterExecutor(runtimeexecutor.NewOpenAICompatExecutor("placeholder-chat", cfg))
			mgr.SetRetryConfig(0, 0, 0)
			mgr.SetConfig(cfg)
			registerResponsesToolsProvider(t, mgr, "claude-placeholder-chat", "placeholder-chat", "placeholder-model", server.URL)
			body := []byte(claudeDeferredPlaceholderOnlyRequest)
			req := executor.Request{Model: "placeholder-model", Payload: body}
			opts := executor.Options{SourceFormat: translator.FormatClaude, OriginalRequest: body, Stream: stream}
			var err error
			if stream {
				result, errStream := mgr.ExecuteStream(context.Background(), []string{"placeholder-chat"}, req, opts)
				err = errStream
				if result != nil {
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							err = chunk.Err
						}
					}
				}
			} else {
				_, err = mgr.Execute(context.Background(), []string{"placeholder-chat"}, req, opts)
			}
			if err != nil {
				t.Fatalf("placeholder-only request rejected: %v", err)
			}
			wire := string(received.get())
			if !strings.Contains(wire, `"Glob"`) {
				t.Fatalf("surviving declaration was not dispatched: %s", wire)
			}
			if strings.Contains(wire, "DeferredToolPlaceholder") || strings.Contains(wire, "defer_loading") || strings.Contains(wire, "tool_reference") {
				t.Fatalf("deferred contract reached the upstream: %s", wire)
			}
		})
	}
}
