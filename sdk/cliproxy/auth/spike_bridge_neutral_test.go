package auth

import (
	"context"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type spikeBridgeErrorExecutor struct {
	mockCustomErrorExecutor
	calls     int
	bootstrap bool
}

func (e *spikeBridgeErrorExecutor) Execute(context.Context, *Auth, core.Request, core.Options) (core.Response, error) {
	e.calls++
	return core.Response{}, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract, Err: fmt.Errorf("spike-bridge-fault")}
}
func (e *spikeBridgeErrorExecutor) CountTokens(context.Context, *Auth, core.Request, core.Options) (core.Response, error) {
	e.calls++
	return core.Response{}, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract, Err: fmt.Errorf("spike-bridge-fault")}
}
func (e *spikeBridgeErrorExecutor) ExecuteStream(context.Context, *Auth, core.Request, core.Options) (*core.StreamResult, error) {
	e.calls++
	err := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract, Err: fmt.Errorf("spike-bridge-fault")}
	if e.bootstrap {
		ch := make(chan core.StreamChunk, 1)
		ch <- core.StreamChunk{Err: err}
		close(ch)
		return &core.StreamResult{Chunks: ch}, nil
	}
	return nil, err
}
func TestSpikeBridgeErrorOverridesCredentialRules(t *testing.T) {
	for _, action := range []string{"continue", "continue-and-cooldown", "stop-and-cooldown"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", action, stream), func(t *testing.T) {
				m := NewManager(nil, nil, nil)
				m.SetRetryConfig(1, 0, 0)
				id1 := "spike-bridge-" + action + fmt.Sprint(stream) + "-1"
				id2 := id1 + "-2"
				for i, id := range []string{id1, id2} {
					a := &Auth{ID: id, Provider: "claude", Status: StatusActive, Attributes: map[string]string{"priority": fmt.Sprint(10 - i)}, Metadata: map[string]any{"request_scoped_errors": []config.RequestScopedErrorRule{{Status: 422, Match: []string{"spike-bridge-fault"}, Action: action}}}}
					registry.GetGlobalRegistry().RegisterClient(id, "claude", []*registry.ModelInfo{{ID: "spike-model"}})
					t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
					if _, err := m.Register(context.Background(), a); err != nil {
						t.Fatal(err)
					}
				}
				e := &spikeBridgeErrorExecutor{mockCustomErrorExecutor: mockCustomErrorExecutor{identifier: "claude"}}
				m.RegisterExecutor(e)
				var err error
				if stream {
					_, err = m.ExecuteStream(context.Background(), []string{"claude"}, core.Request{Model: "spike-model"}, core.Options{})
				} else {
					_, err = m.Execute(context.Background(), []string{"claude"}, core.Request{Model: "spike-model"}, core.Options{})
				}
				if !isResponsesToolsError(err) || e.calls != 1 {
					t.Fatalf("err=%v calls=%d; want bridge error and one call", err, e.calls)
				}
				a, _ := m.GetByID(id1)
				if a.Unavailable || !a.NextRetryAfter.IsZero() {
					t.Fatalf("credential unavailable: %+v", a)
				}
				if state := a.ModelStates["spike-model"]; state != nil && (state.Unavailable || !state.NextRetryAfter.IsZero()) {
					t.Fatalf("model unavailable: %+v", state)
				}
			})
		}
	}
}

func TestSpikeBridgeCountErrorOverridesCredentialRules(t *testing.T) {
	for _, action := range []string{"continue", "continue-and-cooldown", "stop-and-cooldown"} {
		t.Run(action, func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			m.SetRetryConfig(1, 0, 0)
			id1 := "spike-bridge-count-" + action + "-1"
			id2 := id1 + "-2"
			for i, id := range []string{id1, id2} {
				a := &Auth{ID: id, Provider: "claude", Status: StatusActive, Attributes: map[string]string{"priority": fmt.Sprint(10 - i)}, Metadata: map[string]any{"request_scoped_errors": []config.RequestScopedErrorRule{{Status: 422, Match: []string{"spike-bridge-fault"}, Action: action}}}}
				registry.GetGlobalRegistry().RegisterClient(id, "claude", []*registry.ModelInfo{{ID: "spike-model"}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
				if _, err := m.Register(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			}
			e := &spikeBridgeErrorExecutor{mockCustomErrorExecutor: mockCustomErrorExecutor{identifier: "claude"}}
			m.RegisterExecutor(e)
			_, err := m.ExecuteCount(context.Background(), []string{"claude"}, core.Request{Model: "spike-model"}, core.Options{})
			if !isResponsesToolsError(err) || e.calls != 1 {
				t.Fatalf("err=%v calls=%d; want bridge error and one call", err, e.calls)
			}
			a, _ := m.GetByID(id1)
			if a.Unavailable || !a.NextRetryAfter.IsZero() {
				t.Fatalf("credential unavailable: %+v", a)
			}
			if state := a.ModelStates["spike-model"]; state != nil && (state.Unavailable || !state.NextRetryAfter.IsZero()) {
				t.Fatalf("model unavailable: %+v", state)
			}
		})
	}
}

type spikeScopedOnly struct{}

func (spikeScopedOnly) Error() string         { return "scoped only" }
func (spikeScopedOnly) IsRequestScoped() bool { return true }
func TestSpikeBridgeErrorRequiresAvailabilityNeutral(t *testing.T) {
	if isResponsesToolsError(spikeScopedOnly{}) {
		t.Fatal("ordinary request-scoped error treated as neutral bridge error")
	}
	if !isResponsesToolsError(fmt.Errorf("wrapped: %w", &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract})) {
		t.Fatal("wrapped bridge error was not recognized")
	}
}

func TestSpikeBridgeBootstrapErrorStopsAndRemainsNeutral(t *testing.T) {
	m := NewManager(nil, nil, nil)
	id := "spike-bridge-bootstrap"
	a := &Auth{ID: id, Provider: "claude", Status: StatusActive, Metadata: map[string]any{"request_scoped_errors": []config.RequestScopedErrorRule{{Status: 422, Match: []string{"spike-bridge-fault"}, Action: "continue-and-cooldown"}}}}
	registry.GetGlobalRegistry().RegisterClient(id, "claude", []*registry.ModelInfo{{ID: "spike-bootstrap-model"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
	if _, err := m.Register(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	e := &spikeBridgeErrorExecutor{mockCustomErrorExecutor: mockCustomErrorExecutor{identifier: "claude"}, bootstrap: true}
	m.RegisterExecutor(e)
	_, err := m.ExecuteStream(context.Background(), []string{"claude"}, core.Request{Model: "spike-bootstrap-model"}, core.Options{})
	if !isResponsesToolsError(err) || e.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, e.calls)
	}
	a, _ = m.GetByID(id)
	if a.Unavailable || !a.NextRetryAfter.IsZero() {
		t.Fatal("credential cooled down")
	}
	if state := a.ModelStates["spike-bootstrap-model"]; state != nil && state.Unavailable {
		t.Fatal("model cooled down")
	}
}
