package responsestools

import (
	"context"
	"errors"
	"net/http"
)

// PreparationFailure contains only fixed diagnostics from the local preparation
// boundary. It deliberately excludes error text, request data and route secrets.
type PreparationFailure struct {
	Phase      string `json:"phase"`
	Reason     string `json:"reason"`
	StatusCode int    `json:"status_code"`
}

type preparationObserverKey struct{}
type preparationObserver func(PreparationFailure)

// WithPreparationFailureObserver installs an optional synchronous diagnostics
// callback. The callback must not change policy or execute tools.
func WithPreparationFailureObserver(ctx context.Context, observer func(PreparationFailure)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preparationObserverKey{}, preparationObserver(observer))
}

// CopyPreparationFailureObserver bridges HTTP request context values while
// preserving an explicitly supplied parent observer and all parent lifetimes.
func CopyPreparationFailureObserver(dst, src context.Context) context.Context {
	if dst == nil {
		dst = context.Background()
	}
	if observerFromContext(dst) != nil || observerFromContext(src) == nil {
		return dst
	}
	return WithPreparationFailureObserver(dst, observerFromContext(src))
}

func observerFromContext(ctx context.Context) preparationObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(preparationObserverKey{}).(preparationObserver)
	return observer
}

// ReportPreparationFailure must be called at the local Prepare return boundary,
// never on an upstream error or a rendered error message.
func ReportPreparationFailure(ctx context.Context, err error) {
	observer := observerFromContext(ctx)
	if observer == nil {
		return
	}
	var compat *ToolCompatibilityError
	if !errors.As(err, &compat) || compat == nil {
		return
	}
	reason := "unknown"
	switch compat.Reason {
	case ReasonJSONSyntax, ReasonHistoryLink, ReasonUnsupportedProtocol, ReasonUnsupportedGrammar,
		ReasonOpaqueHistory, ReasonDeclarationBudget, ReasonAttemptBudget, ReasonSharedCapacity,
		ReasonUpstreamContract, ReasonSchemaPolicy, ReasonCustomForcedCall, ReasonAmbiguousIdentity,
		ReasonInvalidCustomInput, ReasonInvalidConfiguration:
		reason = string(compat.Reason)
	}
	status := compat.StatusCode()
	if status < http.StatusBadRequest || status > 599 {
		reason, status = "unknown", http.StatusInternalServerError
	}
	observer(PreparationFailure{Phase: "prepare", Reason: reason, StatusCode: status})
}
