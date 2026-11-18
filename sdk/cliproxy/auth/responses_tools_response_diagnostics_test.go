package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestResponsesToolsBufferedMalformedCustomArgumentsDiagnostic(t *testing.T) {
	mgr := responsesToolsTestManagerWithModes("native", "function")
	body := []byte(`{"tools":[{"type":"custom","name":"exec","format":{"type":"text"}}],"input":[]}`)
	var observed []responsestools.ResponseFailure
	ctx := responsestools.WithResponseFailureObserver(context.Background(), func(event responsestools.ResponseFailure) { observed = append(observed, event) })
	_, err := mgr.responsesToolsCallToFormat(ctx, nil, "meta", sdktranslator.FormatOpenAIResponse,
		cliproxyexecutor.Request{Model: "fixture", Payload: body}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse},
		func(_ context.Context, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
			var value struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(req.Payload, &value); err != nil || len(value.Tools) != 1 {
				t.Fatalf("invalid normalized declaration: %s", req.Payload)
			}
			output := map[string]any{"output": []any{map[string]any{"type": "function_call", "id": "fc_1", "call_id": "c_1", "name": value.Tools[0].Name, "arguments": "{}"}}}
			raw, _ := json.Marshal(output)
			return cliproxyexecutor.Response{Payload: raw}, nil
		})
	var compat *responsestools.ToolCompatibilityError
	if !errors.As(err, &compat) || compat.Reason != responsestools.ReasonUpstreamContract || len(observed) != 1 || observed[0].Phase != "response_restore" {
		t.Fatalf("response boundary lost: events=%+v err=%v", observed, err)
	}
}

type diagnosticStreamAttempt struct {
	phase   string
	failure error
	closed  bool
}

type blockedDiagnosticAttempt struct {
	diagnosticStreamAttempt
	entered chan struct{}
	release chan struct{}
}

func (a *blockedDiagnosticAttempt) Feed([]byte) ([][]byte, error) {
	close(a.entered)
	<-a.release
	return nil, a.failure
}

func TestResponsesToolsCanceledInFlightConversionDoesNotReportLate(t *testing.T) {
	ctx, cancelConsumer := context.WithCancel(context.Background())
	observed := 0
	ctx = responsestools.WithResponseFailureObserver(ctx, func(responsestools.ResponseFailure) { observed++ })
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	attempt := &blockedDiagnosticAttempt{diagnosticStreamAttempt: diagnosticStreamAttempt{
		failure: &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract}},
		entered: make(chan struct{}), release: make(chan struct{})}
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`{"type":"response.output_item.done"}`)}
	close(chunks)
	result := adaptResponsesToolsStream(ctx, attemptCtx, cancelAttempt, attempt, &cliproxyexecutor.StreamResult{Chunks: chunks}, false)
	<-attempt.entered
	cancelConsumer()
	close(attempt.release)
	for chunk := range result.Chunks {
		if len(chunk.Payload) != 0 || chunk.Err == nil {
			t.Fatal("canceled conversion manufactured a successful frame")
		}
	}
	if observed != 0 || !attempt.closed || attemptCtx.Err() != context.Canceled {
		t.Fatal("canceled conversion reported late or failed to release")
	}
}

func (a *diagnosticStreamAttempt) Feed([]byte) ([][]byte, error) {
	if a.phase == "stream_feed" {
		return nil, a.failure
	}
	return nil, nil
}
func (a *diagnosticStreamAttempt) Finish() ([][]byte, error) { return nil, a.failure }
func (a *diagnosticStreamAttempt) Close()                    { a.closed = true }

func TestResponsesToolsStreamDiagnosticsPreserveErrorAndRelease(t *testing.T) {
	for _, phase := range []string{"stream_feed", "stream_finish", "upstream"} {
		t.Run(phase, func(t *testing.T) {
			var observed []responsestools.ResponseFailure
			ctx := responsestools.WithResponseFailureObserver(context.Background(), func(event responsestools.ResponseFailure) { observed = append(observed, event) })
			attemptCtx, cancel := context.WithCancel(ctx)
			failure := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract, Err: errors.New("private arguments")}
			attempt := &diagnosticStreamAttempt{phase: phase, failure: failure}
			chunks := make(chan cliproxyexecutor.StreamChunk, 1)
			if phase == "stream_feed" {
				chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(`{"type":"response.output_item.done"}`)}
			}
			if phase == "upstream" {
				chunks <- cliproxyexecutor.StreamChunk{Err: failure}
			}
			close(chunks)
			result := adaptResponsesToolsStream(ctx, attemptCtx, cancel, attempt, &cliproxyexecutor.StreamResult{Chunks: chunks}, false)
			count := 0
			for chunk := range result.Chunks {
				count++
				if chunk.Err != failure || len(chunk.Payload) != 0 {
					t.Fatal("error changed or fake completion emitted")
				}
			}
			expected := 1
			if phase == "upstream" {
				expected = 0
			}
			if count != 1 || len(observed) != expected || expected == 1 && observed[0].Phase != phase || !attempt.closed || attemptCtx.Err() != context.Canceled {
				t.Fatalf("stream boundary not settled: phase=%s observed=%+v count=%d", phase, observed, count)
			}
		})
	}
}
