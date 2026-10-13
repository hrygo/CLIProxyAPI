package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/responsestools"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestGetContextWithCancelPropagatesPreparationFailureObserver(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	observed := 0
	c.Request = c.Request.WithContext(responsestools.WithPreparationFailureObserver(c.Request.Context(), func(responsestools.PreparationFailure) { observed++ }))
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
	ctx, cancel := h.GetContextWithCancel(nil, c, context.Background())
	defer cancel()
	err := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonJSONSyntax, Err: fmt.Errorf("synthetic malformed declaration")}
	responsestools.ReportPreparationFailure(ctx, err)
	if observed != 1 {
		t.Fatal("HTTP request observer did not reach the execution context")
	}
}

func TestGetContextWithCancelPropagatesResponseFailureObserver(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	observed := 0
	c.Request = c.Request.WithContext(responsestools.WithResponseFailureObserver(c.Request.Context(), func(responsestools.ResponseFailure) { observed++ }))
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
	ctx, cancel := h.GetContextWithCancel(nil, c, context.Background())
	defer cancel()
	responsestools.ReportResponseFailure(ctx, "stream_feed", &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract})
	if observed != 1 {
		t.Fatal("response observer did not reach actual execution context")
	}
}
