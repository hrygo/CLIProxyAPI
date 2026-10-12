package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

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
