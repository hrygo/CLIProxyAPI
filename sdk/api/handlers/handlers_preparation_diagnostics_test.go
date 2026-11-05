package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestGetContextWithCancelDiagnosticsPreferParentAndIsolateRequests(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
	var parentPreparations, parentResponses, requestPreparations, requestResponses int
	parent := responsestools.WithPreparationFailureObserver(context.Background(), func(responsestools.PreparationFailure) { parentPreparations++ })
	parent = responsestools.WithResponseFailureObserver(parent, func(responsestools.ResponseFailure) { parentResponses++ })
	requestCtx := responsestools.WithPreparationFailureObserver(context.Background(), func(responsestools.PreparationFailure) { requestPreparations++ })
	requestCtx = responsestools.WithResponseFailureObserver(requestCtx, func(responsestools.ResponseFailure) { requestResponses++ })
	requestCtx, cancelRequest := context.WithCancel(requestCtx)
	defer cancelRequest()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestCtx)
	ctx, cancel := h.GetContextWithCancel(nil, c, parent)
	defer cancel()

	var secondPreparations, secondResponses int
	secondCtx := responsestools.WithPreparationFailureObserver(context.Background(), func(responsestools.PreparationFailure) { secondPreparations++ })
	secondCtx = responsestools.WithResponseFailureObserver(secondCtx, func(responsestools.ResponseFailure) { secondResponses++ })
	secondGin, _ := gin.CreateTestContext(httptest.NewRecorder())
	secondGin.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(secondCtx)
	other, cancelOther := h.GetContextWithCancel(nil, secondGin, nil)
	defer cancelOther()

	preparationErr := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonJSONSyntax, Err: fmt.Errorf("synthetic")}
	responseErr := &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract}
	for _, execution := range []context.Context{ctx, other} {
		responsestools.ReportPreparationFailure(execution, preparationErr)
		responsestools.ReportResponseFailure(execution, "stream_feed", responseErr)
	}
	if parentPreparations != 1 || parentResponses != 1 || requestPreparations != 0 || requestResponses != 0 || secondPreparations != 1 || secondResponses != 1 {
		t.Fatalf("observer calls: parent=%d/%d first request=%d/%d second request=%d/%d", parentPreparations, parentResponses, requestPreparations, requestResponses, secondPreparations, secondResponses)
	}
	cancelRequest()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not observe request cancellation")
	}
	if parent.Err() != nil || other.Err() != nil {
		t.Fatal("request cancellation leaked into an independent parent or request")
	}
}

func TestGetContextWithCancelDiagnosticsHandlesAbsentRequest(t *testing.T) {
	h := &BaseAPIHandler{Cfg: &config.SDKConfig{}}
	ctx, cancel := h.GetContextWithCancel(nil, nil, nil)
	defer cancel()
	responsestools.ReportPreparationFailure(ctx, &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonJSONSyntax})
	responsestools.ReportResponseFailure(ctx, "stream_feed", &responsestools.ToolCompatibilityError{Reason: responsestools.ReasonUpstreamContract})
	if ctx.Err() != nil {
		t.Fatalf("execution canceled without a request: %v", ctx.Err())
	}
}
