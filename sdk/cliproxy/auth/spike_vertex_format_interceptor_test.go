package auth

import (
	"context"
	core "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	translator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"testing"
)

type spikeAuthFormatExecutor struct{ quotaAttemptIsolationExecutor }

func (*spikeAuthFormatExecutor) RequestToFormatWithAuth(a *Auth, _ core.Request, _ core.Options) translator.Format {
	if a != nil && a.ID == "openapi" {
		return translator.FormatOpenAI
	}
	return translator.FormatGemini
}
func TestSpikeAfterAuthInterceptorUsesCredentialFormat(t *testing.T) {
	e := &spikeAuthFormatExecutor{}
	a := &Auth{ID: "openapi"}
	var got translator.Format
	opts := core.Options{RequestAfterAuthInterceptor: func(_ context.Context, req core.RequestAfterAuthInterceptRequest) core.RequestAfterAuthInterceptResponse {
		got = req.ToFormat
		return core.RequestAfterAuthInterceptResponse{}
	}}
	_, _, err := applyRequestAfterAuthInterceptor(context.Background(), e, "vertex", core.Request{}, opts, "", a)
	if err != nil || got != translator.FormatOpenAI {
		t.Fatalf("format %s, error %v", got, err)
	}
	if got = requestToFormatWithAuth("vertex", e, a, core.Request{}, opts); got != translator.FormatOpenAI {
		t.Fatalf("route format %s", got)
	}
}
