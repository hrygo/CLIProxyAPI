package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAntigravityAuxiliaryObserverDeniesBeforeDispatch(t *testing.T) {
	denied := errors.New("synthetic admission denial")
	observed, dispatched := 0, 0
	ctx := WithHTTPAttemptObserver(context.Background(), func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		observed++
		identity := HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Executor != "antigravity" || identity.Operation != "credits_query" ||
			identity.AuthSHA256 != httpAttemptFingerprint("synthetic-auth") || identity.ModelSHA256 != "" {
			t.Fatalf("missing auxiliary identity: %+v", identity)
		}
		return nil, denied
	})
	base := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		dispatched++
		return nil, errors.New("unexpected dispatch")
	})}
	auth := &cliproxyauth.Auth{ID: "synthetic-auth"}
	client := WithAntigravityHTTPClientTrace(WithAntigravityHTTPClientTrace(base, auth, "credits_query"), auth, "credits_query")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid/v1internal:loadCodeAssist", nil)
	_, err := client.Do(req)
	if !errors.Is(err, denied) || observed != 1 || dispatched != 0 {
		t.Fatalf("auxiliary admission failed: error=%v observed=%d dispatched=%d", err, observed, dispatched)
	}
}

func TestAntigravityAuxiliaryObserverCountsRedirectsWithoutNestedDuplicates(t *testing.T) {
	observed, dispatched := 0, 0
	ctx := WithHTTPAttemptObserver(context.Background(), func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		observed++
		identity := HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Operation != "project_discovery" {
			t.Errorf("redirect lost operation: %+v", identity)
		}
		return next.RoundTrip(req)
	})
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		dispatched++
		resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}
		if dispatched == 1 {
			resp.StatusCode = http.StatusFound
			resp.Header.Set("Location", "/redirected")
		}
		return resp, nil
	})}
	auth := &cliproxyauth.Auth{ID: "synthetic-auth"}
	client := WithAntigravityHTTPClientTrace(WithAntigravityHTTPClientTrace(base, auth, "project_discovery"), auth, "project_discovery")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid/start", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if observed != 2 || dispatched != 2 {
		t.Fatalf("incorrect physical attempt count: observed=%d dispatched=%d", observed, dispatched)
	}
}

func TestCopyHTTPAttemptObserverPreservesIndependentLifetimeAndParentChoice(t *testing.T) {
	first, second := 0, 0
	source, cancel := context.WithCancel(WithHTTPAttemptObserver(context.Background(), func(*http.Request, http.RoundTripper) (*http.Response, error) {
		second++
		return nil, nil
	}))
	parent := WithHTTPAttemptObserver(context.Background(), func(*http.Request, http.RoundTripper) (*http.Response, error) {
		first++
		return nil, nil
	})
	ctx := CopyHTTPAttemptObserver(parent, source)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	_, _ = observeHTTPAttempt(req, func(*http.Request) (*http.Response, error) { return nil, nil }, func() HTTPAttemptIdentity { return HTTPAttemptIdentity{} })
	if first != 1 || second != 0 {
		t.Fatal("source observer replaced an explicit parent observer")
	}
	ctx = CopyHTTPAttemptObserver(context.Background(), source)
	cancel()
	if ctx.Err() != nil {
		t.Fatal("observer copy also copied the source cancellation lifetime")
	}
	req = req.WithContext(ctx)
	_, _ = observeHTTPAttempt(req, func(*http.Request) (*http.Response, error) { return nil, nil }, func() HTTPAttemptIdentity { return HTTPAttemptIdentity{} })
	if second != 1 {
		t.Fatal("source observer was lost")
	}
	disabled := CopyHTTPAttemptObserver(WithHTTPAttemptObserver(context.Background(), nil), source)
	req = req.WithContext(disabled)
	_, _ = observeHTTPAttempt(req, func(*http.Request) (*http.Response, error) { return nil, nil }, func() HTTPAttemptIdentity { return HTTPAttemptIdentity{} })
	if second != 1 {
		t.Fatal("explicit disabled observer was replaced")
	}
	requestContext := WithHTTPAttemptObserver(context.Background(), func(*http.Request, http.RoundTripper) (*http.Response, error) {
		second++
		return nil, nil
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
	ctx = CopyHTTPAttemptObserver(context.Background(), context.WithValue(context.Background(), "gin", c))
	_, _ = observeHTTPAttempt(req.WithContext(ctx), func(*http.Request) (*http.Response, error) { return nil, nil }, func() HTTPAttemptIdentity { return HTTPAttemptIdentity{} })
	if second != 2 {
		t.Fatal("HTTP handler's Gin-attached observer was not copied")
	}
}
