package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestGeminiFinishWithoutUsageCompletesWhileUpstreamOpen(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex_api_key", "vertex_service_account"} {
		for _, tc := range []struct {
			name, finishReason, event, status string
		}{
			{name: "stop", finishReason: "STOP", event: "response.completed", status: "completed"},
			{name: "max_tokens", finishReason: "MAX_TOKENS", event: "response.incomplete", status: "incomplete"},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				release := make(chan struct{})
				allowEOF := sync.OnceFunc(func() { close(release) })
				upstreamSent := make(chan struct{}, 1)
				upstreamDone := make(chan struct{})
				tokenCalled := make(chan struct{}, 1)
				type upstreamRequest struct{ path, apiKey, authorization string }
				requests := make(chan upstreamRequest, 1)
				writeErrors := make(chan error, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/token" {
						tokenCalled <- struct{}{}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
						return
					}
					defer close(upstreamDone)
					requests <- upstreamRequest{r.URL.Path, r.Header.Get("x-goog-api-key"), r.Header.Get("Authorization")}
					w.Header().Set("Content-Type", "text/event-stream")
					_, errWrite := fmt.Fprintf(w, "data: {\"responseId\":\"held-upstream\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":%q}]}\n\n", tc.finishReason)
					if errWrite != nil {
						writeErrors <- errWrite
						return
					}
					w.(http.Flusher).Flush()
					upstreamSent <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}))
				defer func() {
					allowEOF()
					server.Close()
				}()

				baseCtx, executor, auth := issue6258Executor(t, provider, server.URL)
				ctx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
				defer cancel()
				payload := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
				result, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{
					Model: "gemini-3.8-flash-high", Payload: payload,
				}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse,
					OriginalRequest: payload, Stream: true,
				})
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}
				select {
				case <-upstreamSent:
				case errWrite := <-writeErrors:
					t.Fatalf("upstream write: %v", errWrite)
				case <-ctx.Done():
					t.Fatalf("upstream did not send finish: %v", ctx.Err())
				}
				request := <-requests
				if !strings.Contains(request.path, ":streamGenerateContent") {
					t.Fatalf("unexpected upstream path %q", request.path)
				}
				if provider == "vertex_service_account" {
					select {
					case <-tokenCalled:
					default:
						t.Fatal("service account did not request a local token")
					}
					if request.authorization != "Bearer local-token" || !strings.Contains(request.path, "/projects/proxy-test/") {
						t.Fatalf("unexpected service account request: %+v", request)
					}
				} else if request.apiKey != "test-key" {
					t.Fatalf("API key request had unexpected key %q", request.apiKey)
				}

				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				var eventTypes []string
				var text strings.Builder
				terminals := 0
				for {
					select {
					case chunk, ok := <-result.Chunks:
						if !ok {
							if terminals != 1 {
								t.Fatalf("stream closed with %d terminals, want 1; events: %v", terminals, eventTypes)
							}
							return
						}
						if chunk.Err != nil {
							t.Fatalf("stream error: %v; events: %v", chunk.Err, eventTypes)
						}
						for _, event := range issue6258Events(chunk.Payload) {
							kind := event.Get("type").String()
							eventTypes = append(eventTypes, kind)
							if kind == "response.output_text.delta" {
								text.WriteString(event.Get("delta").String())
							}
							if kind != "response.completed" && kind != "response.incomplete" && kind != "response.failed" {
								continue
							}
							terminals++
							if terminals != 1 || kind != tc.event || event.Get("response.status").String() != tc.status {
								t.Fatalf("unexpected terminal %s", event.Raw)
							}
							if text.String() != "answer" || event.Get(`response.output.#(type=="message").content.0.text`).String() != "answer" {
								t.Fatalf("terminal lost streamed text: delta=%q, event=%s", text.String(), event.Raw)
							}
							if usage := event.Get("response.usage"); usage.Exists() {
								t.Fatalf("terminal invented usage: %s", usage.Raw)
							}
							if tc.finishReason == "MAX_TOKENS" && event.Get("response.incomplete_details.reason").String() != "max_output_tokens" {
								t.Fatalf("unexpected incomplete terminal: %s", event.Raw)
							}
							allowEOF()
						}
					case <-upstreamDone:
						if terminals == 0 {
							t.Fatalf("upstream closed before terminal; events: %v", eventTypes)
						}
						upstreamDone = nil
					case <-timer.C:
						t.Fatalf("no correct terminal while upstream remains open; streamed text=%q, events: %v", text.String(), eventTypes)
					case <-ctx.Done():
						t.Fatalf("stream timed out: %v; events: %v", ctx.Err(), eventTypes)
					}
				}
			})
		}
	}
}
