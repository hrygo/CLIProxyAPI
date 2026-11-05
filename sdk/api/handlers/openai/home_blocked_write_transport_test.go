package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executionregistry"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// The zero-buffer pipe makes a WebSocket Write block until the peer reads or
// closes. Arm only after the first response frame has been completely read.
type homeBlockedWriteConn struct {
	net.Conn
	armed    atomic.Bool
	started  chan struct{}
	finished chan struct{}
}

func (c *homeBlockedWriteConn) Write(p []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		close(c.started)
		defer close(c.finished)
	}
	return c.Conn.Write(p)
}

type homeBlockedPipeListener struct {
	conn chan net.Conn
	done chan struct{}
	once sync.Once
}

func (l *homeBlockedPipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conn:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *homeBlockedPipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (*homeBlockedPipeListener) Addr() net.Addr { return homeBlockedPipeAddr{} }

type homeBlockedPipeAddr struct{}

func (homeBlockedPipeAddr) Network() string { return "pipe" }
func (homeBlockedPipeAddr) String() string  { return "home-blocked-write.test" }

type homeBlockedStreamObservation struct {
	ctx        context.Context
	sourceDone <-chan struct{}
	pumpDone   <-chan struct{}
	duplex     bool
}

type homeBlockedDispatcher struct {
	baseURL string
	id      string
	calls   atomic.Int32
}

func (*homeBlockedDispatcher) HeartbeatOK() bool { return true }
func (d *homeBlockedDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	d.calls.Add(1)
	return json.Marshal(map[string]any{
		"model": "home-upstream-model", "auth_index": d.id,
		"auth": coreauth.Auth{ID: d.id, Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{
			"websockets": "true", "api_key": "fixture-only", "auth_kind": "oauth", "base_url": d.baseURL,
		}},
		"concurrency": map[string]any{"accounted": true, "credential_id": d.id, "model": "home-upstream-model"},
	})
}
func (*homeBlockedDispatcher) AbortAmbiguousDispatch() {}

type homeBlockedRelease struct {
	group    executionregistry.ReleaseGroup
	sequence int64
}

type homeBlockedFixtureState struct {
	manager    *coreauth.Manager
	registry   *executionregistry.Registry
	dispatcher *homeBlockedDispatcher
	releases   chan homeBlockedRelease
}

type homeBlockedSourceExecutor struct {
	*runtimeexecutor.CodexAutoExecutor
	observed chan homeBlockedStreamObservation
}

func (e *homeBlockedSourceExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	result, err := e.CodexAutoExecutor.ExecuteStream(ctx, auth, req, opts)
	if err != nil || result == nil || result.Chunks == nil {
		return result, err
	}
	out := make(chan coreexecutor.StreamChunk)
	sourceDone := make(chan struct{})
	pumpDone := make(chan struct{})
	e.observed <- homeBlockedStreamObservation{ctx: ctx, sourceDone: sourceDone, pumpDone: pumpDone, duplex: coreexecutor.WebsocketInputFromContext(ctx) != nil}
	go func() {
		defer close(pumpDone)
		defer close(out)
		forward := true
		for chunk := range result.Chunks {
			if !forward {
				continue
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				// Drain only after cancellation, so source closure is observable
				// even when its final send was already in progress.
				forward = false
			}
		}
		close(sourceDone)
	}()
	return &coreexecutor.StreamResult{Headers: result.Headers, Chunks: out}, nil
}

func homeBlockedFixture(t *testing.T, upstreamURL, mode string) (*homeBlockedFixtureState, *homeBlockedSourceExecutor, http.Handler) {
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
	f := &homeBlockedFixtureState{
		manager:    coreauth.NewManager(nil, nil, nil),
		registry:   executionregistry.New(),
		dispatcher: &homeBlockedDispatcher{baseURL: upstreamURL, id: "home-blocked-" + strings.ReplaceAll(t.Name(), "/", "-")},
		releases:   make(chan homeBlockedRelease, 4),
	}
	executor := &homeBlockedSourceExecutor{CodexAutoExecutor: runtimeexecutor.NewCodexAutoExecutor(cfg), observed: make(chan homeBlockedStreamObservation, 4)}
	f.registry.SetReleaseSink(func(group executionregistry.ReleaseGroup, sequence int64) {
		f.releases <- homeBlockedRelease{group: group, sequence: sequence}
	})
	f.manager.SetConfig(cfg)
	f.manager.PublishHomeDispatch(f.dispatcher, f.registry, 1)
	f.manager.RegisterExecutor(executor)
	registry.GetGlobalRegistry().RegisterClient(f.dispatcher.id, "codex", []*registry.ModelInfo{{ID: "home-client-alias"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(f.dispatcher.id) })
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&cfg.SDKConfig, f.manager))
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	return f, executor, router
}

func homeBlockedWait(t *testing.T, ctx context.Context, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
		return
	default:
	}
	select {
	case <-done:
	case <-ctx.Done():
		select {
		case <-done:
		default:
			t.Errorf("%s did not finish before the deadline", what)
		}
	}
}

func homeBlockedWrite(t *testing.T, c *websocket.Conn, payload string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write downstream request: %v", err)
	}
}

func homeBlockedRead(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read downstream response: %v", err)
	}
	return payload
}

func TestResponsesWebsocketHomeBlockedWriteReleasesResources(t *testing.T) {
	for _, mode := range []string{"native", "bridge"} {
		for _, action := range []string{"drain", "upstream_disconnect", "downstream_disconnect"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				sendDelta := make(chan struct{})
				upstreamRequests := make(chan []byte, 1)
				stopUpstream := make(chan struct{})
				var stopOnce sync.Once
				stop := func() { stopOnce.Do(func() { close(stopUpstream) }) }
				upstreamDone := make(chan struct{})
				upstreamReaderDone := make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(upstreamDone)
					conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						t.Errorf("upgrade upstream: %v", err)
						return
					}
					defer conn.Close()
					_, request, err := conn.ReadMessage()
					if err != nil {
						t.Errorf("read upstream request: %v", err)
						return
					}
					upstreamRequests <- request
					if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"blocked","status":"in_progress","output":[]}}`)); err != nil {
						t.Errorf("write upstream created: %v", err)
						return
					}
					select {
					case <-sendDelta:
					case <-stopUpstream:
						return
					}
					if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_text.delta","response_id":"blocked","delta":"blocked-write"}`)); err != nil {
						t.Errorf("write upstream delta: %v", err)
						return
					}
					go func() {
						defer close(upstreamReaderDone)
						_, _, _ = conn.ReadMessage()
					}()
					select {
					case <-upstreamReaderDone:
					case <-stopUpstream:
					}
				}))
				t.Cleanup(upstream.Close)
				t.Cleanup(stop)

				fixture, executor, handler := homeBlockedFixture(t, upstream.URL, mode)
				clientConn, serverConn := net.Pipe()
				watchedConn := &homeBlockedWriteConn{Conn: serverConn, started: make(chan struct{}), finished: make(chan struct{})}
				listener := &homeBlockedPipeListener{conn: make(chan net.Conn, 1), done: make(chan struct{})}
				listener.conn <- watchedConn
				handlerDone := make(chan struct{})
				handlerStarted := make(chan struct{})
				serveDone := make(chan struct{})
				server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(handlerStarted)
					defer close(handlerDone)
					handler.ServeHTTP(w, r)
				})}
				go func() {
					defer close(serveDone)
					if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
						t.Errorf("serve downstream: %v", err)
					}
				}()
				t.Cleanup(func() {
					stop()
					_ = clientConn.Close()
					_ = serverConn.Close()
					_ = server.Close()
					<-serveDone
					select {
					case <-handlerStarted:
						select {
						case <-handlerDone:
						case <-time.After(3 * time.Second):
							t.Error("cleanup did not unblock downstream handler")
						}
					default:
					}
					select {
					case duplicate := <-fixture.releases:
						t.Errorf("extra Home release after cleanup: %+v", duplicate)
					default:
					}
				})
				clientURL, err := url.Parse("ws://home-blocked-write.test/v1/responses")
				if err != nil {
					t.Fatal(err)
				}
				client, _, err := websocket.NewClient(clientConn, clientURL, nil, 4096, 4096)
				if err != nil {
					t.Fatalf("dial downstream: %v", err)
				}
				homeBlockedWrite(t, client, `{"type":"response.create","model":"home-client-alias","tools":[{"type":"tool_search"}],"input":[]}`)
				created := homeBlockedRead(t, client)
				if got := gjson.GetBytes(created, "type").String(); got != "response.created" {
					t.Fatalf("first event = %q, want response.created", got)
				}
				select {
				case request := <-upstreamRequests:
					wantType := "tool_search"
					if mode == "bridge" {
						wantType = "function"
					}
					if got := gjson.GetBytes(request, "tools.0.type").String(); got != wantType {
						t.Fatalf("upstream tool type = %q, want %q", got, wantType)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("upstream request was not observed")
				}
				var observation homeBlockedStreamObservation
				select {
				case observation = <-executor.observed:
				case <-time.After(3 * time.Second):
					t.Fatal("upstream stream was not observed")
				}
				if observation.duplex != (mode == "native") {
					t.Fatalf("duplex = %v, mode = %s", observation.duplex, mode)
				}
				watchedConn.armed.Store(true)
				close(sendDelta)
				select {
				case <-watchedConn.started:
				case <-time.After(3 * time.Second):
					t.Fatal("downstream WebSocket write did not start")
				}
				select {
				case <-watchedConn.finished:
					t.Fatal("downstream WebSocket write returned before the peer read or disconnected")
				default:
				}

				drainDone := make(chan error, 1)
				switch action {
				case "drain":
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						drainDone <- fixture.registry.Drain(ctx)
					}()
				case "upstream_disconnect":
					stop()
				case "downstream_disconnect":
					_ = client.Close()
				}
				waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancelWait()
				select {
				case got := <-fixture.releases:
					want := executionregistry.ReleaseGroup{CredentialID: fixture.dispatcher.id, Model: "home-upstream-model"}
					if got.group != want || got.sequence != 1 {
						t.Errorf("Home release = %+v, want group=%+v sequence=1", got, want)
					}
				case <-waitCtx.Done():
					t.Error("Home slot was not released")
				}
				homeBlockedWait(t, waitCtx, upstreamDone, "upstream connection")
				homeBlockedWait(t, waitCtx, upstreamReaderDone, "upstream reader")
				homeBlockedWait(t, waitCtx, observation.sourceDone, "executor source")
				homeBlockedWait(t, waitCtx, observation.pumpDone, "source observation pump")
				homeBlockedWait(t, waitCtx, observation.ctx.Done(), "execution attempt")
				if executions := fixture.registry.FreezeInFlight(time.Now()).Executions; len(executions) != 0 {
					t.Errorf("Home still counts %d in-flight executions after release", len(executions))
				}
				if action == "drain" {
					select {
					case err := <-drainDone:
						if err != nil {
							t.Errorf("Drain: %v", err)
						}
					case <-waitCtx.Done():
						t.Error("Drain did not return")
					}
				}
				if action != "downstream_disconnect" {
					select {
					case <-handlerDone:
						t.Log("downstream handler exited before the peer closed")
					case <-time.After(250 * time.Millisecond):
						t.Log("downstream handler remained blocked until the peer closed")
					}
					_ = client.Close()
				}
				homeBlockedWait(t, waitCtx, watchedConn.finished, "downstream WebSocket write after peer close")
				homeBlockedWait(t, waitCtx, handlerDone, "downstream handler and input reader after peer close")
				select {
				case duplicate := <-fixture.releases:
					t.Errorf("unexpected extra Home release: %+v", duplicate)
				default:
				}
				if got := fixture.dispatcher.calls.Load(); got != 1 {
					t.Errorf("Home dispatches = %d, want 1", got)
				}
			})
		}
	}
}
