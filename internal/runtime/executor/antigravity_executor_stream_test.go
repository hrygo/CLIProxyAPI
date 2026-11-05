package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	internalcache "github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

type antigravityBufferedBody struct {
	first []byte
	err   error
	reads atomic.Int32
}

func (b *antigravityBufferedBody) Read(p []byte) (int, error) {
	if b.reads.Add(1) != 1 {
		return 0, errors.New("unexpected later network read")
	}
	if len(p) < len(b.first) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, b.first), b.err
}

func (*antigravityBufferedBody) Close() error { return nil }

func TestAntigravityBufferedCurrentEventBoundary(t *testing.T) {
	const response = `{"candidates":[{"content":{"parts":[{"functionCall":{"id":"first","name":"lookup","args":{"key":"value"}}}]},"finishReason":"STOP"}]}`
	const finish = `data: {"response":` + response + `}` + "\n"
	for _, tc := range []struct {
		name, wire string
		wantStatus int
	}{
		{name: "error_before_finish", wire: "event: error\n" + `data: {"code":429,"response":` + response + `}` + "\n\n", wantStatus: 429},
		{name: "error_overridden_before_finish", wire: "event: error\nevent: message\n" + finish + "\n"},
		{name: "message_overridden_before_finish", wire: "event: message\nevent: error\n" + finish + "\n", wantStatus: 502},
		{name: "error_overridden_after_finish", wire: finish + "event: error\nevent: message\n\n"},
		{name: "message_overridden_after_finish", wire: finish + "event: message\nevent: error\n\n", wantStatus: 502},
		{name: "incomplete_error_before_finish", wire: "event: response.failed\n" + finish, wantStatus: 502},
		{name: "incomplete_error_after_finish", wire: finish + "event: error\n", wantStatus: 502},
		{name: "extra_data_invalidates_finish", wire: finish + `data: {"error":{"code":429}}` + "\n\n", wantStatus: 502},
		{name: "duplicate_in_finish", wire: `data: {"response":` + response + `,"response":{"error":{"code":429}}}` + "\n\n", wantStatus: 502},
		{name: "nested_error_in_finish", wire: `data: {"response":` + strings.TrimSuffix(response, "}") + `,"error":{"code":429}}}` + "\n\n", wantStatus: 429},
		{name: "duplicate_in_multiline_finish", wire: `data: {"response":` + response + ",\n" + `data: "response":{"error":{"code":429}}}` + "\n\n", wantStatus: 502},
		{name: "valid_multiline_finish", wire: "data: {\"response\":\n" + `data: ` + response + `}` + "\n\n"},
		{name: "incomplete_extra_data", wire: finish + `data: {"error":{"code":429}}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3.7-flash"
			session := t.Name()
			body := &antigravityBufferedBody{first: []byte(tc.wire)}
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
			ctx := context.WithValue(baseCtx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
			}))
			request := fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"key":{"type":"string"}}}}]}`, model, session)
			var gotError error
			var terminal string
			for chunk := range issue6258StartStream(t, ctx, executor, auth, request) {
				if chunk.Err != nil {
					gotError = chunk.Err
				} else if event := geminiResponsesTerminalEvent(chunk.Payload); event != "" {
					terminal = event
				}
			}
			if tc.wantStatus != 0 {
				status, ok := gotError.(interface{ StatusCode() int })
				if !ok || status.StatusCode() != tc.wantStatus || terminal == "response.completed" {
					t.Fatalf("error=%v, terminal=%q; want failure status %d", gotError, terminal, tc.wantStatus)
				}
				if items, ok := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session); ok {
					t.Fatalf("failed event committed replay: %q", items)
				}
			} else if gotError != nil || terminal != "response.completed" {
				t.Fatalf("error=%v, terminal=%q; want successful finish", gotError, terminal)
			}
			if got := body.reads.Load(); got != 1 {
				t.Fatalf("underlying reads = %d, want 1", got)
			}
		})
	}
}

func TestAntigravityBufferedTailSignatureAndSafety(t *testing.T) {
	const signature = "buffered-native-signature-123456789"
	const finish = `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"id":"first","name":"lookup","args":{"key":"value"}}}]},"finishReason":"STOP"}]}}` + "\n\n"
	const carrier = `data: {"response":{"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"` + signature + `"}]}}]}}` + "\n\n"
	const mixedTool = `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"injected","args":{}},"thoughtSignature":"` + signature + `"}]}}]}}` + "\n\n"
	const mixedText = `data: {"response":{"candidates":[{"content":{"parts":[{"text":"injected","thoughtSignature":"` + signature + `"}]}}]}}` + "\n\n"
	const mixedUnknown = `data: {"response":{"candidates":[{"content":{"parts":[{"inlineData":{"data":"injected"},"thoughtSignature":"` + signature + `"}]}}]}}` + "\n\n"
	const unknownEnvelope = `data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"` + signature + `"}]} }],"output":{"functionCall":{"name":"injected"}}}}` + "\n\n"
	const incomplete = `data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"` + signature + `"}]}}]}}` + "\n"
	const heartbeat = "event: ping\ndata: ping\n\n"
	for _, tc := range []struct {
		name          string
		tail          string
		readErr       error
		wantSignature bool
		wantError     bool
	}{
		{name: "complete_signature", tail: carrier, wantSignature: true},
		{name: "heartbeat_then_signature", tail: heartbeat + carrier, wantSignature: true},
		{name: "not_arrived"},
		{name: "incomplete_signature", tail: incomplete},
		{name: "injected_tool", tail: mixedTool},
		{name: "injected_text", tail: mixedText},
		{name: "unknown_part", tail: mixedUnknown},
		{name: "unknown_envelope", tail: unknownEnvelope},
		{name: "unknown_event_then_signature", tail: `data: {"text":"injected"}` + "\n\n" + carrier},
		{name: "injected_tool_then_signature", tail: mixedTool + carrier},
		{name: "signature_then_injected_tool", tail: carrier + mixedTool},
		{name: "malformed_frame", tail: "data: {\"bad\":}\n\n", wantError: true},
		{name: "signature_then_malformed", tail: carrier + "data: {\"bad\":}\n\n", wantError: true},
		{name: "upstream_error", tail: `data: {"error":{"code":502,"message":"failure"}}` + "\n\n", wantError: true},
		{name: "heartbeat_json_error", tail: "event: ping\n" + `data: {"error":{"code":502,"message":"failure"}}` + "\n\n", wantError: true},
		{name: "heartbeat_duplicate_error", tail: "event: ping\n" + `data: {"error":null,"error":{"code":502}}` + "\n\n", wantError: true},
		{name: "signature_then_error", tail: carrier + `data: {"error":{"code":502,"message":"failure"}}` + "\n\n", wantError: true},
		{name: "upstream_code_message_error", tail: `data: {"code":502,"message":"failure"}` + "\n\n", wantError: true},
		{name: "known_read_error", readErr: io.ErrUnexpectedEOF, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3.7-flash"
			session := t.Name()
			body := &antigravityBufferedBody{first: []byte(finish + tc.tail), err: tc.readErr}
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
			ctx := context.WithValue(baseCtx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
			}))
			request := fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"key":{"type":"string"}}}}]}`, model, session)
			chunks := issue6258StartStream(t, ctx, executor, auth, request)
			var gotError error
			var terminal string
			for chunk := range chunks {
				if chunk.Err != nil {
					gotError = chunk.Err
					continue
				}
				if event := geminiResponsesTerminalEvent(chunk.Payload); event != "" {
					terminal = event
				}
			}
			if gotError != nil != tc.wantError {
				t.Fatalf("stream error = %v, want error %v", gotError, tc.wantError)
			}
			if tc.wantError && terminal == "response.completed" {
				t.Fatalf("failed upstream was completed")
			}
			if !tc.wantError && terminal != "response.completed" {
				t.Fatalf("terminal = %q", terminal)
			}
			if got := body.reads.Load(); got != 1 {
				t.Fatalf("underlying reads = %d, want 1", got)
			}
			items, ok := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session)
			if tc.wantError && ok {
				t.Fatalf("failed response committed replay: %q", items)
			}
			var replay []byte
			for _, item := range items {
				replay = append(replay, item...)
			}
			if bytes.Contains(replay, []byte(signature)) != tc.wantSignature {
				t.Fatalf("signature presence mismatch: replay=%s", replay)
			}
			if strings.Contains(string(replay), "injected") {
				t.Fatalf("unverified tail entered replay: %s", replay)
			}
		})
	}
}

func TestAntigravityBufferedTailUsageIgnoresUnvalidatedArtifacts(t *testing.T) {
	var observed helps.StreamUsageBuffer
	payload := []byte(`{"response":{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"totalTokenCount":110},"candidates":[{"content":{"parts":[{"functionCall":{"name":"injected","args":{}}}]}}]}}`)
	if _, err := observeAntigravityBufferedEvents([][]byte{payload}, nil, &observed); err != nil {
		t.Fatal(err)
	}
	detail, ok := observed.Detail()
	if !ok || detail.InputTokens != 100 || detail.OutputTokens != 10 || detail.TotalTokens != 110 || detail.ToolUsageDetails != nil {
		t.Fatalf("usage incorporated unvalidated content: %+v, ok=%v", detail, ok)
	}
}

func TestAntigravityBufferedErrorStatusAndRedaction(t *testing.T) {
	const details = `"message":"synthetic-sensitive-message","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3s"}]`
	for _, tc := range []struct {
		name, payload string
		wantStatus    int
		wantRetry     bool
	}{
		{name: "root_429", payload: `{"error":{"code":429,` + details + `},"access_token":"synthetic-sensitive-token"}`, wantStatus: 429, wantRetry: true},
		{name: "nested_429", payload: `{"response":{"error":{"code":429,` + details + `}},"debug":"synthetic-sensitive-debug"}`, wantStatus: 429, wantRetry: true},
		{name: "fractional", payload: `{"error":{"code":429.5}}`, wantStatus: 502},
		{name: "precise_fractional", payload: `{"error":{"code":429.00000000000000001}}`, wantStatus: 502},
		{name: "out_of_range", payload: `{"error":{"code":200}}`, wantStatus: 502},
		{name: "string_code", payload: `{"error":{"code":"429"}}`, wantStatus: 429},
		{name: "invalid_string", payload: `{"error":{"code":"429oops"}}`, wantStatus: 502},
		{name: "direct_fractional", payload: `{"code":429.5,"message":"synthetic-sensitive-message"}`, wantStatus: 502},
		{name: "direct_valid", payload: `{"response":{"code":429,"message":"synthetic-sensitive-message"}}`, wantStatus: 429},
		{name: "malformed_retry", payload: `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"bad"}]}}`, wantStatus: 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := observeAntigravityBufferedEvents([][]byte{[]byte(tc.payload)}, nil, nil)
			status, ok := err.(interface {
				StatusCode() int
				RetryAfter() *time.Duration
			})
			if !ok || status.StatusCode() != tc.wantStatus || (status.RetryAfter() != nil) != tc.wantRetry {
				t.Fatalf("error=%v, want status=%d retry=%t", err, tc.wantStatus, tc.wantRetry)
			}
			if tc.wantRetry && *status.RetryAfter() != 3*time.Second {
				t.Fatalf("retry=%v, want 3s", *status.RetryAfter())
			}
			if strings.Contains(err.Error(), "synthetic-sensitive") {
				t.Fatalf("error exposes upstream content: %v", err)
			}
		})
	}
}

func TestAntigravityBufferedErrorIsRedactedFromStreamAndLog(t *testing.T) {
	const finish = `data: {"response":{"candidates":[{"finishReason":"STOP"}]}}` + "\n\n"
	const errorBody = `{"code":429,"message":"synthetic-sensitive-message","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3s"}]}`
	for _, tc := range []struct {
		name, failure string
		wantRetry     bool
	}{
		{name: "json_error", failure: `data: {"error":` + errorBody + `,"access_token":"synthetic-sensitive-token"}` + "\n\n", wantRetry: true},
		{name: "sse_error", failure: "event: error\n" + `data: {"error":` + errorBody + `,"access_token":"synthetic-sensitive-token"}` + "\n\n", wantRetry: true},
		{name: "sse_nested_error", failure: "event: response.failed\n" + `data: {"response":{"error":` + errorBody + `},"debug":"synthetic-sensitive-debug"}` + "\n\n", wantRetry: true},
		{name: "sse_bad_retry", failure: "event: error\n" + `data: {"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"bad"}]},"debug":"synthetic-sensitive-debug"}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &antigravityBufferedBody{first: []byte(finish + tc.failure)}
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
			executor.(*AntigravityExecutor).cfg.RequestLog = true
			ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx := context.WithValue(baseCtx, "gin", ginCtx)
			ctx = context.WithValue(ctx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
			}))
			var gotError error
			var output strings.Builder
			for chunk := range issue6258StartStream(t, ctx, executor, auth, `{"model":"gemini-3.7-flash","input":"hello","stream":true}`) {
				output.Write(chunk.Payload)
				if chunk.Err != nil {
					gotError = chunk.Err
					output.WriteString(chunk.Err.Error())
				}
			}
			status, ok := gotError.(interface {
				StatusCode() int
				RetryAfter() *time.Duration
			})
			if !ok || status.StatusCode() != 429 || (status.RetryAfter() != nil) != tc.wantRetry {
				t.Fatalf("error=%v, want status 429 and retry=%t", gotError, tc.wantRetry)
			}
			if tc.wantRetry && *status.RetryAfter() != 3*time.Second {
				t.Fatalf("retry delay=%v, want 3s", *status.RetryAfter())
			}
			value, exists := ginCtx.Get("API_RESPONSE")
			logBody, ok := value.([]byte)
			if !exists || !ok || !bytes.Contains(logBody, []byte("Error:")) {
				t.Fatalf("response log is missing the error: %q", logBody)
			}
			if strings.Contains(output.String(), "synthetic-sensitive") || bytes.Contains(logBody, []byte("synthetic-sensitive")) {
				t.Fatalf("sensitive buffered error reached the stream or log: stream=%q log=%q", output.String(), logBody)
			}
			if body.reads.Load() != 1 {
				t.Fatalf("underlying reads = %d, want 1", body.reads.Load())
			}
		})
	}
}

type antigravityBufferedUsageCapture struct {
	authID  string
	records chan usage.Record
}

func (c *antigravityBufferedUsageCapture) HandleUsage(_ context.Context, record usage.Record) {
	if record.AuthID == c.authID {
		c.records <- record
	}
}

type antigravityBufferedUsageNoop struct{}

func (antigravityBufferedUsageNoop) HandleUsage(context.Context, usage.Record) {}

func TestAntigravityBufferedFailurePreservesPriorUsage(t *testing.T) {
	const finish = `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"id":"first","name":"lookup","args":{"key":"value"}}}]},"finishReason":"STOP"}]}}` + "\n\n"
	const usageFrame = `data: {"response":{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":115}}}` + "\n\n"
	const signature = `data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"buffered-signature-must-not-commit"}]}}]}}` + "\n\n"
	const upstreamError = `data: {"error":{"code":502,"message":"upstream failure"}}` + "\n\n"
	const sseError = "event: error\ndata: {\"message\":\"private quota details\"}\n\n"
	const sseErrorWithoutData = "event: response.failed\n\n"
	const sseError429 = "event: error\n" + `data: {"error":{"code":429,"message":"private quota details"}}` + "\n\n"
	const malformed = "data: {\"bad\":}\n\n"
	const duplicateError = `data: {"error":null,"error":{"code":429}}` + "\n\n"
	const escapedDuplicate = `data: {"response":{"error":null,"\u0065rror":{"code":429}}}` + "\n\n"
	for _, tc := range []struct {
		name       string
		tail       string
		readErr    error
		wantUsage  bool
		wantSource string
	}{
		{name: "read_error", tail: usageFrame + signature, readErr: io.ErrUnexpectedEOF, wantUsage: true, wantSource: "read"},
		{name: "error_event", tail: usageFrame + signature + upstreamError + usageFrame, wantUsage: true, wantSource: "event"},
		{name: "sse_error_event", tail: usageFrame + signature + sseError + usageFrame, wantUsage: true, wantSource: "sse_event"},
		{name: "sse_error_without_data", tail: usageFrame + sseErrorWithoutData, wantUsage: true, wantSource: "sse_event"},
		{name: "sse_error_429", tail: usageFrame + signature + sseError429, wantUsage: true, wantSource: "sse_event_429"},
		{name: "malformed_event", tail: usageFrame + signature + malformed + usageFrame, wantUsage: true, wantSource: "malformed"},
		{name: "duplicate_error_key", tail: usageFrame + signature + duplicateError, wantUsage: true, wantSource: "malformed"},
		{name: "escaped_duplicate_error_key", tail: usageFrame + signature + escapedDuplicate, wantUsage: true, wantSource: "malformed"},
		{name: "error_precedes_usage", tail: upstreamError + usageFrame, wantSource: "event"},
		{name: "event_precedes_read_error", tail: usageFrame + upstreamError, readErr: io.ErrUnexpectedEOF, wantUsage: true, wantSource: "event"},
		{name: "malformed_precedes_read_error", tail: usageFrame + malformed, readErr: io.ErrUnexpectedEOF, wantUsage: true, wantSource: "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3.7-flash"
			session := t.Name()
			body := &antigravityBufferedBody{first: []byte(finish + tc.tail), err: tc.readErr}
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
			capture := &antigravityBufferedUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), antigravityBufferedUsageNoop{}) })
			ctx := context.WithValue(baseCtx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
			}))
			request := fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hello","stream":true,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"key":{"type":"string"}}}}]}`, model, session)
			chunks := issue6258StartStream(t, ctx, executor, auth, request)
			var gotError error
			for chunk := range chunks {
				if chunk.Err != nil {
					gotError = chunk.Err
				}
				if geminiResponsesTerminalEvent(chunk.Payload) == "response.completed" {
					t.Fatal("failed stream emitted a successful terminal")
				}
			}
			if gotError == nil {
				t.Fatal("expected an upstream failure")
			}
			switch tc.wantSource {
			case "read":
				if !errors.Is(gotError, io.ErrUnexpectedEOF) {
					t.Fatalf("error = %v, want unexpected EOF", gotError)
				}
			case "event":
				if errors.Is(gotError, io.ErrUnexpectedEOF) || gotError.Error() != "upstream returned an error after finish" {
					t.Fatalf("error = %v, want upstream error event", gotError)
				}
			case "malformed":
				if !strings.Contains(gotError.Error(), "invalid buffered SSE JSON event") {
					t.Fatalf("error = %v, want malformed event", gotError)
				}
			case "sse_event":
				if !strings.Contains(gotError.Error(), "buffered SSE error event") || strings.Contains(gotError.Error(), "private quota details") {
					t.Fatalf("error = %v, want redacted SSE error event", gotError)
				}
			case "sse_event_429":
				status, ok := gotError.(interface{ StatusCode() int })
				if !ok || status.StatusCode() != 429 || strings.Contains(gotError.Error(), "private quota details") {
					t.Fatalf("error = %v, want redacted 429 SSE error", gotError)
				}
			}
			if got := body.reads.Load(); got != 1 {
				t.Fatalf("underlying reads = %d, want 1", got)
			}
			if items, ok := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session); ok {
				t.Fatalf("failed stream committed replay: %q", items)
			}
			select {
			case record := <-capture.records:
				if !record.Failed {
					t.Fatalf("usage record marked successful: %+v", record)
				}
				detail := record.Detail
				if tc.wantUsage && (detail.InputTokens != 100 || detail.OutputTokens != 10 || detail.ReasoningTokens != 5 || detail.TotalTokens != 115) {
					t.Fatalf("failure usage = %+v, want input=100 output=10 reasoning=5 total=115", detail)
				}
				if !tc.wantUsage && (detail.InputTokens != 0 || detail.OutputTokens != 0 || detail.TotalTokens != 0) {
					t.Fatalf("usage after failure was recorded: %+v", detail)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("failure usage record not published")
			}
		})
	}
}

func TestAntigravityBufferedTerminalUsage(t *testing.T) {
	const finish = `data: {"response":{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}}` + "\n\n"
	const finishWithUsage = `data: {"response":{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":9,"thoughtsTokenCount":5,"totalTokenCount":114}}}` + "\n\n"
	const complete = `data: {"response":{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":115}}}` + "\n\n"
	const partial = `data: {"response":{"usageMetadata":{"promptTokenCount":100}}}` + "\n\n" +
		`data: {"response":{"usageMetadata":{"candidatesTokenCount":10,"thoughtsTokenCount":5,"totalTokenCount":115}}}` + "\n\n"
	const updated = `data: {"response":{"usageMetadata":{"candidatesTokenCount":10,"totalTokenCount":115}}}` + "\n\n"
	const multiline = "data: {\"response\":{\"usageMetadata\":\n" + "data: {\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":5,\"totalTokenCount\":115}}}\n\n"
	for _, tc := range []struct {
		name, finish, tail string
	}{
		{name: "complete", finish: finish, tail: complete},
		{name: "partial", finish: finish, tail: partial},
		{name: "multiline", finish: finish, tail: multiline},
		{name: "finish_then_updated", finish: finishWithUsage, tail: updated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-3.7-flash"
			body := &antigravityBufferedBody{first: []byte(tc.finish + tc.tail)}
			baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
			capture := &antigravityBufferedUsageCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), antigravityBufferedUsageNoop{}) })
			ctx := context.WithValue(baseCtx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
			}))
			request := fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hello","stream":true}`, model, t.Name())
			var terminal gjson.Result
			for chunk := range issue6258StartStream(t, ctx, executor, auth, request) {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				for _, event := range issue6258Events(chunk.Payload) {
					if event.Get("type").String() == "response.completed" {
						terminal = event
					}
				}
			}
			clientUsage := terminal.Get("response.usage")
			if clientUsage.Get("input_tokens").Int() != 100 || clientUsage.Get("output_tokens").Int() != 15 || clientUsage.Get("total_tokens").Int() != 115 {
				t.Fatalf("terminal usage = %s, want input=100 output=15 total=115", clientUsage.Raw)
			}
			if body.reads.Load() != 1 {
				t.Fatalf("underlying reads = %d, want 1", body.reads.Load())
			}
			select {
			case record := <-capture.records:
				if record.Failed || record.Detail.InputTokens != 100 || record.Detail.OutputTokens != 10 || record.Detail.ReasoningTokens != 5 || record.Detail.TotalTokens != 115 {
					t.Fatalf("reported usage = %+v", record)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("usage record not published")
			}
		})
	}
}

type antigravityNestedUsageKey struct{}

type antigravityNestedUsageHooks struct {
	observed chan gjson.Result
}

func (*antigravityNestedUsageHooks) NormalizeRequest(_ context.Context, _, _ sdktranslator.Format, _ string, body []byte, _ bool) []byte {
	return body
}

func (*antigravityNestedUsageHooks) TranslateRequest(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (h *antigravityNestedUsageHooks) NormalizeResponseBefore(ctx context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	if ctx.Value(antigravityNestedUsageKey{}) != nil || gjson.GetBytes(body, "response.candidates.0.finishReason").String() == "" {
		return body
	}
	nestedCtx := context.WithValue(ctx, antigravityNestedUsageKey{}, true)
	var nestedState any
	nested := []byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)
	for _, chunk := range sdktranslator.TranslateStream(nestedCtx, sdktranslator.FormatGemini, sdktranslator.FormatOpenAIResponse, "gemini-3.7-flash", nil, nil, nested, &nestedState) {
		for _, event := range issue6258Events(chunk) {
			if event.Get("type").String() == "response.completed" {
				h.observed <- event.Get("response.usage")
			}
		}
	}
	return body
}

func (*antigravityNestedUsageHooks) TranslateResponse(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, []byte, []byte, bool) ([]byte, bool) {
	return nil, false
}

func (*antigravityNestedUsageHooks) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	return body
}

func TestAntigravityBufferedUsageDoesNotLeakIntoNestedPlugin(t *testing.T) {
	const finish = `data: {"response":{"candidates":[{"finishReason":"STOP"}]}}` + "\n\n"
	const tail = `data: {"response":{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"totalTokenCount":110}}}` + "\n\n"
	body := &antigravityBufferedBody{first: []byte(finish + tail)}
	hooks := &antigravityNestedUsageHooks{observed: make(chan gjson.Result, 1)}
	sdktranslator.SetPluginHooks(hooks)
	t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
	baseCtx, executor, auth := issue6258Executor(t, "antigravity", "https://upstream.invalid")
	ctx := context.WithValue(baseCtx, "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
	}))
	request := `{"model":"gemini-3.7-flash","input":"hello","stream":true}`
	var outer gjson.Result
	for chunk := range issue6258StartStream(t, ctx, executor, auth, request) {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		for _, event := range issue6258Events(chunk.Payload) {
			if event.Get("type").String() == "response.completed" {
				outer = event.Get("response.usage")
			}
		}
	}
	select {
	case nested := <-hooks.observed:
		if nested.Get("input_tokens").Int() != 1 || nested.Get("output_tokens").Int() != 2 || nested.Get("total_tokens").Int() != 3 {
			t.Fatalf("nested plugin inherited outer usage: %s", nested.Raw)
		}
	default:
		t.Fatal("nested plugin translation did not run")
	}
	if outer.Get("input_tokens").Int() != 100 || outer.Get("output_tokens").Int() != 10 || outer.Get("total_tokens").Int() != 110 {
		t.Fatalf("outer buffered usage = %s", outer.Raw)
	}
	if body.reads.Load() != 1 {
		t.Fatalf("underlying reads = %d, want 1", body.reads.Load())
	}
}
