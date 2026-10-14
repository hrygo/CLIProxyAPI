package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestOpenAICompatToolTerminalMatrix(t *testing.T) {
	for _, finish := range []bool{false, true} {
		for _, done := range []bool{false, true} {
			for _, completeArgs := range []bool{false, true} {
				for _, lateIdentity := range []bool{false, true} {
					t.Run(fmt.Sprintf("finish_%v/done_%v/args_%v/late_identity_%v", finish, done, completeArgs, lateIdentity), func(t *testing.T) {
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "text/event-stream")
							args := `{"nonce":`
							if completeArgs {
								args = `{"nonce":"fixture"}`
							}
							call := map[string]any{"index": 0, "id": "call_fixture", "type": "function", "function": map[string]any{"name": "record_nonce", "arguments": args}}
							if lateIdentity {
								delete(call, "id")
								call["function"] = map[string]any{"arguments": args}
							}
							writeChunk := func(delta map[string]any, reason any) {
								body, err := json.Marshal(map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "created": 1773896263, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}})
								if err != nil {
									t.Error(err)
									return
								}
								_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
							}
							writeChunk(map[string]any{"role": "assistant", "tool_calls": []any{call}}, nil)
							if lateIdentity {
								writeChunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_fixture", "type": "function", "function": map[string]any{"name": "record_nonce", "arguments": ""}}}}, nil)
							}
							if finish {
								writeChunk(map[string]any{}, "tool_calls")
							}
							if done {
								// A repeated terminator must not emit duplicate calls/completions.
								_, _ = fmt.Fprint(w, "data: [DONE]\n\ndata: [DONE]\n\n")
							}
						}))
						defer server.Close()
						executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
						auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
						body := []byte(`{"model":"fixture-model","input":"Record the fixture nonce once.","tools":[{"type":"function","name":"record_nonce","parameters":{"type":"object","properties":{"nonce":{"type":"string"}},"required":["nonce"],"additionalProperties":false}}],"stream":true}`)
						result, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "fixture-model", Payload: body}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body, Stream: true})
						if err != nil {
							t.Fatal(err)
						}
						var streamErr error
						var stream strings.Builder
						for chunk := range result.Chunks {
							stream.Write(chunk.Payload)
							if chunk.Err != nil {
								streamErr = chunk.Err
							}
						}
						completed, calls, dispatches := 0, 0, 0
						for _, line := range strings.Split(stream.String(), "\n") {
							if !strings.HasPrefix(line, "data:") {
								continue
							}
							var event struct {
								Type string `json:"type"`
								Item struct {
									Type, Name, ID, Arguments string
									CallID                    string `json:"call_id"`
								} `json:"item"`
							}
							if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
								t.Fatalf("invalid translated record: %s: %v", line, err)
							}
							if event.Type == "response.completed" {
								completed++
							}
							if event.Type == "response.output_item.done" && event.Item.Type == "function_call" {
								calls++
								if event.Item.ID != "fc_call_fixture" || event.Item.CallID != "call_fixture" || event.Item.Name != "record_nonce" {
									t.Fatalf("late identity changed: %+v", event.Item)
								}
								var arguments map[string]any
								if json.Unmarshal([]byte(event.Item.Arguments), &arguments) == nil && len(arguments) == 1 && arguments["nonce"] == "fixture" {
									// Invalid source-model arguments cannot execute a fixture tool.
									dispatches++
								}
							}
						}
						wantCalls := 0
						if finish || (done && completeArgs) {
							wantCalls = 1
						}
						wantCompleted := 0
						if done && (finish || completeArgs) {
							wantCompleted = 1
						}
						wantDispatches := 0
						if completeArgs {
							wantDispatches = wantCalls
						}
						if calls != wantCalls || dispatches != wantDispatches || completed != wantCompleted || (streamErr == nil) != (wantCompleted == 1) {
							t.Fatalf("calls/dispatches/completed/error = %d/%d/%d/%v, want %d/%d/%d", calls, dispatches, completed, streamErr, wantCalls, wantDispatches, wantCompleted)
						}
						if streamErr != nil {
							var status interface{ StatusCode() int }
							if !errors.As(streamErr, &status) || status.StatusCode() != http.StatusBadGateway {
								t.Fatalf("missing terminal status = %v, want 502", streamErr)
							}
						}
					})
				}
			}
		}
	}
}
