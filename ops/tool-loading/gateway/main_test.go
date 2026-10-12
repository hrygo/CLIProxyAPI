package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureGatewayUsesActualChatExecutorAndCollector(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected actual upstream path")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"fixture answer\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	state := filepath.Join(t.TempDir(), "state")
	gateway, err := newFixtureGateway(upstream.URL+"/v1", "fixture-chat", state, 8)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"fixture-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-acceptance-placeholder")
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, req)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "fixture answer") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if err := gateway.close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "capture", "attempts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var start map[string]any
	if err := json.Unmarshal(bytes.Split(data, []byte("\n"))[0], &start); err != nil {
		t.Fatal(err)
	}
	identity, ok := start["executor_identity"].(map[string]any)
	if !ok || identity["executor"] != "fixture-chat" || start["contract"] != "openai-chat-v1" {
		t.Fatalf("identity = %v", start)
	}
}

func TestFixtureGatewayRejectsNonFixtureUpstreamsBeforeState(t *testing.T) {
	for _, address := range []string{"https://example.com/v1", "http://user:secret@127.0.0.1/v1", "http://127.0.0.1/v1?key=secret", "http://127.0.0.1/other"} {
		state := filepath.Join(t.TempDir(), "state")
		if _, err := newFixtureGateway(address, "fixture-chat", state, 8); err == nil {
			t.Fatal("accepted non-fixture upstream")
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatal("created state before validation")
		}
	}
}
