package executor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAntigravityCreditsObserverRejectsActualQueryBeforeTransport(t *testing.T) {
	observed, dispatched := 0, 0
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(*http.Request) (*http.Response, error) {
		dispatched++
		return nil, errors.New("unexpected dispatch")
	}))
	ctx = helps.WithHTTPAttemptObserver(ctx, func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		observed++
		identity := helps.HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Operation != "credits_query" || identity.Executor != "antigravity" || identity.AuthSHA256 == "" {
			t.Errorf("missing query identity: %+v", identity)
		}
		return nil, errors.New("synthetic admission denial")
	})
	exec := NewAntigravityExecutor(&config.Config{})
	exec.updateAntigravityCreditsBalance(ctx, &cliproxyauth.Auth{ID: t.Name()}, "synthetic-access-token")
	if observed != 1 || dispatched != 0 {
		t.Fatalf("query escaped admission: observed=%d dispatched=%d", observed, dispatched)
	}
}

func TestAntigravityTimedCreditsQueueCopiesObserverAndJoinsTask(t *testing.T) {
	var observed atomic.Int32
	reached := make(chan *helps.HTTPAttemptIdentity, 1)
	release := make(chan struct{})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		reached <- helps.HTTPAttemptIdentityFromContext(req.Context())
		<-release
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"currentTier":{"id":"free-tier"}}`))}, nil
	}))
	ctx = helps.WithHTTPAttemptObserver(ctx, func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		observed.Add(1)
		return next.RoundTrip(req)
	})
	source, cancel := context.WithCancel(ctx)
	defer cancel()
	auth := &cliproxyauth.Auth{ID: t.Name()}
	exec := NewAntigravityExecutor(&config.Config{})
	exec.queueAntigravityCreditsRefresh(source, auth, "synthetic-access-token", time.Minute)
	identity := <-reached
	value, ok := antigravityCreditsHintRefreshByID.Load(auth.ID)
	if !ok {
		t.Fatal("queued refresh state missing")
	}
	state := value.(*antigravityCreditsHintRefreshState)
	state.mu.Lock()
	task := state.task
	state.mu.Unlock()
	if task == nil {
		t.Fatal("blocked query task missing")
	}
	cancel()
	if task.ctx.Err() != nil {
		t.Error("warm-hint independent lifecycle changed")
	}
	close(release)
	<-task.done
	antigravityCreditsHintRefreshByID.Delete(auth.ID)
	if identity == nil || identity.Operation != "credits_query" || observed.Load() != 1 {
		t.Fatalf("timed query lost observer: identity=%+v observed=%d", identity, observed.Load())
	}
}

func TestAntigravityTimedCreditsQueueOwnerCancelsAndJoinsIndependentTask(t *testing.T) {
	scope := helps.NewHTTPAuxiliaryWorkScope()
	reached := make(chan context.Context, 1)
	release := make(chan struct{})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		reached <- req.Context()
		<-release
		return nil, req.Context().Err()
	}))
	ctx = helps.WithHTTPAuxiliaryWorkScope(ctx, scope)
	auth := &cliproxyauth.Auth{ID: t.Name()}
	defer antigravityCreditsHintRefreshByID.Delete(auth.ID)
	exec := NewAntigravityExecutor(&config.Config{})
	exec.queueAntigravityCreditsRefresh(ctx, auth, "synthetic-access-token", time.Minute)
	work := <-reached
	if state := scope.Snapshot(); state.Active != 1 || state.Started != 1 {
		close(release)
		t.Fatalf("independent task was not registered: %+v", state)
	}
	result := make(chan error, 1)
	go func() { result <- scope.CloseContext(context.Background()) }()
	<-work.Done()
	select {
	case <-result:
		t.Error("owner returned before underlying transport released")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if state := scope.Snapshot(); !state.Sealed || state.Active != 0 || state.Completed != 1 {
		t.Fatalf("owner failed to join task cleanup: %+v", state)
	}
	exec.queueAntigravityCreditsRefresh(ctx, auth, "synthetic-access-token", time.Minute)
	if state := scope.Snapshot(); state.Started != 1 || state.Rejected != 1 {
		t.Fatalf("sealed owner admitted more work: %+v", state)
	}
}
