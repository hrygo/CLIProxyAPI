package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/wsrelay"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

type geminiFailureSignalHooks struct{ replacement []byte }

func (*geminiFailureSignalHooks) NormalizeRequest(_ context.Context, _, _ sdktranslator.Format, _ string, body []byte, _ bool) []byte {
	return body
}

func (*geminiFailureSignalHooks) TranslateRequest(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (*geminiFailureSignalHooks) NormalizeResponseBefore(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	return body
}

func (*geminiFailureSignalHooks) TranslateResponse(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, []byte, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (h *geminiFailureSignalHooks) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	if bytes.Contains(body, []byte("event: response.completed\n")) {
		return bytes.Clone(h.replacement)
	}
	return body
}

type geminiResponsesDeliveryCapture struct {
	authID  string
	records chan usage.Record
}

func (c *geminiResponsesDeliveryCapture) HandleUsage(_ context.Context, record usage.Record) {
	if record.AuthID == c.authID {
		c.records <- record
	}
}

func TestGeminiResponsesDeliveryClosesBeforeAcknowledgement(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex_api_key", "vertex_service_account", "aistudio", "antigravity"} {
		for _, tc := range []struct {
			name                string
			delivery            error
			cancel              bool
			cancelAfterTerminal bool
			incomplete          bool
			invalidTool         bool
			explicitDone        bool
			cleanEOF            bool
			markerOnly          bool
			wantError           bool
		}{
			{name: "delivered"},
			{name: "incomplete", incomplete: true},
			{name: "delivered_then_canceled", cancelAfterTerminal: true},
			{name: "write_failed", delivery: io.ErrClosedPipe, cancelAfterTerminal: true, wantError: true},
			{name: "delivery_canceled", delivery: context.Canceled, cancelAfterTerminal: true, wantError: true},
			{name: "canceled_before_terminal", delivery: context.Canceled, cancel: true, wantError: true},
			{name: "invalid_ordinary_tool", invalidTool: true, wantError: true},
			{name: "invalid_ordinary_tool_done", invalidTool: true, explicitDone: true, wantError: true},
			{name: "invalid_ordinary_tool_eof", invalidTool: true, cleanEOF: true, wantError: true},
			{name: "done_without_finish", markerOnly: true},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				finish := `,"finishReason":"STOP"`
				if tc.cancel || tc.markerOnly {
					finish = ""
				} else if tc.incomplete {
					finish = `,"finishReason":"MAX_TOKENS"`
				}
				payload := `{"responseId":"delivery-hold","candidates":[{"content":{"parts":[{"text":"answer"}]}` + finish + `}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`
				if provider == "antigravity" {
					payload = `{"response":` + payload + `}`
				}
				frame := "data: " + payload + "\n\n"
				if tc.invalidTool {
					partial := `{"responseId":"delivery-hold","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":"get_weather","partialArgs":[{"jsonPath":"$.city","stringValue":"SECRET_PARTIAL_VALUE","willContinue":true}],"willContinue":true}}]}}]}`
					finishFrame := `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`
					if tc.explicitDone || tc.cleanEOF {
						partial = strings.TrimSuffix(partial, "}") + `,"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`
					}
					if provider == "antigravity" {
						partial = `{"response":` + partial + `}`
						finishFrame = `{"response":` + finishFrame + `}`
					}
					if tc.explicitDone || tc.cleanEOF {
						frame = "data: " + partial + "\n\n"
					} else {
						frame = "data: " + partial + "\n\ndata: " + finishFrame + "\n\n"
					}
				}
				if tc.explicitDone || tc.markerOnly {
					frame += "data: [DONE]\n\ndata: [DONE]\n\n"
				}
				release := make(chan struct{})
				defer close(release)
				authID := t.Name()
				capture := &geminiResponsesDeliveryCapture{authID: authID, records: make(chan usage.Record, 4)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })

				var baseCtx context.Context
				var exec issue6258StreamExecutor
				var auth *cliproxyauth.Auth
				if provider == "aistudio" {
					connected := make(chan struct{})
					relay := wsrelay.NewManager(wsrelay.Options{
						ProviderFactory: func(*http.Request) (string, error) { return authID, nil },
						OnConnected:     func(string) { close(connected) },
					})
					server := httptest.NewServer(relay.Handler())
					t.Cleanup(server.Close)
					t.Cleanup(func() { _ = relay.Stop(context.Background()) })
					conn, _, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+relay.Path(), nil)
					if errDial != nil {
						t.Fatal(errDial)
					}
					t.Cleanup(func() { _ = conn.Close() })
					<-connected
					go func() {
						var request wsrelay.Message
						if conn.ReadJSON(&request) != nil {
							return
						}
						if conn.WriteJSON(wsrelay.Message{ID: request.ID, Type: wsrelay.MessageTypeStreamStart, Payload: map[string]any{"status": 200}}) != nil {
							return
						}
						_ = conn.WriteJSON(wsrelay.Message{ID: request.ID, Type: wsrelay.MessageTypeStreamChunk, Payload: map[string]any{"data": frame}})
						if tc.cleanEOF {
							_ = conn.WriteJSON(wsrelay.Message{ID: request.ID, Type: wsrelay.MessageTypeStreamEnd})
						}
						<-release
					}()
					baseCtx = context.Background()
					exec = NewAIStudioExecutor(&config.Config{}, "aistudio", relay)
					auth = &cliproxyauth.Auth{ID: authID, Provider: "aistudio"}
				} else {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/token" {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, frame)
						w.(http.Flusher).Flush()
						if tc.cleanEOF {
							return
						}
						<-release
					}))
					t.Cleanup(server.Close)
					baseCtx, exec, auth = issue6258Executor(t, provider, server.URL)
					auth.ID = authID
				}

				deliveryCtx, acknowledge := usage.WithStreamDelivery(baseCtx)
				defer acknowledge(context.Canceled)
				ctx, cancel := context.WithCancel(deliveryCtx)
				defer cancel()
				request := []byte(`{"model":"gemini-3.7-flash","input":"hi","stream":true}`)
				if tc.invalidTool {
					request = []byte(`{"model":"gemini-3.7-flash","input":"hi","stream":true,"tools":[{"type":"custom","name":"apply_patch"},{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}`)
				}
				result, errExecute := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{
					Model: "gemini-3.7-flash", Payload: request,
				}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse,
					OriginalRequest: request, Stream: true,
				})
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}

				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				terminals := 0
				streamErrors := 0
				canceled := false
			streamLoop:
				for {
					select {
					case chunk, ok := <-result.Chunks:
						if !ok {
							break streamLoop
						}
						if chunk.Err != nil {
							streamErrors++
							continue
						}
						for _, event := range issue6258Events(chunk.Payload) {
							switch event.Get("type").String() {
							case "response.output_text.delta":
								if tc.cancel && !canceled {
									canceled = true
									cancel()
								}
							case "response.completed", "response.incomplete", "response.failed":
								if tc.invalidTool {
									terminals++
									if event.Get("type").String() != "response.failed" || strings.Contains(event.Raw, "apply_patch") || strings.Contains(event.Raw, "SECRET_PARTIAL_VALUE") {
										t.Fatalf("invalid ordinary tool failure = %s", event.Raw)
									}
									continue
								}
								if (event.Get("type").String() == "response.incomplete") != tc.incomplete {
									t.Fatalf("unexpected terminal event: %s", event.Raw)
								}
								terminals++
								got := event.Get("response.usage")
								if got.Get("input_tokens").Int() != 11 || got.Get("output_tokens").Int() != 4 || got.Get("total_tokens").Int() != 15 {
									t.Fatalf("terminal usage = %s", got.Raw)
								}
								if tc.cancelAfterTerminal {
									cancel()
								}
							}
						}
					case <-timer.C:
						t.Fatal("output channel did not close while upstream and delivery acknowledgement remained open")
					}
				}
				if tc.cancel && (!canceled || terminals != 0) {
					t.Fatalf("before-terminal cancellation: canceled=%v terminals=%d", canceled, terminals)
				}
				if !tc.cancel && (terminals != 1 || streamErrors != 0) {
					t.Fatalf("terminals=%d errors=%d, want one terminal and no errors", terminals, streamErrors)
				}
				if !tc.cancel && !tc.invalidTool && !usage.StreamDeliverySupported(ctx) {
					t.Fatal("producer stopped delivery support before acknowledgement")
				}
				acknowledge(tc.delivery)
				select {
				case record := <-capture.records:
					if record.Failed != tc.wantError {
						t.Fatalf("usage failed=%v, want %v; failure=%+v", record.Failed, tc.wantError, record.Fail)
					}
					if record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 || record.Detail.TotalTokens != 15 {
						t.Fatalf("usage detail = %+v", record.Detail)
					}
					if tc.delivery != nil && !strings.Contains(record.Fail.Body, tc.delivery.Error()) {
						t.Fatalf("usage failure body = %q, want %q", record.Fail.Body, tc.delivery.Error())
					}
					if tc.invalidTool && (strings.Contains(record.Fail.Body, "apply_patch") || strings.Contains(record.Fail.Body, "SECRET_PARTIAL_VALUE")) {
						t.Fatalf("ordinary tool usage failure exposed or misidentified its cause: %q", record.Fail.Body)
					}
				case <-timer.C:
					t.Fatal("missing usage record after delivery acknowledgement")
				}
			})
		}
	}
}

func TestGeminiResponsesFailureSignalsOverrideCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, event, payload string
	}{
		{name: "failed_payload", event: "response.completed", payload: `{"type":"response.failed","response":{"status":"failed"}}`},
		{name: "response_error", event: "response.completed", payload: `{"type":"response.completed","response":{"error":{"message":"failed"}}}`},
		{name: "top_error", event: "response.completed", payload: `{"type":"response.completed","error":{"message":"failed"}}`},
		{name: "code_message", event: "response.completed", payload: `{"type":"response.completed","code":"bad","message":"failed"}`},
		{name: "failed_event", event: "error", payload: `{"type":"response.completed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replacement := []byte("event: " + tc.event + "\ndata: " + tc.payload + "\n\n")
			sdktranslator.SetPluginHooks(&geminiFailureSignalHooks{replacement: replacement})
			t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`+"\n\n")
				w.(http.Flusher).Flush()
				<-release
			}))
			defer server.Close()
			defer close(release)
			capture := &geminiResponsesDeliveryCapture{authID: t.Name(), records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
			ctx, acknowledge := usage.WithStreamDelivery(context.Background())
			defer acknowledge(context.Canceled)
			request := []byte(`{"model":"gemini-3.7-flash","input":"hi","stream":true}`)
			result, errExecute := NewGeminiExecutor(&config.Config{}).ExecuteStream(ctx, &cliproxyauth.Auth{
				ID: t.Name(), Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL},
			}, cliproxyexecutor.Request{Model: "gemini-3.7-flash", Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse,
				OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatalf("ExecuteStream() error = %v", errExecute)
			}
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			seen := false
		streamLoop:
			for {
				select {
				case chunk, ok := <-result.Chunks:
					if !ok {
						break streamLoop
					}
					if bytes.Equal(chunk.Payload, replacement) {
						seen = true
					}
				case <-timer.C:
					t.Fatal("stream did not close after failure signal")
				}
			}
			if !seen {
				t.Fatal("replacement terminal was not delivered to direct consumer")
			}
			acknowledge(nil)
			select {
			case record := <-capture.records:
				if !record.Failed || record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 {
					t.Fatalf("failure signal usage record = %+v", record)
				}
			case <-timer.C:
				t.Fatal("missing failure usage record")
			}
		})
	}
	if got := geminiResponsesTerminalEvent([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"error\":null}}\n\n")); got != "response.completed" {
		t.Fatalf("null error changed a successful terminal to %q", got)
	}
}
