package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestMetaStreamPreservesDeclaredToolName(t *testing.T) {
	const toolName = "collaboration-optimize__spawn_agent"
	body := []byte(`{"tools":[{"type":"function","name":"collaboration-optimize__spawn_agent","parameters":{"type":"object","properties":{},"required":[],"additionalProperties":false},"strict":true}],"input":[{"type":"agent_message","author":"review-agent","recipient":"all","content":[{"type":"input_text","text":"hello"}]}]}`)
	completion := `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","id":"fc_1","name":"collaboration-optimize__spawn_agent","call_id":"call1","arguments":"{}"}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if got := gjson.GetBytes(raw, "tools.0.name").String(); got != toolName {
			t.Errorf("outbound declaration renamed: %s", raw)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", completion)
	}))
	defer server.Close()
	cfg := &config.Config{}
	cfg.Client.Codex.OptimizeMultiAgentV2 = true
	e := NewMetaExecutor(cfg)
	auth := &cliproxyauth.Auth{ID: "strict-review-meta", Provider: "meta", Attributes: map[string]string{"api_key": "test-token", "base_url": server.URL}}
	req := cliproxyexecutor.Request{Model: "muse-spark-1.3", Payload: body}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body, Headers: http.Header{"User-Agent": []string{"codex-tui/0.154.0"}}, Stream: true}
	prepared, err := e.prepareResponsesRequest(context.Background(), auth, req, opts, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(prepared.body, "tools.0.name").String(); got != toolName {
		t.Fatalf("fixture changed declared tool name: %s", prepared.body)
	}
	if got := gjson.GetBytes(prepared.body, "input.0.type").String(); got != "message" {
		t.Fatalf("fixture did not convert agent message: %s", prepared.body)
	}
	// The non-stream path is a positive control: the declared tool remains callable.
	nonStream, err := e.translateMetaCompleted(context.Background(), req, prepared, []byte("data: "+completion+"\n\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(nonStream.payload, "output.0.name").String(); got != toolName {
		t.Fatalf("non-stream positive control changed declared tool: %s", nonStream.payload)
	}
	streamed, err := e.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	var result []byte
	for chunk := range streamed.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		result = append(result, chunk.Payload...)
	}
	if !bytes.Contains(result, []byte(`"name":"`+toolName+`"`)) {
		t.Fatalf("stream changed a declared user tool merely because input was rewritten: %s", result)
	}
}

// Meta uses a Responses-compatible backend, so compatibility conversion is
// independent of the Codex optimization switch and client user agent.
func TestMetaAgentMessageCompatibilityScope(t *testing.T) {
	for _, tc := range []struct {
		name, userAgent string
		enabled         bool
	}{
		{"disabled_other_client", "curl/8", false},
		{"disabled_codex", "codex-tui/0.154.0", false},
		{"enabled_other_client", "curl/8", true},
		{"enabled_codex", "codex-tui/0.154.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Client.Codex.OptimizeMultiAgentV2 = tc.enabled
			body := []byte(`{"tools":[{"type":"function","name":"collaboration-optimize__spawn_agent","parameters":{"type":"object","properties":{}}}],"input":[{"type":"agent_message","author":"agent","recipient":"all","internal_chat_message_metadata_passthrough":{"turn_id":"turn"},"content":[{"type":"input_text","text":"hello"}]}]}`)
			e := NewMetaExecutor(cfg)
			auth := &cliproxyauth.Auth{Provider: "meta", Attributes: map[string]string{"api_key": "test", "base_url": "https://example.invalid"}}
			prepared, err := e.prepareResponsesRequest(context.Background(), auth,
				cliproxyexecutor.Request{Model: "muse-spark-1.3", Payload: body},
				cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body, Headers: http.Header{"User-Agent": []string{tc.userAgent}}}, true)
			if err != nil {
				t.Fatal(err)
			}
			item := gjson.GetBytes(prepared.body, "input.0")
			if item.Get("type").String() != "message" || item.Get("role").String() != "user" || item.Get("content.0.text").String() != "hello" {
				t.Fatalf("agent message not converted: %s", item.Raw)
			}
			for _, field := range []string{"author", "recipient", "internal_chat_message_metadata_passthrough"} {
				if item.Get(field).Exists() {
					t.Fatalf("compatibility metadata %q survived: %s", field, item.Raw)
				}
			}
			if got := gjson.GetBytes(prepared.body, "tools.0.name").String(); got != "collaboration-optimize__spawn_agent" {
				t.Fatalf("tool name changed: %q", got)
			}
		})
	}
}

func TestMetaOrphanDelegationAfterPayloadRules(t *testing.T) {
	for _, tc := range []struct {
		name        string
		source      sdktranslator.Format
		enabled     bool
		header      string
		inject      bool
		wantMessage bool
	}{
		{"codex_enabled", sdktranslator.FormatCodex, true, "collab_spawn", false, true},
		{"codex_disabled", sdktranslator.FormatCodex, false, "collab_spawn", false, false},
		{"codex_missing_header", sdktranslator.FormatCodex, true, "", false, false},
		{"responses_idempotent", sdktranslator.FormatOpenAIResponse, true, "collab_spawn", false, true},
		{"responses_injected_by_rule", sdktranslator.FormatOpenAIResponse, true, "collab_spawn", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Codex.OrphanDelegationCompatibility = tc.enabled
			orphan := map[string]any{"type": "function_call_output", "namespace": "codex_app", "name": "create_thread", "output": "handoff"}
			body := []byte(`{"input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"handoff"}]}`)
			if tc.inject {
				body = []byte(`{"input":[]}`)
				cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "muse-spark-1.3"}}, Params: map[string]any{"input": []any{orphan}}}}
			}
			headers := http.Header{}
			if tc.header != "" {
				headers.Set("X-Openai-Subagent", tc.header)
			}
			e := NewMetaExecutor(cfg)
			auth := &cliproxyauth.Auth{Provider: "meta", Attributes: map[string]string{"api_key": "test", "base_url": "https://example.invalid"}}
			prepared, err := e.prepareResponsesRequest(context.Background(), auth,
				cliproxyexecutor.Request{Model: "muse-spark-1.3", Payload: body},
				cliproxyexecutor.Options{SourceFormat: tc.source, OriginalRequest: body, Headers: headers}, true)
			if err != nil {
				t.Fatal(err)
			}
			item := gjson.GetBytes(prepared.body, "input.0")
			if tc.wantMessage {
				if item.Get("type").String() != "message" || item.Get("role").String() != "user" || item.Get("content.0.text").String() != "Tool output from codex_app__create_thread:\nhandoff" {
					t.Fatalf("orphan delegation not converted exactly once: %s", item.Raw)
				}
			} else if item.Get("type").String() != "function_call_output" {
				t.Fatalf("disabled or non-subagent request was changed: %s", item.Raw)
			}
		})
	}
}
