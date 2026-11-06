package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executionregistry"
)

type homeToolsReplayDispatcher struct {
	baseURL string
	calls   atomic.Int32
}

func (*homeToolsReplayDispatcher) HeartbeatOK() bool { return true }
func (d *homeToolsReplayDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	d.calls.Add(1)
	return json.Marshal(coreauth.Auth{ID: "home-tools-replay", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"websockets": "true", "api_key": "test-key", "auth_kind": "oauth", "base_url": d.baseURL, "home_upstream_model": "home-upstream-model"}})
}
func (*homeToolsReplayDispatcher) AbortAmbiguousDispatch() {}

func TestResponsesWebsocketHomePreparationRejectsBeforeDial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
	cfg.Codex.ResponseSteering = true
	cfg.CodexResponseSteering = true
	cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{Match: config.ResponsesToolsMatch{Provider: "codex", AuthKind: "oauth", UpstreamModel: "home-upstream-model", UpstreamFormat: "codex", BaseURL: upstream.URL}, ClientSearch: "native", CustomTools: "reject"}}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		t.Fatal(err)
	}
	mgr := coreauth.NewManager(nil, nil, nil)
	mgr.SetConfig(cfg)
	dispatcher := &homeToolsReplayDispatcher{baseURL: upstream.URL}
	mgr.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
	mgr.RegisterExecutor(runtimeexecutor.NewCodexAutoExecutor(cfg))
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("home-tools-replay", "codex", []*registry.ModelInfo{{ID: "home-client-alias"}})
	t.Cleanup(func() { reg.UnregisterClient("home-tools-replay") })
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, mgr))
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"home-client-alias","tools":[{"type":"custom","name":"patcher"}],"input":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(payload, "type").String() != "error" || gjson.GetBytes(payload, "status").Int() != http.StatusUnprocessableEntity {
		t.Fatalf("payload=%s err=%v, want preparation rejection", payload, err)
	}
	if n := dispatcher.calls.Load(); n != 1 {
		t.Fatalf("Home selections=%d, want 1", n)
	}
	if n := upstreamRequests.Load(); n != 0 {
		t.Fatalf("upstream requests=%d, want 0", n)
	}
}
