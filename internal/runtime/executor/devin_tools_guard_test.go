package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	gjson "github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestDevinOutboundToolContractOnBothExecutionPaths(t *testing.T) {
	nativeID := "native:call#1"
	clientID := helps.EncodeDevinToolCallID(nativeID)
	payload := []byte(fmt.Sprintf(`{"model":"devin/swe-2","input":[{"type":"message","role":"user","content":"look it up"},{"type":"function_call","name":"lookup","call_id":%q,"arguments":"{\"q\":\"abc\"}"},{"type":"function_call_output","call_id":%q,"output":"found"}],"tools":[{"type":"function","name":"lookup","description":"look up","parameters":{"type":"object"}}]}`, clientID, clientID))
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			overrides  map[string]any
			filter     []string
			wantStatus int
		}{
			{name: "safe"},
			{name: "safe non-tool override", overrides: map[string]any{"completion_config.max_tokens": 77}},
			{name: "renamed tool", overrides: map[string]any{"tools.0.name": "changed"}, wantStatus: 422},
			{name: "extra tool", overrides: map[string]any{"tools.1": map[string]any{"name": "injected", "parameters": map[string]any{"type": "object"}}}, wantStatus: 422},
			{name: "duplicate tool", overrides: map[string]any{"tools.1": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}, wantStatus: 422},
			{name: "oversized tool", overrides: map[string]any{"tools.0.description": strings.Repeat("x", 2048)}, wantStatus: 413},
			{name: "renamed call", overrides: map[string]any{"prompts.1.tool_calls.0.name": "changed"}, wantStatus: 422},
			{name: "changed call id", overrides: map[string]any{"prompts.1.tool_calls.0.id": "changed"}, wantStatus: 422},
			{name: "changed result id", overrides: map[string]any{"prompts.2.tool_call_id": "changed"}, wantStatus: 422},
			{name: "changed call source", overrides: map[string]any{"prompts.1.source": 1}, wantStatus: 422},
			{name: "removed result", filter: []string{"prompts.2"}, wantStatus: 422},
		} {
			mode := "execute"
			if stream {
				mode = "stream"
			}
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				cfg := &config.Config{}
				if len(tc.overrides) > 0 {
					cfg.Payload.Override = []config.PayloadRule{{Models: []config.PayloadModelRule{{Name: "*"}}, Params: tc.overrides}}
				}
				if len(tc.filter) > 0 {
					cfg.Payload.Filter = []config.PayloadFilterRule{{Models: []config.PayloadModelRule{{Name: "*"}}, Params: tc.filter}}
				}
				var sent []byte
				calls := 0
				transport := kimiRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					var err error
					sent, err = io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					frame := appendDevinFieldBytes(nil, 2, []byte("ok"))
					response := helps.WrapConnectEnvelope(frame)
					response = append(response, helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{}`))...)
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/connect+proto"}}, Body: io.NopCloser(bytes.NewReader(response)), Request: r}, nil
				})
				ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
				ctx = cliproxyexecutor.WithWireContract(ctx, &cliproxyexecutor.WireContract{
					CustomAliases: []string{"lookup"}, RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true,
					HistoryCalls: []cliproxyexecutor.WireToolHistoryReference{{Name: "lookup", CallID: clientID}}, MaxActiveToolBytes: 1024,
				})
				exec := NewDevinExecutor(cfg)
				auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "devin", Attributes: map[string]string{"api_key": "test", "base_url": "http://127.0.0.1:1"}}
				req := cliproxyexecutor.Request{Model: "devin/swe-2", Payload: payload}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: payload, Stream: stream}
				var err error
				if stream {
					var result *cliproxyexecutor.StreamResult
					result, err = exec.ExecuteStream(ctx, auth, req, opts)
					if err == nil {
						for chunk := range result.Chunks {
							if chunk.Err != nil {
								t.Fatalf("stream chunk: %v", chunk.Err)
							}
						}
					}
				} else {
					_, err = exec.Execute(ctx, auth, req, opts)
				}
				if tc.wantStatus != 0 {
					var status interface{ StatusCode() int }
					if !errors.As(err, &status) || status.StatusCode() != tc.wantStatus {
						t.Fatalf("error = %v, want status %d", err, tc.wantStatus)
					}
					if calls != 0 {
						t.Fatalf("rejected request was sent %d times", calls)
					}
					return
				}
				if err != nil || calls != 1 {
					t.Fatalf("execution error = %v, requests = %d", err, calls)
				}
				reader := bytes.NewReader(sent)
				flag, wire, errFrame := helps.ReadConnectFrame(reader)
				if errFrame != nil || flag != helps.ConnectFlagData || reader.Len() != 0 {
					t.Fatalf("invalid sent frame: flag=%d remaining=%d err=%v", flag, reader.Len(), errFrame)
				}
				_, view, errView := helps.FinalizeDevinPayload(wire, func(body []byte) []byte { return body })
				if errView != nil {
					t.Fatal(errView)
				}
				if gjson.GetBytes(view, "tools.0.name").String() != "lookup" || gjson.GetBytes(view, "prompts.1.tool_calls.0.id").String() != nativeID || gjson.GetBytes(view, "prompts.2.tool_call_id").String() != nativeID {
					t.Fatalf("unexpected tool identity on sent wire: %s", view)
				}
				if tc.name == "safe non-tool override" && gjson.GetBytes(view, "completion_config.max_tokens").Int() != 77 {
					t.Fatalf("non-tool override was not sent: %s", view)
				}
			})
		}
	}
}

func TestDevinRawHTTPRequestGuardsTheBodyActuallySent(t *testing.T) {
	build := func(name string) []byte {
		wire := helps.BuildDevinGetChatMessageRequest("test", "device", "model", "", nil,
			[]helps.DevinTool{{Name: name, Parameters: []byte(`{"type":"object"}`)}}, nil, 100, "session", "cascade", nil)
		return helps.WrapConnectEnvelope(wire)
	}
	valid, damaged := build("lookup"), build("other")
	for _, tampered := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "body differs from GetBody"}[tampered], func(t *testing.T) {
			var sent []byte
			transport := kimiRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				sent, _ = io.ReadAll(r.Body)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", transport)
			ctx = cliproxyexecutor.WithWireContract(ctx, &cliproxyexecutor.WireContract{RequiredFunctionNames: []string{"lookup"}, ExactFunctionNames: true})
			req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1", bytes.NewReader(valid))
			if err != nil {
				t.Fatal(err)
			}
			if tampered {
				req.Body = io.NopCloser(bytes.NewReader(damaged))
			} else {
				req.GetBody = nil
			}
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test"}}
			resp, err := NewDevinExecutor(&config.Config{}).HttpRequest(ctx, auth, req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if tampered {
				var status interface{ StatusCode() int }
				if !errors.As(err, &status) || status.StatusCode() != http.StatusUnprocessableEntity || len(sent) != 0 {
					t.Fatalf("tampered body was not rejected before send: err=%v sent=%d", err, len(sent))
				}
				return
			}
			if err != nil || !bytes.Equal(sent, valid) {
				t.Fatalf("valid body changed or was not sent: err=%v sent=%d", err, len(sent))
			}
		})
	}
}
