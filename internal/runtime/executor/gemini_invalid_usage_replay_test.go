package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestGeminiStreamInvalidUsageCompletesBeforeEOF(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex_api_key", "vertex_service_account"} {
		for _, tc := range []struct {
			name, invalid string
			late          bool
		}{
			{name: "null", invalid: `null`},
			{name: "string", invalid: `"not usage"`},
			{name: "array", invalid: `[]`},
			{name: "field_null", invalid: `{"promptTokenCount":100,"candidatesTokenCount":null}`},
			{name: "field_string", invalid: `{"promptTokenCount":100,"candidatesTokenCount":"not a number"}`},
			{name: "field_array", invalid: `{"promptTokenCount":100,"candidatesTokenCount":[]}`},
			{name: "null_later_usage", invalid: `null`, late: true},
			{name: "string_later_usage", invalid: `"not usage"`, late: true},
			{name: "array_later_usage", invalid: `[]`, late: true},
			{name: "field_null_later_usage", invalid: `{"promptTokenCount":100,"candidatesTokenCount":null}`, late: true},
			{name: "field_string_later_usage", invalid: `{"promptTokenCount":100,"candidatesTokenCount":"not a number"}`, late: true},
			{name: "field_array_later_usage", invalid: `{"promptTokenCount":100,"candidatesTokenCount":[]}`, late: true},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				release := make(chan struct{})
				tailAttempted := make(chan struct{})
				var releaseOnce sync.Once
				allowEnd := func() { releaseOnce.Do(func() { close(release) }) }
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.URL.Path == "/token" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":%s}\n\n", tc.invalid)
					w.(http.Flusher).Flush()
					// Do not send a later snapshot until the client stream has closed.
					<-release
					if tc.late {
						_, _ = io.WriteString(w, "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":50,\"totalTokenCount\":160}}\n\n")
						w.(http.Flusher).Flush()
						close(tailAttempted)
					}
				}))
				defer func() {
					allowEnd()
					server.Close()
				}()

				capture := &geminiPartialUsageCapture{authID: t.Name(), records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				auth := &cliproxyauth.Auth{ID: capture.authID, Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
				if provider == "vertex_service_account" {
					var serviceAccount map[string]any
					if errUnmarshal := json.Unmarshal(testVertexServiceAccountJSON(t, server.URL+"/token"), &serviceAccount); errUnmarshal != nil {
						t.Fatal(errUnmarshal)
					}
					auth.Attributes = nil
					auth.Metadata = map[string]any{"project_id": "proxy-test", "location": "global", "service_account": serviceAccount}
					target, errParse := parseTestURL(t, server.URL)
					if errParse != nil {
						t.Fatal(errParse)
					}
					transport := http.DefaultTransport.(*http.Transport).Clone()
					transport.Proxy = nil
					t.Cleanup(transport.CloseIdleConnections)
					ctx = context.WithValue(ctx, "cliproxy.roundtripper", issue6258RedirectTransport{target: target, base: transport})
				}
				request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
				req := cliproxyexecutor.Request{Model: "gemini-3.8-flash-high", Payload: request}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true}
				var result *cliproxyexecutor.StreamResult
				var errExecute error
				if provider == "gemini" {
					result, errExecute = NewGeminiExecutor(&config.Config{}).ExecuteStream(ctx, auth, req, opts)
				} else {
					result, errExecute = NewGeminiVertexExecutor(&config.Config{}).ExecuteStream(ctx, auth, req, opts)
				}
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}

				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				terminals := 0
			streamLoop:
				for {
					select {
					case chunk, ok := <-result.Chunks:
						if !ok {
							break streamLoop
						}
						if chunk.Err != nil {
							t.Fatalf("stream error: %v", chunk.Err)
						}
						for _, line := range strings.Split(string(chunk.Payload), "\n") {
							if !strings.HasPrefix(line, "data:") {
								continue
							}
							event := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
							switch event.Get("type").String() {
							case "response.completed":
								terminals++
								got := event.Get("response.usage")
								if got.Exists() {
									t.Fatalf("terminal invented unknown usage: %s", got.Raw)
								}
							}
						}
					case <-timer.C:
						t.Fatal("terminal blocked while the upstream connection remained open")
					}
				}
				allowEnd()
				if tc.late {
					select {
					case <-tailAttempted:
					case <-timer.C:
						t.Fatal("upstream did not attempt its gated usage tail")
					}
				}
				if terminals != 1 {
					t.Fatalf("terminal events = %d, want 1", terminals)
				}
				select {
				case record := <-capture.records:
					d := record.Detail
					// Integer record fields cannot distinguish an absent count from zero.
					wantInput, wantTotal := int64(0), int64(0)
					if strings.HasPrefix(tc.name, "field_") {
						wantInput, wantTotal = 100, 100
					}
					if record.Failed || d.InputTokens != wantInput || d.OutputTokens != 0 || d.ReasoningTokens != 0 || d.TotalTokens != wantTotal {
						t.Fatalf("reported usage = %+v, failed=%t", d, record.Failed)
					}
				case <-timer.C:
					t.Fatal("observed usage was not reported after stream closure")
				}
			})
		}
	}
}
