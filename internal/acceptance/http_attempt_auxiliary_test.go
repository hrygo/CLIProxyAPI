package acceptance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestHTTPAttemptJournalAuxiliaryAndGenerateSharePhysicalBudget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	j, err := NewHTTPAttemptJournal(dir, "antigravity-generate-v1", 4096, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	dispatches := 0
	base := &http.Client{Transport: attemptTransport(func(*http.Request) (*http.Response, error) {
		dispatches++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	auth := &cliproxyauth.Auth{ID: "fixture-auth"}
	ctx := helps.WithHTTPAttemptObserver(context.Background(), j.Observe)
	aux := helps.WithAntigravityHTTPClientTrace(base, auth, "credits_query")
	model := helps.NewUsageReporter(ctx, "antigravity", "fixture-model", auth).TrackHTTPClient(base)
	for _, client := range []*http.Client{aux, model} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/", strings.NewReader("{}"))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/", strings.NewReader("{}"))
	if _, err := aux.Do(req); !errors.Is(err, ErrHTTPAttemptBudgetExhausted) || dispatches != 2 {
		t.Fatalf("shared budget: dispatches=%d error=%v", dispatches, err)
	}
	events := readAttemptEvents(t, dir)
	if len(events) != 4 {
		t.Fatalf("event count = %d", len(events))
	}
	for index, contract := range []string{"antigravity-auxiliary-v1", "antigravity-generate-v1"} {
		if events[index*2]["contract"] != contract || events[index*2+1]["contract"] != contract {
			t.Fatal("attempt contract changed between start and finish")
		}
	}
	identity := events[0]["executor_identity"].(map[string]any)
	if identity["operation"] != "credits_query" || identity["model_sha256"] != "" || identity["auth_sha256"] == "" {
		t.Fatal("auxiliary executor identity lost")
	}
}

func TestHTTPAttemptJournalRejectsUnboundAuxiliaryBeforeDispatch(t *testing.T) {
	for _, value := range []struct{ contract, authID string }{
		{"openai-chat-v1", "fixture-auth"}, {"antigravity-generate-v1", ""},
	} {
		t.Run(value.contract+value.authID, func(t *testing.T) {
			j, err := NewHTTPAttemptJournal(filepath.Join(t.TempDir(), "run"), value.contract, 4096, 2)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = j.Close() }()
			dispatches := 0
			client := helps.WithAntigravityHTTPClientTrace(&http.Client{Transport: attemptTransport(func(*http.Request) (*http.Response, error) {
				dispatches++
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})}, &cliproxyauth.Auth{ID: value.authID}, "credits_query")
			ctx := helps.WithHTTPAttemptObserver(context.Background(), j.Observe)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://fixture.invalid/", strings.NewReader("{}"))
			if _, err := client.Do(req); err == nil || dispatches != 0 {
				t.Fatalf("unbound auxiliary dispatched: %d, %v", dispatches, err)
			}
			if len(readAttemptEvents(t, j.dir)) != 0 {
				t.Fatal("rejected operation became a physical attempt")
			}
		})
	}
}
