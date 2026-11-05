package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// heldGeminiResponse returns its first batch in one read. Another read blocks
// until Close, so a timely terminal cannot depend on a subsequent network read.
type heldGeminiResponse struct {
	first  []byte
	err    error
	reads  atomic.Int32
	closed chan struct{}
	once   sync.Once
}

func collectHeldGeminiChunks(t *testing.T, input <-chan cliproxyexecutor.StreamChunk) []cliproxyexecutor.StreamChunk {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var chunks []cliproxyexecutor.StreamChunk
	for {
		select {
		case chunk, ok := <-input:
			if !ok {
				return chunks
			}
			chunks = append(chunks, chunk)
		case <-timer.C:
			t.Fatal("stream did not finish without another read")
		}
	}
}

func TestGeminiResponsesKnownReadErrorDoesNotProduceSuccess(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, provider := range []string{"gemini", "vertex"} {
		t.Run(provider, func(t *testing.T) {
			body := &heldGeminiResponse{first: []byte(finish), err: io.ErrUnexpectedEOF, closed: make(chan struct{})}
			t.Cleanup(func() { _ = body.Close() })
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: req}, nil
			}))
			auth := &cliproxyauth.Auth{ID: t.Name(), Attributes: map[string]string{"api_key": "test-key", "base_url": "https://fixture.invalid"}}
			capture := &geminiPartialUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
			var executor issue6258StreamExecutor
			if provider == "gemini" {
				executor = NewGeminiExecutor(&config.Config{})
			} else {
				executor = NewGeminiVertexExecutor(&config.Config{})
			}
			request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
			result, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gemini-3.8-flash-high", Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			var gotErr error
			for _, chunk := range collectHeldGeminiChunks(t, result.Chunks) {
				if chunk.Err != nil {
					gotErr = chunk.Err
				}
				for _, event := range issue6258Events(chunk.Payload) {
					if event.Get("type").String() == "response.completed" {
						t.Fatalf("known read error was reported as successful completion: %s", event.Raw)
					}
				}
			}
			if !errors.Is(gotErr, io.ErrUnexpectedEOF) || body.reads.Load() != 1 {
				t.Fatalf("read error=%v reads=%d; want already-known unexpected EOF with one read", gotErr, body.reads.Load())
			}
			select {
			case record := <-capture.records:
				if !record.Failed || record.Detail.InputTokens != 100 || record.Detail.OutputTokens != 10 || record.Detail.TotalTokens != 110 {
					t.Fatalf("known read failure lost buffered usage: failed=%t detail=%+v", record.Failed, record.Detail)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("known read failure was not reported")
			}
		})
	}
}

func (body *heldGeminiResponse) Read(p []byte) (int, error) {
	if body.reads.Add(1) == 1 {
		n := copy(p, body.first)
		return n, body.err
	}
	<-body.closed
	return 0, io.ErrClosedPipe
}

func (body *heldGeminiResponse) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestGeminiResponsesRejectsAlreadyBufferedInvalidEvents(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"
	const usagePrefix = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, provider := range []string{"gemini", "vertex"} {
		for _, tc := range []struct {
			name, tail string
			frame      string
			wantUsage  bool
			wantStatus int
		}{
			{name: "malformed_json", tail: "data: {\"usageMetadata\":\n\n"},
			{name: "upstream_error", tail: "data: {\"error\":{\"code\":500,\"message\":\"failure\"}}\n\n", wantStatus: http.StatusInternalServerError},
			{name: "wrapped_error", tail: "data: {\"response\":{\"error\":{\"message\":\"failure\"}}}\n\n"},
			{name: "code_message_error", tail: "data: {\"code\":500,\"message\":\"failure\"}\n\n", wantStatus: http.StatusInternalServerError},
			{name: "typed_error", tail: "data: {\"type\":\"error\"}\n\n"},
			{name: "sse_error", tail: "event: error\ndata: {\"message\":\"failure\"}\n\n"},
			{name: "sse_error_without_data", tail: "event: error\n\n"},
			{name: "sse_rate_limit", tail: "event: error\ndata: {\"error\":{\"code\":429}}\n\n", wantStatus: http.StatusTooManyRequests},
			{name: "json_rate_limit", tail: "data: {\"error\":{\"code\":429}}\n\n", wantStatus: http.StatusTooManyRequests},
			{name: "duplicate_error", tail: "data: {\"error\":null,\"error\":{\"code\":429}}\n\n"},
			{name: "escaped_duplicate_error", tail: "data: {\"err\\u006fr\":null,\"error\":{\"code\":429}}\n\n"},
			{name: "ping_with_error", tail: "event: ping\ndata: {\"error\":{\"code\":429}}\n\n", wantStatus: http.StatusTooManyRequests},
			{name: "usage_before_malformed", tail: usagePrefix + "data: {\"bad\":}\n\n", wantUsage: true},
			{name: "usage_before_error", tail: usagePrefix + "data: {\"error\":{\"code\":500}}\n\n", wantUsage: true, wantStatus: http.StatusInternalServerError},
			{name: "usage_before_sse_error", tail: usagePrefix + "event: error\ndata: {\"message\":\"failure\"}\n\n", wantUsage: true},
			{name: "invalid_second_data_in_finish_event", frame: "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\ndata: {\"error\":{\"code\":429}}\n\n"},
			{name: "error_event_before_finish", frame: "event: error\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
			{name: "error_event_after_finish", frame: "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\nevent: error\n\n"},
			{name: "last_event_type_error", frame: "event: message\nevent: error\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
			{name: "incomplete_error_event_before_finish", frame: "event: error\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n"},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				first := finish + tc.tail
				if tc.frame != "" {
					first = tc.frame
				}
				body := &heldGeminiResponse{first: []byte(first), closed: make(chan struct{})}
				t.Cleanup(func() { _ = body.Close() })
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: req}, nil
				}))
				auth := &cliproxyauth.Auth{ID: t.Name(), Attributes: map[string]string{"api_key": "test-key", "base_url": "https://fixture.invalid"}}
				capture := &geminiPartialUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
				var executor issue6258StreamExecutor
				if provider == "gemini" {
					executor = NewGeminiExecutor(&config.Config{})
				} else {
					executor = NewGeminiVertexExecutor(&config.Config{})
				}
				request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
				result, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gemini-3.8-flash-high", Payload: request}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
				})
				if errExecute != nil {
					t.Fatal(errExecute)
				}
				var gotErr error
				for _, chunk := range collectHeldGeminiChunks(t, result.Chunks) {
					if chunk.Err != nil {
						gotErr = chunk.Err
					}
					for _, event := range issue6258Events(chunk.Payload) {
						if event.Get("type").String() == "response.completed" {
							t.Fatalf("invalid buffered event was reported as success: %s", event.Raw)
						}
					}
				}
				if gotErr == nil || body.reads.Load() != 1 {
					t.Fatalf("error=%v reads=%d; want failure without another read", gotErr, body.reads.Load())
				}
				wantStatus := tc.wantStatus
				if wantStatus == 0 {
					wantStatus = http.StatusBadGateway
				}
				var statusCoder interface{ StatusCode() int }
				if !errors.As(gotErr, &statusCoder) || statusCoder.StatusCode() != wantStatus {
					t.Fatalf("error status = %v, want %d", gotErr, wantStatus)
				}
				select {
				case record := <-capture.records:
					if !record.Failed {
						t.Fatalf("invalid buffered event was recorded as success: %+v", record)
					}
					if tc.wantUsage && (record.Detail.InputTokens != 100 || record.Detail.OutputTokens != 10 || record.Detail.TotalTokens != 110) {
						t.Fatalf("failure lost previously received usage: %+v", record.Detail)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("invalid buffered event was not reported")
				}
			})
		}
	}
}

func TestGeminiResponsesUsesAlreadyBufferedUsageWithoutWaiting(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	const valid = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const multiline = "data: {\"usageMetadata\":\n" + "data: {\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const wrapped = "data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}}\n\n"
	const partial = "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n\n" + "data: {\"usageMetadata\":{\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const keepalive = "event: ping\ndata: ping\n\n"
	const finishWithUsage = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":9,\"totalTokenCount\":109}}\n\n"
	const updatePartial = "data: {\"usageMetadata\":{\"promptTokenCount\":\"invalid\",\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, provider := range []string{"gemini", "vertex"} {
		for _, tc := range []struct {
			name     string
			finish   string
			tail     string
			wantSeen bool
		}{
			{name: "complete", tail: valid, wantSeen: true},
			{name: "multiline", tail: multiline, wantSeen: true},
			{name: "wrapped", tail: wrapped, wantSeen: true},
			{name: "partial", tail: partial, wantSeen: true},
			{name: "keepalive_then_usage", tail: keepalive + valid, wantSeen: true},
			{name: "same_frame_then_partial", finish: finishWithUsage, tail: updatePartial, wantSeen: true},
			{name: "event_error_replaced_before_finish", finish: "event: error\nevent: message\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
			{name: "event_error_replaced_after_finish", finish: "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\nevent: error\nevent: message\n\n"},
			{name: "not_yet_received"},
			{name: "not_event_delimited", tail: strings.TrimSuffix(valid, "\n")},
			{name: "after_done", tail: "data: [DONE]\n\n" + valid},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				finishFrame := finish
				if tc.finish != "" {
					finishFrame = tc.finish
				}
				body := &heldGeminiResponse{first: []byte(finishFrame + tc.tail), closed: make(chan struct{})}
				t.Cleanup(func() { _ = body.Close() })
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: req}, nil
				}))
				auth := &cliproxyauth.Auth{ID: t.Name(), Attributes: map[string]string{"api_key": "test-key", "base_url": "https://fixture.invalid"}}
				capture := &geminiPartialUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
				var executor issue6258StreamExecutor
				if provider == "gemini" {
					executor = NewGeminiExecutor(&config.Config{})
				} else {
					executor = NewGeminiVertexExecutor(&config.Config{})
				}
				request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
				result, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gemini-3.8-flash-high", Payload: request}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
				})
				if errExecute != nil {
					t.Fatal(errExecute)
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
							t.Fatalf("unexpected stream error: %v", chunk.Err)
						}
						for _, event := range issue6258Events(chunk.Payload) {
							if event.Get("type").String() == "response.completed" {
								terminals++
								got := event.Get("response.usage")
								if tc.wantSeen {
									if !got.Exists() || got.Get("input_tokens").Int() != 100 || got.Get("output_tokens").Int() != 10 || got.Get("total_tokens").Int() != 110 {
										t.Fatalf("terminal lost already-buffered usage: %s", got.Raw)
									}
								} else if got.Exists() {
									t.Fatalf("terminal invented unavailable usage: %s", got.Raw)
								}
							}
						}
					case <-timer.C:
						t.Fatal("stream did not finish while the next read was blocked")
					}
				}
				if terminals != 1 || body.reads.Load() != 1 {
					t.Fatalf("terminals=%d reads=%d, want one terminal and one read", terminals, body.reads.Load())
				}
				select {
				case record := <-capture.records:
					if record.Failed {
						t.Fatalf("request unexpectedly failed: %+v", record.Fail)
					}
					if tc.wantSeen {
						if record.Detail.InputTokens != 100 || record.Detail.OutputTokens != 10 || record.Detail.TotalTokens != 110 {
							t.Fatalf("buffered usage was not retained: %+v", record.Detail)
						}
					} else if record.Detail.InputTokens != 0 || record.Detail.OutputTokens != 0 || record.Detail.TotalTokens != 0 {
						t.Fatalf("unavailable or incomplete usage was counted: %+v", record.Detail)
					}
				case <-timer.C:
					t.Fatal("usage was not reported after stream completion")
				}
			})
		}
	}
}

func TestGeminiVertexServiceAccountRetainsBufferedUsage(t *testing.T) {
	const first = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n" +
		"data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	body := &heldGeminiResponse{first: []byte(first), closed: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close() })
	var tokenCalls atomic.Int32
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/token" {
			tokenCalls.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: req}, nil
	}))
	var serviceAccount map[string]any
	if errDecode := json.Unmarshal(testVertexServiceAccountJSON(t, "https://fixture.invalid/token"), &serviceAccount); errDecode != nil {
		t.Fatal(errDecode)
	}
	auth := &cliproxyauth.Auth{ID: t.Name(), Metadata: map[string]any{"project_id": "proxy-test", "location": "global", "service_account": serviceAccount}}
	capture := &geminiPartialUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiPartialUsageNoop{}) })
	request := []byte(`{"model":"gemini-3.8-flash-high","input":"fixture","stream":true}`)
	result, errExecute := NewGeminiVertexExecutor(&config.Config{}).ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gemini-3.8-flash-high", Payload: request}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
	})
	if errExecute != nil {
		t.Fatal(errExecute)
	}
	response := issue6258ExecutorTerminal(t, result.Chunks)
	terminalUsage := response.Get("usage")
	if !terminalUsage.Exists() || terminalUsage.Get("input_tokens").Int() != 100 || terminalUsage.Get("output_tokens").Int() != 10 || terminalUsage.Get("total_tokens").Int() != 110 {
		t.Fatalf("service-account terminal lost already-buffered usage: %s", response.Raw)
	}
	if tokenCalls.Load() != 1 || body.reads.Load() != 1 {
		t.Fatalf("token calls=%d, stream reads=%d; want one each", tokenCalls.Load(), body.reads.Load())
	}
	select {
	case record := <-capture.records:
		if record.Failed || record.Detail.InputTokens != 100 || record.Detail.OutputTokens != 10 || record.Detail.TotalTokens != 110 {
			t.Fatalf("service-account buffered usage: failed=%t detail=%+v", record.Failed, record.Detail)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service-account usage was not reported")
	}
}
