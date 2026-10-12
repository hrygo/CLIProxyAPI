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
