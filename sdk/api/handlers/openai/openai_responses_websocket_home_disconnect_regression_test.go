package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executionregistry"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

type homeDisconnectDispatcher struct {
	baseURL  string
	ids      [2]string
	calls    atomic.Int32
	requests chan homeDisconnectDispatch
}

type homeDisconnectDispatch struct {
	session string
	pinned  string
}

func (*homeDisconnectDispatcher) HeartbeatOK() bool { return true }
func (*homeDisconnectDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return nil, errors.New("Home constraints were not supplied")
}
func (d *homeDisconnectDispatcher) RPopAuthWithConstraints(_ context.Context, _ string, session string, _ http.Header, _ int, _ []string, pinned string) ([]byte, error) {
	call := d.calls.Add(1)
	d.requests <- homeDisconnectDispatch{session: session, pinned: pinned}
	var index int
	switch call {
	case 1:
		index = 0
	case 2:
		index = 1
	case 3:
		if pinned != d.ids[0] {
			return nil, fmt.Errorf("recovery did not request the original credential: %q", pinned)
		}
		index = 0
	default:
		return nil, errors.New("unexpected Home redispatch")
	}
	return json.Marshal(coreauth.Auth{
		ID: d.ids[index], Provider: "codex", Status: coreauth.StatusActive,
		Attributes: map[string]string{
			"websockets": "true", "api_key": fmt.Sprintf("home-key-%d", index),
			"auth_kind": "oauth", "base_url": d.baseURL, "home_upstream_model": "home-upstream-model",
		},
		Metadata: map[string]any{"request_scoped_errors": []config.RequestScopedErrorRule{
			{Status: http.StatusBadGateway, Match: []string{"upstream_websocket_lost"}, Action: "continue"},
		}},
	})
}
func (*homeDisconnectDispatcher) AbortAmbiguousDispatch() {}

type homeDisconnectFrame struct {
	connection int32
	payload    []byte
}

type homeObservedDispatcher struct {
	baseURL string
	id      string
	calls   atomic.Int32
}

func (*homeObservedDispatcher) HeartbeatOK() bool { return true }
func (d *homeObservedDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	d.calls.Add(1)
	return json.Marshal(map[string]any{
		"model": "home-upstream-model", "auth_index": d.id,
		"auth": coreauth.Auth{ID: d.id, Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{
			"websockets": "true", "api_key": "home-observed-key", "auth_kind": "oauth", "base_url": d.baseURL,
		}},
		"concurrency": map[string]any{"accounted": true, "credential_id": d.id, "model": "home-upstream-model"},
	})
}
func (*homeObservedDispatcher) AbortAmbiguousDispatch() {}

type homeObservedExecutor struct {
	*runtimeexecutor.CodexAutoExecutor
	attempts chan context.Context
}

func (e *homeObservedExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.attempts <- ctx
	return e.CodexAutoExecutor.ExecuteStream(ctx, auth, req, opts)
}

type homeObservedRelease struct {
	group    executionregistry.ReleaseGroup
	sequence int64
}

type homeObservedFixture struct {
	manager    *coreauth.Manager
	registry   *executionregistry.Registry
	dispatcher *homeObservedDispatcher
	executor   *homeObservedExecutor
	releases   chan homeObservedRelease
	server     *httptest.Server
}

func newHomeObservedFixture(t *testing.T, upstreamURL, mode string) *homeObservedFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
	cfg.Codex.ResponseSteering, cfg.CodexResponseSteering = true, true
	cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{
		Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: upstreamURL},
		ClientSearch: mode, CustomTools: "inherit",
	}}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatal(err)
	}
	f := &homeObservedFixture{
		manager:    coreauth.NewManager(nil, nil, nil),
		registry:   executionregistry.New(),
		dispatcher: &homeObservedDispatcher{baseURL: upstreamURL, id: "home-observed-" + strings.ReplaceAll(t.Name(), "/", "-")},
		executor:   &homeObservedExecutor{CodexAutoExecutor: runtimeexecutor.NewCodexAutoExecutor(cfg), attempts: make(chan context.Context, 4)},
		releases:   make(chan homeObservedRelease, 4),
	}
	f.registry.SetReleaseSink(func(group executionregistry.ReleaseGroup, sequence int64) {
		f.releases <- homeObservedRelease{group: group, sequence: sequence}
	})
	f.manager.SetConfig(cfg)
	f.manager.PublishHomeDispatch(f.dispatcher, f.registry, 1)
	f.manager.RegisterExecutor(f.executor)
	registry.GetGlobalRegistry().RegisterClient(f.dispatcher.id, "codex", []*registry.ModelInfo{{ID: "home-client-alias"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(f.dispatcher.id) })
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, f.manager))
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
	return f
}

func (f *homeObservedFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (f *homeObservedFixture) awaitRelease(t *testing.T, sequence int64) {
	t.Helper()
	select {
	case got := <-f.releases:
		want := executionregistry.ReleaseGroup{CredentialID: f.dispatcher.id, Model: "home-upstream-model"}
		if got.group != want || got.sequence != sequence {
			t.Fatalf("Home release=%+v, want group=%+v sequence=%d", got, want, sequence)
		}
	case <-time.After(8 * time.Second):
		t.Fatalf("timed out waiting for Home release sequence %d", sequence)
	}
}

func homeObservedAttempt(t *testing.T, attempts <-chan context.Context) context.Context {
	t.Helper()
	select {
	case ctx := <-attempts:
		return ctx
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for the execution attempt")
		return nil
	}
}

func homeObservedAttemptCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
		t.Fatal("execution attempt did not terminate")
	}
}

func homeDisconnectRead(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read downstream: %v", err)
	}
	return payload
}

func homeDisconnectWrite(t *testing.T, c *websocket.Conn, payload string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write downstream: %v", err)
	}
}

func homeDisconnectCompletion(t *testing.T, c *websocket.Conn, id string) {
	t.Helper()
	for _, event := range []string{"response.created", "response.completed"} {
		frame := homeDisconnectRead(t, c)
		if gjson.GetBytes(frame, "type").String() != event || gjson.GetBytes(frame, "response.id").String() != id {
			t.Fatalf("response event=%s, want %s for %s", frame, event, id)
		}
	}
}

func homeDisconnectReceive(t *testing.T, frames <-chan homeDisconnectFrame) homeDisconnectFrame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for upstream request")
		return homeDisconnectFrame{}
	}
}

// The sockets, manager, Home selections, executor, and route adaptation are all real;
// the Home dispatcher and the upstream are local scripted fixtures.
func TestResponsesWebsocketHomeDisconnectDoesNotReplayOrCrossSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			var aConnections, bConnections atomic.Int32
			aFrames := make(chan homeDisconnectFrame, 4)
			bFrames := make(chan homeDisconnectFrame, 4)
			serverErrors := make(chan error, 8)
			loseA := make(chan struct{})
			defer func() {
				select {
				case <-loseA:
				default:
					close(loseA)
				}
			}()

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth := r.Header.Get("Authorization")
				var connection int32
				var frames chan<- homeDisconnectFrame
				switch auth {
				case "Bearer home-key-0":
					connection, frames = aConnections.Add(1), aFrames
				case "Bearer home-key-1":
					connection, frames = bConnections.Add(1), bFrames
				default:
					serverErrors <- fmt.Errorf("unexpected upstream authorization %q", auth)
					http.Error(w, "unexpected authorization", http.StatusUnauthorized)
					return
				}
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(12 * time.Second))
				for turn := 1; ; turn++ {
					_, payload, errRead := c.ReadMessage()
					if errRead != nil {
						return
					}
					frames <- homeDisconnectFrame{connection: connection, payload: payload}
					var responses []string
					switch {
					case auth == "Bearer home-key-0" && connection == 1:
						responses = []string{
							`{"type":"response.created","response":{"id":"a-lost","status":"in_progress","output":[]}}`,
							`{"type":"response.output_text.delta","response_id":"a-lost","item_id":"a-item","output_index":0,"content_index":0,"delta":"A_PARTIAL"}`,
						}
					case auth == "Bearer home-key-0":
						responses = []string{
							`{"type":"response.created","response":{"id":"a-recovered","status":"in_progress","output":[]}}`,
							`{"type":"response.completed","response":{"id":"a-recovered","status":"completed","output":[]}}`,
						}
					default:
						responses = []string{
							fmt.Sprintf(`{"type":"response.created","response":{"id":"b-%d","status":"in_progress","output":[]}}`, turn),
							fmt.Sprintf(`{"type":"response.completed","response":{"id":"b-%d","status":"completed","output":[{"id":"b-item-%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"B_OUTPUT"}]}]}}`, turn, turn),
						}
					}
					for _, response := range responses {
						if errWrite := c.WriteMessage(websocket.TextMessage, []byte(response)); errWrite != nil {
							serverErrors <- errWrite
							return
						}
					}
					if auth == "Bearer home-key-0" && connection == 1 {
						<-loseA
						return
					}
				}
			}))
			t.Cleanup(upstream.Close)

			cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
			cfg.Codex.ResponseSteering, cfg.CodexResponseSteering = true, true
			cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{
				Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: upstream.URL},
				ClientSearch: mode, CustomTools: "inherit",
			}}
			cfg.NormalizeResponsesToolsConfig()
			if err := cfg.ValidateResponsesToolsConfig(); err != nil {
				t.Fatal(err)
			}
			model := "home-disconnect-" + mode
			dispatcher := &homeDisconnectDispatcher{baseURL: upstream.URL, ids: [2]string{model + "-a", model + "-b"}, requests: make(chan homeDisconnectDispatch, 8)}
			manager := coreauth.NewManager(nil, nil, nil)
			manager.SetConfig(cfg)
			manager.SetRetryConfig(0, 0, 2)
			executionRegistry := executionregistry.New()
			manager.PublishHomeDispatch(dispatcher, executionRegistry, 1)
			manager.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
			// A conflicting local record must not replace the Home session credential.
			if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: dispatcher.ids[0], Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"api_key": "wrong-local-key", "websockets": "true", "base_url": "http://127.0.0.1:1"}}); err != nil {
				t.Fatal(err)
			}
			reg := registry.GetGlobalRegistry()
			for _, id := range dispatcher.ids {
				reg.RegisterClient(id, "codex", []*registry.ModelInfo{{ID: model}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
			}
			h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager))
			access := &revocableWebsocketAccessProvider{}
			h.AccessManager = sdkaccess.NewManager()
			h.AccessManager.SetProviders([]sdkaccess.Provider{access})
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)
			downstream := httptest.NewServer(router)
			t.Cleanup(downstream.Close)
			dial := func() *websocket.Conn {
				t.Helper()
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(downstream.URL, "http")+"/v1/responses", http.Header{"Authorization": {"Bearer key-1"}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = conn.Close() })
				return conn
			}
			a, b := dial(), dial()

			homeDisconnectWrite(t, a, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"a-create","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"A_PRIVATE"}]}`, model))
			firstA := homeDisconnectReceive(t, aFrames)
			for _, want := range []string{"response.created", "response.output_text.delta"} {
				frame := homeDisconnectRead(t, a)
				if got := gjson.GetBytes(frame, "type").String(); got != want {
					t.Fatalf("A event=%s, want %s", frame, want)
				}
			}

			homeDisconnectWrite(t, b, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"b-create","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"B_PRIVATE"}]}`, model))
			firstB := homeDisconnectReceive(t, bFrames)
			homeDisconnectCompletion(t, b, "b-1")
			close(loseA)
			lost := homeDisconnectRead(t, a)
			if gjson.GetBytes(lost, "type").String() != "error" || gjson.GetBytes(lost, "error.code").String() != "upstream_websocket_lost" || gjson.GetBytes(lost, "error.event_id").String() != "a-create" {
				t.Fatalf("A loss=%s, want correlated loss rather than fabricated completion", lost)
			}
			if aConnections.Load() != 1 || dispatcher.calls.Load() != 2 {
				t.Fatalf("loss replayed automatically: A connections=%d Home selections=%d", aConnections.Load(), dispatcher.calls.Load())
			}

			homeDisconnectWrite(t, a, `{"type":"response.create","previous_response_id":"a-lost","event_id":"a-continuation","input":[{"role":"user","content":"A_CONTINUATION"}]}`)
			if frame := homeDisconnectRead(t, a); gjson.GetBytes(frame, "error.code").String() != "previous_response_not_found" || gjson.GetBytes(frame, "error.event_id").String() != "a-continuation" {
				t.Fatalf("A continuation after loss=%s", frame)
			}
			access.revoked.Store(true)
			homeDisconnectWrite(t, a, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"a-denied","input":[{"role":"user","content":"A_DENIED"}]}`, model))
			if frame := homeDisconnectRead(t, a); gjson.GetBytes(frame, "status").Int() != http.StatusForbidden {
				t.Fatalf("A revoked client=%s, want 403", frame)
			}
			access.revoked.Store(false)

			homeDisconnectWrite(t, b, `{"type":"response.create","previous_response_id":"b-1","event_id":"b-follow","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"B_FOLLOW"}]}`)
			homeDisconnectCompletion(t, b, "b-2")
			secondB := homeDisconnectReceive(t, bFrames)
			homeDisconnectWrite(t, a, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"a-recovery","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"A_RECOVERY"}]}`, model))
			_ = a.SetReadDeadline(time.Now().Add(8 * time.Second))
			_, recovered, err := a.ReadMessage()
			if err != nil {
				t.Fatalf("A recovery read: %v; Home selections=%d A connections=%d B connections=%d", err, dispatcher.calls.Load(), aConnections.Load(), bConnections.Load())
			}
			if gjson.GetBytes(recovered, "type").String() != "response.created" || gjson.GetBytes(recovered, "response.id").String() != "a-recovered" {
				t.Fatalf("A recovery first event=%s", recovered)
			}
			if frame := homeDisconnectRead(t, a); gjson.GetBytes(frame, "type").String() != "response.completed" || gjson.GetBytes(frame, "response.id").String() != "a-recovered" {
				t.Fatalf("A recovery completion=%s", frame)
			}
			secondA := homeDisconnectReceive(t, aFrames)

			wantTool := "tool_search"
			if mode == "bridge" {
				wantTool = "function"
			}
			framesToCheck := []homeDisconnectFrame{firstA, firstB, secondA}
			if mode == "bridge" {
				framesToCheck = append(framesToCheck, secondB)
			}
			for _, frame := range framesToCheck {
				if got := gjson.GetBytes(frame.payload, "model").String(); got != "home-upstream-model" {
					t.Errorf("upstream model=%q frame=%s", got, frame.payload)
				}
				if got := gjson.GetBytes(frame.payload, "tools.0.type").String(); got != wantTool {
					t.Errorf("%s route tool=%q frame=%s", mode, got, frame.payload)
				}
			}
			if firstA.connection != 1 || secondA.connection != 2 || firstB.connection != 1 || secondB.connection != 1 || aConnections.Load() != 2 || bConnections.Load() != 1 || dispatcher.calls.Load() != 3 {
				t.Fatalf("connections A=(%d,%d)/%d B=(%d,%d)/%d Home selections=%d", firstA.connection, secondA.connection, aConnections.Load(), firstB.connection, secondB.connection, bConnections.Load(), dispatcher.calls.Load())
			}
			firstDispatch, secondDispatch, recoveryDispatch := <-dispatcher.requests, <-dispatcher.requests, <-dispatcher.requests
			if firstDispatch.session == "" || firstDispatch.session == secondDispatch.session || recoveryDispatch.session != firstDispatch.session || firstDispatch.pinned != "" || secondDispatch.pinned != "" || recoveryDispatch.pinned != dispatcher.ids[0] {
				t.Errorf("unexpected Home dispatch sessions or pins: first=%+v second=%+v recovery=%+v", firstDispatch, secondDispatch, recoveryDispatch)
			}
			if got := string(secondA.payload); !strings.Contains(got, "A_RECOVERY") || strings.Contains(got, "A_PRIVATE") || strings.Contains(got, "A_PARTIAL") || strings.Contains(got, "A_CONTINUATION") || strings.Contains(got, "A_DENIED") || strings.Contains(got, "B_PRIVATE") || gjson.GetBytes(secondA.payload, "previous_response_id").Exists() {
				t.Errorf("A recovery leaked or replayed old data: %s", got)
			}
			if got := string(secondB.payload); !strings.Contains(got, "B_FOLLOW") || strings.Contains(got, "A_PRIVATE") || strings.Contains(got, "A_PARTIAL") || strings.Contains(got, "A_RECOVERY") {
				t.Errorf("B follow-up crossed sessions: %s", got)
			} else if mode == "bridge" && (!strings.Contains(got, "B_PRIVATE") || !strings.Contains(got, "B_OUTPUT") || gjson.GetBytes(secondB.payload, "previous_response_id").Exists()) {
				t.Errorf("B bridge history missing: %s", got)
			} else if mode == "native" && (strings.Contains(got, "B_PRIVATE") || gjson.GetBytes(secondB.payload, "previous_response_id").String() != "b-1") {
				t.Errorf("B native continuation was not incremental: %s", got)
			}
			select {
			case err := <-serverErrors:
				t.Fatalf("upstream: %v", err)
			default:
			}
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancelDrain()
			if err := executionRegistry.Drain(drainCtx); err != nil {
				t.Fatalf("drain idle Home sessions: %v", err)
			}
		})
	}
}

type homeRetryDisconnectDispatch struct {
	session  string
	model    string
	pinned   string
	excluded []string
}

type homeRetryDisconnectDispatcher struct {
	urls     [2]string
	ids      [2]string
	calls    atomic.Int32
	requests chan homeRetryDisconnectDispatch
}

func (*homeRetryDisconnectDispatcher) HeartbeatOK() bool { return true }
func (*homeRetryDisconnectDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return nil, errors.New("Home constraints were not supplied")
}
func (d *homeRetryDisconnectDispatcher) RPopAuthWithConstraints(_ context.Context, model, session string, _ http.Header, _ int, excluded []string, pinned string) ([]byte, error) {
	call := d.calls.Add(1)
	d.requests <- homeRetryDisconnectDispatch{session: session, model: model, pinned: pinned, excluded: append([]string(nil), excluded...)}
	var index int
	switch call {
	case 1:
		index = 0
	case 2:
		index = 1
	case 3:
		if pinned != d.ids[1] {
			return nil, fmt.Errorf("recovery did not request the successful credential: %q", pinned)
		}
		index = 1
	default:
		return nil, errors.New("unexpected Home dispatch")
	}
	return json.Marshal(coreauth.Auth{
		ID: d.ids[index], Provider: "codex", Status: coreauth.StatusActive,
		Attributes: map[string]string{
			"websockets": "true", "api_key": fmt.Sprintf("retry-key-%d", index),
			"auth_kind": "oauth", "base_url": d.urls[index], "home_upstream_model": "home-upstream-model",
		},
		Metadata: map[string]any{"request_scoped_errors": []config.RequestScopedErrorRule{
			{Status: http.StatusBadGateway, Match: []string{"upstream_websocket_lost"}, Action: "continue"},
		}},
	})
}
func (*homeRetryDisconnectDispatcher) AbortAmbiguousDispatch() {}

func TestResponsesWebsocketHomeRetryThenDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []struct{ first, second string }{{"native", "bridge"}, {"bridge", "native"}} {
		for _, stage := range []string{"before_created", "after_partial"} {
			t.Run(route.first+"_to_"+route.second+"/"+stage, func(t *testing.T) {
				badFrames := make(chan homeDisconnectFrame, 2)
				goodFrames := make(chan homeDisconnectFrame, 3)
				serverErrors := make(chan error, 4)
				var badConnections, goodConnections atomic.Int32
				loseGood := make(chan struct{})
				defer func() {
					select {
					case <-loseGood:
					default:
						close(loseGood)
					}
				}()
				bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					connection := badConnections.Add(1)
					if r.Header.Get("Authorization") != "Bearer retry-key-0" {
						serverErrors <- fmt.Errorf("wrong first credential: %q", r.Header.Get("Authorization"))
						http.Error(w, "unexpected authorization", http.StatusUnauthorized)
						return
					}
					c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						serverErrors <- err
						return
					}
					defer c.Close()
					_ = c.SetReadDeadline(time.Now().Add(12 * time.Second))
					_, payload, err := c.ReadMessage()
					if err != nil {
						serverErrors <- err
						return
					}
					badFrames <- homeDisconnectFrame{connection: connection, payload: payload}
					if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted","resets_in_seconds":600}}`)); err != nil {
						serverErrors <- err
						return
					}
					_, _, _ = c.ReadMessage()
				}))
				t.Cleanup(bad.Close)
				good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					connection := goodConnections.Add(1)
					if r.Header.Get("Authorization") != "Bearer retry-key-1" {
						serverErrors <- fmt.Errorf("wrong successful credential: %q", r.Header.Get("Authorization"))
						http.Error(w, "unexpected authorization", http.StatusUnauthorized)
						return
					}
					c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						serverErrors <- err
						return
					}
					defer c.Close()
					_ = c.SetReadDeadline(time.Now().Add(12 * time.Second))
					_, payload, err := c.ReadMessage()
					if err != nil {
						serverErrors <- err
						return
					}
					goodFrames <- homeDisconnectFrame{connection: connection, payload: payload}
					if connection == 1 && stage == "before_created" {
						return
					}
					responses := []string{
						`{"type":"response.created","response":{"id":"retry-lost","status":"in_progress","output":[]}}`,
						`{"type":"response.output_text.delta","response_id":"retry-lost","item_id":"retry-item","output_index":0,"content_index":0,"delta":"RETRY_PARTIAL"}`,
					}
					if connection != 1 {
						responses = []string{
							`{"type":"response.created","response":{"id":"retry-recovered","status":"in_progress","output":[]}}`,
							`{"type":"response.completed","response":{"id":"retry-recovered","status":"completed","output":[]}}`,
						}
					}
					for _, response := range responses {
						if err = c.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
							serverErrors <- err
							return
						}
					}
					if connection == 1 {
						<-loseGood
						return
					}
					_, _, _ = c.ReadMessage()
				}))
				t.Cleanup(good.Close)

				cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
				cfg.Codex.ResponseSteering, cfg.CodexResponseSteering = true, true
				for _, entry := range []struct{ url, mode string }{{bad.URL, route.first}, {good.URL, route.second}} {
					cfg.ResponsesTools.Routes = append(cfg.ResponsesTools.Routes, config.ResponsesToolsRoute{
						Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: entry.url},
						ClientSearch: entry.mode, CustomTools: "inherit",
					})
				}
				cfg.NormalizeResponsesToolsConfig()
				if err := cfg.ValidateResponsesToolsConfig(); err != nil {
					t.Fatal(err)
				}
				model := "home-retry-disconnect-" + route.first + "-" + route.second + "-" + stage
				dispatcher := &homeRetryDisconnectDispatcher{
					urls: [2]string{bad.URL, good.URL}, ids: [2]string{model + "-bad", model + "-good"}, requests: make(chan homeRetryDisconnectDispatch, 8),
				}
				manager := coreauth.NewManager(nil, nil, nil)
				manager.SetConfig(cfg)
				manager.SetRetryConfig(0, 0, 2)
				manager.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
				manager.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
				reg := registry.GetGlobalRegistry()
				for _, id := range dispatcher.ids {
					reg.RegisterClient(id, "codex", []*registry.ModelInfo{{ID: model}})
					t.Cleanup(func() { reg.UnregisterClient(id) })
				}
				h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager))
				router := gin.New()
				router.GET("/v1/responses", h.ResponsesWebsocket)
				downstream := httptest.NewServer(router)
				t.Cleanup(downstream.Close)
				client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(downstream.URL, "http")+"/v1/responses", nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				homeDisconnectWrite(t, client, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"retry-create","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"RETRY_REQUEST"}]}`, model))
				first := homeDisconnectReceive(t, badFrames)
				second := homeDisconnectReceive(t, goodFrames)
				if stage == "after_partial" {
					for _, event := range []string{"response.created", "response.output_text.delta"} {
						if frame := homeDisconnectRead(t, client); gjson.GetBytes(frame, "type").String() != event {
							t.Fatalf("retried event=%s, want %s", frame, event)
						}
					}
					close(loseGood)
				}
				lost := homeDisconnectRead(t, client)
				if gjson.GetBytes(lost, "type").String() != "error" || gjson.GetBytes(lost, "error.code").String() != "upstream_websocket_lost" || gjson.GetBytes(lost, "error.event_id").String() != "retry-create" {
					t.Fatalf("loss after retry=%s, want correlated loss without fabricated completion", lost)
				}
				if badConnections.Load() != 1 || goodConnections.Load() != 1 || dispatcher.calls.Load() != 2 {
					t.Fatalf("lost request was retried: first=%d second=%d Home selections=%d", badConnections.Load(), goodConnections.Load(), dispatcher.calls.Load())
				}
				homeDisconnectWrite(t, client, `{"type":"response.create","previous_response_id":"retry-lost","event_id":"retry-continuation","input":[{"role":"user","content":"UNSAFE_CONTINUATION"}]}`)
				if frame := homeDisconnectRead(t, client); gjson.GetBytes(frame, "error.code").String() != "previous_response_not_found" || gjson.GetBytes(frame, "error.event_id").String() != "retry-continuation" {
					t.Fatalf("continuation after loss=%s", frame)
				}
				homeDisconnectWrite(t, client, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"retry-recovery","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"RETRY_RECOVERY"}]}`, model))
				homeDisconnectCompletion(t, client, "retry-recovered")
				recovery := homeDisconnectReceive(t, goodFrames)
				if first.connection != 1 || second.connection != 1 || recovery.connection != 2 || badConnections.Load() != 1 || goodConnections.Load() != 2 || dispatcher.calls.Load() != 3 {
					t.Fatalf("connections first=%d/%d successful=%d,%d/%d Home selections=%d", first.connection, badConnections.Load(), second.connection, recovery.connection, goodConnections.Load(), dispatcher.calls.Load())
				}
				for i, frame := range []homeDisconnectFrame{first, second, recovery} {
					mode := route.second
					if i == 0 {
						mode = route.first
					}
					wantTool := "tool_search"
					if mode == "bridge" {
						wantTool = "function"
					}
					if gjson.GetBytes(frame.payload, "tools.0.type").String() != wantTool || gjson.GetBytes(frame.payload, "model").String() != "home-upstream-model" {
						t.Errorf("attempt %d was not prepared for %s: %s", i, mode, frame.payload)
					}
				}
				if !strings.Contains(string(first.payload), "RETRY_REQUEST") || !strings.Contains(string(second.payload), "RETRY_REQUEST") {
					t.Errorf("eligible 429 retry did not preserve request: first=%s second=%s", first.payload, second.payload)
				}
				if got := string(recovery.payload); !strings.Contains(got, "RETRY_RECOVERY") || strings.Contains(got, "RETRY_REQUEST") || strings.Contains(got, "RETRY_PARTIAL") || strings.Contains(got, "UNSAFE_CONTINUATION") || gjson.GetBytes(recovery.payload, "previous_response_id").Exists() {
					t.Errorf("recovery replayed unknown or partial state: %s", got)
				}
				firstDispatch, retryDispatch, recoveryDispatch := <-dispatcher.requests, <-dispatcher.requests, <-dispatcher.requests
				if firstDispatch.session == "" || retryDispatch.session != firstDispatch.session || recoveryDispatch.session != firstDispatch.session || firstDispatch.model != model || retryDispatch.model != model || recoveryDispatch.model != model || firstDispatch.pinned != "" || retryDispatch.pinned != "" || recoveryDispatch.pinned != dispatcher.ids[1] {
					t.Errorf("unexpected dispatch session, model, or pin: first=%+v retry=%+v recovery=%+v", firstDispatch, retryDispatch, recoveryDispatch)
				}
				if len(retryDispatch.excluded) != 1 || retryDispatch.excluded[0] != dispatcher.ids[0] {
					t.Errorf("retry did not exclude rejected credential: %+v", retryDispatch)
				}
				select {
				case err := <-serverErrors:
					t.Fatalf("upstream: %v", err)
				default:
				}
			})
		}
	}
}

func TestResponsesWebsocketHomeCallerCancelClosesUpstream(t *testing.T) {
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			frames := make(chan []byte, 1)
			closed := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					closed <- err
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
				_, payload, err := c.ReadMessage()
				if err != nil {
					closed <- err
					return
				}
				frames <- payload
				_, _, err = c.ReadMessage()
				closed <- err
			}))
			t.Cleanup(upstream.Close)
			fixture := newHomeObservedFixture(t, upstream.URL, mode)
			client := fixture.dial(t)
			homeDisconnectWrite(t, client, `{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"CANCEL_REQUEST"}]}`)
			select {
			case frame := <-frames:
				if !strings.Contains(string(frame), "CANCEL_REQUEST") {
					t.Fatalf("upstream create=%s", frame)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("upstream never received the create")
			}
			attempt := homeObservedAttempt(t, fixture.executor.attempts)
			select {
			case release := <-fixture.releases:
				t.Fatalf("Home selection ended before client cancellation: %+v", release)
			default:
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-closed:
				if err == nil {
					t.Fatal("upstream remained open after client cancellation")
				}
				var timeout interface{ Timeout() bool }
				if errors.As(err, &timeout) && timeout.Timeout() {
					t.Fatalf("upstream closed only after the test deadline: %v", err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("client cancellation did not close the upstream socket")
			}
			fixture.awaitRelease(t, 1)
			homeObservedAttemptCanceled(t, attempt)
			if got := fixture.dispatcher.calls.Load(); got != 1 {
				t.Fatalf("client cancellation caused %d Home selections, want 1", got)
			}
		})
	}
}

func TestResponsesWebsocketHomeNoRetryAfterDeliveredOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			var firstConnections, retryConnections atomic.Int32
			frames := make(chan []byte, 1)
			serverErrors := make(chan error, 4)
			reject := make(chan struct{})
			defer func() {
				select {
				case <-reject:
				default:
					close(reject)
				}
			}()
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstConnections.Add(1)
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
				_, payload, err := c.ReadMessage()
				if err != nil {
					serverErrors <- err
					return
				}
				frames <- payload
				for _, response := range []string{
					`{"type":"response.created","response":{"id":"delivered","status":"in_progress","output":[]}}`,
					`{"type":"response.output_text.delta","response_id":"delivered","item_id":"delivered-item","output_index":0,"content_index":0,"delta":"DELIVERED_PARTIAL"}`,
				} {
					if err = c.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
						serverErrors <- err
						return
					}
				}
				<-reject
				if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted","resets_in_seconds":600}}`)); err != nil {
					serverErrors <- err
					return
				}
				_, _, _ = c.ReadMessage()
			}))
			t.Cleanup(first.Close)
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				retryConnections.Add(1)
				http.Error(w, "unexpected retry after delivery", http.StatusServiceUnavailable)
			}))
			t.Cleanup(second.Close)
			cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
			cfg.Codex.ResponseSteering, cfg.CodexResponseSteering = true, true
			for _, url := range []string{first.URL, second.URL} {
				cfg.ResponsesTools.Routes = append(cfg.ResponsesTools.Routes, config.ResponsesToolsRoute{
					Match:        config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: url},
					ClientSearch: mode, CustomTools: "inherit",
				})
			}
			cfg.NormalizeResponsesToolsConfig()
			if err := cfg.ValidateResponsesToolsConfig(); err != nil {
				t.Fatal(err)
			}
			model := "home-after-output-" + mode
			dispatcher := &homeRetryDisconnectDispatcher{urls: [2]string{first.URL, second.URL}, ids: [2]string{model + "-first", model + "-second"}, requests: make(chan homeRetryDisconnectDispatch, 4)}
			manager := coreauth.NewManager(nil, nil, nil)
			manager.SetConfig(cfg)
			manager.SetRetryConfig(0, 0, 2)
			manager.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
			manager.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
			for _, id := range dispatcher.ids {
				registry.GetGlobalRegistry().RegisterClient(id, "codex", []*registry.ModelInfo{{ID: model}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
			}
			h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, manager))
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)
			downstream := httptest.NewServer(router)
			t.Cleanup(downstream.Close)
			client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(downstream.URL, "http")+"/v1/responses", nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			homeDisconnectWrite(t, client, fmt.Sprintf(`{"type":"response.create","model":%q,"event_id":"delivered-create","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"DELIVERED_REQUEST"}]}`, model))
			for _, event := range []string{"response.created", "response.output_text.delta"} {
				if frame := homeDisconnectRead(t, client); gjson.GetBytes(frame, "type").String() != event {
					t.Fatalf("first event=%s, want %s", frame, event)
				}
			}
			close(reject)
			_ = client.SetReadDeadline(time.Now().Add(8 * time.Second))
			_, terminal, err := client.ReadMessage()
			if err != nil {
				var closeErr *websocket.CloseError
				if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseAbnormalClosure {
					t.Fatalf("post-output close=%v, want the quota rejection or an immediate close", err)
				}
			} else if gjson.GetBytes(terminal, "type").String() != "error" || gjson.GetBytes(terminal, "status").Int() != http.StatusTooManyRequests {
				t.Fatalf("post-output terminal=%s, want the upstream 429", terminal)
			}
			if frame := <-frames; !strings.Contains(string(frame), "DELIVERED_REQUEST") {
				t.Errorf("first upstream request=%s", frame)
			}
			if firstConnections.Load() != 1 || retryConnections.Load() != 0 || dispatcher.calls.Load() != 1 {
				t.Fatalf("delivered request retried: first=%d retry=%d selections=%d", firstConnections.Load(), retryConnections.Load(), dispatcher.calls.Load())
			}
			select {
			case err := <-serverErrors:
				t.Fatalf("upstream: %v", err)
			default:
			}
		})
	}
}

func TestResponsesWebsocketHomeIdleDisconnectAndDrain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			closeIdle := make(chan struct{})
			idleClosed := make(chan struct{})
			drainClosed := make(chan error, 1)
			var connections atomic.Int32
			defer func() {
				select {
				case <-closeIdle:
				default:
					close(closeIdle)
				}
			}()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := connections.Add(1)
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
				if _, _, err = c.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				for _, frame := range []string{
					`{"type":"response.created","response":{"id":"idle-complete","status":"in_progress","output":[]}}`,
					`{"type":"response.completed","response":{"id":"idle-complete","status":"completed","output":[]}}`,
				} {
					if err = c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						t.Error(err)
						return
					}
				}
				if index == 1 {
					<-closeIdle
					close(idleClosed)
					return
				}
				_, _, err = c.ReadMessage()
				drainClosed <- err
			}))
			t.Cleanup(upstream.Close)
			fixture := newHomeObservedFixture(t, upstream.URL, mode)
			client := fixture.dial(t)
			homeDisconnectWrite(t, client, `{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[]}`)
			homeDisconnectCompletion(t, client, "idle-complete")
			firstAttempt := homeObservedAttempt(t, fixture.executor.attempts)
			select {
			case release := <-fixture.releases:
				t.Fatalf("selection released before the idle socket closed: %+v", release)
			default:
			}
			close(closeIdle)
			select {
			case <-idleClosed:
			case <-time.After(8 * time.Second):
				t.Fatal("upstream did not close its idle socket")
			}
			fixture.awaitRelease(t, 1)
			homeObservedAttemptCanceled(t, firstAttempt)

			// A separate live selection must be ended by Drain itself.
			other := fixture.dial(t)
			homeDisconnectWrite(t, other, `{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[]}`)
			homeDisconnectCompletion(t, other, "idle-complete")
			secondAttempt := homeObservedAttempt(t, fixture.executor.attempts)
			select {
			case release := <-fixture.releases:
				t.Fatalf("second selection released before Drain: %+v", release)
			default:
			}
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancelDrain()
			if err := fixture.registry.Drain(drainCtx); err != nil {
				t.Fatalf("drain live Home selection: %v", err)
			}
			fixture.awaitRelease(t, 2)
			homeObservedAttemptCanceled(t, secondAttempt)
			select {
			case err := <-drainClosed:
				if err == nil {
					t.Fatal("Drain did not close the live upstream")
				}
				var timeout interface{ Timeout() bool }
				if errors.As(err, &timeout) && timeout.Timeout() {
					t.Fatalf("upstream closed only after the test deadline: %v", err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("Drain did not close the live upstream")
			}
			if got := fixture.dispatcher.calls.Load(); got != 2 {
				t.Fatalf("Home selections=%d, want one per downstream session", got)
			}
		})
	}
}

func TestResponsesWebsocketHomeDrainCancelsUnconsumedTerminalAttempt(t *testing.T) {
	for _, mode := range []string{"native", "bridge"} {
		t.Run(mode, func(t *testing.T) {
			reject := make(chan struct{})
			defer func() {
				select {
				case <-reject:
				default:
					close(reject)
				}
			}()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
				if _, _, err = c.ReadMessage(); err != nil {
					t.Error(err)
					return
				}
				if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"blocked-terminal","status":"in_progress","output":[]}}`)); err != nil {
					t.Error(err)
					return
				}
				<-reject
				if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"quota exhausted","resets_in_seconds":600}}`)); err != nil {
					t.Error(err)
					return
				}
				if mode == "bridge" {
					// Keep EOF from racing the 429 until the proxy closes the connection.
					_, _, _ = c.ReadMessage()
				}
			}))
			t.Cleanup(upstream.Close)
			fixture := newHomeObservedFixture(t, upstream.URL, mode)
			caller, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			ctx := coreexecutor.WithDownstreamWebsocket(caller)
			if mode == "native" {
				ctx = coreexecutor.WithWebsocketInput(ctx, make(chan coreexecutor.WebsocketInput))
			}
			const sessionID = "blocked-terminal-session"
			t.Cleanup(func() { fixture.manager.CloseExecutionSession(sessionID) })
			body := []byte(`{"model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"BLOCKED_TERMINAL"}]}`)
			result, err := fixture.manager.ExecuteStream(ctx, []string{"codex"}, coreexecutor.Request{Model: "home-client-alias", Payload: body}, coreexecutor.Options{
				Stream: true, SourceFormat: sdktranslator.FromString("openai-response"), OriginalRequest: body,
				Metadata: map[string]any{coreexecutor.ExecutionSessionMetadataKey: sessionID},
			})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			select {
			case chunk, ok := <-result.Chunks:
				if !ok || chunk.Err != nil || !strings.Contains(string(chunk.Payload), "response.created") {
					t.Fatalf("bootstrap chunk=%+v open=%v", chunk, ok)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("bootstrap was not delivered")
			}
			// Leave the terminal unread until Drain has canceled its delivery attempt.
			close(reject)
			fixture.awaitRelease(t, 1)
			drainCtx, cancelDrain := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancelDrain()
			if err := fixture.registry.Drain(drainCtx); err != nil {
				t.Fatalf("drain: %v", err)
			}
			select {
			case chunk, ok := <-result.Chunks:
				if ok {
					t.Fatalf("Drain returned before the unconsumed stream stopped: %+v", chunk)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("Drain did not end stream forwarding before caller cancellation")
			}
			if err := caller.Err(); err != nil {
				t.Fatalf("caller canceled before stream forwarding ended: %v", err)
			}
		})
	}
}
