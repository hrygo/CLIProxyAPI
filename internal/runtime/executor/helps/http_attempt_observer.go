package helps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTPAttemptObserver is an opt-in transport hook for isolated acceptance runs.
// It runs after proxy/TLS selection but before dispatch. Returning an error before
// calling next prevents dispatch and does not mark an upstream transport attempt.
// Requests may contain secrets: observers must not persist headers or addresses.
// This hook covers instrumented HTTP transports, not WebSocket traffic.
type HTTPAttemptObserver func(req *http.Request, next http.RoundTripper) (*http.Response, error)

type httpAttemptObserverKey struct{}
type httpAttemptObserverActiveKey struct{}
type httpAttemptIdentityKey struct{}

// HTTPAttemptIdentity names the executor that actually owns the transport.
// Model and credential record IDs are fingerprints, not exposed identifiers.
// This does not infer a wire protocol from a provider/model name.
// Auxiliary operations have a fixed operation and no model fingerprint.
type HTTPAttemptIdentity struct {
	Executor    string `json:"executor"`
	ModelSHA256 string `json:"model_sha256"`
	AuthSHA256  string `json:"auth_sha256"`
	Operation   string `json:"operation,omitempty"`
}

func HTTPAttemptIdentityFromContext(ctx context.Context) *HTTPAttemptIdentity {
	if ctx == nil {
		return nil
	}
	identity, ok := ctx.Value(httpAttemptIdentityKey{}).(HTTPAttemptIdentity)
	if !ok {
		return nil
	}
	return &identity
}

func httpAttemptFingerprint(value string) string {
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (r *UsageReporter) httpAttemptIdentity() HTTPAttemptIdentity {
	return HTTPAttemptIdentity{Executor: r.provider, ModelSHA256: httpAttemptFingerprint(r.model), AuthSHA256: httpAttemptFingerprint(r.authID)}
}

// WithHTTPAttemptObserver attaches observation to one request context. There is
// no global configuration or default observer; nil disables inherited observation.
func WithHTTPAttemptObserver(ctx context.Context, observer HTTPAttemptObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpAttemptObserverKey{}, observer)
}

func httpAttemptObserverValue(ctx context.Context) any {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(httpAttemptObserverKey{})
	if value == nil {
		if c, ok := ctx.Value("gin").(*gin.Context); ok && c != nil && c.Request != nil {
			value = c.Request.Context().Value(httpAttemptObserverKey{})
		}
	}
	return value
}

// CopyHTTPAttemptObserver copies only the opt-in hook to an independent context.
// It preserves the destination lifetime and an explicitly disabled observer.
func CopyHTTPAttemptObserver(dst, src context.Context) context.Context {
	if dst == nil {
		dst = context.Background()
	}
	if httpAttemptObserverValue(dst) != nil {
		return dst
	}
	if observer, ok := httpAttemptObserverValue(src).(HTTPAttemptObserver); ok {
		return WithHTTPAttemptObserver(dst, observer)
	}
	return dst
}

type observedRoundTripFunc func(*http.Request) (*http.Response, error)

func (f observedRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func observeHTTPAttempt(req *http.Request, next observedRoundTripFunc, identity func() HTTPAttemptIdentity) (*http.Response, error) {
	ctx := req.Context()
	// HTTP handlers may supply a fresh parent with Gin attached separately.
	value := httpAttemptObserverValue(ctx)
	observer, _ := value.(HTTPAttemptObserver)
	active, _ := ctx.Value(httpAttemptObserverActiveKey{}).(bool)
	if observer == nil || active {
		return next(req)
	}
	// Nested usage wrappers still dispatch once; retries and redirects receive
	// their own outer observation, since the caller's context is not modified.
	ctx = context.WithValue(ctx, httpAttemptIdentityKey{}, identity())
	observed := req.WithContext(context.WithValue(ctx, httpAttemptObserverActiveKey{}, true))
	return observer(observed, next)
}
