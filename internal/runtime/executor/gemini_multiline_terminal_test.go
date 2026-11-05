package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestGeminiResponsesMultilineTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		failed        bool
	}{
		{name: "completed", payload: `{"type":"response.completed",` + "\ndata: " + `"response":{"status":"completed"}}`},
		{name: "failed", payload: `{"type":"response.completed",` + "\ndata: " + `"response":{"error":{"message":"failed"}}}`, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replacement := []byte("event: response.completed\ndata: " + tc.payload + "\n\n")
			sdktranslator.SetPluginHooks(&geminiFailureSignalHooks{replacement: replacement})
			t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`+"\n\n")
				w.(http.Flusher).Flush()
				<-release
			}))
			t.Cleanup(server.Close)
			defer close(release)
			baseCtx, executor, auth := issue6258Executor(t, "gemini", server.URL)
			capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
			ctx, acknowledge := usage.WithStreamDelivery(baseCtx)
			defer acknowledge(context.Canceled)
			request := []byte(`{"model":"gemini-3.7-flash","input":"hi","stream":true}`)
			stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gemini-3.7-flash", Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatalf("ExecuteStream() error = %v", errExecute)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			seen := false
		streamLoop:
			for {
				select {
				case chunk, ok := <-stream.Chunks:
					if !ok {
						break streamLoop
					}
					if chunk.Err != nil {
						t.Fatalf("unexpected stream error: %v", chunk.Err)
					}
					if bytes.Equal(chunk.Payload, replacement) {
						seen = true
					}
				case <-timer.C:
					t.Fatalf("terminal delivered=%v; stream remained open", seen)
				}
			}
			if !seen {
				t.Fatal("multiline terminal was not delivered")
			}
			acknowledge(nil)
			select {
			case record := <-capture.records:
				if record.Failed != tc.failed || record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 {
					t.Fatalf("usage record = %+v", record)
				}
			case <-timer.C:
				t.Fatal("usage was not recorded")
			}
		})
	}
}

func TestGeminiResponsesTerminalEventMultiline(t *testing.T) {
	for _, tc := range []struct{ name, frame, want string }{
		{"crlf_completed", "event: response.completed\r\ndata: {\"type\":\"response.completed\",\r\ndata: \"response\":{\"error\":null}}\r\n\r\n", "response.completed"},
		{"no_blank_completed", "event: response.completed\ndata: {\"type\":\"response.completed\"}", "response.completed"},
		{"no_blank_failed", "event: response.completed\ndata: {\"type\":\"response.failed\"}", "response.failed"},
		{"error_priority", "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"error\":{\"message\":\"bad\"}}}\n\n", "response.failed"},
		{"failed_type_priority", "event: response.completed\ndata: {\"type\":\"response.failed\",\ndata: \"response\":{\"status\":\"failed\"}}\n\n", "response.failed"},
		{"invalid_multiline_is_not_success", "event: response.completed\ndata: {\"type\":\"response.completed\"}\ndata: {\"error\":{}}\n\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := geminiResponsesTerminalEvent([]byte(tc.frame)); got != tc.want {
				t.Fatalf("terminal = %q, want %q", got, tc.want)
			}
		})
	}
}
