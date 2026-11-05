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
)

type geminiTerminalDisconnectUsageCapture struct {
	authID  string
	records chan usage.Record
}

func (c *geminiTerminalDisconnectUsageCapture) HandleUsage(_ context.Context, record usage.Record) {
	if record.Provider == "gemini" && record.AuthID == c.authID {
		c.records <- record
	}
}

type geminiTerminalDisconnectUsageNoop struct{}

func (geminiTerminalDisconnectUsageNoop) HandleUsage(context.Context, usage.Record) {}

func TestGeminiStream_TerminalDisconnectUsage(t *testing.T) {
	const usageJSON = `"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}`
	for _, model := range []string{"claude-opus-4-6", "gemini-3.8-flash-high"} {
		for _, tc := range []struct {
			name          string
			finishReason  string
			terminalEvent string
			cancelEvent   string
			splitUsage    bool
			readError     bool
			wantFailed    bool
			wantStatus    int
		}{
			{name: "completed_cancel", finishReason: "STOP", terminalEvent: "response.completed", cancelEvent: "response.completed"},
			{name: "incomplete_cancel", finishReason: "MAX_TOKENS", terminalEvent: "response.incomplete", cancelEvent: "response.incomplete"},
			{name: "usage_tail_cancel", finishReason: "STOP", terminalEvent: "response.completed", cancelEvent: "response.completed", splitUsage: true},
			{name: "completed_eof", finishReason: "STOP", terminalEvent: "response.completed"},
			{name: "before_completion_cancel", cancelEvent: "response.output_text.delta", wantFailed: true, wantStatus: 499},
			{name: "future_read_error_after_completion", finishReason: "STOP", terminalEvent: "response.completed", readError: true},
			{name: "before_completion_read_error", readError: true, wantFailed: true},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				finishJSON := ""
				if tc.finishReason != "" {
					finishJSON = fmt.Sprintf(`,"finishReason":%q`, tc.finishReason)
				}
				metadata := "," + usageJSON
				if tc.splitUsage {
					metadata = ""
				}
				sse := fmt.Sprintf("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"OK\"}]}%s}]%s,\"traceId\":%q}\n\n", finishJSON, metadata, t.Name())
				releaseUpstream := make(chan struct{})
				allowUpstream := sync.OnceFunc(func() { close(releaseUpstream) })
				lateAttempted := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if tc.readError {
						// A short body produces a real, non-cancellation scanner error.
						w.Header().Set("Content-Length", fmt.Sprint(len(sse)+100))
					}
					if _, errWrite := io.WriteString(w, sse); errWrite != nil {
						return
					}
					w.(http.Flusher).Flush()
					if tc.splitUsage || (tc.readError && tc.finishReason != "") {
						// Release the tail or truncated EOF only after the client stream closes.
						<-releaseUpstream
						if tc.splitUsage {
							_, _ = fmt.Fprintf(w, "data: {%s,\"traceId\":%q}\n\n", usageJSON, t.Name())
							close(lateAttempted)
						}
						return
					}
					if tc.cancelEvent != "" {
						// Keep EOF behind the downstream event and cancellation.
						select {
						case <-request.Context().Done():
						case <-releaseUpstream:
						}
					}
				}))
				defer func() {
					allowUpstream()
					server.Close()
				}()

				capture := &geminiTerminalDisconnectUsageCapture{authID: t.Name(), records: make(chan usage.Record, 4)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() {
					usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{})
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result, errExecute := NewGeminiExecutor(&config.Config{}).ExecuteStream(ctx, &cliproxyauth.Auth{
					ID: capture.authID,
					Attributes: map[string]string{
						"api_key":  "test-key",
						"base_url": server.URL,
					},
				}, cliproxyexecutor.Request{
					Model:   model,
					Payload: []byte(fmt.Sprintf(`{"model":%q,"input":"Reply only OK.","stream":true,"max_output_tokens":32}`, model)),
				}, cliproxyexecutor.Options{
					SourceFormat:   sdktranslator.FormatOpenAIResponse,
					ResponseFormat: sdktranslator.FormatOpenAIResponse,
					Stream:         true,
				})
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
				}

				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				terminalCount := 0
				canceled := false
				var streamErr error
			streamLoop:
				for {
					select {
					case chunk, ok := <-result.Chunks:
						if !ok {
							break streamLoop
						}
						if chunk.Err != nil {
							streamErr = chunk.Err
						}
						for _, event := range issue6258Events(chunk.Payload) {
							kind := event.Get("type").String()
							if kind != "response.completed" && kind != "response.incomplete" && kind != "response.failed" {
								continue
							}
							terminalCount++
							if kind != tc.terminalEvent {
								t.Fatalf("unexpected terminal %s", event.Raw)
							}
							usage := event.Get("response.usage")
							if tc.splitUsage && usage.Exists() {
								t.Fatalf("terminal invented future usage: %s", event.Raw)
							}
							if !tc.splitUsage && (!usage.Exists() || usage.Get("input_tokens").Int() != 11 || usage.Get("output_tokens").Int() != 4 || usage.Get("total_tokens").Int() != 15) {
								t.Fatalf("terminal lost observed usage: %s", event.Raw)
							}
						}
						if tc.cancelEvent != "" && strings.Contains(string(chunk.Payload), "event: "+tc.cancelEvent+"\n") {
							canceled = true
							cancel()
						}
					case <-timer.C:
						t.Fatal("timed out waiting for stream closure")
					}
				}
				allowUpstream()
				if tc.splitUsage {
					select {
					case <-lateAttempted:
					case <-timer.C:
						t.Fatal("upstream did not attempt its gated usage tail")
					}
				}
				wantTerminals := 0
				if tc.terminalEvent != "" {
					wantTerminals = 1
				}
				if terminalCount != wantTerminals {
					t.Fatalf("terminal count = %d, want %d (%s)", terminalCount, wantTerminals, tc.terminalEvent)
				}
				if tc.cancelEvent != "" && !canceled {
					t.Fatalf("expected cancellation after %s", tc.cancelEvent)
				}
				select {
				case record := <-capture.records:
					if record.Failed != tc.wantFailed || record.Fail.StatusCode != tc.wantStatus {
						t.Fatalf("usage outcome = failed %v, status %d, body %q; want failed %v, status %d", record.Failed, record.Fail.StatusCode, record.Fail.Body, tc.wantFailed, tc.wantStatus)
					}
					if tc.splitUsage {
						if record.Detail.InputTokens != 0 || record.Detail.OutputTokens != 0 || record.Detail.TotalTokens != 0 {
							t.Fatalf("future usage was recorded: %+v", record.Detail)
						}
					} else if record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 || record.Detail.TotalTokens != 15 {
						t.Fatalf("reported usage = %+v; want input 11, output 4, total 15", record.Detail)
					}
					if tc.readError && tc.wantFailed && (streamErr == nil || record.Fail.Body != io.ErrUnexpectedEOF.Error()) {
						t.Fatalf("expected unexpected EOF failure, got stream error %v, failure %q", streamErr, record.Fail.Body)
					}
					if !tc.wantFailed && streamErr != nil {
						t.Fatalf("unexpected stream error: %v", streamErr)
					}
				case <-timer.C:
					t.Fatal("timed out waiting for usage record")
				}
			})
		}
	}
}
