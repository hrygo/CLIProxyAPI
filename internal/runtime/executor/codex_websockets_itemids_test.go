package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const (
	wsPoisonedSearchCallID = "fc_call_function_olq4gx3ow6cs_1"
	wsRepairedSearchCallID = "tsc_call_function_olq4gx3ow6cs_1"
)

func wsPollutedCreateFrame(extra string) []byte {
	return []byte(`{"type":"response.create","model":"gpt-6-astra"` + extra + `,"input":[` +
		`{"type":"tool_search_call","id":"` + wsPoisonedSearchCallID + `","call_id":"s1","arguments":{"query":"x"},"execution":"client"},` +
		`{"type":"tool_search_output","id":"tso_out_1","call_id":"s1","execution":"client","tools":[]}]}`)
}

// duplexSteeringConfig enables the successor-frame path a duplex connection
// uses for every create after the first response.
func duplexSteeringConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Codex.ResponseSteering = true
	cfg.CodexResponseSteering = true
	return cfg
}

// The real handler installs this preparation probe on duplex sessions.
func withItemIDReplayChecker(ctx context.Context) context.Context {
	return cliproxyexecutor.WithResponsesToolsReplayChecker(ctx, func(payload []byte, model, provider, _ string) bool {
		policy := responsestools.ConventionPolicy(responsestools.Route{Provider: provider, UpstreamModel: model, UpstreamFormat: "codex"})
		return responsestools.RequiresToolReplay(payload, policy, responsestools.DefaultLimits())
	})
}

// The prepared body is what the executor would actually write, so the repair
// has to be visible there and not only in the request object.
func TestCodexWebsocketPreparedItemIDRepair(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		enabled      bool
		wantWire     string
		wantOriginal string
	}{
		{"enabled", true, wsRepairedSearchCallID, wsRepairedSearchCallID},
		// Upstream's independent Codex ID sanitizer still prefixes this ID when
		// the responses-tools bridge is disabled.
		{"disabled", false, "tsc_" + wsPoisonedSearchCallID, wsPoisonedSearchCallID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &config.Config{}
			if !testCase.enabled {
				disabled := false
				cfg.ResponsesTools.Enabled = &disabled
			}
			exec := NewCodexWebsocketsExecutor(cfg)
			exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
			auth := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{
				"api_key": "test", "base_url": "https://example.invalid", "websockets": "true",
			}}
			payload := []byte(`{"model":"gpt-6-astra","input":[` +
				`{"type":"tool_search_call","id":"` + wsPoisonedSearchCallID + `","call_id":"s1","arguments":{"query":"x"},"execution":"client"}]}`)
			prepared, err := exec.prepareCodexWebsocketStream(context.Background(), auth,
				cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: payload},
				cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("codex")})
			if err != nil {
				t.Fatalf("prepare: %v", err)
			}
			if got := gjson.GetBytes(prepared.clientBody, "input.0.id").String(); got != testCase.wantWire {
				t.Fatalf("prepared id = %q, want %q", got, testCase.wantWire)
			}
			if got := gjson.GetBytes(prepared.originalPayload, "input.0.id").String(); got != testCase.wantOriginal {
				t.Fatalf("original payload id = %q, want %q", got, testCase.wantOriginal)
			}
			if !testCase.enabled {
				// The gate must not have rewritten the caller's slice either.
				if got := gjson.GetBytes(payload, "input.0.id").String(); got != wsPoisonedSearchCallID {
					t.Fatalf("caller payload was mutated: %s", payload)
				}
			}
		})
	}
}

func TestCodexWebsocketPreparedOpaqueItemIDRepairFeatureGate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.ResponsesTools.Enabled = &enabled
			exec := NewCodexWebsocketsExecutor(cfg)
			exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
			payload := wsPollutedCreateFrame(`,"previous_response_id":"r1"`)
			auth := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": "https://example.invalid"}}
			prepared, err := exec.prepareCodexWebsocketStream(context.Background(), auth,
				cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: payload},
				cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex})
			if enabled {
				var compat *responsestools.ToolCompatibilityError
				if !errors.As(err, &compat) || compat.StatusCode() != http.StatusUnprocessableEntity {
					t.Fatalf("opaque history = %v, want request-scoped 422", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("disabled bridge rejected opaque history: %v", err)
			}
			if got := gjson.GetBytes(prepared.originalPayload, "input.0.id").String(); got != wsPoisonedSearchCallID {
				t.Fatalf("disabled bridge changed original id: %q", got)
			}
		})
	}
}

func TestCodexWebsocketFinalizerItemIDRepair(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		opaque   bool
		wantID   string
		wantFail bool
	}{
		{"repair", true, false, wsRepairedSearchCallID, false},
		{"opaque", true, true, "", true},
		{"disabled", false, true, wsPoisonedSearchCallID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.ResponsesTools.Enabled = &tc.enabled
			cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-6-astra"}}, Params: map[string]any{"input.0.id": wsPoisonedSearchCallID}}}
			exec := NewCodexWebsocketsExecutor(cfg)
			payload := []byte(`{"model":"gpt-6-astra","input":[{"type":"tool_search_call","id":"` + wsRepairedSearchCallID + `","call_id":"s1","arguments":{},"execution":"client"}]}`)
			if tc.opaque {
				payload = []byte(`{"model":"gpt-6-astra","previous_response_id":"r1","input":[{"type":"tool_search_call","id":"` + wsRepairedSearchCallID + `","call_id":"s1","arguments":{},"execution":"client"}]}`)
			}
			auth := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": "https://example.invalid"}}
			prepared, err := exec.prepareCodexWebsocketStream(context.Background(), auth,
				cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: payload},
				cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex})
			if tc.wantFail {
				var compat *responsestools.ToolCompatibilityError
				if !errors.As(err, &compat) || compat.StatusCode() != http.StatusUnprocessableEntity {
					t.Fatalf("finalizer opaque history = %v, want 422", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(prepared.clientBody, "input.0.id").String(); got != tc.wantID {
				t.Fatalf("final id = %q, want %q", got, tc.wantID)
			}
		})
	}
}

// A healthy first frame followed by a polluted successor proves the repair
// runs on the second write, not only on the initial request.
func TestCodexDuplexCreateRepairsSuccessorItemIDs(t *testing.T) {
	successorBody := make(chan string, 1)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		read := func() []byte {
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				t.Errorf("upstream read: %v", errRead)
				return nil
			}
			return payload
		}
		first := read()
		if gjson.GetBytes(first, "type").String() != "response.create" {
			t.Errorf("initial frame: %s", first)
		}
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		// The connection accepts a successor create only once the previous
		// response is terminal, so the frame under test is the second write.
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.completed","response":{"id":"r1","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		successor := read()
		successorBody <- string(successor)
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.completed","response":{"id":"r2","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx = withItemIDReplayChecker(ctx)
	input := make(chan cliproxyexecutor.WebsocketInput, 4)
	ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
	exec := NewCodexWebsocketsExecutor(duplexSteeringConfig())
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	credential := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{
		"api_key": "test", "base_url": server.URL, "websockets": "true",
	}}
	req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[]}`)}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("codex"),
		Metadata:     map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()},
	}
	result, err := exec.ExecuteStream(ctx, credential, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	initialDone := false
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		kind := gjson.GetBytes(chunk.Payload, "type").String()
		id := gjson.GetBytes(chunk.Payload, "response.id").String()
		if kind == "response.completed" && id == "r1" && !initialDone {
			initialDone = true
			input <- cliproxyexecutor.WebsocketInput{Payload: wsPollutedCreateFrame("")}
		}
		if kind == "response.completed" && id == "r2" {
			cancel()
			break
		}
	}
	if !initialDone {
		t.Fatal("the initial response never completed")
	}
	body := <-successorBody
	if got := gjson.Get(body, "input.0.id").String(); got != wsRepairedSearchCallID {
		t.Fatalf("successor frame was not repaired: %s", body)
	}
	if bytes.Contains([]byte(body), []byte(wsPoisonedSearchCallID)) {
		t.Fatalf("successor frame still carries the polluted id: %s", body)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not close")
	}
}

// An append inherits its parent response, which makes the history opaque. A
// frame that would need repair is refused locally and never sent, because the
// proxy cannot know which stored item the parent holds.
func TestCodexDuplexAppendRejectsOpaqueItemIDRepair(t *testing.T) {
	var upstreamFrames atomic.Int32
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, first, _ := conn.ReadMessage()
		upstreamFrames.Add(1)
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.completed","response":{"id":"r1","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		// Only a healthy follow-up frame may reach the upstream.
		_, second, errRead := conn.ReadMessage()
		if errRead == nil {
			upstreamFrames.Add(1)
			if gjson.GetBytes(second, "type").String() != "response.create" ||
				gjson.GetBytes(second, "input.0.id").String() != "msg_1" {
				t.Errorf("unexpected follow-up frame: %s", second)
			}
			_ = conn.WriteMessage(websocket.TextMessage,
				[]byte(`{"type":"response.created","response":{"id":"r2","output":[]}}`))
			_ = conn.WriteMessage(websocket.TextMessage,
				[]byte(`{"type":"response.completed","response":{"id":"r2","output":[]}}`))
		}
		_, _, _ = conn.ReadMessage()
		_ = first
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx = withItemIDReplayChecker(ctx)
	input := make(chan cliproxyexecutor.WebsocketInput, 4)
	ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
	exec := NewCodexWebsocketsExecutor(duplexSteeringConfig())
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	credential := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{
		"api_key": "test", "base_url": server.URL, "websockets": "true",
	}}
	req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[]}`)}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("codex"),
		Metadata:     map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()},
	}
	result, err := exec.ExecuteStream(ctx, credential, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	sawRejection := false
	initialDone := false
	loopEnded := false
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		kind := gjson.GetBytes(chunk.Payload, "type").String()
		id := gjson.GetBytes(chunk.Payload, "response.id").String()
		switch {
		case kind == "response.completed" && id == "r1" && !initialDone:
			initialDone = true
			// An append inherits its parent response, which makes the replayed
			// history opaque, so this frame must be refused locally.
			input <- cliproxyexecutor.WebsocketInput{
				Payload: []byte(`{"type":"response.append","model":"gpt-6-astra","input":[` +
					`{"type":"tool_search_call","id":"` + wsPoisonedSearchCallID + `","call_id":"s1","arguments":{},"execution":"client"}]}`),
			}
			// A well-formed frame right after proves the refusal did not wedge
			// the connection or leave the frame in the pending queue.
			input <- cliproxyexecutor.WebsocketInput{
				Payload: []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","id":"msg_1","role":"user","content":"hi"}]}`),
			}
		case kind == "error":
			if got := gjson.GetBytes(chunk.Payload, "status").Int(); got != 422 {
				t.Fatalf("local rejection status = %d, want 422: %s", got, chunk.Payload)
			}
			sawRejection = true
		case kind == "response.completed" && id == "r2":
			cancel()
			loopEnded = true
		}
		if loopEnded {
			break
		}
	}
	if !initialDone || !sawRejection {
		t.Fatalf("initialDone=%t rejection=%t", initialDone, sawRejection)
	}
	if got := upstreamFrames.Load(); got != 2 {
		t.Fatalf("upstream saw %d frames, want the initial create and the healthy follow-up only", got)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not close")
	}
}

// A steer frame keeps its exact bytes when it needs no repair, and a polluted
// one is refused before it becomes steering state.
func TestCodexDuplexSteerItemIDRepairPreservesState(t *testing.T) {
	steerSeen := make(chan string, 2)
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		read := func() []byte {
			_, payload, errRead := conn.ReadMessage()
			if errRead != nil {
				t.Errorf("upstream read: %v", errRead)
				return nil
			}
			return payload
		}
		_ = read()
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		healthy := read()
		steerSeen <- string(healthy)
		if err := conn.WriteMessage(websocket.TextMessage,
			[]byte(`{"type":"response.steer.accepted","steer":{"id":"s1","previous_response_id":"r1"}}`)); err != nil {
			t.Errorf("upstream write: %v", err)
		}
		// The refused steer must never arrive. This read therefore blocks until
		// the client closes the connection after seeing the local rejection.
		if _, extra, errRead := conn.ReadMessage(); errRead == nil {
			steerSeen <- string(extra)
		}
	}))
	defer server.Close()

	healthySteer := `{"type":"response.steer","previous_response_id":"r1","input":[{"role":"user","content":[{"type":"input_text","text":"go on"}]}]}`
	pollutedSteer := `{"type":"response.steer","previous_response_id":"r1","input":[{"type":"tool_search_call","id":"` +
		wsPoisonedSearchCallID + `","call_id":"s1","arguments":{},"execution":"client"}]}`

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx = withItemIDReplayChecker(ctx)
	input := make(chan cliproxyexecutor.WebsocketInput, 4)
	ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
	exec := NewCodexWebsocketsExecutor(duplexSteeringConfig())
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	credential := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{
		"api_key": "test", "base_url": server.URL, "websockets": "true",
	}}
	req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[]}`)}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("codex"),
		Metadata:     map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()},
	}
	result, err := exec.ExecuteStream(ctx, credential, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	accepted, rejected := false, false
	for !rejected {
		select {
		case chunk, ok := <-result.Chunks:
			if !ok {
				t.Fatal("stream ended before the polluted steer was refused")
			}
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
			switch gjson.GetBytes(chunk.Payload, "type").String() {
			case "response.created":
				if !accepted {
					input <- cliproxyexecutor.WebsocketInput{Payload: []byte(healthySteer)}
				}
			case "response.steer.accepted":
				accepted = true
				input <- cliproxyexecutor.WebsocketInput{Payload: []byte(pollutedSteer)}
			case "error":
				if gjson.GetBytes(chunk.Payload, "status").Int() != 422 {
					t.Fatalf("polluted steer status = %s", chunk.Payload)
				}
				rejected = true
			}
		case <-ctx.Done():
			t.Fatal("the polluted steer was never refused")
		}
	}
	if !accepted || !rejected {
		t.Fatalf("accepted=%t rejected=%t", accepted, rejected)
	}
	if got := <-steerSeen; got != healthySteer {
		t.Fatalf("healthy steer was rewritten:\n got %s\nwant %s", got, healthySteer)
	}
	select {
	case extra := <-steerSeen:
		t.Fatalf("the refused steer reached the upstream: %s", extra)
	default:
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not close")
	}
}

// Disabling responses-tools must leave opaque controls to upstream, including
// steering, which bypasses the independent Codex create-body sanitizer.
func TestCodexDuplexDisabledItemIDRepair(t *testing.T) {
	for _, kind := range []string{"response.append", "response.steer"} {
		t.Run(kind, func(t *testing.T) {
			observed := make(chan []byte, 1)
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(serverDone)
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				if _, _, err := conn.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`)); err != nil {
					t.Error(err)
					return
				}
				if kind == "response.append" {
					if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"r1","output":[]}}`)); err != nil {
						t.Error(err)
						return
					}
				}
				_, payload, err := conn.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				observed <- payload
				response := `{"type":"response.steer.accepted","steer":{"id":"s1","previous_response_id":"r1"}}`
				if kind == "response.append" {
					response = `{"type":"response.completed","response":{"id":"r2","output":[]}}`
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
					t.Error(err)
					return
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			ctx = cliproxyexecutor.WithResponsesToolsReplayChecker(ctx, func([]byte, string, string, string) bool { return false })
			input := make(chan cliproxyexecutor.WebsocketInput, 1)
			ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
			cfg := duplexSteeringConfig()
			disabled := false
			cfg.ResponsesTools.Enabled = &disabled
			exec := NewCodexWebsocketsExecutor(cfg)
			exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
			auth := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": server.URL, "websockets": "true"}}
			req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[]}`)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()}}
			result, err := exec.ExecuteStream(ctx, auth, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			sent, finished := false, false
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				event := gjson.GetBytes(chunk.Payload, "type").String()
				if event == "error" {
					t.Fatalf("disabled bridge locally rejected control: %s", chunk.Payload)
				}
				if !sent && ((kind == "response.append" && event == "response.completed") || (kind == "response.steer" && event == "response.created")) {
					frame := `{"type":"` + kind + `","previous_response_id":"r1","input":[{"type":"tool_search_call","id":"` + wsPoisonedSearchCallID + `","call_id":"s1","arguments":{},"execution":"client"}]}`
					input <- cliproxyexecutor.WebsocketInput{Payload: []byte(frame)}
					sent = true
				} else if sent && (event == "response.steer.accepted" || (event == "response.completed" && gjson.GetBytes(chunk.Payload, "response.id").String() == "r2")) {
					finished = true
					break
				}
			}
			if !sent || !finished {
				t.Fatalf("sent=%t finished=%t", sent, finished)
			}
			payload := <-observed
			wantID := wsPoisonedSearchCallID
			if kind == "response.append" {
				wantID = "tsc_" + wantID // upstream create-body sanitizer
			}
			if got := gjson.GetBytes(payload, "input.0.id").String(); got != wantID {
				t.Fatalf("disabled %s upstream id = %q, want %q: %s", kind, got, wantID, payload)
			}
			cancel()
			select {
			case <-serverDone:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not close")
			}
		})
	}
}

// Concurrent input submissions and independent sessions must each receive the
// ID of the rejected client frame, even if an interceptor rewrites its body.
func TestCodexDuplexItemIDErrorEventCorrelation(t *testing.T) {
	finished := map[string]chan struct{}{"A": make(chan struct{}), "B": make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		_, first, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("initial read: %v", err)
			return
		}
		session := strings.TrimPrefix(gjson.GetBytes(first, "event_id").String(), "initial-")
		serverDone, ok := finished[session]
		if !ok {
			t.Errorf("unexpected initial session: %s", first)
			return
		}
		defer close(serverDone)
		for _, event := range []string{"response.created", "response.completed"} {
			response := fmt.Sprintf(`{"type":%q,"response":{"id":%q,"output":[]}}`, event, "parent-"+session)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
				t.Errorf("initial response: %v", err)
				return
			}
		}
		_, next, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("follow-up read: %v", err)
			return
		}
		if got := gjson.GetBytes(next, "event_id").String(); got != "healthy-"+session {
			t.Errorf("session %s forwarded rejected or wrong frame: %s", session, next)
			return
		}
		response := fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"output":[]}}`, "done-"+session)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
			t.Errorf("follow-up response: %v", err)
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	cfg := duplexSteeringConfig()
	// A post-translation rule exercises errors found by the final wire check as
	// well as errors found while reading the original frame.
	cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "gpt-6-astra"}}, Params: map[string]any{"input.0.id": wsPoisonedSearchCallID}}}
	exec := NewCodexWebsocketsExecutor(cfg)
	exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}

	for _, session := range []string{"A", "B"} {
		t.Run(session, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			ctx = withItemIDReplayChecker(ctx)
			input := make(chan cliproxyexecutor.WebsocketInput, 10)
			ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
			auth := &cliproxyauth.Auth{ID: session, Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": server.URL, "websockets": "true"}}
			request := fmt.Sprintf(`{"model":"gpt-6-astra","event_id":%q,"input":[]}`, "initial-"+session)
			var intercepted atomic.Bool
			opts := cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FormatCodex,
				Metadata:     map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()},
				RequestAfterAuthInterceptor: func(_ context.Context, req cliproxyexecutor.RequestAfterAuthInterceptRequest) cliproxyexecutor.RequestAfterAuthInterceptResponse {
					if gjson.GetBytes(req.Body, "event_id").String() == session+"-create" {
						before := []byte(`"event_id":"` + session + `-create"`)
						after := []byte(`"event_id":"interceptor-id"`)
						rewritten := bytes.Replace(req.Body, before, after, 1)
						intercepted.Store(!bytes.Equal(req.Body, rewritten))
						return cliproxyexecutor.RequestAfterAuthInterceptResponse{Body: rewritten}
					}
					return cliproxyexecutor.RequestAfterAuthInterceptResponse{}
				},
			}
			result, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(request)}, opts)
			if err != nil {
				t.Fatal(err)
			}
			read := func() cliproxyexecutor.StreamChunk {
				t.Helper()
				select {
				case chunk, ok := <-result.Chunks:
					if !ok {
						t.Fatal("stream closed unexpectedly")
					}
					if chunk.Err != nil {
						t.Fatal(chunk.Err)
					}
					return chunk
				case <-ctx.Done():
					t.Fatal("timed out waiting for stream")
					return cliproxyexecutor.StreamChunk{}
				}
			}
			for {
				chunk := read()
				if gjson.GetBytes(chunk.Payload, "type").String() == "response.completed" {
					break
				}
			}
			frames := []struct {
				kind, idField string
				validItemID   bool
			}{
				{"response.create", `"event_id":"` + session + `-create",`, false},
				{"response.append", `"event_id":"` + session + `-append",`, false},
				{"response.steer", `"event_id":"` + session + `-steer",`, false},
				{"response.create", `"event_id":"` + session + `-final-create",`, true},
				{"response.steer", `"event_id":"` + session + `-final-steer",`, true},
				{"response.create", "", false},
				{"response.append", `"event_id":"",`, false},
				{"response.steer", `"event_id":123,`, false},
				{"response.create", `"event_id":"` + session + `-ambiguous-a","\u0065vent_id":"` + session + `-ambiguous-b",`, false},
			}
			start := make(chan struct{})
			var senders sync.WaitGroup
			for _, frame := range frames {
				itemID := wsPoisonedSearchCallID
				if frame.validItemID {
					itemID = wsRepairedSearchCallID
				}
				payload := fmt.Sprintf(`{"type":%q,"model":"gpt-6-astra","previous_response_id":%q,%s"input":[{"type":"tool_search_call","id":%q,"call_id":"s1","arguments":{},"execution":"client"}]}`, frame.kind, "parent-"+session, frame.idField, itemID)
				senders.Add(1)
				go func() {
					defer senders.Done()
					<-start
					select {
					case input <- cliproxyexecutor.WebsocketInput{Payload: []byte(payload)}:
					case <-ctx.Done():
					}
				}()
			}
			close(start)
			senders.Wait()
			want := map[string]bool{session + "-create": true, session + "-append": true, session + "-steer": true, session + "-final-create": true, session + "-final-steer": true}
			withoutID := 0
			for range frames {
				chunk := read()
				if gjson.GetBytes(chunk.Payload, "type").String() != "error" || gjson.GetBytes(chunk.Payload, "status").Int() != http.StatusUnprocessableEntity {
					t.Fatalf("expected local 422, got %s", chunk.Payload)
				}
				id := gjson.GetBytes(chunk.Payload, "error.event_id")
				if !id.Exists() {
					withoutID++
					continue
				}
				if !want[id.String()] {
					t.Fatalf("wrong or duplicate rejected event id %q: %s", id.String(), chunk.Payload)
				}
				delete(want, id.String())
			}
			if len(want) != 0 || withoutID != 4 {
				t.Fatalf("unmatched ids = %v, errors without ids = %d", want, withoutID)
			}
			if !intercepted.Load() {
				t.Fatal("fixture did not change the intercepted event id")
			}
			healthy := fmt.Sprintf(`{"type":"response.create","model":"gpt-6-astra","event_id":%q,"input":[{"type":"message","role":"user","content":"next"}]}`, "healthy-"+session)
			input <- cliproxyexecutor.WebsocketInput{Payload: []byte(healthy)}
			for {
				chunk := read()
				if gjson.GetBytes(chunk.Payload, "type").String() == "response.completed" && gjson.GetBytes(chunk.Payload, "response.id").String() == "done-"+session {
					break
				}
			}
			cancel()
			select {
			case <-finished[session]:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not close")
			}
		})
	}
}

// A payload filter must not hide a steer that needed managed preparation.
func TestCodexDuplexSteerReplayBeforePayloadFilter(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		rawReplay   bool
		wantReplay  bool
		inject      bool
		checker     bool
	}{
		{"additional_tools", `[{"type":"additional_tools","tools":[{"type":"tool_search"}]}]`, true, true, false, true},
		{"ordinary_input", `[{"type":"message","role":"user","content":"continue"}]`, false, false, false, true},
		{"tools_injected_by_rule", `[{"type":"message","role":"user","content":"continue"}]`, false, true, true, true},
		{"direct_without_checker", `[{"type":"additional_tools","tools":[{"type":"tool_search"}]}]`, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forwarded := make(chan []byte, 1)
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(serverDone)
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"r1","output":[]}}`)); err != nil {
					return
				}
				if _, payload, err := conn.ReadMessage(); err == nil {
					forwarded <- payload
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer.accepted","steer":{"id":"s1","previous_response_id":"r1"}}`))
					_, _, _ = conn.ReadMessage()
				}
			}))
			defer server.Close()
			cfg := duplexSteeringConfig()
			models := []config.PayloadModelRule{{Name: "gpt-6-astra", Match: []map[string]any{{"type": "response.steer"}}}}
			if tc.inject {
				tool := map[string]any{"type": "additional_tools", "tools": []any{map[string]any{"type": "tool_search"}}}
				cfg.Payload.Override = []config.PayloadRule{{Models: models, Params: map[string]any{"input": []any{tool}}}}
			} else {
				cfg.Payload.Filter = []config.PayloadFilterRule{{Models: models, Params: []string{"input"}}}
			}
			policy := responsestools.ConventionPolicy(responsestools.Route{Provider: "codex", UpstreamFormat: "codex"})
			steer := []byte(`{"type":"response.steer","event_id":"steer-check","previous_response_id":"r1","input":` + tc.input + `}`)
			if got := responsestools.RequiresToolReplay(steer, policy, responsestools.DefaultLimits()); got != tc.rawReplay {
				t.Fatalf("raw fixture replay requirement = %t, want %t", got, tc.rawReplay)
			}
			if tc.rawReplay {
				filtered := []byte(`{"type":"response.steer","event_id":"steer-check","previous_response_id":"r1"}`)
				if responsestools.RequiresToolReplay(filtered, policy, responsestools.DefaultLimits()) {
					t.Fatal("filtered fixture unexpectedly requires replay")
				}
				limits := responsestools.DefaultLimits()
				prepared, err := responsestools.Prepare(steer, policy, limits, responsestools.NewLimiter(limits))
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Attempt == nil || !prepared.Attempt.ResponseOnly() {
					t.Fatal("native tool search did not retain its response-only adapter")
				}
				defer prepared.Attempt.Close()
				alias := []byte(`{"output":[{"type":"function_call","id":"fc_native_search","call_id":"search_1","namespace":"tool_search","name":"tool_search_tool","arguments":"{\"query\":\"budget\"}"}]}`)
				restored, err := prepared.Attempt.RewriteResponse(alias)
				if err != nil || gjson.GetBytes(restored, "output.0.type").String() != "tool_search_call" {
					t.Fatalf("response-only adapter did not restore search identity: %s, %v", restored, err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			input := make(chan cliproxyexecutor.WebsocketInput, 1)
			ctx = cliproxyexecutor.WithWebsocketInput(cliproxyexecutor.WithDownstreamWebsocket(ctx), input)
			if tc.checker {
				ctx = cliproxyexecutor.WithResponsesToolsReplayChecker(ctx, func(payload []byte, _, _, _ string) bool {
					return responsestools.RequiresToolReplay(payload, policy, responsestools.DefaultLimits())
				})
			}
			exec := NewCodexWebsocketsExecutor(cfg)
			exec.store = &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
			auth := &cliproxyauth.Auth{ID: "A", Provider: "codex", Attributes: map[string]string{"api_key": "test", "base_url": server.URL, "websockets": "true"}}
			req := cliproxyexecutor.Request{Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[]}`)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatCodex, Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: t.Name()}}
			result, err := exec.ExecuteStream(ctx, auth, req, opts)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case chunk := <-result.Chunks:
				if chunk.Err != nil || gjson.GetBytes(chunk.Payload, "type").String() != "response.created" {
					t.Fatalf("initial response = %s, %v", chunk.Payload, chunk.Err)
				}
			case <-ctx.Done():
				t.Fatal("initial response timed out")
			}
			input <- cliproxyexecutor.WebsocketInput{Payload: steer}
			if tc.wantReplay {
				select {
				case chunk := <-result.Chunks:
					if !cliproxyexecutor.IsUpstreamWebsocketReplayRequired(chunk.Err) {
						t.Fatalf("steer result = %s, %v; want replay-required", chunk.Payload, chunk.Err)
					}
				case payload := <-forwarded:
					t.Fatalf("replay-required steer was forwarded: %s", payload)
				case <-ctx.Done():
					t.Fatal("replay-required steer timed out")
				}
			} else {
				select {
				case payload := <-forwarded:
					if gjson.GetBytes(payload, "input").Exists() || responsestools.RequiresToolReplay(payload, policy, responsestools.DefaultLimits()) {
						t.Fatalf("ordinary filtered steer = %s", payload)
					}
				case <-ctx.Done():
					t.Fatal("ordinary steer was not forwarded")
				}
			}
			cancel()
			select {
			case <-serverDone:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream did not close")
			}
			if tc.wantReplay {
				select {
				case payload := <-forwarded:
					t.Fatalf("replay-required steer reached upstream: %s", payload)
				default:
				}
			}
		})
	}
}
