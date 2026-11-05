package executor

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

type openAICompatTerminalHook struct{ replacement []byte }

func (*openAICompatTerminalHook) NormalizeRequest(_ context.Context, _, _ sdktranslator.Format, _ string, body []byte, _ bool) []byte {
	return body
}

func (*openAICompatTerminalHook) TranslateRequest(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (*openAICompatTerminalHook) NormalizeResponseBefore(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	return body
}

func (*openAICompatTerminalHook) TranslateResponse(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, []byte, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (h *openAICompatTerminalHook) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	for _, event := range issue6258Events(body) {
		if event.Get("type").String() == "response.completed" {
			return h.replacement
		}
	}
	return body
}

func TestOpenAICompatRejectsPluginRemovedTerminal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement []byte
	}{
		{name: "removed"},
		{name: "wrong_status", replacement: []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"in_progress\"}}\n\n")},
	} {
		for _, marker := range []string{"done", "clean_eof"} {
			t.Run(tc.name+"/"+marker, func(t *testing.T) {
				sdktranslator.SetPluginHooks(&openAICompatTerminalHook{replacement: tc.replacement})
				t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
				upstream := `data: {"id":"r","choices":[{"index":0,"delta":{"content":"ok"}}]}` + "\n\n" +
					`data: {"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
					`data: {"id":"r","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}` + "\n\n"
				if marker == "done" {
					upstream += "data: [DONE]\n\n"
				}
				exec, auth, _ := newApplyPatchCompatTestExecutor(t, upstream)
				auth.ID = t.Name()
				capture := &geminiPartialUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
				request := []byte(`{"model":"test","input":"hello","stream":true}`)
				result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "test", Payload: request}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				var streamErr error
				var content strings.Builder
				for _, chunk := range collectHeldGeminiChunks(t, result.Chunks) {
					content.Write(chunk.Payload)
					if chunk.Err != nil {
						streamErr = chunk.Err
					}
				}
				status, ok := streamErr.(interface{ StatusCode() int })
				if !ok || status.StatusCode() != http.StatusBadGateway {
					t.Fatalf("missing terminal: error=%v, output=%s", streamErr, content.String())
				}
				select {
				case record := <-capture.records:
					if !record.Failed || record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 3 || record.Detail.TotalTokens != 14 {
						t.Fatalf("missing terminal lost failure accounting: failed=%t detail=%+v", record.Failed, record.Detail)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("missing-terminal failure was not reported")
				}
			})
		}
	}
}
