package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

type observedWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestHTTPAttemptJournalCloseContextWaitsForBodyAndFreezesAdmission(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	journal, err := NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
	dispatches := 0
	next := attemptTransport(func(*http.Request) (*http.Response, error) {
		dispatches++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("unfinished"))}, nil
	})
	response, err := journal.Observe(req, next)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := &observedWaitContext{Context: parent, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- journal.CloseContext(ctx) }()
	select {
	case <-ctx.waiting:
	case <-parent.Done():
		t.Fatal("seal never awaited body completion")
	}
	select {
	case err := <-result:
		t.Fatalf("seal returned before body release: %v", err)
	default:
	}
	if _, err := journal.Observe(req, next); err == nil || dispatches != 1 {
		t.Fatal("seal admitted another physical dispatch")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-parent.Done():
		t.Fatal("seal did not finish after body release")
	}
	events := readAttemptEvents(t, dir)
	if len(events) != 2 || events[1]["outcome"] != "closed" || events[1]["response"].(map[string]any)["complete"] != false {
		t.Fatal("seal changed the actual partial outcome")
	}
}

func TestHTTPAttemptJournalCloseContextCancelledKeepsUnfinishedEvidence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	journal, err := NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
	response, err := journal.Observe(req, attemptTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("unfinished"))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := journal.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled seal = %v", err)
	}
	if events := readAttemptEvents(t, dir); len(events) != 1 {
		t.Fatal("seal fabricated a finish")
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAttemptJournalCloseContextRequiresCloseEvenAfterEOF(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	journal, err := NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
	response, err := journal.Observe(req, attemptTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("complete bytes"))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err == nil {
		t.Fatal("EOF without body Close was certified as released")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := journal.CloseContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EOF without body Close was certified as released: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events := readAttemptEvents(t, dir); len(events) != 2 || events[1]["outcome"] != "eof" {
		t.Fatal("release rewrote the original EOF evidence")
	}
}

type closeFailureBody struct{ io.Reader }

func (*closeFailureBody) Close() error { return errors.New("private close detail") }

func TestHTTPAttemptJournalCloseContextReportsBodyCloseFailure(t *testing.T) {
	for _, afterEOF := range []bool{false, true} {
		t.Run(fmt.Sprint(afterEOF), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "run")
			journal, err := NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 4)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
			response, err := journal.Observe(req, attemptTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: &closeFailureBody{Reader: strings.NewReader("complete bytes")}}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if afterEOF {
				if _, err := io.ReadAll(response.Body); err != nil {
					t.Fatal(err)
				}
			}
			if err := response.Body.Close(); err == nil {
				t.Fatal("source close error hidden")
			}
			if err := journal.CloseContext(context.Background()); err == nil {
				t.Fatal("body close failure certified as resource release")
			}
			data, err := os.ReadFile(filepath.Join(dir, "attempts.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "private close detail") {
				t.Fatal("source error leaked into journal")
			}
		})
	}
}

func readAttemptEvents(t *testing.T, dir string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "attempts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestHTTPAttemptJournalActualHTTPStartsBeforeDispatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	journal, err := NewHTTPAttemptJournal(dir, "openai-chat-v1", 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	response := `{"usage":{"prompt_tokens":31,"completion_tokens":2}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events := readAttemptEvents(t, dir)
		if len(events) != 1 || events[0]["event"] != "request-started" {
			t.Error("request dispatched before durable start")
		}
		if r.Header.Get("Authorization") != "Bearer private-fixture-value" {
			t.Error("authorization altered")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"model":"fixture","tools":[]}` {
			t.Error("request body altered")
		}
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	ctx := helps.WithHTTPAttemptObserver(context.Background(), journal.Observe)
	reporter := helps.NewUsageReporter(ctx, "fixture", "fixture", nil)
	client := reporter.TrackHTTPClient(&http.Client{Transport: http.DefaultTransport})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/private-path?api_key=private-query-value", strings.NewReader(`{"model":"fixture","tools":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer private-fixture-value")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != response {
		t.Fatalf("response changed: %q, %v", body, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	events := readAttemptEvents(t, dir)
	if len(events) != 2 || events[1]["outcome"] != "eof" {
		t.Fatalf("events = %+v", events)
	}
	for _, name := range []string{"attempts.jsonl", "request-000001.body", "response-000001.body"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private-") || strings.Contains(string(data), server.URL) {
			t.Fatal("credentials or URL captured")
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("artifact not private")
		}
	}
}

type attemptTransport func(*http.Request) (*http.Response, error)

func (f attemptTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPAttemptJournalFailurePartialAndTruncation(t *testing.T) {
	for _, scenario := range []string{"transport_error", "closed", "truncated"} {
		t.Run(scenario, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "run")
			journal, err := NewHTTPAttemptJournal(dir, "openai-responses-v1", 8, 2)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			next := attemptTransport(func(*http.Request) (*http.Response, error) {
				if scenario == "transport_error" {
					return nil, errors.New("secret-url-and-token")
				}
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("12345678901234567890"))}, nil
			})
			resp, err := journal.Observe(req, next)
			if scenario == "transport_error" {
				if err == nil {
					t.Fatal("transport error lost")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "truncated" {
					body, err := io.ReadAll(resp.Body)
					if err != nil || len(body) != 20 {
						t.Fatal("capture cap changed upstream response")
					}
				} else {
					if err := journal.Close(); err == nil {
						t.Fatal("closed journal with unread active body")
					}
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			events := readAttemptEvents(t, dir)
			if len(events) != 2 {
				t.Fatalf("events = %+v", events)
			}
			want := scenario
			if scenario == "truncated" {
				want = "eof"
			}
			if events[1]["outcome"] != want {
				t.Fatalf("outcome = %v", events[1])
			}
			artifact, _ := events[1]["response"].(map[string]any)
			if scenario != "transport_error" && artifact["complete"] != false {
				t.Fatal("partial capture reported complete")
			}
			data, _ := os.ReadFile(filepath.Join(dir, "attempts.jsonl"))
			if strings.Contains(string(data), "secret") {
				t.Fatal("transport error leaked")
			}
		})
	}
}

func TestHTTPAttemptJournalRejectsReuseAndUnreplayableBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	journal, err := NewHTTPAttemptJournal(dir, "gemini-generate-v1", 16, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := journal.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := NewHTTPAttemptJournal(dir, "gemini-generate-v1", 16, 1); err == nil {
		t.Fatal("overwrote existing run")
	}
	req, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/", io.NopCloser(strings.NewReader("{}")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = journal.Observe(req, attemptTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("dispatch with missing request evidence")
		return nil, nil
	}))
	if err == nil {
		t.Fatal("accepted body without GetBody")
	}
	req.GetBody = func() (io.ReadCloser, error) { return nil, nil }
	if _, err := journal.Observe(req, attemptTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("dispatched after empty body-copy handle")
		return nil, nil
	})); err == nil {
		t.Fatal("accepted an empty body-copy handle")
	}
}

func TestHTTPAttemptJournalBudgetPreventsFurtherDispatch(t *testing.T) {
	journal, err := NewHTTPAttemptJournal(filepath.Join(t.TempDir(), "run"), "openai-chat-v1", 16, 1)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	dispatches := 0
	next := attemptTransport(func(*http.Request) (*http.Response, error) {
		dispatches++
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	if _, err := journal.Observe(req, next); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Observe(req, next); err == nil || dispatches != 1 {
		t.Fatalf("budget allowed further dispatch: %d, %v", dispatches, err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAdmissionSeparatesBudgetClosureAndEvidenceFailure(t *testing.T) {
	for _, scenario := range []string{"budget", "closed", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			journal, err := NewHTTPAttemptJournal(filepath.Join(t.TempDir(), "run"), "openai-chat-v1", 64, 1)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest(http.MethodPost, "http://fixture.invalid/", strings.NewReader("{}"))
			calls := 0
			next := attemptTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			if _, err := journal.Observe(req, next); err != nil {
				t.Fatal(err)
			}
			if scenario == "closed" {
				if err := journal.Close(); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "failed" {
				journal.err = errors.New("synthetic capture failure")
			}
			_, err = journal.Observe(req, next)
			if err == nil || calls != 1 || errors.Is(err, ErrHTTPAttemptBudgetExhausted) != (scenario == "budget") {
				t.Fatalf("incorrect local admission cause or dispatch: %v, %d", err, calls)
			}
			state := journal.AdmissionDiagnostics()
			if state.ReservedAttempts != 1 || state.ActiveBodies != 0 ||
				(state.BudgetRejections == 1) != (scenario == "budget") ||
				(state.ClosedRejections == 1) != (scenario == "closed") ||
				(state.FailedRejections == 1) != (scenario == "failed") {
				t.Fatalf("incorrect diagnostic: %+v", state)
			}
			_ = journal.Close()
		})
	}
}

func TestHTTPAdmissionDoesNotMistakeProviderErrorForLocalBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"synthetic Provider overload"}}`))
	}))
	defer server.Close()
	journal, err := NewHTTPAttemptJournal(filepath.Join(t.TempDir(), "run"), "openai-chat-v1", 4096, 2)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
	resp, err := journal.Observe(req, http.DefaultTransport)
	if err != nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatal("Provider status lost", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := journal.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := journal.AdmissionDiagnostics()
	if state.BudgetRejections != 0 || state.FailedRejections != 0 || state.JournalFailed || !state.JournalClosed {
		t.Fatalf("Provider response attributed to local admission: %+v", state)
	}
}
