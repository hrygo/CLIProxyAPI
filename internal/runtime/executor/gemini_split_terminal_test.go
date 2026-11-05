package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	internalcache "github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/wsrelay"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

type geminiSplitTerminalHooks struct {
	*geminiFailureSignalHooks
	failed   bool
	withhold bool
}

func (h *geminiSplitTerminalHooks) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	if bytes.Contains(body, []byte("event: response.output_item.done\n")) && bytes.Contains(body, []byte(`"type":"function_call"`)) {
		if h.withhold {
			return append(bytes.Clone(body), []byte("event: response.completed\ndata: {\"type\":\"response.completed\",")...)
		}
		return append(bytes.Clone(body), []byte("event: response.completed\ndata: {\"type\":\"response.")...)
	}
	if bytes.Contains(body, []byte("event: response.completed\n")) {
		if h.withhold {
			return nil
		}
		if h.failed {
			return []byte("failed\",\ndata: \"response\":{\"error\":{\"message\":\"bad\"}}}\n\n")
		}
		return []byte("completed\",\ndata: \"response\":{\"status\":\"completed\"}}\n\n")
	}
	return body
}

func TestGeminiResponsesSplitTerminal(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex_api_key", "vertex_service_account", "aistudio", "antigravity"} {
		for _, tc := range []struct {
			name        string
			failed      bool
			deliveryErr error
		}{
			{name: "completed"},
			{name: "failed", failed: true},
			{name: "write_failed", deliveryErr: io.ErrClosedPipe},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				sdktranslator.SetPluginHooks(&geminiSplitTerminalHooks{geminiFailureSignalHooks: &geminiFailureSignalHooks{}, failed: tc.failed})
				t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
				release := make(chan struct{})
				defer close(release)
				frame := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{"file_path":"/a"},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"totalTokenCount":15}}`
				if provider == "antigravity" {
					frame = `{"response":` + frame + `}`
				}
				var baseCtx context.Context
				var executor issue6258StreamExecutor
				var auth *cliproxyauth.Auth
				if provider == "aistudio" {
					connected := make(chan struct{})
					relay := wsrelay.NewManager(wsrelay.Options{ProviderFactory: func(*http.Request) (string, error) { return t.Name(), nil }, OnConnected: func(string) { close(connected) }})
					server := httptest.NewServer(relay.Handler())
					t.Cleanup(server.Close)
					t.Cleanup(func() { _ = relay.Stop(context.Background()) })
					conn, _, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+relay.Path(), nil)
					if errDial != nil {
						t.Fatal(errDial)
					}
					t.Cleanup(func() { _ = conn.Close() })
					<-connected
					go func() {
						var request wsrelay.Message
						if conn.ReadJSON(&request) != nil {
							return
						}
						if conn.WriteJSON(wsrelay.Message{ID: request.ID, Type: wsrelay.MessageTypeStreamStart, Payload: map[string]any{"status": 200}}) != nil {
							return
						}
						_ = conn.WriteJSON(wsrelay.Message{ID: request.ID, Type: wsrelay.MessageTypeStreamChunk, Payload: map[string]any{"data": "data: " + frame + "\n\n"}})
						<-release
					}()
					baseCtx, executor, auth = context.Background(), NewAIStudioExecutor(&config.Config{}, "aistudio", relay), &cliproxyauth.Auth{ID: t.Name(), Provider: "aistudio"}
				} else {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/token" {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: "+frame+"\n\n")
						w.(http.Flusher).Flush()
						<-release
					}))
					t.Cleanup(server.Close)
					baseCtx, executor, auth = issue6258Executor(t, provider, server.URL)
				}
				capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
				usage.RegisterNamedPlugin(t.Name(), capture)
				t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
				ctx, acknowledge := usage.WithStreamDelivery(baseCtx)
				defer acknowledge(context.Canceled)
				model := "gemini-3-flash-agent"
				session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
				key := "responses:" + session
				request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
				stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
					SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
				})
				if errExecute != nil {
					t.Fatalf("ExecuteStream() error = %v", errExecute)
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
						if provider == "antigravity" && bytes.HasPrefix(chunk.Payload, []byte("completed\",\n")) {
							items, found := internalcache.GetAntigravityReasoningReplayItems(model, key)
							if !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
								t.Fatalf("successful terminal received before replay was committed: %q", items)
							}
						}
					case <-timer.C:
						t.Fatalf("reassembled terminal stream did not close: %s", output.String())
					}
				}
				want := "response.completed"
				if tc.failed {
					want = "response.failed"
				}
				if !bytes.Contains(output.Bytes(), []byte(`"type":"`+want+`"`)) {
					t.Fatalf("missing split terminal %s in %s", want, output.String())
				}
				if provider == "antigravity" {
					if items, found := internalcache.GetAntigravityReasoningReplayItems(model, key); !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
						t.Fatalf("completed upstream replay missing after terminal: %q", items)
					}
				}
				acknowledge(tc.deliveryErr)
				select {
				case record := <-capture.records:
					if record.Failed != (tc.failed || tc.deliveryErr != nil) || record.Detail.InputTokens != 11 || record.Detail.OutputTokens != 4 {
						t.Fatalf("usage record = %+v", record)
					}
					if tc.deliveryErr != nil && !strings.Contains(record.Fail.Body, tc.deliveryErr.Error()) {
						t.Fatalf("usage failure = %q, want write error %q", record.Fail.Body, tc.deliveryErr)
					}
				case <-timer.C:
					t.Fatal("usage was not recorded")
				}
			})
		}
	}
}

func TestGeminiResponsesClassifierSplitAndLimit(t *testing.T) {
	frame := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"error\":{\"message\":\"bad\"}}}\n\n")
	for split := 1; split < len(frame); split++ {
		var detector geminiResponsesStreamClassifier
		first, errFirst := detector.Feed(frame[:split], false)
		if errFirst != nil {
			t.Fatalf("split %d: first feed: %v", split, errFirst)
		}
		second, errSecond := detector.Feed(frame[split:], true)
		if errSecond != nil {
			t.Fatalf("split %d: second feed: %v", split, errSecond)
		}
		if first != "response.failed" && second != "response.failed" {
			t.Fatalf("split %d: events %q, %q; want failure", split, first, second)
		}
		if first == "response.completed" || second == "response.completed" {
			t.Fatalf("split %d: failed event classified as success", split)
		}
	}

	auth := &cliproxyauth.Auth{ID: t.Name()}
	capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 1)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
	ctx := context.Background()
	reporter := helps.NewExecutorUsageReporter(ctx, NewGeminiExecutor(&config.Config{}), "gemini-test", auth)
	var streamUsage helps.StreamUsageBuffer
	detector := geminiResponsesStreamClassifier{maxFrame: 64}
	out := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks := [][]byte{[]byte("event: response.completed\ndata: {\"type\":\""), []byte(strings.Repeat("a", 64))}
	if _, ok := classifyGeminiStreamTranslation(ctx, sdktranslator.FormatOpenAIResponse, chunks, &detector, nil, reporter, &streamUsage, out); ok {
		t.Fatal("oversized frame was accepted")
	}
	select {
	case chunk := <-out:
		if chunk.Err == nil || !strings.Contains(chunk.Err.Error(), "size limit") {
			t.Fatalf("overflow consumer error = %v", chunk.Err)
		}
	default:
		t.Fatal("overflow did not reach the consumer")
	}
	select {
	case record := <-capture.records:
		if !record.Failed || !strings.Contains(record.Fail.Body, "size limit") {
			t.Fatalf("overflow usage record = %+v", record)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow was not recorded as failure")
	}
}

func TestAntigravityCancelDuringSplitTerminalKeepsCompleteUpstreamReplay(t *testing.T) {
	sdktranslator.SetPluginHooks(&geminiSplitTerminalHooks{geminiFailureSignalHooks: &geminiFailureSignalHooks{}, withhold: true})
	t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}]}}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	defer close(release)
	baseCtx, executor, auth := issue6258Executor(t, "antigravity", server.URL)
	capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
	deliveryCtx, acknowledge := usage.WithStreamDelivery(baseCtx)
	ctx, cancel := context.WithCancel(deliveryCtx)
	defer cancel()
	defer acknowledge(context.Canceled)
	model := "gemini-3-flash-agent"
	session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
	request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
	stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
	})
	if errExecute != nil {
		t.Fatal(errExecute)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	seenPrefix := false
	for !seenPrefix {
		select {
		case chunk, ok := <-stream.Chunks:
			if !ok {
				t.Fatal("stream closed before partial terminal")
			}
			seenPrefix = bytes.Contains(chunk.Payload, []byte("event: response.completed\ndata: {\"type\":\"response.completed\","))
		case <-timer.C:
			t.Fatal("partial terminal was not received")
		}
	}
	cancel()
	acknowledge(context.Canceled)
	for {
		select {
		case _, ok := <-stream.Chunks:
			if !ok {
				goto closed
			}
		case <-timer.C:
			t.Fatal("stream did not close after cancellation")
		}
	}
closed:
	if items, found := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session); !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
		t.Fatalf("canceled downstream lost completed upstream replay: %q", items)
	}
	select {
	case record := <-capture.records:
		if !record.Failed {
			t.Fatalf("cancellation reported success: %+v", record)
		}
	case <-timer.C:
		t.Fatal("missing cancellation usage record")
	}
}

func TestGeminiResponsesPlainContextCanceledDuringPartialTerminal(t *testing.T) {
	for _, provider := range []string{"gemini", "vertex_api_key", "vertex_service_account", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			sdktranslator.SetPluginHooks(&geminiSplitTerminalHooks{geminiFailureSignalHooks: &geminiFailureSignalHooks{}, withhold: true})
			t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"access_token":"local-token","token_type":"Bearer","expires_in":3600}`)
					return
				}
				frame := `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}]}`
				if provider == "antigravity" {
					frame = `{"response":` + frame + `}`
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+frame+"\n\n")
				w.(http.Flusher).Flush()
				<-release
			}))
			t.Cleanup(server.Close)
			defer close(release)
			baseCtx, executor, auth := issue6258Executor(t, provider, server.URL)
			ctx, cancel := context.WithCancel(baseCtx)
			defer cancel()
			capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
			usage.RegisterNamedPlugin(t.Name(), capture)
			t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
			model := "gemini-3-flash-agent"
			session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
			request := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
			stream, errExecute := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: request}, cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: request, Stream: true,
			})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			seenPrefix, seenEmpty := false, false
			for !seenEmpty {
				select {
				case chunk, ok := <-stream.Chunks:
					if !ok {
						t.Fatal("stream closed before cancellation")
					}
					if bytes.Contains(chunk.Payload, []byte("event: response.completed\ndata: {\"type\":\"response.completed\",")) {
						seenPrefix = true
					} else if seenPrefix && len(chunk.Payload) == 0 {
						seenEmpty = true
					}
				case <-timer.C:
					t.Fatal("did not receive partial terminal")
				}
			}
			cancel()
			for {
				select {
				case _, ok := <-stream.Chunks:
					if !ok {
						goto closed
					}
				case <-timer.C:
					t.Fatal("stream did not close after cancellation")
				}
			}
		closed:
			if provider == "antigravity" {
				if items, found := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session); !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
					t.Fatalf("canceled downstream lost completed upstream replay: %q", items)
				}
			}
			select {
			case record := <-capture.records:
				if !record.Failed {
					t.Fatalf("cancellation reported success: %+v", record)
				}
			case <-timer.C:
				t.Fatal("missing cancellation usage record")
			}
		})
	}
}

type geminiBlankSuffixHooks struct{ *geminiFailureSignalHooks }

func (h *geminiBlankSuffixHooks) NormalizeResponseAfter(_ context.Context, _, _ sdktranslator.Format, _ string, _, _, body []byte, _ bool) []byte {
	if bytes.Contains(body, []byte("event: response.output_item.done\n")) && bytes.Contains(body, []byte(`"type":"function_call"`)) {
		return append(bytes.Clone(body), []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}")...)
	}
	if bytes.Contains(body, []byte("event: response.completed\n")) {
		return []byte("\n\n")
	}
	return body
}

func TestGeminiTerminalBlankSuffixPreserved(t *testing.T) {
	sdktranslator.SetPluginHooks(&geminiBlankSuffixHooks{&geminiFailureSignalHooks{}})
	t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"}}]},"finishReason":"STOP"}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	defer close(release)
	baseCtx, executor, auth := issue6258Executor(t, "gemini", server.URL)
	req := []byte(`{"model":"gemini-3-flash-agent","input":"hi","stream":true}`)
	stream, err := executor.ExecuteStream(baseCtx, auth, cliproxyexecutor.Request{Model: "gemini-3-flash-agent", Payload: req}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: req, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-stream.Chunks:
			if !ok {
				terminal := []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}")
				i := bytes.Index(output.Bytes(), terminal)
				if i < 0 {
					t.Fatalf("missing terminal: %q", output.String())
				}
				suffix := output.Bytes()[i+len(terminal):]
				if !bytes.HasPrefix(suffix, []byte("\n\n")) {
					t.Fatalf("missing existing SSE terminating blank line; suffix=%q", suffix)
				}
				return
			}
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
			output.Write(chunk.Payload)
		case <-timer.C:
			t.Fatal("stream did not close")
		}
	}
}

func TestAntigravityCancelBeforeCompleteTerminalKeepsCompleteUpstreamReplay(t *testing.T) {
	sdktranslator.SetPluginHooks(&geminiSplitTerminalHooks{geminiFailureSignalHooks: &geminiFailureSignalHooks{}})
	t.Cleanup(func() { sdktranslator.SetPluginHooks(nil) })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"Read","args":{},"id":"id1"},"thoughtSignature":"sig-first"}]},"finishReason":"STOP"}]}}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	defer close(release)
	baseCtx, executor, auth := issue6258Executor(t, "antigravity", server.URL)
	capture := &geminiResponsesDeliveryCapture{authID: auth.ID, records: make(chan usage.Record, 2)}
	usage.RegisterNamedPlugin(t.Name(), capture)
	t.Cleanup(func() { usage.RegisterNamedPlugin(t.Name(), geminiTerminalDisconnectUsageNoop{}) })
	deliveryCtx, acknowledge := usage.WithStreamDelivery(baseCtx)
	ctx, cancel := context.WithCancel(deliveryCtx)
	defer cancel()
	defer acknowledge(context.Canceled)
	model := "gemini-3-flash-agent"
	session := fmt.Sprintf("%s-%d", t.Name(), antigravityReplayFailureSequence.Add(1))
	req := []byte(fmt.Sprintf(`{"model":%q,"session_id":%q,"input":"hi","stream":true}`, model, session))
	stream, err := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: model, Payload: req}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: req, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-stream.Chunks:
			if !ok {
				t.Fatal("stream closed before partial terminal")
			}
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
			if bytes.Contains(chunk.Payload, []byte("event: response.completed\ndata: {\"type\":\"response.")) {
				goto received
			}
		case <-timer.C:
			t.Fatal("partial terminal was not received")
		}
	}
received:
	cancel()
	acknowledge(context.Canceled)
	select {
	case record := <-capture.records:
		if !record.Failed {
			t.Fatalf("cancellation reported success: %+v", record)
		}
	case <-timer.C:
		t.Fatal("producer did not stop without another downstream receiver")
	}
	if chunk, ok := <-stream.Chunks; ok {
		t.Fatalf("unexpected post-cancel chunk: %q", chunk.Payload)
	}
	if items, found := internalcache.GetAntigravityReasoningReplayItems(model, "responses:"+session); !found || len(items) == 0 || !bytes.Contains(items[0], []byte("sig-first")) {
		t.Fatalf("canceled downstream lost completed upstream replay: %q", items)
	}
}
