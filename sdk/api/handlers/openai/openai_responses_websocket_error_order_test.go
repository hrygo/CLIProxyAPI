package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestResponsesWebsocketClosedDataPreservesQueuedCredentialError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, duplex := range []bool{false, true} {
		for iteration := 0; iteration < 128; iteration++ {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses/ws", nil)
			h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil))
			data := make(chan []byte)
			close(data)
			errs := make(chan *interfaces.ErrorMessage, 1)
			want := &interfaces.ErrorMessage{StatusCode: http.StatusTooManyRequests, Error: errors.New("credential failed")}
			errs <- want
			close(errs)
			var cancelled error
			var suppressed int
			_, _, _, got, err := h.forwardResponsesWebsocket(c, newResponsesWebsocketWriter(nil), func(reasons ...interface{}) {
				if len(reasons) == 1 {
					cancelled, _ = reasons[0].(error)
				}
			}, data, errs, nil, "queued-error-regression", responsesWebsocketForwardOptions{
				duplexStream: func() bool { return duplex },
				suppressError: func(message *interfaces.ErrorMessage) bool {
					suppressed++
					return message == want
				},
			})
			if got != want || err != nil || cancelled != want.Error || suppressed != 1 {
				t.Fatalf("duplex=%t iteration=%d: error=%v forward=%v cancelled=%v suppressed=%d; queued credential error must reach replay policy", duplex, iteration, got, err, cancelled, suppressed)
			}
		}
	}
}
