package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type cancellationObservedAttempt struct {
	*responsestools.Attempt
	fed         chan struct{}
	finishCalls int
}

func (a *cancellationObservedAttempt) Feed(payload []byte) ([][]byte, error) {
	frames, err := a.Attempt.Feed(payload)
	close(a.fed)
	return frames, err
}

func (a *cancellationObservedAttempt) Finish() ([][]byte, error) {
	a.finishCalls++
	return a.Attempt.Finish()
}

func TestResponsesToolsCancellationReleasesRealRetainedState(t *testing.T) {
	for _, mode := range []string{"waiting_input", "blocked_output", "exposed_call"} {
		t.Run(mode, func(t *testing.T) {
			limits := responsestools.DefaultLimits()
			limiter := responsestools.NewLimiter(limits)
			policy := responsestools.ConventionPolicy(responsestools.Route{Provider: "gemini", UpstreamFormat: "gemini"})
			prepared, err := responsestools.Prepare([]byte(`{"tools":[{"type":"tool_search"}],"input":[]}`), policy, limits, limiter)
			if err != nil || prepared.Attempt == nil {
				t.Fatalf("prepare: %v", err)
			}
			defer prepared.Attempt.Close()
			_, initialBytes := limiter.Usage()
			for _, payload := range []string{
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_search","name":"tool_search","call_id":"s","arguments":""}}`,
				`{"type":"response.function_call_arguments.delta","item_id":"fc_search","output_index":0,"delta":"{\"query\":\"partial"}`,
			} {
				if _, err := prepared.Attempt.Feed([]byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			if attempts, bytes := limiter.Usage(); attempts != 1 || bytes <= initialBytes {
				t.Fatalf("fixture did not retain partial call state: %d/%d initial=%d", attempts, bytes, initialBytes)
			}
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			attemptCtx, cancelAttempt := context.WithCancel(parent)
			defer cancelAttempt()
			upstream := make(chan cliproxyexecutor.StreamChunk, 1)
			observed := &cancellationObservedAttempt{Attempt: prepared.Attempt, fed: make(chan struct{})}
			result := adaptResponsesToolsStream(parent, attemptCtx, cancelAttempt, observed, &cliproxyexecutor.StreamResult{Chunks: upstream}, false)
			// The deadline is only a deadlock watchdog; ordering uses channels.
			watchdog := time.NewTimer(5 * time.Second)
			defer watchdog.Stop()
			if mode != "waiting_input" {
				upstream <- cliproxyexecutor.StreamChunk{Payload: []byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_visible","name":"ordinary","call_id":"v","arguments":""}}`)}
				select {
				case <-observed.fed:
				case <-watchdog.C:
					t.Fatal("adapter did not process the input frame")
				}
			}
			if mode == "exposed_call" {
				select {
				case chunk := <-result.Chunks:
					if chunk.Err != nil || !strings.Contains(string(chunk.Payload), "function_call") {
						t.Fatalf("call was not exposed: %+v", chunk)
					}
				case <-watchdog.C:
					t.Fatal("adapter did not expose the call")
				}
			}
			cancelParent()
			for {
				select {
				case chunk, ok := <-result.Chunks:
					if !ok {
						if observed.finishCalls != 0 {
							t.Fatal("cancellation invoked terminal synthesis")
						}
						if attempts, bytes := limiter.Usage(); attempts != 0 || bytes != 0 {
							t.Fatalf("cancellation retained reservations: %d/%d", attempts, bytes)
						}
						if attemptCtx.Err() == nil {
							t.Fatal("upstream context was not cancelled")
						}
						return
					}
					if strings.Contains(string(chunk.Payload), "response.completed") || strings.Contains(string(chunk.Payload), "[DONE]") {
						t.Fatal("cancellation fabricated a terminal event")
					}
				case <-watchdog.C:
					t.Fatal("cancelled adapter failed to release output")
				}
			}
		})
	}
}
