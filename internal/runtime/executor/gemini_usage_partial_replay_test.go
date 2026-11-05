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

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

type geminiPartialUsageCapture struct {
	authID  string
	records chan usage.Record
}

func (c *geminiPartialUsageCapture) HandleUsage(_ context.Context, record usage.Record) {
	if record.AuthID == c.authID {
		c.records <- record
	}
}

type geminiPartialUsageNoop struct{}

func (geminiPartialUsageNoop) HandleUsage(context.Context, usage.Record) {}

// The server holds the connection open until the client receives its terminal.
// The later usage update is observable only while the client keeps reading.
func TestGeminiStreamUsageAfterTerminal(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex"} {
		for _, tc := range []struct {
			name, firstUsage, laterUsage        string
			firstInput, firstOutput, firstTotal int64
			finalInput, finalOutput, reasoning  int64
			finalTotal                          int64
		}{
			{
				name:       "partial_usage",
				firstUsage: `{"promptTokenCount":100}`,
				laterUsage: `{"candidatesTokenCount":10,"thoughtsTokenCount":50,"totalTokenCount":160}`,
				firstInput: 100, firstTotal: 0,
				finalInput: 100, finalOutput: 10, reasoning: 50, finalTotal: 160,
			},
			{
				name:       "complete_then_updated_usage",
				firstUsage: `{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":115}`,
				laterUsage: `{"candidatesTokenCount":12,"thoughtsTokenCount":7,"totalTokenCount":119}`,
				firstInput: 100, firstOutput: 15, firstTotal: 115,
				finalInput: 100, finalOutput: 12, reasoning: 7, finalTotal: 119,
			},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				release := make(chan struct{})
				var releaseOnce sync.Once
				releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					first := fmt.Sprintf("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":%s}\n\n", tc.firstUsage)
					if _, errWrite := io.WriteString(w, first); errWrite != nil {
						return
					}
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-request.Context().Done():
						return
					}
					_, _ = fmt.Fprintf(w, "data: {\"usageMetadata\":%s}\n\n", tc.laterUsage)
				}))
				defer func() {
					releaseServer()
					server.Close()
				}()

				capture := &geminiPartialUsageCapture{authID: t.Name(), records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
				auth := &cliproxyauth.Auth{ID: capture.authID, Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
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
							if event.Get("type").String() != "response.completed" {
								continue
							}
							terminals++
							got := event.Get("response.usage")
							if got.Get("input_tokens").Int() != tc.firstInput || got.Get("output_tokens").Int() != tc.firstOutput || got.Get("total_tokens").Int() != tc.firstTotal {
								t.Fatalf("terminal usage = %s; want initial snapshot", got.Raw)
							}
							releaseServer()
						}
					case <-timer.C:
						t.Fatal("terminal or stream close blocked while the server held the connection open")
					}
				}
				if terminals != 1 {
					t.Fatalf("terminal events = %d, want 1", terminals)
				}
				select {
				case record := <-capture.records:
					d := record.Detail
					if record.Failed || d.InputTokens != tc.finalInput || d.OutputTokens != tc.finalOutput || d.ReasoningTokens != tc.reasoning || d.TotalTokens != tc.finalTotal {
						t.Fatalf("reported usage = %+v, failed=%t", d, record.Failed)
					}
				case <-timer.C:
					t.Fatal("usage record was not published after the stream closed")
				}
			})
		}
	}
}
