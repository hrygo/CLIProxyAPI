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
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

type liveProfile struct {
	ClientModel   string              `json:"client_model"`
	UpstreamModel string              `json:"upstream_model"`
	Executor      string              `json:"executor"`
	Contract      string              `json:"contract"`
	AuthFiles     []credentialBinding `json:"auth_files,omitempty"`
}

func (p liveProfile) validate() error {
	if strings.TrimSpace(p.ClientModel) == "" || strings.TrimSpace(p.UpstreamModel) == "" ||
		!regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`).MatchString(p.Executor) {
		return errors.New("invalid acceptance route profile")
	}
	expected := "openai-chat-v1"
	switch p.Executor {
	case "codex", "meta":
		expected = "openai-responses-v1"
	case "gemini":
		expected = "gemini-generate-v1"
	case "antigravity":
		expected = "antigravity-generate-v1"
		if len(p.AuthFiles) == 0 {
			return errors.New("Antigravity profile requires explicit credential bindings")
		}
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
	var bindings []string
	for _, binding := range p.AuthFiles {
		bindings = append(bindings, binding.NameSHA256+":"+binding.ContentSHA256)
	}
	if _, err := parseCredentialBindings(bindings); err != nil {
		return err
	}
	if len(p.AuthFiles) > 0 && strings.HasPrefix(p.Executor, "openai-compatible-") {
		return errors.New("OAuth bindings cannot select a configured Chat provider")
	}
	return nil
}

func fingerprint(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func acceptanceCredentialMatches(authorization, apiKey string) bool {
	if authorization != "" && authorization != "Bearer local-acceptance-placeholder" {
		return false
	}
	if apiKey != "" && apiKey != "local-acceptance-placeholder" {
		return false
	}
	return authorization != "" || apiKey != ""
}

func acceptanceInferencePath(path string) bool {
	return path == "/v1/responses" || path == "/v1/chat/completions" || path == "/v1/messages"
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
			for _, key := range []string{coreauth.AttributePath, coreauth.AttributeSource, "dca_token"} {
				delete(clone.Attributes, key)
			}
			if strings.HasPrefix(clone.Attributes["access_token"], "dca:") {
				delete(clone.Attributes, "access_token")
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
			if key == "refresh_token" || key == "refreshToken" || key == "dca_token" || key == "access_token" && strings.HasPrefix(fmt.Sprint(child), "dca:") {
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
		delete(value, "dca_token")
		if strings.HasPrefix(value["access_token"], "dca:") {
			delete(value, "access_token")
		}
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
	// Coding fixtures grant only local MCP work, never provider-hosted images.
	isolated.DisableImageGeneration = config.DisableImageGenerationAll
	isolated.ErrorLogsMaxFiles, isolated.CommercialMode = 0, true
	isolated.SaveCooldownStatus, isolated.Pprof.Enable = false, false
	isolated.Discovery.Enabled, isolated.Home.Enabled, isolated.Plugins.Enabled = false, false, false
	isolated.RemoteManagement.SecretKey = ""
	isolated.RemoteManagement.DisableControlPanel, isolated.RemoteManagement.DisableAutoUpdatePanel = true, true
	return &isolated
}

func guardObserver(profile liveProfile, observer helps.HTTPAttemptObserver, authAllowed func(string) bool, auxiliaryAllowed func(*http.Request, *helps.HTTPAttemptIdentity) bool) helps.HTTPAttemptObserver {
	return func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		identity := helps.HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Executor != profile.Executor || identity.AuthSHA256 == "" || !authAllowed(identity.AuthSHA256) {
			return nil, errors.New("actual acceptance route differs from pinned scope")
		}
		inference := identity.Operation == "" && identity.ModelSHA256 == fingerprint([]byte(profile.UpstreamModel))
		auxiliary := profile.Executor == "antigravity" && profile.Contract == "antigravity-generate-v1" &&
			(identity.Operation == "credits_query" || identity.Operation == "model_discovery") && identity.ModelSHA256 == "" && auxiliaryAllowed != nil && auxiliaryAllowed(req, identity)
		if !inference && !auxiliary {
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
	var credentialSelections credentialFlags
	flag.Var(&credentialSelections, "auth-file-binding", "Explicit filename SHA-256:content SHA-256 within configured auth directory; repeat up to eight")
	maxAttempts := flag.Int("max-attempts", 16, "Maximum observed upstream attempts")
	flag.Parse()
	bindings, err := parseCredentialBindings(credentialSelections)
	if err != nil {
		return candidateFailure("arguments", "credential_binding_invalid", err)
	}
	profile := liveProfile{ClientModel: *clientModel, UpstreamModel: *upstreamModel, Executor: *provider, Contract: *contract, AuthFiles: bindings}
	if flag.NArg() != 0 || !filepath.IsAbs(*configPath) || !filepath.IsAbs(*state) || *port < 1024 || *port > 65535 || *maxAttempts < 1 || *maxAttempts > 32 {
		return candidateFailure("arguments", "invalid_arguments", errors.New("invalid isolated candidate arguments"))
	}
	if err := profile.validate(); err != nil {
		return candidateFailure("arguments", "invalid_profile", err)
	}
	if os.Getenv("MANAGEMENT_PASSWORD") != "" {
		return candidateFailure("arguments", "unsafe_environment", errors.New("acceptance requires filtered management environment"))
	}
	log.SetOutput(io.Discard)
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter, gin.DefaultErrorWriter = io.Discard, io.Discard
	raw, err := readLimited(*configPath)
	if err != nil {
		return candidateFailure("configuration", "configuration_unavailable", err)
	}
	source, err := config.ParseConfigBytes(raw)
	if err != nil {
		return candidateFailure("configuration", "configuration_invalid", errors.New("cannot parse operator configuration snapshot"))
	}
	policy, err := json.Marshal(source.ResponsesTools)
	if err != nil {
		return candidateFailure("configuration", "policy_invalid", err)
	}
	auths, err := loadSelectedCredentials(source, profile)
	if err != nil {
		return err
	}
	if err := os.Mkdir(*state, 0700); err != nil {
		return candidateFailure("evidence", "evidence_failed", err)
	}
	journal, err := acceptance.NewHTTPAttemptJournal(filepath.Join(*state, "capture"), profile.Contract, 1<<20, *maxAttempts)
	if err != nil {
		return candidateFailure("evidence", "evidence_failed", err)
	}
	cfg := isolateConfig(source, *state, *port)
	preparation := newPreparationEvidence()
	responseEvidence := newResponseEvidence()
	auxiliaryWork := helps.NewHTTPAuxiliaryWorkScope()
	isolateSelectedAPIKeys(cfg, profile)
	store := newSnapshotStore(auths, profile.Executor)
	sdkauth.RegisterTokenStore(store)
	var manager *coreauth.Manager
	readyNonce := uuid.NewString()
	ready := map[string]any{"version": 1, "scope": "isolated-live-candidate", "pid": os.Getpid(),
		"base_url": fmt.Sprintf("http://127.0.0.1:%d/v1", *port), "ready_nonce": readyNonce,
		"profile": profile, "source_config_sha256": fingerprint(raw), "source_policy_sha256": fingerprint(policy),
		"isolation_version": 7, "credential_refresh_disabled": true}
	var allowedAuths atomic.Value
	var auxiliaryRoutes atomic.Value
	initialAllowed := map[string]bool{}
	initialRoutes := map[string]map[string]string{}
	// Antigravity model registration performs HTTP before OnBeforeStart. Bind
	// that work to the explicit snapshots; readiness later verifies actual IDs.
	if profile.Executor == "antigravity" {
		for _, item := range store.auths {
			id := fingerprint([]byte(item.ID))
			routes, errRoutes := antigravityAuxiliaryEndpoints(item)
			if errRoutes != nil {
				return errors.Join(errRoutes, closeAuxiliaryWork(context.Background(), auxiliaryWork, filepath.Join(*state, "capture")),
					closeEvidence(journal, filepath.Join(*state, "capture")), preparation.close(filepath.Join(*state, "capture")), responseEvidence.close(filepath.Join(*state, "capture")))
			}
			initialAllowed[id], initialRoutes[id] = true, routes
		}
	}
	allowedAuths.Store(initialAllowed)
	auxiliaryRoutes.Store(initialRoutes)
	observer := guardObserver(profile, journal.Observe, func(id string) bool { return allowedAuths.Load().(map[string]bool)[id] },
		func(req *http.Request, identity *helps.HTTPAttemptIdentity) bool {
			return req.Method == http.MethodPost && req.URL.String() == auxiliaryRoutes.Load().(map[string]map[string]string)[identity.AuthSHA256][identity.Operation]
		})
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
			routes := map[string]map[string]string{}
			for _, item := range manager.List() {
				if item.Provider == profile.Executor {
					id := fingerprint([]byte(item.ID))
					if profile.Executor == "antigravity" {
						endpoint, errEndpoint := antigravityAuxiliaryEndpoints(item)
						if errEndpoint != nil {
							readyError = errEndpoint
							cancel()
							return
						}
						routes[id] = endpoint
					}
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
			auxiliaryRoutes.Store(routes)
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
		if !acceptanceCredentialMatches(c.GetHeader("Authorization"), c.GetHeader("X-Api-Key")) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/acceptance/ready" {
			c.JSON(http.StatusOK, gin.H{"pid": os.Getpid(), "ready_nonce": readyNonce})
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/acceptance/catalog-state" {
			c.JSON(http.StatusOK, gin.H{"pid": os.Getpid(), "ready_nonce": readyNonce,
				"active_auxiliary_work": auxiliaryWork.Snapshot().Active})
			c.Abort()
			return
		}
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/v1/models" {
			c.Next()
			return
		}
		if c.Request.Method != http.MethodPost || !acceptanceInferencePath(c.Request.URL.Path) {
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
		requestCtx, finish, errEvidence := preparation.begin(c.Request.Context())
		if errEvidence != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		defer finish()
		requestCtx, finishResponse, errResponseEvidence := responseEvidence.begin(requestCtx)
		if errResponseEvidence != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		defer finishResponse()
		requestCtx = helps.WithHTTPAuxiliaryWorkScope(requestCtx, auxiliaryWork)
		c.Request = c.Request.WithContext(helps.WithHTTPAttemptObserver(requestCtx, observer))
		c.Next()
	})).Build()
	if err != nil {
		return errors.Join(candidateFailure("startup", "startup_failed", err), closeAuxiliaryWork(context.Background(), auxiliaryWork, filepath.Join(*state, "capture")),
			closeEvidence(journal, filepath.Join(*state, "capture")), preparation.close(filepath.Join(*state, "capture")), responseEvidence.close(filepath.Join(*state, "capture")))
	}
	serviceCtx := helps.WithHTTPAttemptObserver(helps.WithHTTPAuxiliaryWorkScope(ctx, auxiliaryWork), observer)
	err = service.Run(serviceCtx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	// The launcher owns the cleanup budget. Do not reuse the canceled service
	// context or add a network deadline while asynchronous readers release bodies.
	return errors.Join(err, readyError, closeAuxiliaryWork(context.Background(), auxiliaryWork, filepath.Join(*state, "capture")),
		closeEvidence(journal, filepath.Join(*state, "capture")), preparation.close(filepath.Join(*state, "capture")), responseEvidence.close(filepath.Join(*state, "capture")), verifySelectedCredentialSources(source, profile))
}

func main() {
	if err := run(); err != nil {
		_ = writeCandidateFailure(os.Stderr, err)
		os.Exit(1)
	}
}
