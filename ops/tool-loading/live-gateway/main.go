// This standalone candidate reads an operator snapshot into memory only.
// Launch it with a filtered environment and synthetic acceptance requests.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/acceptance"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

type liveProfile struct {
	ClientModel   string `json:"client_model"`
	UpstreamModel string `json:"upstream_model"`
	Executor      string `json:"executor"`
	Contract      string `json:"contract"`
}

func (p liveProfile) validate() error {
	if strings.TrimSpace(p.ClientModel) == "" || strings.TrimSpace(p.UpstreamModel) == "" ||
		!regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`).MatchString(p.Executor) {
		return errors.New("invalid acceptance route profile")
	}
	expected := "openai-chat-v1"
	switch p.Executor {
	case "codex":
		expected = "openai-responses-v1"
	case "gemini":
		expected = "gemini-generate-v1"
	case "claude":
		expected = "anthropic-messages-v1"
	default:
		if !strings.HasPrefix(p.Executor, "openai-compatible-") {
			return errors.New("executor wire contract has no acceptance profile")
		}
	}
	if p.Contract != expected {
		return errors.New("executor and explicit wire contract disagree")
	}
	return nil
}

func fingerprint(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

type snapshotStore struct{ auths []*coreauth.Auth }

func cloneMetadataValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = cloneMetadataValue(child)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = cloneMetadataValue(child)
		}
		return result
	case map[string]string:
		result := make(map[string]string, len(value))
		for key, child := range value {
			result[key] = child
		}
		return result
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}

func cloneSnapshot(item *coreauth.Auth) *coreauth.Auth {
	clone := item.Clone()
	if item.Metadata != nil {
		clone.Metadata = cloneMetadataValue(item.Metadata).(map[string]any)
	}
	clone.Storage = nil
	return clone
}

func newSnapshotStore(source []*coreauth.Auth, provider string) *snapshotStore {
	store := &snapshotStore{}
	for _, item := range source {
		if item != nil && item.Provider == provider {
			clone := cloneSnapshot(item)
			// Shared OAuth refresh can rotate the original service's credential.
			// This candidate uses only the current access token; expired snapshots
			// are unavailable, never silently refreshed or made to look successful.
			removeRefreshCredentials(clone.Metadata)
			for _, key := range []string{"refresh_token", "refreshToken"} {
				delete(clone.Attributes, key)
			}
			clone.NextRefreshAfter = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
			store.auths = append(store.auths, clone)
		}
	}
	return store
}

func removeRefreshCredentials(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "refresh_token" || key == "refreshToken" {
				delete(value, key)
			} else {
				removeRefreshCredentials(child)
			}
		}
	case []any:
		for _, child := range value {
			removeRefreshCredentials(child)
		}
	case map[string]string:
		delete(value, "refresh_token")
		delete(value, "refreshToken")
	}
}

func (s *snapshotStore) List(context.Context) ([]*coreauth.Auth, error) {
	result := make([]*coreauth.Auth, 0, len(s.auths))
	for _, item := range s.auths {
		clone := cloneSnapshot(item)
		result = append(result, clone)
	}
	return result, nil
}

// The runtime manager owns ephemeral updates; none are persisted to a source.
func (*snapshotStore) Save(context.Context, *coreauth.Auth) (string, error) { return "", nil }
func (*snapshotStore) Delete(context.Context, string) error {
	return errors.New("acceptance credential snapshot is read-only")
}

func isolateConfig(source *config.Config, state string, port int) *config.Config {
	isolated := *source
	isolated.Host, isolated.Port = "127.0.0.1", port
	isolated.AuthDir = filepath.Join(state, "auths")
	isolated.APIKeys = []string{"local-acceptance-placeholder"}
	isolated.TLS.Enable = false
	isolated.LoggingToFile, isolated.RequestLog = false, false
	isolated.ErrorLogsMaxFiles, isolated.CommercialMode = 0, true
	isolated.SaveCooldownStatus, isolated.Pprof.Enable = false, false
	isolated.Discovery.Enabled, isolated.Home.Enabled, isolated.Plugins.Enabled = false, false, false
	isolated.RemoteManagement.SecretKey = ""
	isolated.RemoteManagement.DisableControlPanel, isolated.RemoteManagement.DisableAutoUpdatePanel = true, true
	return &isolated
}

func guardObserver(profile liveProfile, observer helps.HTTPAttemptObserver, authAllowed func(string) bool) helps.HTTPAttemptObserver {
	return func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		identity := helps.HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Executor != profile.Executor || identity.ModelSHA256 != fingerprint([]byte(profile.UpstreamModel)) || identity.AuthSHA256 == "" || !authAllowed(identity.AuthSHA256) {
			return nil, errors.New("actual acceptance route differs from pinned scope")
		}
		return observer(req, next)
	}
}

func readLimited(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("configuration snapshot unavailable or oversized")
	}
	return data, nil
}

func closeEvidence(journal *acceptance.HTTPAttemptJournal, directory string) error {
	errClose := journal.CloseContext(context.Background())
	file, err := os.OpenFile(filepath.Join(directory, "admission.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.Join(errClose, err)
	}
	errEncode := json.NewEncoder(file).Encode(journal.AdmissionDiagnostics())
	return errors.Join(errClose, errEncode, file.Sync(), file.Close())
}

func run() error {
	configPath := flag.String("config", "", "Absolute operator config source, read only")
	state := flag.String("state-dir", "", "New absolute private directory")
	port := flag.Int("port", 0, "Explicit temporary loopback port; collision fails")
	clientModel := flag.String("client-model", "", "Only admitted public model ID")
	upstreamModel := flag.String("upstream-model", "", "Pinned actual executor model ID")
	provider := flag.String("executor", "", "Pinned actual executor identifier")
	contract := flag.String("contract", "", "Explicit upstream usage/wire contract")
	maxAttempts := flag.Int("max-attempts", 16, "Maximum observed upstream attempts")
	flag.Parse()
	profile := liveProfile{*clientModel, *upstreamModel, *provider, *contract}
	if flag.NArg() != 0 || !filepath.IsAbs(*configPath) || !filepath.IsAbs(*state) || *port < 1024 || *port > 65535 || *maxAttempts < 1 || *maxAttempts > 32 {
		return errors.New("invalid isolated candidate arguments")
	}
	if err := profile.validate(); err != nil {
		return err
	}
	if os.Getenv("MANAGEMENT_PASSWORD") != "" {
		return errors.New("acceptance requires filtered management environment")
	}
	log.SetOutput(io.Discard)
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	raw, err := readLimited(*configPath)
	if err != nil {
		return err
	}
	source, err := config.ParseConfigBytes(raw)
	if err != nil {
		return errors.New("cannot parse operator configuration snapshot")
	}
	policy, err := json.Marshal(source.ResponsesTools)
	if err != nil {
		return err
	}
	var auths []*coreauth.Auth
	if profile.Executor == "codex" || profile.Executor == "gemini" || profile.Executor == "claude" {
		directory, err := util.ResolveAuthDir(source.AuthDir)
		if err != nil {
			return err
		}
		store := sdkauth.NewFileTokenStore()
		store.SetBaseDir(directory)
		auths, err = store.List(context.Background())
		if err != nil {
			return errors.New("cannot load selected credential snapshot")
		}
	}
	if err := os.Mkdir(*state, 0700); err != nil {
		return err
	}
	journal, err := acceptance.NewHTTPAttemptJournal(filepath.Join(*state, "capture"), profile.Contract, 1<<20, *maxAttempts)
	if err != nil {
		return err
	}
	cfg := isolateConfig(source, *state, *port)
	store := newSnapshotStore(auths, profile.Executor)
	sdkauth.RegisterTokenStore(store)
	var manager *coreauth.Manager
	readyNonce := uuid.NewString()
	ready := map[string]any{"version": 1, "scope": "isolated-live-candidate", "pid": os.Getpid(),
		"base_url": fmt.Sprintf("http://127.0.0.1:%d/v1", *port), "ready_nonce": readyNonce,
		"profile": profile, "source_config_sha256": fingerprint(raw), "source_policy_sha256": fingerprint(policy),
		"isolation_version": 3, "credential_refresh_disabled": true}
	var allowedAuths atomic.Value
	allowedAuths.Store(map[string]bool{})
	observer := guardObserver(profile, journal.Observe, func(id string) bool { return allowedAuths.Load().(map[string]bool)[id] })
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	var readyError error
	service, err := cliproxy.NewBuilder().WithConfig(cfg).WithConfigPath(filepath.Join(*state, "absent-config.yaml")).
		WithWatcherFactory(func(string, string, func(*config.Config)) (*cliproxy.WatcherWrapper, error) {
			return &cliproxy.WatcherWrapper{}, nil
		}).
		WithHooks(cliproxy.Hooks{OnBeforeStart: func(*config.Config) {
			if manager == nil {
				readyError = errors.New("SDK auth manager was not captured")
				cancel()
				return
			}
			var ids []string
			allowed := map[string]bool{}
			for _, item := range manager.List() {
				if item.Provider == profile.Executor {
					id := fingerprint([]byte(item.ID))
					ids = append(ids, id)
					allowed[id] = true
				}
			}
			sort.Strings(ids)
			encoded, _ := json.Marshal(ids)
			ready["selected_auth_ids_sha256"] = fingerprint(encoded)
			ready["selected_auth_ids"] = ids
			ready["selected_auth_count"] = len(ids)
			allowedAuths.Store(allowed)
		}, OnAfterStart: func(*cliproxy.Service) {
			if readyError != nil {
				return
			}
			if readyError = json.NewEncoder(os.Stdout).Encode(ready); readyError != nil {
				cancel()
			}
		}}).WithServerOptions(api.WithRouterConfigurator(func(_ *gin.Engine, handler *handlers.BaseAPIHandler, _ *config.Config) {
		manager = handler.AuthManager
	}), api.WithMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer local-acceptance-placeholder" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/acceptance/ready" {
			c.JSON(http.StatusOK, gin.H{"pid": os.Getpid(), "ready_nonce": readyNonce})
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/v1/models" {
			c.Next()
			return
		}
		if c.Request.Method != http.MethodPost || (c.Request.URL.Path != "/v1/responses" && c.Request.URL.Path != "/v1/chat/completions") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		var request struct {
			Model string `json:"model"`
		}
		if err != nil || json.Unmarshal(body, &request) != nil || request.Model != profile.ClientModel {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request = c.Request.WithContext(helps.WithHTTPAttemptObserver(c.Request.Context(), observer))
		c.Next()
	})).Build()
	if err != nil {
		return errors.Join(err, closeEvidence(journal, filepath.Join(*state, "capture")))
	}
	err = service.Run(ctx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	// The launcher owns the cleanup budget. Do not reuse the canceled service
	// context or add a network deadline while asynchronous readers release bodies.
	return errors.Join(err, readyError, closeEvidence(journal, filepath.Join(*state, "capture")))
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "isolated live candidate failed; evidence remains unverified")
		os.Exit(1)
	}
}
