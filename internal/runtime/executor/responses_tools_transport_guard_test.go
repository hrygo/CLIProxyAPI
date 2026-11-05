package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/wsrelay"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

// These tests use local transports and inspect requests after the executor's
// payload rules, not merely the intermediate translation helper.
func TestVertexToolGuardAtTransport(t *testing.T) {
	for _, interactions := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			name := fmt.Sprintf("interactions=%t/stream=%t", interactions, stream)
			t.Run(name, func(t *testing.T) {
				var requests, tokens atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/token" {
						tokens.Add(1)
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
						return
					}
					requests.Add(1)
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					path := "tools.0.function_declarations.0.name"
					if interactions {
						path = "tools.0.name"
					}
					if got := gjson.GetBytes(body, path).String(); got != "lookup" {
						t.Errorf("wire %s = %q, body %s", path, got, body)
					}
					if interactions {
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, "event: interaction.complete\ndata: {\"event_type\":\"interaction.complete\",\"interaction\":{\"id\":\"ok\",\"status\":\"completed\"}}\n\n")
						} else {
							_, _ = io.WriteString(w, `{"id":"ok","object":"interaction","status":"completed","steps":[]}`)
						}
					} else if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
					} else {
						_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
					}
				}))
				defer server.Close()
				target, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				transport := http.DefaultTransport.(*http.Transport).Clone()
				transport.Proxy = nil
				t.Cleanup(transport.CloseIdleConnections)
				ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", issue6258RedirectTransport{target: target, base: transport})
				ctx = core.WithWireContract(ctx, &core.WireContract{RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true})
				var sa map[string]any
				if err := json.Unmarshal(testVertexServiceAccountJSON(t, server.URL+"/token"), &sa); err != nil {
					t.Fatal(err)
				}
				a := &auth.Auth{ID: name, Provider: "vertex", Metadata: map[string]any{"project_id": "local", "location": "global", "service_account": sa}}
				format := translator.FormatGemini
				payload := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`
				if interactions {
					a.Metadata["interactions"] = true
					format = translator.FormatInteractions
					payload = `{"model":"review-model","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`
				}
				req := core.Request{Model: "review-model", Payload: []byte(payload)}
				opts := core.Options{SourceFormat: format, ResponseFormat: format, Stream: stream}
				bad := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: map[string]any{"tools": []any{}}}}}}
				run := func(cfg *config.Config) error {
					e := NewGeminiVertexExecutor(cfg)
					if !stream {
						_, err := e.Execute(ctx, a, req, opts)
						return err
					}
					result, err := e.ExecuteStream(ctx, a, req, opts)
					if err != nil {
						return err
					}
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							return chunk.Err
						}
					}
					return nil
				}
				var local interface{ StatusCode() int }
				if err := run(bad); !errors.As(err, &local) || local.StatusCode() != http.StatusUnprocessableEntity {
					t.Fatalf("corrupted contract: %v", err)
				}
				if requests.Load() != 0 || tokens.Load() != 0 {
					t.Fatalf("rejected request contacted endpoint: model=%d token=%d", requests.Load(), tokens.Load())
				}
				if err := run(&config.Config{}); err != nil {
					t.Fatalf("valid request: %v", err)
				}
				if requests.Load() != 1 || tokens.Load() != 1 {
					t.Fatalf("valid request did not reach endpoint: model=%d token=%d", requests.Load(), tokens.Load())
				}
			})
		}
	}
}

func TestAIStudioToolGuardAtRelay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			const authID = "aistudio-local-guard"
			connected := make(chan struct{}, 1)
			relay := wsrelay.NewManager(wsrelay.Options{ProviderFactory: func(*http.Request) (string, error) { return authID, nil }, OnConnected: func(string) { connected <- struct{}{} }})
			server := httptest.NewServer(relay.Handler())
			defer server.Close()
			defer func() {
				if err := relay.Stop(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+relay.Path(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			select {
			case <-connected:
			case <-time.After(3 * time.Second):
				t.Fatal("relay did not connect")
			}
			peerDone := make(chan error, 1)
			go func() {
				var msg wsrelay.Message
				if err := conn.ReadJSON(&msg); err != nil {
					peerDone <- err
					return
				}
				var peerErr error
				if msg.Type != wsrelay.MessageTypeHTTPReq || gjson.Get(msg.Payload["body"].(string), "tools.0.function_declarations.0.name").String() != "lookup" {
					peerErr = fmt.Errorf("unexpected relay request: %v", msg.Payload)
				}
				body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
				if !stream {
					if err := conn.WriteJSON(wsrelay.Message{ID: msg.ID, Type: wsrelay.MessageTypeHTTPResp, Payload: map[string]any{"status": 200, "body": body}}); err != nil {
						peerDone <- err
						return
					}
					peerDone <- peerErr
					return
				}
				for _, response := range []wsrelay.Message{{ID: msg.ID, Type: wsrelay.MessageTypeStreamStart, Payload: map[string]any{"status": 200}}, {ID: msg.ID, Type: wsrelay.MessageTypeStreamChunk, Payload: map[string]any{"data": "data: " + body + "\n\n"}}, {ID: msg.ID, Type: wsrelay.MessageTypeStreamEnd}} {
					if err := conn.WriteJSON(response); err != nil {
						peerDone <- err
						return
					}
				}
				peerDone <- peerErr
			}()
			req := core.Request{Model: "review-model", Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}}]}]}`)}
			opts := core.Options{SourceFormat: translator.FormatGemini, ResponseFormat: translator.FormatGemini, Stream: stream}
			a := &auth.Auth{ID: authID, Provider: "aistudio"}
			bad := &config.Config{Payload: config.PayloadConfig{Override: []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: map[string]any{"tools": []any{}}}}}}
			run := func(cfg *config.Config, ctx context.Context) error {
				e := NewAIStudioExecutor(cfg, "aistudio", relay)
				if !stream {
					_, err := e.Execute(ctx, a, req, opts)
					return err
				}
				result, err := e.ExecuteStream(ctx, a, req, opts)
				if err != nil {
					return err
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						return chunk.Err
					}
				}
				return nil
			}
			baseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx := core.WithUpstreamAttemptTracker(core.WithWireContract(baseCtx, &core.WireContract{RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true}))
			var local interface{ StatusCode() int }
			if err := run(bad, ctx); !errors.As(err, &local) || local.StatusCode() != http.StatusUnprocessableEntity {
				t.Fatalf("corrupted contract: %v", err)
			}
			if core.UpstreamAttempted(ctx) {
				t.Fatal("rejected request reached relay")
			}
			if err := run(&config.Config{}, ctx); err != nil {
				t.Fatalf("valid request: %v", err)
			}
			if err := <-peerDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}
