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
	"github.com/tidwall/gjson"
)

func homeToolsTransportClient(t *testing.T, upstreamURL, mode string) (*websocket.Conn, *homeToolsReplayDispatcher, *revocableWebsocketAccessProvider) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
	cfg.Codex.ResponseSteering = true
	cfg.CodexResponseSteering = true
	otherMode := "bridge"
	if mode == "bridge" {
		otherMode = "native"
	}
	cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{
		{Match: config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: upstreamURL}, ClientSearch: mode, CustomTools: "inherit"},
		{Match: config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: "http://127.0.0.1:1"}, ClientSearch: otherMode, CustomTools: "inherit"},
	}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatal(err)
	}
	mgr := coreauth.NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	dispatcher := &homeToolsReplayDispatcher{baseURL: upstreamURL}
	mgr.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
	mgr.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
	// A local credential with the same ID must never replace Home's selected
	// session credential when deciding whether later frames can be forwarded.
	if _, err := mgr.Register(context.Background(), &coreauth.Auth{ID: "home-tools-replay", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"websockets": "true", "auth_kind": "oauth", "base_url": "http://127.0.0.1:1", "home_upstream_model": "home-upstream-model"}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("home-tools-replay", "codex", []*registry.ModelInfo{{ID: "home-client-alias"}})
	t.Cleanup(func() { reg.UnregisterClient("home-tools-replay") })
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, mgr))
	accessProvider := &revocableWebsocketAccessProvider{}
	access := sdkaccess.NewManager()
	access.SetProviders([]sdkaccess.Provider{accessProvider})
	h.AccessManager = access
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{"Authorization": {"Bearer key-1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	return conn, dispatcher, accessProvider
}

func TestResponsesWebsocketHomeBridgeFirstTurnTransportsAdaptedRequest(t *testing.T) {
	frames := make(chan []byte, 2)
	errs := make(chan error, 1)
	release := make(chan struct{}, 1)
	defer func() {
		select {
		case release <- struct{}{}:
		default:
		}
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			errs <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
		_, b, err := c.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		frames <- b
		if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"home-bridge-r1","output":[]}}`)); err != nil {
			errs <- err
			return
		}
		<-release
		if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"home-bridge-r1","status":"completed","output":[]}}`)); err != nil {
			errs <- err
			return
		}
		_, second, err := c.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		frames <- second
		if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"home-bridge-r2","status":"completed","output":[]}}`)); err != nil {
			errs <- err
		}
	}))
	t.Cleanup(upstream.Close)
	conn, dispatcher, _ := homeToolsTransportClient(t, upstream.URL, "bridge")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"first-turn"}]}`)); err != nil {
		t.Fatal(err)
	}
	_, created, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(created, "type").String() != "response.created" {
		t.Fatalf("created=%s error=%v", created, err)
	}
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer","previous_response_id":"home-bridge-r1","input":"DENIED_STEER"}`)); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("first bridge response: %v", err)
	}
	if got := gjson.GetBytes(payload, "type").String(); got != "response.completed" {
		t.Fatalf("event=%s", payload)
	}
	_, denied, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(denied, "type").String() != "error" || gjson.GetBytes(denied, "status").Int() != http.StatusUnprocessableEntity {
		t.Fatalf("bridge steer rejection=%s error=%v", denied, err)
	}
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","previous_response_id":"home-bridge-r1","tools":[{"type":"tool_search"}],"input":[{"role":"user","content":"follow-up"}]}`)); err != nil {
		t.Fatal(err)
	}
	_, secondResponse, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(secondResponse, "type").String() != "response.completed" {
		t.Fatalf("second bridge response=%s error=%v", secondResponse, err)
	}
	select {
	case frame := <-frames:
		if got := gjson.GetBytes(frame, "model").String(); got != "home-upstream-model" {
			t.Errorf("upstream model=%q frame=%s", got, frame)
		}
		if got := gjson.GetBytes(frame, "tools.0.type").String(); got != "function" {
			t.Errorf("bridge declaration type=%q frame=%s", got, frame)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(8 * time.Second):
		t.Fatal("no upstream request")
	}
	select {
	case frame := <-frames:
		if got := gjson.GetBytes(frame, "model").String(); got != "home-upstream-model" || gjson.GetBytes(frame, "previous_response_id").Exists() || !strings.Contains(string(frame), "first-turn") || !strings.Contains(string(frame), "follow-up") || gjson.GetBytes(frame, "tools.0.type").String() != "function" || strings.Contains(string(frame), "DENIED_STEER") {
			t.Errorf("bridge follow-up did not replay the prepared transcript: %s", frame)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(8 * time.Second):
		t.Fatal("no upstream follow-up")
	}
	if got := dispatcher.calls.Load(); got != 1 {
		t.Fatalf("Home selections=%d, want the retained session selection", got)
	}
}

func TestResponsesWebsocketHomeNativeFirstTurnAllowsLiveSteering(t *testing.T) {
	frames := make(chan []byte, 2)
	errs := make(chan error, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			errs <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
		_, first, err := c.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		frames <- first
		if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"home-native-r1","output":[]}}`)); err != nil {
			errs <- err
			return
		}
		_, steer, err := c.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		frames <- steer
		if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer.accepted","steer":{"id":"s1","previous_response_id":"home-native-r1"}}`)); err != nil {
			errs <- err
			return
		}
		_, _, _ = c.ReadMessage()
	}))
	t.Cleanup(upstream.Close)
	conn, dispatcher, accessProvider := homeToolsTransportClient(t, upstream.URL, "native")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, created, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(created, "type").String() != "response.created" {
		t.Fatalf("created=%s error=%v", created, err)
	}
	// A revoked client cannot send a second create on the live duplex stream.
	// The upstream reads only the subsequent authorized steer frame.
	accessProvider.revoked.Store(true)
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"home-native-r1"}`)); err != nil {
		t.Fatal(err)
	}
	_, deniedInterrupt, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(deniedInterrupt, "type").String() != "error" {
		t.Fatalf("denied interrupt=%s error=%v", deniedInterrupt, err)
	}
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","input":[{"role":"user","content":"DENIED_FRAME"}]}`)); err != nil {
		t.Fatal(err)
	}
	_, denied, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(denied, "type").String() != "error" || gjson.GetBytes(denied, "status").Int() != http.StatusForbidden {
		t.Fatalf("denied=%s error=%v", denied, err)
	}
	accessProvider.revoked.Store(false)
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer","previous_response_id":"home-native-r1","input":"continue"}`)); err != nil {
		t.Fatal(err)
	}
	_, accepted, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(accepted, "type").String() != "response.steer.accepted" {
		t.Fatalf("accepted=%s error=%v", accepted, err)
	}
	first, steer := <-frames, <-frames
	if got := gjson.GetBytes(first, "model").String(); got != "home-upstream-model" {
		t.Errorf("upstream model=%q frame=%s", got, first)
	}
	if got := gjson.GetBytes(first, "tools.0.type").String(); got != "tool_search" {
		t.Errorf("native declaration type=%q frame=%s", got, first)
	}
	if got := gjson.GetBytes(steer, "type").String(); got != "response.steer" {
		t.Errorf("steer frame=%s", steer)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	if got := dispatcher.calls.Load(); got != 1 {
		t.Fatalf("Home selections=%d", got)
	}
}

type homeToolsSequenceDispatcher struct {
	baseURLs []string
	calls    atomic.Int32
	models   chan string
}

func (*homeToolsSequenceDispatcher) HeartbeatOK() bool { return true }
func (d *homeToolsSequenceDispatcher) RPopAuth(_ context.Context, model, _ string, _ http.Header, _ int) ([]byte, error) {
	d.models <- model
	index := int(d.calls.Add(1)) - 1
	if index >= len(d.baseURLs) {
		return nil, errors.New("unexpected Home selection")
	}
	return json.Marshal(coreauth.Auth{
		ID: fmt.Sprintf("home-tools-retry-%d", index), Provider: "codex", Status: coreauth.StatusActive,
		Attributes: map[string]string{"websockets": "true", "api_key": "test-key", "auth_kind": "oauth", "base_url": d.baseURLs[index], "home_upstream_model": "home-upstream-model"},
	})
}
func (*homeToolsSequenceDispatcher) AbortAmbiguousDispatch() {}

func TestResponsesWebsocketHomeRetryRepreparesEachSelectedRoute(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"bridge_to_native", "bridge", "native"},
		{"native_to_bridge", "native", "bridge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var failedAttempts atomic.Int32
			failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				failedAttempts.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(failed.Close)
			frames := make(chan []byte, 2)
			errs := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					errs <- err
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
				_, b, err := c.ReadMessage()
				if err != nil {
					errs <- err
					return
				}
				frames <- b
				if tc.second == "bridge" {
					if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"retry-r1","status":"completed","output":[]}}`)); err != nil {
						errs <- err
					}
					_, _, _ = c.ReadMessage()
					return
				}
				if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"retry-r1","output":[]}}`)); err != nil {
					errs <- err
					return
				}
				_, b, err = c.ReadMessage()
				if err != nil {
					errs <- err
					return
				}
				frames <- b
				if err = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer.accepted","steer":{"id":"retry-steer","previous_response_id":"retry-r1"}}`)); err != nil {
					errs <- err
					return
				}
				_, _, _ = c.ReadMessage()
			}))
			t.Cleanup(upstream.Close)
			cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
			cfg.Codex.ResponseSteering, cfg.CodexResponseSteering = true, true
			for _, route := range []struct{ endpoint, mode string }{{failed.URL, tc.first}, {upstream.URL, tc.second}} {
				cfg.ResponsesTools.Routes = append(cfg.ResponsesTools.Routes, config.ResponsesToolsRoute{
					Match: config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: route.endpoint}, ClientSearch: route.mode, CustomTools: "inherit",
				})
			}
			cfg.NormalizeResponsesToolsConfig()
			if err := cfg.ValidateResponsesToolsConfig(); err != nil {
				t.Fatal(err)
			}
			mgr := coreauth.NewManager(nil, nil, nil)
			mgr.SetConfig(cfg)
			mgr.SetRetryConfig(0, 0, 2)
			dispatcher := &homeToolsSequenceDispatcher{baseURLs: []string{failed.URL, upstream.URL}, models: make(chan string, 2)}
			mgr.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
			mgr.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
			reg := registry.GetGlobalRegistry()
			for _, id := range []string{"home-tools-retry-0", "home-tools-retry-1"} {
				reg.RegisterClient(id, "codex", []*registry.ModelInfo{{ID: "home-client-alias"}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
			}
			h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, mgr))
			router := gin.New()
			router.GET("/v1/responses", h.ResponsesWebsocket)
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
			if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[]}`)); err != nil {
				t.Fatal(err)
			}
			_, response, err := conn.ReadMessage()
			wantType := "response.completed"
			if tc.second == "native" {
				wantType = "response.created"
			}
			if err != nil || gjson.GetBytes(response, "type").String() != wantType {
				t.Fatalf("response=%s err=%v, want %s", response, err, wantType)
			}
			frame := <-frames
			wantTool := "function"
			if tc.second == "native" {
				wantTool = "tool_search"
			}
			if gjson.GetBytes(frame, "tools.0.type").String() != wantTool || gjson.GetBytes(frame, "model").String() != "home-upstream-model" {
				t.Fatalf("retry route payload=%s, want %s", frame, wantTool)
			}
			if tc.second == "native" {
				if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.steer","previous_response_id":"retry-r1","input":"follow"}`)); err != nil {
					t.Fatal(err)
				}
				_, accepted, err := conn.ReadMessage()
				if err != nil || gjson.GetBytes(accepted, "type").String() != "response.steer.accepted" {
					t.Fatalf("accepted=%s err=%v", accepted, err)
				}
				if steer := <-frames; gjson.GetBytes(steer, "type").String() != "response.steer" {
					t.Fatalf("steer=%s", steer)
				}
			}
			select {
			case err := <-errs:
				t.Fatal(err)
			default:
			}
			if failedAttempts.Load() != 1 || dispatcher.calls.Load() != 2 {
				t.Fatalf("failed dials=%d Home selections=%d", failedAttempts.Load(), dispatcher.calls.Load())
			}
			for range 2 {
				if model := <-dispatcher.models; model != "home-client-alias" {
					t.Errorf("Home selection used model %q", model)
				}
			}
		})
	}
}
