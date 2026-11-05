package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestHTTPAttemptObserverRunsBeforeDispatchAndRetainsRetries(t *testing.T) {
	var events []string
	ctx := WithHTTPAttemptObserver(context.Background(), func(req *http.Request, next http.RoundTripper) (*http.Response, error) {
		identity := HTTPAttemptIdentityFromContext(req.Context())
		if identity == nil || identity.Executor != "fixture" || len(identity.ModelSHA256) != 64 || identity.AuthSHA256 != "" {
			t.Fatalf("missing or incorrect actual executor identity: %+v", identity)
		}
		events = append(events, "start")
		resp, err := next.RoundTrip(req)
		if err != nil {
			events = append(events, "failed")
		} else {
			events = append(events, "response")
		}
		return resp, err
	})
	ctx = cliproxyexecutor.WithUpstreamAttemptTracker(ctx)
	reporter := NewUsageReporter(ctx, "fixture", "model", nil)
	count := 0
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		events = append(events, "dispatch")
		count++
		if count == 1 {
			return nil, errors.New("transport failed")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})}
	// Repeated usage instrumentation must not create extra observed attempts.
	client := reporter.TrackHTTPClient(reporter.TrackHTTPClient(base))
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if i == 0 && err == nil {
			t.Fatal("missing transport failure")
		}
		if i == 1 {
			if err != nil {
				t.Fatal(err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := "start,dispatch,failed,start,dispatch,response"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	if !cliproxyexecutor.UpstreamAttempted(ctx) {
		t.Fatal("actual dispatch not marked")
	}
	if base.Transport == client.Transport {
		t.Fatal("original client was mutated")
	}
}

func TestHTTPAttemptObserverFailureDoesNotMarkNetworkAttempt(t *testing.T) {
	want := errors.New("journal unavailable")
	ctx := WithHTTPAttemptObserver(context.Background(), func(*http.Request, http.RoundTripper) (*http.Response, error) { return nil, want })
	ctx = cliproxyexecutor.WithUpstreamAttemptTracker(ctx)
	reporter := NewUsageReporter(ctx, "fixture", "model", nil)
	client := reporter.TrackHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("dispatch after observer rejected request")
		return nil, nil
	})})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if cliproxyexecutor.UpstreamAttempted(ctx) {
		t.Fatal("local observation failure marked as upstream dispatch")
	}
}
