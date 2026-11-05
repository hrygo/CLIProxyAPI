package executor

import (
	"context"
	"sync/atomic"
)

type downstreamWebsocketContextKey struct{}
type requireUpstreamWebsocketContextKey struct{}
type upstreamAttemptTrackerContextKey struct{}

type upstreamAttemptTracker struct {
	attempted atomic.Bool
}

// WithDownstreamWebsocket marks the current request as coming from a downstream websocket connection.
func WithDownstreamWebsocket(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, downstreamWebsocketContextKey{}, true)
}

// DownstreamWebsocket reports whether the current request originates from a downstream websocket connection.
func DownstreamWebsocket(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	raw := ctx.Value(downstreamWebsocketContextKey{})
	enabled, ok := raw.(bool)
	return ok && enabled
}

// WithRequiredUpstreamWebsocket marks a request whose incremental context is valid only on the current upstream websocket.
func WithRequiredUpstreamWebsocket(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requireUpstreamWebsocketContextKey{}, true)
}

// RequiredUpstreamWebsocket reports whether falling back to an HTTP upstream would lose request context.
func RequiredUpstreamWebsocket(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	raw := ctx.Value(requireUpstreamWebsocketContextKey{})
	enabled, ok := raw.(bool)
	return ok && enabled
}

// WithUpstreamAttemptTracker installs a fresh tracker for one provider execution attempt.
func WithUpstreamAttemptTracker(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, upstreamAttemptTrackerContextKey{}, &upstreamAttemptTracker{})
}

// MarkUpstreamAttempt records that the provider execution reached an upstream transport boundary.
func MarkUpstreamAttempt(ctx context.Context) {
	if ctx == nil {
		return
	}
	tracker, ok := ctx.Value(upstreamAttemptTrackerContextKey{}).(*upstreamAttemptTracker)
	if !ok || tracker == nil {
		return
	}
	tracker.attempted.Store(true)
}

// UpstreamAttempted reports whether the tracked provider execution reached an upstream transport boundary.
func UpstreamAttempted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	tracker, ok := ctx.Value(upstreamAttemptTrackerContextKey{}).(*upstreamAttemptTracker)
	return ok && tracker != nil && tracker.attempted.Load()
}

type responsesToolsReplayCheckerContextKey struct{}

// WithResponsesToolsReplayChecker supplies route-aware replay detection for later duplex turns.
func WithResponsesToolsReplayChecker(ctx context.Context, checker func([]byte, string, string, string) bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if checker == nil {
		return ctx
	}
	return context.WithValue(ctx, responsesToolsReplayCheckerContextKey{}, checker)
}

// ResponsesToolsReplayCheckerFromContext retrieves the per-session replay detector, if any.
func ResponsesToolsReplayCheckerFromContext(ctx context.Context) func([]byte, string, string, string) bool {
	if ctx == nil {
		return nil
	}
	checker, _ := ctx.Value(responsesToolsReplayCheckerContextKey{}).(func([]byte, string, string, string) bool)
	return checker
}

type responsesToolsPreparationObserverContextKey struct{}

// WithResponsesToolsPreparationObserver enables per-attempt replay mode
// selection after the credential and its actual tool policy are known.
func WithResponsesToolsPreparationObserver(ctx context.Context, observer func(bool)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, responsesToolsPreparationObserverContextKey{}, observer)
}

// ReportResponsesToolsPreparation reports whether this selected attempt needs
// managed replay and returns whether a handler requested per-attempt selection.
func ReportResponsesToolsPreparation(ctx context.Context, replay bool) bool {
	if ctx == nil {
		return false
	}
	observer, _ := ctx.Value(responsesToolsPreparationObserverContextKey{}).(func(bool))
	if observer == nil {
		return false
	}
	observer(replay)
	return true
}
