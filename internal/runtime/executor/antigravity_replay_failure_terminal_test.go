package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalcache "github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

var antigravityReplayFailureSequence atomic.Uint64

func TestAntigravityReplayFollowsUpstreamCompletionWhenPluginChangesTerminal(t *testing.T) {
	const model = "gemini-3-flash-agent"
	for _, tc := range []struct {
		name        string
		event       string
		payload     string
		finish      string
		seed        bool
		wantSuccess bool
	}{
		{name: "completed", wantSuccess: true},
		{name: "max_tokens", finish: "MAX_TOKENS", wantSuccess: true},
		{name: "multiline_completed", event: "response.completed", payload: `{"type":"response.completed",` + "\ndata: " + `"response":{"status":"completed"}}`, wantSuccess: true},
		{name: "failed_payload", event: "response.completed", payload: `{"type":"response.failed","response":{"status":"failed"}}`},
		{name: "response_error", event: "response.completed", payload: `{"type":"response.completed","response":{"error":{"message":"failed"}}}`},
		{name: "multiline_response_error", event: "response.completed", payload: `{"type":"response.completed",` + "\ndata: " + `"response":{"error":{"message":"failed"}}}`},
		{name: "top_error", event: "response.completed", payload: `{"type":"response.completed","error":{"message":"failed"}}`},
		{name: "code_message", event: "response.completed", payload: `{"type":"response.completed","code":"bad","message":"failed"}`},
		{name: "error_event", event: "error", payload: `{"type":"response.completed"}`},
		{name: "replace_existing_on_plugin_failure", event: "response.completed", payload: `{"type":"response.failed","response":{"status":"failed"}}`, seed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
			key := "responses:" + session
			if tc.seed {
				item := []byte(`{"type":"function_call_part","contentIndex":0,"partIndex":0,"call_id":"prior","name":"Read","args":{},"thoughtSignature":"prior-signature"}`)
				if !internalcache.CacheAntigravityReasoningReplayItems(model, key, [][]byte{item}) {
					t.Fatal("could not seed replay ledger")
				}
			}
			var replacement []byte
			if tc.payload != "" {
				replacement = []byte("event: " + tc.event + "\ndata: " + tc.payload + "\n\n")
				sdktranslator.SetPluginHooks(&geminiFailureSignalHooks{replacement: replacement})
				t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
			}

			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				finish := tc.finish
				if finish == "" {
					finish = "STOP"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{"file_path":"/a"},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"`+finish+`"}]}}`+"\n\n")
				w.(http.Flusher).Flush()
				<-release
			}))
			t.Cleanup(server.Close)
			defer close(release)
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", server.URL)
			ctx, acknowledge := usage.WithStreamDelivery(baseCtx)
			defer acknowledge(context.Canceled)
			request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
			stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatalf("ExecuteStream() error = %v", errExecute)
			}
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			seenTerminal := false
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
					if geminiResponsesTerminalEvent(chunk.Payload) == "" {
						continue
					}
					seenTerminal = true
					items, ok := internalcache.GetAntigravityReasoningReplayItems(model, key)
					if !ok || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
						t.Fatalf("completed upstream replay not available when terminal was received: %q, found=%v", items, ok)
					}
					if !tc.wantSuccess && !bytes.Equal(chunk.Payload, replacement) {
						t.Fatalf("unexpected terminal event: %s", chunk.Payload)
					}
				case <-timer.C:
					t.Fatal("stream did not close after terminal")
				}
			}
			if !seenTerminal {
				t.Fatal("terminal event not received")
			}
			acknowledge(nil)
		})
	}
}

func TestAntigravityReplayRejectsIncompleteToolAtUpstreamFinish(t *testing.T) {
	for _, seed := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_%v", seed), func(t *testing.T) {
			const model = "gemini-3-flash-agent"
			session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
			key := "responses:" + session
			if seed {
				item := []byte(`{"type":"function_call_part","contentIndex":0,"partIndex":0,"call_id":"prior","name":"Read","args":{},"thoughtSignature":"prior-signature"}`)
				if !internalcache.CacheAntigravityReasoningReplayItems(model, key, [][]byte{item}) {
					t.Fatal("could not seed replay ledger")
				}
			}
			before, foundBefore := internalcache.GetAntigravityReasoningReplayItems(model, key)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_a","name":"get_weather","partialArgs":[{"jsonPath":"$.city","stringValue":"SECRET_PARTIAL_VALUE","willContinue":true}],"willContinue":true},"thoughtSignature":"sig-first"}]}}]}}`+"\n\n"+`data: {"response":{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}}`+"\n\n")
				w.(http.Flusher).Flush()
				<-release
			}))
			t.Cleanup(server.Close)
			defer close(release)
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", server.URL)
			capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
			ctx, acknowledge := usage.WithStreamDelivery(baseCtx)
			defer acknowledge(context.Canceled)
			request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true,"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}`, model, session))
			stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			var output bytes.Buffer
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
					output.Write(chunk.Payload)
				case <-timer.C:
					t.Fatal("stream did not end after invalid tool")
				}
			}
			failureStart := bytes.Index(output.Bytes(), []byte("event: response.failed"))
			if failureStart < 0 || !bytes.Contains(output.Bytes()[failureStart:], []byte("invalid_tool_arguments")) || bytes.Contains(output.Bytes()[failureStart:], []byte("SECRET_PARTIAL_VALUE")) || bytes.Contains(output.Bytes(), []byte("event: response.completed")) {
				t.Fatal("missing or unsafe invalid-tool failure event")
			}
			after, foundAfter := internalcache.GetAntigravityReasoningReplayItems(model, key)
			if foundAfter != foundBefore || !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid tool changed replay ledger: before=%q after=%q", before, after)
			}
			acknowledge(nil)
			select {
			case record := <-capture.records:
				if !record.Failed {
					t.Fatalf("invalid tool reported success: %+v", record)
				}
			case <-timer.C:
				t.Fatal("invalid tool was not recorded")
			}
		})
	}
}

func TestAntigravityReplayValidatesReassembledUpstreamFrames(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frame     string
		truncated bool
	}{
		{
			name: "truncated_finish",
			frame: `data: {"response":{"candidates":[{"content":{"parts":[{"text":"first"}]}}]}}` + "\n\n" +
				`data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"`,
			truncated: true,
		},
		{
			name: "invalid_separate_events",
			frame: `data: {"response":{"candidates":[` + "\n\n" +
				`data: {"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}]}}` + "\n\n",
			truncated: true,
		},
		{
			name: "valid_multiline",
			frame: `data: {"response":{"candidates":[` + "\n" +
				": keepalive\nid: source-frame\n" +
				`data: {"content":{"parts":[{"functionCall":{"name":"Read","args":{"file_path":"/a"},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}],` + "\n" +
				`data: "usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}}` + "\n\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3-flash-agent"
			session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
			key := "responses:" + session
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.frame)
				w.(http.Flusher).Flush()
				if !tc.truncated {
					<-release
				}
			}))
			t.Cleanup(server.Close)
			defer close(release)
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", server.URL)
			capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
			ctx, acknowledge := usage.WithStreamDelivery(baseCtx)
			defer acknowledge(context.Canceled)
			request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
			stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			var output bytes.Buffer
			var streamErr error
		streamLoop:
			for {
				select {
				case chunk, ok := <-stream.Chunks:
					if !ok {
						break streamLoop
					}
					if chunk.Err != nil {
						streamErr = chunk.Err
					}
					output.Write(chunk.Payload)
				case <-timer.C:
					t.Fatal("stream did not close")
				}
			}
			items, found := internalcache.GetAntigravityReasoningReplayItems(model, key)
			if tc.truncated {
				if streamErr == nil || !strings.Contains(streamErr.Error(), "incomplete upstream") || strings.Contains(streamErr.Error(), "sig-first") || bytes.Contains(output.Bytes(), []byte("event: response.completed")) {
					t.Fatalf("truncated upstream did not report a safe error: %v", streamErr)
				}
				if found {
					t.Fatalf("truncated upstream committed replay: %q", items)
				}
			} else if streamErr != nil || !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) || !bytes.Contains(output.Bytes(), []byte("event: response.completed")) {
				t.Fatalf("valid multiline upstream lost replay or completion: err=%v items=%q", streamErr, items)
			}
			acknowledge(nil)
			select {
			case record := <-capture.records:
				if record.Failed != tc.truncated {
					t.Fatalf("usage failure=%v, want %v", record.Failed, tc.truncated)
				}
				if !tc.truncated && (record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 || record.Detail.TotalTokens != 15) {
					t.Fatalf("multiline usage = %+v", record.Detail)
				}
			case <-timer.C:
				t.Fatal("missing usage record")
			}
		})
	}
}
