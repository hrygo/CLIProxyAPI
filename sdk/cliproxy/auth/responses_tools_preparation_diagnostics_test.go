package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestResponsesToolsPreparationDiagnosticsBeforeEveryExecutorEntry(t *testing.T) {
	for _, entry := range []string{"buffered", "stream", "count"} {
		t.Run(entry, func(t *testing.T) {
			mgr := NewManager(nil, nil, nil)
			limits := responsestools.DefaultLimits()
			limits.MaxActiveToolBytes = 16
			mgr.syncResponsesToolsPolicy(responsestools.Policy{Enabled: true, Limits: limits})
			body := []byte(`{"tools":[{"type":"tool_search","execution":"client"}],"input":[{"type":"additional_tools","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}]}`)
			req := cliproxyexecutor.Request{Model: "fixture", Payload: body}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, OriginalRequest: body}
			var observed []responsestools.PreparationFailure
			ctx := responsestools.WithPreparationFailureObserver(context.Background(), func(event responsestools.PreparationFailure) { observed = append(observed, event) })
			calls := 0
			buffered := func(context.Context, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
				calls++
				return cliproxyexecutor.Response{}, nil
			}
			var err error
			switch entry {
			case "buffered":
				_, err = mgr.responsesToolsCallToFormat(ctx, nil, "meta", sdktranslator.FormatOpenAIResponse, req, opts, buffered)
			case "count":
				_, err = mgr.responsesToolsCountCall(ctx, nil, "meta", nil, req, opts, buffered)
			case "stream":
				_, _, err = mgr.responsesToolsStreamCall(ctx, nil, "meta", nil, req, opts,
					func(context.Context, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
						calls++
						return nil, nil
					})
			}
			var compat *responsestools.ToolCompatibilityError
			if !errors.As(err, &compat) || calls != 0 || len(observed) != 1 || observed[0].Reason != "declaration_budget" || observed[0].StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("pre-executor diagnostic failed: calls=%d events=%+v error=%v", calls, observed, err)
			}
		})
	}
}

func TestResponsesToolsUpstreamTextCannotImpersonatePreparationError(t *testing.T) {
	mgr := responsesToolsTestManagerWithModes("native", "transparent")
	observed := 0
	ctx := responsestools.WithPreparationFailureObserver(context.Background(), func(responsestools.PreparationFailure) { observed++ })
	body := []byte(`{"tools":[],"input":[]}`)
	_, err := mgr.responsesToolsCallToFormat(ctx, nil, "meta", sdktranslator.FormatOpenAIResponse,
		cliproxyexecutor.Request{Model: "fixture", Payload: body}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse},
		func(context.Context, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
			return cliproxyexecutor.Response{}, errors.New("responses tools declaration_budget: upstream imitation")
		})
	if err == nil || observed != 0 {
		t.Fatal("upstream text was promoted to a trusted local failure")
	}
}
