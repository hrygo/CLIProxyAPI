// The acceptance gateway serves only synthetic loopback fixtures. It never
// reads operator configuration, token stores, plugins or environment credentials.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/acceptance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

type fixtureGateway struct {
	handler    http.Handler
	journal    *acceptance.HTTPAttemptJournal
	authID     string
	contract   string
	policyHash string
}

func newFixtureGateway(upstream, provider, state string, maxAttempts int) (*fixtureGateway, error) {
	address, err := url.Parse(upstream)
	if err != nil || address.Scheme != "http" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || address.Path != "/v1" ||
		(address.Hostname() != "127.0.0.1" && address.Hostname() != "::1" && address.Hostname() != "localhost") {
		return nil, errors.New("acceptance requires a synthetic loopback /v1 upstream")
	}
	contract := ""
	upstreamFormat := ""
	switch provider {
	case "codex":
		contract = "openai-responses-v1"
		upstreamFormat = "codex"
	case "fixture-chat":
		contract = "openai-chat-v1"
		upstreamFormat = "openai"
	default:
		return nil, errors.New("unknown fixture executor")
	}
	if !filepath.IsAbs(state) || maxAttempts < 1 || maxAttempts > 32 {
		return nil, errors.New("invalid fixture state or request budget")
	}
	if err := os.Mkdir(state, 0700); err != nil {
		return nil, fmt.Errorf("create fixture state: %w", err)
	}
	journal, err := acceptance.NewHTTPAttemptJournal(filepath.Join(state, "capture"), contract, 1<<20, maxAttempts)
	if err != nil {
		return nil, err
	}
	enabled := true
	cfg := &config.Config{Host: "127.0.0.1", AuthDir: filepath.Join(state, "auths"), CommercialMode: true}
	cfg.ResponsesTools.Enabled = &enabled
	cfg.ResponsesTools.Routes = []config.ResponsesToolsRoute{{Match: config.ResponsesToolsMatch{Provider: provider, AuthKind: "api-key", UpstreamModel: "fixture-model", UpstreamFormat: upstreamFormat, BaseURL: upstream}, ClientSearch: "bridge"}}
	cfg.NormalizeResponsesToolsConfig()
	if err := cfg.ValidateResponsesToolsConfig(); err != nil {
		return nil, errors.Join(err, journal.Close())
	}
	cfg.RemoteManagement.DisableControlPanel = true
	cfg.RemoteManagement.DisableAutoUpdatePanel = true
	manager := auth.NewManager(nil, &auth.RoundRobinSelector{}, nil)
	manager.SetConfig(cfg)
	if provider == "codex" {
		manager.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))
	} else {
		manager.RegisterExecutor(runtimeexecutor.NewOpenAICompatExecutor(provider, cfg))
	}
	authID := "acceptance-" + uuid.NewString()
	registry.GetGlobalRegistry().RegisterClient(authID, provider, []*registry.ModelInfo{{ID: "fixture-model"}})
	if _, err := manager.Register(context.Background(), &auth.Auth{
		ID: authID, Provider: provider, Status: auth.StatusActive,
		Attributes: map[string]string{"base_url": upstream, "api_key": "synthetic-fixture-only", "auth_kind": "api-key"},
		Metadata:   map[string]any{"disable_cooling": true},
	}); err != nil {
		registry.GetGlobalRegistry().UnregisterClient(authID)
		return nil, errors.Join(err, journal.Close())
	}
	var engine *gin.Engine
	api.NewServer(cfg, manager, access.NewManager(), filepath.Join(state, "absent-config.yaml"),
		api.WithEngineConfigurator(func(value *gin.Engine) { engine = value }),
		api.WithMiddleware(func(c *gin.Context) {
			if c.Request.Method != http.MethodPost || (c.Request.URL.Path != "/v1/responses" && c.Request.URL.Path != "/v1/chat/completions") {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
			if c.GetHeader("Authorization") != "Bearer local-acceptance-placeholder" {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
			c.Request = c.Request.WithContext(helps.WithHTTPAttemptObserver(c.Request.Context(), journal.Observe))
			c.Next()
		}))
	policy, err := json.Marshal(cfg.ResponsesTools)
	if err != nil {
		registry.GetGlobalRegistry().UnregisterClient(authID)
		return nil, errors.Join(err, journal.Close())
	}
	digest := sha256.Sum256(policy)
	return &fixtureGateway{handler: engine, journal: journal, authID: authID, contract: contract, policyHash: hex.EncodeToString(digest[:])}, nil
}

func (g *fixtureGateway) close() error {
	registry.GetGlobalRegistry().UnregisterClient(g.authID)
	return g.journal.Close()
}

func run() error {
	upstream := flag.String("upstream-url", "", "Synthetic loopback /v1 endpoint")
	provider := flag.String("executor", "", "codex or fixture-chat")
	state := flag.String("state-dir", "", "New absolute private directory")
	maxAttempts := flag.Int("max-attempts", 16, "Maximum observed upstream HTTP attempts")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected acceptance arguments")
	}
	log.SetOutput(io.Discard)
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	gateway, err := newFixtureGateway(*upstream, *provider, *state, *maxAttempts)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.Join(err, gateway.close())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: gateway.handler, BaseContext: func(net.Listener) context.Context { return ctx }}
	ready := map[string]any{"version": 1, "scope": "synthetic-loopback-gateway", "pid": os.Getpid(),
		"base_url": "http://" + listener.Addr().String() + "/v1", "executor": *provider,
		"contract": gateway.contract, "fixture_policy_sha256": gateway.policyHash}
	if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
		return errors.Join(err, listener.Close(), gateway.close())
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		err = server.Close()
		errServe := <-served
		if !errors.Is(errServe, http.ErrServerClosed) {
			err = errors.Join(err, errServe)
		}
	case err = <-served:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	return errors.Join(err, gateway.close())
}

func main() {
	if err := run(); err != nil {
		// Never print an error that could contain request bodies or addresses.
		_, _ = fmt.Fprintln(os.Stderr, "isolated acceptance gateway failed; evidence remains unverified")
		os.Exit(1)
	}
}
