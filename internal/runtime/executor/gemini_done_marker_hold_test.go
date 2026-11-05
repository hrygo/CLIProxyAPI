package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestGeminiDoneMarkerFinishesWhileUpstreamRemainsOpen(t *testing.T) {
	for _, tc := range []struct {
		name           string
		format         sdktranslator.Format
		request        string
		finishReason   string
		usage          string
		marker         string
		wantEvent      string
		wantStatus     string
		wantUsage      bool
		wantTruncation bool
	}{
		{
			name: "responses_no_usage", format: sdktranslator.FormatOpenAIResponse,
			request:      `{"model":"gemini-3.8-flash","input":"hi","stream":true}`,
			finishReason: "STOP", marker: "data: [DONE]\n\n",
			wantEvent: "response.completed", wantStatus: "completed",
		},
		{
			name: "responses_incomplete_no_usage", format: sdktranslator.FormatOpenAIResponse,
			request:      `{"model":"gemini-3.8-flash","input":"hi","stream":true}`,
			finishReason: "MAX_TOKENS", marker: "event: done\r\ndata:[DONE]\r\n\r\n",
			wantEvent: "response.incomplete", wantStatus: "incomplete",
		},
		{
			name: "responses_with_usage", format: sdktranslator.FormatOpenAIResponse,
			request:      `{"model":"gemini-3.8-flash","input":"hi","stream":true}`,
			finishReason: "STOP", usage: `,"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}`,
			marker: "data: [DONE]\n\n", wantEvent: "response.completed", wantStatus: "completed", wantUsage: true,
		},
		{
			name: "responses_without_finish_preserves_clean_end", format: sdktranslator.FormatOpenAIResponse,
			request: `{"model":"gemini-3.8-flash","input":"hi","stream":true}`,
			marker:  "data: [DONE]\n\n", wantEvent: "response.completed", wantStatus: "completed",
		},
		{
			name: "chat_without_finish_is_truncated", format: sdktranslator.FormatOpenAI,
			request: `{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			marker:  "data: [DONE]\n\n", wantTruncation: true,
		},
		{
			name: "claude_without_finish_is_truncated", format: sdktranslator.FormatClaude,
			request: `{"model":"gemini-3.8-flash","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			marker:  "data: [DONE]\n\n", wantTruncation: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			upstreamSent := make(chan error, 1)
			upstreamDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				defer close(upstreamDone)
				w.Header().Set("Content-Type", "text/event-stream")
				finish := ""
				if tc.finishReason != "" {
					finish = fmt.Sprintf(`,"finishReason":%q`, tc.finishReason)
				}
				frame := fmt.Sprintf(`data: {"responseId":"done-hold","candidates":[{"content":{"parts":[{"text":"answer"}]}%s}]%s}`, finish, tc.usage)
				// A similar non-marker must not end the stream, and a duplicate marker
				// must not produce a second terminal event.
				_, errWrite := io.WriteString(w, "data: [DONE] trailing\n\n"+frame+"\n\n"+tc.marker+"data: [DONE]\n\n")
				if errWrite != nil {
					upstreamSent <- errWrite
					return
				}
				w.(http.Flusher).Flush()
				upstreamSent <- nil
				<-release
			}))
			defer server.Close()
			defer close(release)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := []byte(tc.request)
			result, errExecute := NewGeminiExecutor(&config.Config{}).ExecuteStream(ctx, &cliproxyauth.Auth{
				Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL},
			}, cliproxyexecutor.Request{Model: "gemini-3.8-flash", Payload: request}, cliproxyexecutor.Options{
				SourceFormat: tc.format, ResponseFormat: tc.format, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatalf("ExecuteStream() error = %v", errExecute)
			}
			if errWrite := <-upstreamSent; errWrite != nil {
				t.Fatalf("write upstream frames: %v", errWrite)
			}

			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			var terminals []gjson.Result
			var streamErrors []error
			var output strings.Builder
		streamLoop:
			for {
				select {
				case chunk, ok := <-result.Chunks:
					if !ok {
						break streamLoop
					}
					if chunk.Err != nil {
						streamErrors = append(streamErrors, chunk.Err)
						continue
					}
					output.Write(chunk.Payload)
					for _, event := range issue6258Events(chunk.Payload) {
						switch event.Get("type").String() {
						case "response.completed", "response.incomplete", "response.failed":
							terminals = append(terminals, event)
						}
					}
				case <-timer.C:
					t.Fatalf("stream did not end after [DONE] while upstream stayed open; terminal count=%d", len(terminals))
				}
			}
			select {
			case <-upstreamDone:
				t.Fatal("upstream handler ended before the stream was checked")
			default:
			}
			if tc.wantTruncation {
				if len(streamErrors) != 1 {
					t.Fatalf("stream errors = %v, want one truncation error", streamErrors)
				}
				status, hasStatus := streamErrors[0].(interface{ StatusCode() int })
				truncated, hasTruncated := streamErrors[0].(interface{ IsStreamTruncated() bool })
				if !hasStatus || status.StatusCode() != http.StatusBadGateway || !hasTruncated || !truncated.IsStreamTruncated() || !strings.Contains(streamErrors[0].Error(), "upstream stream closed before finishReason") {
					t.Fatalf("stream error = %T %v, want 502 truncation before finishReason", streamErrors[0], streamErrors[0])
				}
				if strings.Contains(output.String(), "event: message_stop") || strings.Contains(output.String(), "[DONE]") {
					t.Fatalf("truncated stream emitted a success terminator: %s", output.String())
				}
				return
			}
			if len(streamErrors) != 0 {
				t.Fatalf("unexpected stream errors: %v", streamErrors)
			}
			if len(terminals) != 1 || terminals[0].Get("type").String() != tc.wantEvent || terminals[0].Get("response.status").String() != tc.wantStatus {
				t.Fatalf("terminal events = %v, want one %s/%s", terminals, tc.wantEvent, tc.wantStatus)
			}
			terminal := terminals[0]
			if got := terminal.Get("response.output.0.content.0.text").String(); got != "answer" {
				t.Fatalf("terminal text = %q, want answer", got)
			}
			usage := terminal.Get("response.usage")
			if tc.wantUsage {
				if usage.Get("input_tokens").Int() != 11 || usage.Get("output_tokens").Int() != 4 || usage.Get("total_tokens").Int() != 15 {
					t.Fatalf("terminal usage = %s, want input=11 output=4 total=15", usage.Raw)
				}
			} else if usage.Exists() {
				t.Fatalf("terminal invented usage: %s", usage.Raw)
			}
		})
	}
}
