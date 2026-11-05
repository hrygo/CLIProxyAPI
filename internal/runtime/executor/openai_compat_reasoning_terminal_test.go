package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestOpenAICompatReasoningOnlyExplicitFinish(t *testing.T) {
	for _, tc := range []struct {
		name, reason, terminal, status, marker string
	}{
		{name: "done", reason: "stop", terminal: "response.completed", status: "completed", marker: "data: [DONE]\n\n"},
		{name: "clean_eof", reason: "length", terminal: "response.incomplete", status: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := strings.Join([]string{
				`data: {"id":"reasoning","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"checking"}}]}` + "\n\n",
				fmt.Sprintf(`data: {"id":"reasoning","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, tc.reason) + "\n\n",
				`data: {"id":"reasoning","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}` + "\n\n",
				tc.marker,
			}, "")
			exec, auth, _ := newApplyPatchCompatTestExecutor(t, upstream)
			request := []byte(`{"model":"test","input":"hello","stream":true}`)
			result, err := exec.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "test", Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			terminals := 0
		stream:
			for {
				select {
				case chunk, ok := <-result.Chunks:
					if !ok {
						break stream
					}
					if chunk.Err != nil {
						t.Fatalf("stream error: %v", chunk.Err)
					}
					for _, event := range issue6258Events(chunk.Payload) {
						kind := event.Get("type").String()
						if kind != "response.completed" && kind != "response.incomplete" && kind != "response.failed" {
							continue
						}
						terminals++
						response := event.Get("response")
						if kind != tc.terminal || response.Get("status").String() != tc.status || response.Get("output.0.type").String() != "reasoning" || response.Get("usage.input_tokens").Int() != 11 {
							t.Fatalf("terminal lost reasoning, status or usage: %s", event.Raw)
						}
					}
				case <-timer.C:
					t.Fatal("stream did not finish")
				}
			}
			if terminals != 1 {
				t.Fatalf("terminal count = %d, want one", terminals)
			}
		})
	}
}
