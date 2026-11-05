package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/wsrelay"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

type aiStudioBufferedResult struct {
	events []gjson.Result
	err    error
	record usage.Record
}

func TestAIStudioBufferedBatchReaderAcrossFrames(t *testing.T) {
	frames := [][]byte{[]byte("alpha"), nil, []byte("beta")}
	pending := []byte("gamma")
	reader := &aiStudioBatchReader{frames: frames, pending: pending}
	if n, err := reader.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty read = %d, %v", n, err)
	}
	for _, tc := range []struct {
		size int
		want string
	}{
		{size: 7, want: "alphabe"},
		{size: 3, want: "tag"},
		{size: 12, want: "amma"},
	} {
		buf := make([]byte, tc.size)
		n, err := reader.Read(buf)
		if err != nil || string(buf[:n]) != tc.want {
			t.Fatalf("read = %q, %v; want %q", buf[:n], err, tc.want)
		}
	}
	if n, err := reader.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after batch = %d, %v", n, err)
	}
	if string(frames[0]) != "alpha" || string(frames[2]) != "beta" || string(pending) != "gamma" {
		t.Fatalf("reader modified frame slices: %q, %q", frames, pending)
	}
}

func TestAIStudioBufferedDoneAvoidsAllocatingLaterLargeFrame(t *testing.T) {
	largeFrame := bytes.Repeat([]byte("x"), 16<<20)
	copy(largeFrame, []byte("data: {\"padding\":\""))
	copy(largeFrame[len(largeFrame)-4:], []byte("\"}\n\n"))
	for _, marker := range []string{"data: [DONE]\n\n", "data: [DONE]\r\n\r\n"} {
		t.Run(fmt.Sprintf("crlf=%t", strings.Contains(marker, "\r")), func(t *testing.T) {
			frames := [][]byte{[]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"), []byte(marker), largeFrame}
			pending := []byte("event: error\n")
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 3 {
				var streamUsage helps.StreamUsageBuffer
				snapshots, err := observeAIStudioBufferedUsage(frames, pending, &streamUsage)
				if err != nil || len(snapshots) != 0 {
					t.Fatalf("after [DONE]: snapshots=%q error=%v", snapshots, err)
				}
			}
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 8<<20 {
				t.Fatalf("scanning after [DONE] allocated %d bytes for a 16 MiB ignored frame", allocated)
			}
		})
	}
}

// sendAIStudioBuffered keeps all later WebSocket messages behind a gate that is
// opened only after the client stream closes. batch is sent in one message.
func sendAIStudioBuffered(t *testing.T, batch string) aiStudioBufferedResult {
	t.Helper()
	return sendAIStudioBufferedChunks(t, []string{batch})
}

func sendAIStudioBufferedChunks(t *testing.T, chunks []string) aiStudioBufferedResult {
	t.Helper()
	authID := t.Name()
	connected := make(chan struct{})
	relay := wsrelay.NewManager(wsrelay.Options{
		ProviderFactory: func(*http.Request) (string, error) { return authID, nil },
		OnConnected:     func(string) { close(connected) },
	})
	server := httptest.NewServer(relay.Handler())
	conn, _, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+relay.Path(), nil)
	if errDial != nil {
		server.Close()
		t.Fatal(errDial)
	}
	<-connected
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseServer()
		_ = conn.Close()
		_ = relay.Stop(context.Background())
		server.Close()
	})
	serverDone := make(chan error, 1)
	tailAttempted := make(chan struct{})
	go func() {
		var req wsrelay.Message
		if err := conn.ReadJSON(&req); err != nil {
			serverDone <- err
			return
		}
		messages := []wsrelay.Message{{ID: req.ID, Type: wsrelay.MessageTypeStreamStart, Payload: map[string]any{"status": 200}}}
		for _, chunk := range chunks {
			messages = append(messages, wsrelay.Message{ID: req.ID, Type: wsrelay.MessageTypeStreamChunk, Payload: map[string]any{"data": chunk}})
		}
		for _, msg := range messages {
			if err := conn.WriteJSON(msg); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
		<-release
		// Any data in this second message is not available before termination.
		_ = conn.WriteJSON(wsrelay.Message{ID: req.ID, Type: wsrelay.MessageTypeStreamChunk, Payload: map[string]any{"data": "data: {\"usageMetadata\":{\"promptTokenCount\":999}}\n\n"}})
		close(tailAttempted)
	}()

	capture := &geminiPartialUsageCapture{authID: authID, records: make(chan usage.Record, 2)}
	usage.RegisterNamedPlugin(authID, capture)
	t.Cleanup(func() { usage.RegisterNamedPlugin(authID, geminiPartialUsageNoop{}) })
	request := []byte(`{"model":"gemini-3.1-pro-preview","input":"fixture","stream":true}`)
	result, errExecute := NewAIStudioExecutor(&config.Config{}, "aistudio", relay).ExecuteStream(context.Background(),
		&cliproxyauth.Auth{ID: authID, Provider: "aistudio"},
		cliproxyexecutor.Request{Model: "gemini-3.1-pro-preview", Payload: request},
		cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true})
	if errExecute != nil {
		t.Fatal(errExecute)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var got aiStudioBufferedResult
stream:
	for {
		select {
		case chunk, ok := <-result.Chunks:
			if !ok {
				break stream
			}
			if chunk.Err != nil {
				got.err = chunk.Err
			}
			got.events = append(got.events, issue6258Events(chunk.Payload)...)
		case <-timer.C:
			t.Fatal("stream waited for another WebSocket message")
		}
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("fixture did not send the batch")
	}
	releaseServer()
	select {
	case <-tailAttempted:
	case <-timer.C:
		t.Fatal("fixture did not attempt the gated future message")
	}
	select {
	case got.record = <-capture.records:
	case <-timer.C:
		t.Fatal("usage record not published")
	}
	return got
}

func TestAIStudioResponsesBufferedUsageWithoutWaiting(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}]}\n\n"
	const full = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const partial = "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n\ndata: {\"usageMetadata\":{\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const multiline = "event: message\r\ndata: {\"usageMetadata\":\r\ndata: {\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\r\n\r\n"
	const withUsage = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":9,\"totalTokenCount\":109}}\n\n"
	const update = "data: {\"usageMetadata\":{\"promptTokenCount\":\"invalid\",\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	const malicious = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"injected_tool\",\"args\":{}},\"thoughtSignature\":\"injected_signature\"},{\"text\":\"injected_text\"}]}}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, tc := range []struct {
		name, first, tail, terminal     string
		wantUsage                       bool
		input, output, reasoning, total int64
	}{
		{name: "complete", tail: full, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "partial_snapshots", tail: partial, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "multiline_crlf", tail: multiline, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "multiline_finish", first: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},\n" + "data: \"finishReason\":\"STOP\"}]}\n\n", tail: full, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "wrapped", tail: "data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}}\n\n", wantUsage: true, input: 100, output: 10, total: 110},
		{name: "existing_and_invalid_update", first: withUsage, tail: update, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "reasoning_usage", tail: "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":5,\"totalTokenCount\":115}}\n\n", wantUsage: true, input: 100, output: 15, reasoning: 5, total: 115},
		{name: "heartbeat", tail: "event: ping\ndata: ping\n\n" + full, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "large_prior_event", tail: "data: {\"padding\":\"" + strings.Repeat("x", 1<<20) + "\"}\n\n" + full, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "malicious_content_ignored", tail: malicious, wantUsage: true, input: 100, output: 10, total: 110},
		{name: "explicit_zero", tail: "data: {\"usageMetadata\":{\"promptTokenCount\":0,\"candidatesTokenCount\":0,\"totalTokenCount\":0}}\n\n", wantUsage: true},
		{name: "invalid_usage_not_fabricated", tail: "data: {\"usageMetadata\":{\"promptTokenCount\":-1,\"totalTokenCount\":\"invalid\"}}\n\n"},
		{name: "no_later_frame"},
		{name: "unfinished_event", tail: strings.TrimSuffix(full, "\n")},
		{name: "after_done", tail: "data: [DONE]\n\n" + full},
		{name: "error_after_done", tail: "data: [DONE]\n\nevent: error\ndata: {\"code\":429}\n\n"},
		{name: "terminal_from_done", first: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]}}]}\n\n", tail: "data: [DONE]\n\nevent: error\ndata: {\"code\":429}\n\n"},
		{name: "max_tokens", first: strings.Replace(finish, "STOP", "MAX_TOKENS", 1), tail: full, terminal: "response.incomplete", wantUsage: true, input: 100, output: 10, total: 110},
		{name: "max_tokens_same_frame", first: strings.Replace(withUsage, "STOP", "MAX_TOKENS", 1), terminal: "response.incomplete", wantUsage: true, input: 100, output: 9, total: 109},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.first
			if first == "" {
				first = finish
			}
			got := sendAIStudioBuffered(t, first+tc.tail)
			if got.err != nil || got.record.Failed {
				t.Fatalf("unexpected error=%v failed=%t", got.err, got.record.Failed)
			}
			wantTerminal := tc.terminal
			if wantTerminal == "" {
				wantTerminal = "response.completed"
			}
			var terminal gjson.Result
			lastSeq := int64(0)
			for _, event := range got.events {
				if strings.Contains(event.Raw, "injected_tool") || strings.Contains(event.Raw, "injected_text") || strings.Contains(event.Raw, "injected_signature") {
					t.Fatalf("post-finish content leaked: %s", event.Raw)
				}
				if seq := event.Get("sequence_number"); seq.Exists() {
					if seq.Int() != lastSeq+1 {
						t.Fatalf("sequence jumped from %d to %d", lastSeq, seq.Int())
					}
					lastSeq = seq.Int()
				}
				if event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
					if terminal.Exists() || event.Get("type").String() != wantTerminal {
						t.Fatalf("unexpected terminal: %s", event.Raw)
					}
					terminal = event
				}
			}
			if !terminal.Exists() || !strings.Contains(terminal.Raw, "answer") {
				t.Fatalf("missing terminal content: %v", got.events)
			}
			u := terminal.Get("response.usage")
			if u.Exists() != tc.wantUsage || tc.wantUsage && (u.Get("input_tokens").Int() != tc.input || u.Get("output_tokens").Int() != tc.output || u.Get("output_tokens_details.reasoning_tokens").Int() != tc.reasoning || u.Get("total_tokens").Int() != tc.total) {
				t.Fatalf("terminal usage = %s, want present=%t input=%d output=%d total=%d", u.Raw, tc.wantUsage, tc.input, tc.output, tc.total)
			}
			d := got.record.Detail
			if d.InputTokens != tc.input || d.OutputTokens != tc.output-tc.reasoning || d.ReasoningTokens != tc.reasoning || d.TotalTokens != tc.total {
				t.Fatalf("published usage = %+v, want input=%d output=%d total=%d", d, tc.input, tc.output, tc.total)
			}
		})
	}
}

func TestAIStudioResponsesBufferedErrors(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"
	const usagePrefix = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, tc := range []struct {
		name, tail string
		status     int
		wantUsage  bool
	}{
		{name: "malformed", tail: "data: {\"bad\":}\n\n", status: 502},
		{name: "duplicate", tail: "data: {\"error\":null,\"error\":{\"code\":429}}\n\n", status: 502},
		{name: "json_error", tail: "data: {\"error\":{\"code\":429,\"message\":\"private\"}}\n\n", status: 429},
		{name: "wrapped_error", tail: "data: {\"response\":{\"error\":{\"code\":503}}}\n\n", status: 503},
		{name: "event_error", tail: "event: error\ndata: {\"error\":{\"code\":429}}\n\n", status: 429},
		{name: "event_error_no_data", tail: "event: error\n\n", status: 502},
		{name: "usage_before_error", tail: usagePrefix + "event: error\ndata: {\"code\":500}\n\n", status: 500, wantUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sendAIStudioBuffered(t, finish+tc.tail)
			var coder interface{ StatusCode() int }
			if !errors.As(got.err, &coder) || coder.StatusCode() != tc.status || strings.Contains(fmt.Sprint(got.err), "private") {
				t.Fatalf("error = %v, want safe status %d", got.err, tc.status)
			}
			for _, event := range got.events {
				if event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
					t.Fatalf("buffered error reported as success: %s", event.Raw)
				}
			}
			if !got.record.Failed {
				t.Fatalf("buffered error was reported as success: %+v", got.record)
			}
			if tc.wantUsage && (got.record.Detail.InputTokens != 100 || got.record.Detail.OutputTokens != 10 || got.record.Detail.TotalTokens != 110) {
				t.Fatalf("usage before error lost: %+v", got.record.Detail)
			}
		})
	}
}

func TestAIStudioResponsesPendingErrorWithoutWaiting(t *testing.T) {
	const finish = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"
	const usage = "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n"
	for _, tc := range []struct {
		name, tail string
		wantError  bool
		wantUsage  bool
	}{
		{name: "error_field", tail: "event: error\n", wantError: true},
		{name: "usage_before_error_field", tail: usage + "event: error\n", wantError: true, wantUsage: true},
		{name: "error_overridden", tail: "event: error\nevent: message\n"},
		{name: "last_field_error", tail: "event: message\nevent: error\n", wantError: true},
		{name: "unfinished_error_field", tail: "event: error"},
		{name: "unfinished_usage", tail: "data: {\"usageMetadata\":{\"promptTokenCount\":100}}\n"},
		{name: "done_then_error_field", tail: "data: [DONE]\n\nevent: error\n"},
		{name: "done_crlf_then_error_field", tail: "data: [DONE]\r\n\r\nevent: error\n"},
		{name: "error_before_done", tail: "event: error\n\ndata: [DONE]\n\n", wantError: true},
		{name: "error_overridden_before_done", tail: "event: error\nevent: message\n\ndata: [DONE]\n\n"},
		{name: "done_error_overridden", tail: "event: error\nevent: message\ndata: [DONE]\n\nevent: error\n"},
		{name: "done_error_final", tail: "event: message\nevent: error\ndata: [DONE]\n\n", wantError: true},
		{name: "done_in_error_event", tail: "event: error\ndata: [DONE]\n\n", wantError: true},
		{name: "not_done", tail: "data: [DONE]x\n\nevent: error\n", wantError: true},
		{name: "done_with_extra_data", tail: "data: [DONE]\ndata: {\"error\":{\"code\":429}}\n\n", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sendAIStudioBuffered(t, finish+tc.tail)
			if tc.wantError {
				var coder interface{ StatusCode() int }
				if !errors.As(got.err, &coder) || coder.StatusCode() != http.StatusBadGateway || !got.record.Failed {
					t.Fatalf("error = %v, failed=%t; want bad gateway failure", got.err, got.record.Failed)
				}
			} else if got.err != nil || got.record.Failed {
				t.Fatalf("error = %v, failed=%t; want successful completion", got.err, got.record.Failed)
			}
			if tc.wantUsage && (got.record.Detail.InputTokens != 100 || got.record.Detail.OutputTokens != 10 || got.record.Detail.TotalTokens != 110) {
				t.Fatalf("usage before pending error lost: %+v", got.record.Detail)
			}
			var terminals int
			for _, event := range got.events {
				if event.Get("type").String() == "response.completed" {
					terminals++
					if event.Get("response.usage").Exists() {
						t.Fatalf("incomplete or unavailable usage was fabricated: %s", event.Raw)
					}
				}
			}
			if tc.wantError && terminals != 0 || !tc.wantError && terminals != 1 {
				t.Fatalf("terminal count = %d, wantError=%t", terminals, tc.wantError)
			}
		})
	}
}

func TestAIStudioResponsesBufferedUsageWhenFinishSpansMessages(t *testing.T) {
	got := sendAIStudioBufferedChunks(t, []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]},",
		"\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n",
	})
	if got.err != nil || got.record.Failed || got.record.Detail.InputTokens != 100 || got.record.Detail.OutputTokens != 10 || got.record.Detail.TotalTokens != 110 {
		t.Fatalf("error = %v, record = %+v", got.err, got.record)
	}
	var terminals int
	for _, event := range got.events {
		if event.Get("type").String() == "response.completed" {
			terminals++
			u := event.Get("response.usage")
			if u.Get("input_tokens").Int() != 100 || u.Get("output_tokens").Int() != 10 || u.Get("total_tokens").Int() != 110 {
				t.Fatalf("terminal usage = %s", u.Raw)
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("terminals = %d, want 1", terminals)
	}
}

func TestAIStudioResponsesRejectsErrorOnFinishEvent(t *testing.T) {
	const data = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n"
	for _, tc := range []struct {
		name, frame string
		status      int
	}{
		{name: "event_before_data", frame: "event: error\n" + data + "\n", status: 502},
		{name: "event_after_data", frame: data + "event: error\n\n", status: 502},
		{name: "event_error_with_status", frame: "event: error\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"error\":{\"code\":429}}\n\n", status: 429},
		{name: "extra_invalid_data", frame: data + "data: {\"error\":{\"code\":429}}\n\n", status: 502},
		{name: "duplicate_key", frame: "data: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"error\":null,\"error\":{\"code\":429}}\n\n", status: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sendAIStudioBuffered(t, tc.frame)
			var coder interface{ StatusCode() int }
			if !errors.As(got.err, &coder) || coder.StatusCode() != tc.status || !got.record.Failed {
				t.Fatalf("error = %v, failed=%t; want status %d", got.err, got.record.Failed, tc.status)
			}
			for _, event := range got.events {
				if event.Get("type").String() == "response.completed" || event.Get("type").String() == "response.incomplete" {
					t.Fatalf("invalid finish event was reported as success: %s", event.Raw)
				}
			}
		})
	}
}

type aiStudioNestedUsageKey struct{}
type aiStudioNestedUsageHooks struct{ observed chan gjson.Result }

func (*aiStudioNestedUsageHooks) NormalizeRequest(_ context.Context, _, _ sdktranslator.Format, _ string, body []byte, _ bool) []byte {
	return body
}
func (*aiStudioNestedUsageHooks) TranslateRequest(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, bool) ([]byte, bool) {
	return nil, false
}
func (h *aiStudioNestedUsageHooks) NormalizeResponseBefore(ctx context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	if ctx.Value(aiStudioNestedUsageKey{}) != nil || gjson.GetBytes(helps.JSONPayload(body), "candidates.0.finishReason").String() == "" {
		return body
	}
	var state any
	nested := []byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)
	for _, chunk := range sdktranslator.TranslateStream(context.WithValue(ctx, aiStudioNestedUsageKey{}, true), sdktranslator.FormatGemini, sdktranslator.FormatOpenAIResponse, "gemini-3.1-pro-preview", nil, nil, nested, &state) {
		for _, event := range issue6258Events(chunk) {
			if event.Get("type").String() == "response.completed" {
				h.observed <- event.Get("response.usage")
			}
		}
	}
	return body
}
func (*aiStudioNestedUsageHooks) TranslateResponse(context.Context, sdktranslator.Format, sdktranslator.Format, string, []byte, []byte, []byte, bool) ([]byte, bool) {
	return nil, false
}
func (*aiStudioNestedUsageHooks) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	return body
}

func TestAIStudioBufferedUsageDoesNotLeakIntoNestedPlugin(t *testing.T) {
	hooks := &aiStudioNestedUsageHooks{observed: make(chan gjson.Result, 1)}
	sdktranslator.SetPluginHooks(hooks)
	t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
	got := sendAIStudioBuffered(t, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"+"data: {\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"totalTokenCount\":110}}\n\n")
	if got.err != nil {
		t.Fatal(got.err)
	}
	select {
	case nested := <-hooks.observed:
		if nested.Get("input_tokens").Int() != 1 || nested.Get("output_tokens").Int() != 2 || nested.Get("total_tokens").Int() != 3 {
			t.Fatalf("nested usage = %s", nested.Raw)
		}
	default:
		t.Fatal("nested plugin did not translate")
	}
	for _, event := range got.events {
		if event.Get("type").String() == "response.completed" {
			usage := event.Get("response.usage")
			if usage.Get("input_tokens").Int() == 100 && usage.Get("output_tokens").Int() == 10 && usage.Get("total_tokens").Int() == 110 {
				return
			}
			t.Fatalf("outer usage = %s", usage.Raw)
		}
	}
	t.Fatal("missing outer terminal")
}
