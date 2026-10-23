package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestVertexAPIKeyResourcePrefixAcrossOperations(t *testing.T) {
	for _, prefix := range []string{"/v1", "/v1beta1", "/v1/projects/test/locations/global/publishers/google/models"} {
		for _, op := range []string{"execute", "stream", "count"} {
			t.Run(prefix+"/"+op, func(t *testing.T) {
				paths := make(chan string, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					paths <- r.URL.Path
					if strings.HasSuffix(r.URL.Path, ":countTokens") {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"totalTokens":1}`)
					} else {
						payload := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
						if op == "stream" {
							w.Header().Set("Content-Type", "text/event-stream")
							payload = "data: " + payload + "\n\n"
						}
						_, _ = io.WriteString(w, payload)
					}
				}))
				defer server.Close()
				auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "vertex", Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL + prefix}}
				body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
				req := cliproxyexecutor.Request{Model: "gemini-2.5-flash", Payload: body}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatGemini, OriginalRequest: body}
				exec := NewGeminiVertexExecutor(&config.Config{})
				action := "generateContent"
				var err error
				switch op {
				case "execute":
					_, err = exec.Execute(context.Background(), auth, req, opts)
				case "count":
					action = "countTokens"
					_, err = exec.CountTokens(context.Background(), auth, req, opts)
				case "stream":
					action = "streamGenerateContent"
					var stream *cliproxyexecutor.StreamResult
					stream, err = exec.ExecuteStream(context.Background(), auth, req, opts)
					if err == nil {
						for chunk := range stream.Chunks {
							if chunk.Err != nil {
								err = chunk.Err
							}
						}
					}
				}
				if err != nil {
					t.Fatalf("%s: %v", op, err)
				}
				resource := prefix
				if !strings.Contains(prefix, "/publishers/") {
					resource += "/publishers/google/models"
				}
				want := fmt.Sprintf("%s/gemini-2.5-flash:%s", resource, action)
				if got := <-paths; got != want {
					t.Fatalf("upstream path = %q, want %q", got, want)
				}
			})
		}
	}
}
