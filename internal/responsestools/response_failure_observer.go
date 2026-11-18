package responsestools

import "context"

// ResponseFailure contains only fixed diagnostics from local response restore.
// Upstream errors and arbitrary error prose are not response-restore evidence.
type ResponseFailure struct {
	Phase      string `json:"phase"`
	Reason     string `json:"reason"`
	StatusCode int    `json:"status_code"`
}

type responseObserverKey struct{}
type responseObserver func(ResponseFailure)

// WithResponseFailureObserver adds an optional synchronous, observation-only
// callback. It must not change policy, retry requests or execute tools.
func WithResponseFailureObserver(ctx context.Context, observer func(ResponseFailure)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, responseObserverKey{}, responseObserver(observer))
}

func responseObserverFromContext(ctx context.Context) responseObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(responseObserverKey{}).(responseObserver)
	return observer
}

// CopyResponseFailureObserver retains the parent's observer and lifetime while
// propagating request-local diagnostics through HTTP and WebSocket handlers.
func CopyResponseFailureObserver(dst, src context.Context) context.Context {
	if dst == nil {
		dst = context.Background()
	}
	if responseObserverFromContext(dst) != nil || responseObserverFromContext(src) == nil {
		return dst
	}
	return WithResponseFailureObserver(dst, responseObserverFromContext(src))
}

// ReportResponseFailure is called only when local RewriteResponse, Feed or
// Finish fails. Executor errors must never be reported through this boundary.
func ReportResponseFailure(ctx context.Context, phase string, err error) {
	observer := responseObserverFromContext(ctx)
	if observer == nil {
		return
	}
	// A canceled consumer no longer owns response-restore evidence. In-flight
	// conversion can settle later, but must not report into a finished request.
	if ctx.Err() != nil {
		return
	}
	switch phase {
	case "response_restore", "stream_feed", "stream_finish":
	default:
		return
	}
	// Reuse the typed reason/status allowlist without invoking the actual
	// preparation observer or changing the caller's context values.
	diagnosticCtx := WithPreparationFailureObserver(ctx, func(event PreparationFailure) {
		observer(ResponseFailure{Phase: phase, Reason: event.Reason, StatusCode: event.StatusCode})
	})
	ReportPreparationFailure(diagnosticCtx, err)
}
