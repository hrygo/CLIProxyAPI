package responsestools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPreparationFailureObserverUsesTypedReasonsWithoutPayloads(t *testing.T) {
	var observed []PreparationFailure
	ctx := WithPreparationFailureObserver(context.Background(), func(event PreparationFailure) { observed = append(observed, event) })
	ReportPreparationFailure(ctx, errors.New("responses tools declaration_budget: upstream imitation"))
	ReportPreparationFailure(ctx, nil)
	if len(observed) != 0 {
		t.Fatal("untyped upstream text produced a trusted diagnostic")
	}
	ReportPreparationFailure(ctx, fmt.Errorf("wrapped: %w", budgetError(ReasonDeclarationBudget, errors.New("private receipt"))))
	if len(observed) != 1 || observed[0].Phase != "prepare" || observed[0].Reason != "declaration_budget" || observed[0].StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("typed diagnostic lost: %+v", observed)
	}
	ReportPreparationFailure(ctx, &ToolCompatibilityError{Reason: Reason("private receipt"), Err: errors.New("private grammar")})
	if len(observed) != 2 || observed[1].Reason != "unknown" || strings.Contains(fmt.Sprint(observed), "private") {
		t.Fatal("arbitrary reason or error payload leaked")
	}
	ReportPreparationFailure(nil, budgetError(ReasonAttemptBudget, nil))
}

func TestCopyPreparationFailureObserverPreservesParentContext(t *testing.T) {
	first, second := 0, 0
	parent := WithPreparationFailureObserver(context.Background(), func(PreparationFailure) { first++ })
	request := WithPreparationFailureObserver(context.Background(), func(PreparationFailure) { second++ })
	ctx := CopyPreparationFailureObserver(parent, request)
	ReportPreparationFailure(ctx, budgetError(ReasonDeclarationBudget, nil))
	if first != 1 || second != 0 {
		t.Fatal("request observer replaced the caller context observer")
	}
	ctx = CopyPreparationFailureObserver(context.Background(), request)
	ReportPreparationFailure(ctx, budgetError(ReasonDeclarationBudget, nil))
	if second != 1 {
		t.Fatal("request observer was not propagated")
	}
}
