package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	auth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestSpikeProviderRejectsDamagedFinalToolContract(t *testing.T) {
	base := context.WithValue(context.Background(), "cliproxy.roundtripper", kimiRoundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("unexpected outbound request") }))
	ctx := core.WithWireContract(base, &core.WireContract{RequiredFunctionNames: []string{"missing"}, ExactFunctionNames: true})
	req := core.Request{Model: "review-model", Payload: []byte(`{"model":"review-model","input":"hello"}`)}
	opts := core.Options{SourceFormat: translator.FormatOpenAIResponse}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"vertex-execute", func() error {
			_, err := NewGeminiVertexExecutor(&config.Config{}).executeWithAPIKey(ctx, nil, req, opts, "test", "http://127.0.0.1:1")
			return err
		}},
		{"vertex-stream", func() error {
			_, err := NewGeminiVertexExecutor(&config.Config{}).executeStreamWithAPIKey(ctx, nil, req, opts, "test", "http://127.0.0.1:1")
			return err
		}},
		{"aistudio-execute", func() error {
			_, _, err := NewAIStudioExecutor(&config.Config{}, "aistudio", nil).translateRequest(ctx, req, opts, false)
			return err
		}},
		{"aistudio-stream", func() error {
			_, _, err := NewAIStudioExecutor(&config.Config{}, "aistudio", nil).translateRequest(ctx, req, opts, true)
			return err
		}},
		{"kimi-execute", func() error {
			_, err := NewKimiExecutor(&config.Config{}).executeResponses(ctx, &auth.Auth{Metadata: map[string]any{"access_token": "test"}}, req, opts)
			return err
		}},
		{"kimi-stream", func() error {
			_, err := NewKimiExecutor(&config.Config{}).executeResponsesStream(ctx, &auth.Auth{Metadata: map[string]any{"access_token": "test"}}, req, opts)
			return err
		}},
		{"devin", func() error {
			a := &auth.Auth{Attributes: map[string]string{"api_key": "test", "base_url": "http://127.0.0.1:1"}}
			_, _, _, err := NewDevinExecutor(&config.Config{}).prepareDevinHTTPRequest(ctx, a, req, opts)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			var status interface{ StatusCode() int }
			if !errors.As(err, &status) || status.StatusCode() != http.StatusUnprocessableEntity {
				t.Fatalf("expected local 422 before dispatch, got %v", err)
			}
		})
	}
}

func TestSpikeMetaInjectedNamespaceGuard(t *testing.T) {
	ctx := core.WithWireContract(context.Background(), &core.WireContract{RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true, MaxActiveToolBytes: 4096})
	req := core.Request{Model: "review-model", Payload: []byte(`{"model":"review-model","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`)}
	opts := core.Options{SourceFormat: translator.FormatOpenAIResponse}
	for _, tc := range []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{"canonical", nil, false},
		{"mutated", map[string]any{"tools.1.description": "corrupted"}, true},
		{"removed", map[string]any{"tools.1": nil}, true},
		{"duplicate", map[string]any{"tools.2": map[string]any{"type": "namespace", "name": "muse", "tools": []any{map[string]any{"type": "function", "name": "submit_reminder_decision"}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			if tc.params != nil {
				cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "review-model"}}, Params: tc.params}}
			}
			_, err := NewMetaExecutor(cfg).prepareResponsesRequest(ctx, nil, req, opts, false)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
}

func TestSpikeVertexFormatTracksCredential(t *testing.T) {
	e := NewGeminiVertexExecutor(&config.Config{})
	req := core.Request{Model: "gemini-2.5-pro"}
	opts := core.Options{SourceFormat: translator.FormatClaude}
	a := &auth.Auth{Attributes: map[string]string{"vertex_model_openapi": `[{"name":"gemini-2.5-pro","openapi":true}]`}}
	if got := e.RequestToFormatWithAuth(a, req, opts); got != translator.FormatOpenAI {
		t.Fatalf("OpenAPI format %s", got)
	}
	if got := e.RequestToFormatWithAuth(nil, req, opts); got != translator.FormatGemini {
		t.Fatalf("Gemini format %s", got)
	}
}
