package openai

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/tidwall/gjson"
)

func TestGeminiResponsesHTTPClosesAfterCompletionWhileUpstreamOpen(t *testing.T) {
	for _, tc := range []struct {
		name, finish, event, status, usage string
	}{
		{name: "stop_without_usage", finish: "STOP", event: "response.completed", status: "completed"},
		{name: "max_tokens_without_usage", finish: "MAX_TOKENS", event: "response.incomplete", status: "incomplete"},
		{name: "stop_with_usage", finish: "STOP", event: "response.completed", status: "completed", usage: `,"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":22,"totalTokenCount":33}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3.7-flash"
			release := make(chan struct{})
			allowUpstreamEOF := sync.OnceFunc(func() { close(release) })
			upstreamDone := make(chan struct{})
			var upstreamDoneOnce sync.Once
			type requestInfo struct{ path, apiKey string }
			upstreamRequests := make(chan requestInfo, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer upstreamDoneOnce.Do(func() { close(upstreamDone) })
				select {
				case upstreamRequests <- requestInfo{r.URL.Path, r.Header.Get("x-goog-api-key")}:
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
				frame := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"` + tc.finish + `"}]` + tc.usage + `,"responseId":"held-upstream"}` + "\n\n"
				_, _ = io.WriteString(w, frame)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			var downstream *httptest.Server
			var response *http.Response
			var cancel context.CancelFunc = func() {}
			defer func() {
				cancel()
				allowUpstreamEOF()
				if response != nil {
					_ = response.Body.Close()
				}
				if downstream != nil {
					downstream.Close()
				}
				upstream.Close()
			}()

			manager := cliproxyauth.NewManager(nil, nil, nil)
			manager.RegisterExecutor(runtimeexecutor.NewGeminiExecutor(&config.Config{}))
			auth := &cliproxyauth.Auth{
				ID: t.Name(), Provider: "gemini", Status: cliproxyauth.StatusActive, ProxyURL: "direct",
				Attributes: map[string]string{"api_key": "test-key", "base_url": upstream.URL},
			}
			if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
				t.Fatal(errRegister)
			}
			registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
			t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
			router := gin.New()
			router.POST("/v1/responses", NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)).Responses)
			downstream = httptest.NewServer(router)

			ctx, cancelRequest := context.WithTimeout(context.Background(), 10*time.Second)
			cancel = cancelRequest
			request, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, downstream.URL+"/v1/responses", strings.NewReader(`{"model":"`+model+`","input":"fixture","stream":true}`))
			if errRequest != nil {
				t.Fatal(errRequest)
			}
			request.Header.Set("Content-Type", "application/json")
			var errResponse error
			response, errResponse = http.DefaultClient.Do(request)
			if errResponse != nil {
				t.Fatal(errResponse)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("downstream HTTP status = %d, want 200", response.StatusCode)
			}
			select {
			case requestUpstream := <-upstreamRequests:
				if !strings.Contains(requestUpstream.path, ":streamGenerateContent") || requestUpstream.apiKey != "test-key" {
					t.Fatalf("unexpected Gemini upstream request: %+v", requestUpstream)
				}
			case <-ctx.Done():
				t.Fatalf("Gemini upstream was not called: %v", ctx.Err())
			}

			type observation struct {
				event gjson.Result
				err   error
				done  bool
			}
			observations := make(chan observation, 16)
			go func() {
				send := func(obs observation) bool {
					select {
					case observations <- obs:
						return true
					case <-ctx.Done():
						return false
					}
				}
				scanner := bufio.NewScanner(response.Body)
				for scanner.Scan() {
					line := scanner.Text()
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					event := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
					switch event.Get("type").String() {
					case "response.output_text.delta", "response.completed", "response.incomplete", "response.failed", "error":
						if !send(observation{event: event}) {
							return
						}
					}
				}
				send(observation{err: scanner.Err(), done: true})
			}()

			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			var text strings.Builder
			terminals := 0
			for {
				select {
				case obs := <-observations:
					if obs.done {
						if obs.err != nil {
							t.Fatalf("downstream HTTP body read error: %v", obs.err)
						}
						if terminals != 1 {
							t.Fatalf("downstream HTTP body ended after %d terminals, want one %s", terminals, tc.event)
						}
						return
					}
					kind := obs.event.Get("type").String()
					if kind == "response.output_text.delta" {
						text.WriteString(obs.event.Get("delta").String())
						continue
					}
					terminals++
					if terminals != 1 || kind != tc.event || obs.event.Get("response.status").String() != tc.status {
						t.Fatalf("unexpected terminal: %s", obs.event.Raw)
					}
					if text.String() != "answer" || obs.event.Get(`response.output.#(type=="message").content.0.text`).String() != "answer" {
						t.Fatalf("terminal lost streamed text: delta=%q, event=%s", text.String(), obs.event.Raw)
					}
					if tc.finish == "MAX_TOKENS" && obs.event.Get("response.incomplete_details.reason").String() != "max_output_tokens" {
						t.Fatalf("unexpected incomplete terminal: %s", obs.event.Raw)
					}
					usage := obs.event.Get("response.usage")
					if tc.usage == "" {
						if usage.Exists() && usage.Type != gjson.Null {
							t.Fatalf("terminal invented usage: %s", usage.Raw)
						}
					} else if usage.Get("input_tokens").Int() != 11 || usage.Get("output_tokens").Int() != 22 || usage.Get("total_tokens").Int() != 33 {
						t.Fatalf("terminal usage = %s", usage.Raw)
					}
				case <-timer.C:
					select {
					case <-upstreamDone:
						t.Fatalf("downstream did not complete after upstream disconnected; terminals=%d, text=%q", terminals, text.String())
					default:
						t.Fatalf("downstream did not complete while upstream remained open; terminals=%d, text=%q", terminals, text.String())
					}
				case <-ctx.Done():
					t.Fatalf("downstream HTTP response did not finish: %v", ctx.Err())
				}
			}
		})
	}
}
