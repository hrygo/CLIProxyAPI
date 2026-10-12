package cliproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

func TestAntigravityModelCatalogObserverRejectsBeforeTransport(t *testing.T) {
	var dispatched atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dispatched.Add(1)
		_, _ = w.Write([]byte(`{"models":{"fixture":{}}}`))
	}))
	defer server.Close()
	auth := antigravityTestAuth(t.Name(), server.URL)
	observed := 0
	ctx := helps.WithHTTPAttemptObserver(context.Background(), func(req *http.Request, _ http.RoundTripper) (*http.Response, error) {
		observed++
		identity := helps.HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Executor != "antigravity" || identity.Operation != "model_discovery" || identity.ModelSHA256 != "" || identity.AuthSHA256 == "" {
			t.Error("model catalog identity missing or inference-bound")
		}
		return nil, errors.New("synthetic admission rejection")
	})
	svc := &Service{}
	hints, _ := svc.probeAntigravityModelCapabilityHints(ctx, auth, []string{server.URL}, "", "synthetic-token")
	if observed != 1 || dispatched.Load() != 0 || hints.ModelIDs != nil {
		t.Fatalf("catalog escaped admission: observed=%d dispatched=%d", observed, dispatched.Load())
	}
}

func TestAntigravityModelCatalogOwnerWaitsForActualHTTPRelease(t *testing.T) {
	scope := helps.NewHTTPAuxiliaryWorkScope()
	reached := make(chan context.Context, 1)
	release := make(chan struct{})
	ctx := helps.WithHTTPAuxiliaryWorkScope(context.Background(), scope)
	ctx = helps.WithHTTPAttemptObserver(ctx, func(req *http.Request, _ http.RoundTripper) (*http.Response, error) {
		reached <- req.Context()
		<-release
		return nil, req.Context().Err()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc := &Service{}
		svc.probeAntigravityModelCapabilityHints(ctx, antigravityTestAuth(t.Name(), "https://fixture.invalid"), []string{"https://fixture.invalid"}, "", "synthetic-token")
	}()
	work := <-reached
	if state := scope.Snapshot(); state.Started != 1 || state.Active != 1 {
		close(release)
		t.Fatalf("catalog not owned: %+v", state)
	}
	sealed := make(chan error, 1)
	go func() { sealed <- scope.CloseContext(context.Background()) }()
	<-work.Done()
	select {
	case <-sealed:
		t.Error("owner sealed while HTTP transport remained active")
	default:
	}
	close(release)
	<-done
	if err := <-sealed; err != nil {
		t.Fatal(err)
	}
	if state := scope.Snapshot(); state.Active != 0 || state.Completed != 1 || !state.Sealed {
		t.Fatalf("catalog cleanup did not settle: %+v", state)
	}
}
