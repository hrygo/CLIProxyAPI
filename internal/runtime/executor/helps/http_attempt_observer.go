package helps

import (
	"context"
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

// WithHTTPAttemptObserver attaches observation to one request context. There is
// no global configuration or default observer; nil disables inherited observation.
func WithHTTPAttemptObserver(ctx context.Context, observer HTTPAttemptObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpAttemptObserverKey{}, observer)
}

type observedRoundTripFunc func(*http.Request) (*http.Response, error)

func (f observedRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func observeHTTPAttempt(req *http.Request, next observedRoundTripFunc) (*http.Response, error) {
	ctx := req.Context()
	value := ctx.Value(httpAttemptObserverKey{})
	if value == nil {
		// HTTP handlers intentionally use a fresh execution parent and attach
		// Gin separately. Recover only this explicit opt-in hook, preserving
		// their existing deadline/value isolation and cancellation semantics.
		if c, ok := ctx.Value("gin").(*gin.Context); ok && c != nil && c.Request != nil {
			value = c.Request.Context().Value(httpAttemptObserverKey{})
		}
	}
	observer, _ := value.(HTTPAttemptObserver)
	active, _ := ctx.Value(httpAttemptObserverActiveKey{}).(bool)
	if observer == nil || active {
		return next(req)
	}
	// Nested usage wrappers still dispatch once; retries and redirects receive
	// their own outer observation, since the caller's context is not modified.
	observed := req.WithContext(context.WithValue(ctx, httpAttemptObserverActiveKey{}, true))
	return observer(observed, next)
}
