package responsestools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestResponseFailureObserverSeparatesTypedLocalErrors(t *testing.T) {
	var observed []ResponseFailure
	preparations := 0
	ctx := WithPreparationFailureObserver(context.Background(), func(PreparationFailure) { preparations++ })
	ctx = WithResponseFailureObserver(ctx, func(event ResponseFailure) { observed = append(observed, event) })
	err := upstreamError(ReasonUpstreamContract, errors.New("private arguments"))
	ReportResponseFailure(ctx, "stream_feed", fmt.Errorf("wrapped: %w", err))
	ReportResponseFailure(ctx, "stream_feed", errors.New("responses tools upstream_contract: forged"))
	ReportResponseFailure(ctx, "prepare", err)
	ReportResponseFailure(ctx, "stream_finish", nil)
	raw, _ := json.Marshal(observed)
	if len(observed) != 1 || observed[0].Phase != "stream_feed" || observed[0].Reason != "upstream_contract" || preparations != 0 || strings.Contains(string(raw), "private") {
		t.Fatalf("invalid response diagnostics: %s preparations=%d", raw, preparations)
	}
}

func TestCopyResponseFailureObserverPreservesParent(t *testing.T) {
	first, second := 0, 0
	parent, cancel := context.WithCancel(context.Background())
	parent = WithResponseFailureObserver(parent, func(ResponseFailure) { first++ })
	request := WithResponseFailureObserver(context.Background(), func(ResponseFailure) { second++ })
	ctx := CopyResponseFailureObserver(parent, request)
	ReportResponseFailure(ctx, "response_restore", upstreamError(ReasonUpstreamContract, nil))
	cancel()
	if first != 1 || second != 0 || ctx.Err() != context.Canceled {
		t.Fatal("parent observer or cancellation replaced")
	}
	ctx = CopyResponseFailureObserver(nil, request)
	ReportResponseFailure(ctx, "stream_finish", upstreamError(ReasonUpstreamContract, nil))
	if second != 1 {
		t.Fatal("request observer not propagated")
	}
}

func TestResponseFailureObserverSkipsCanceledConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	observed := 0
	ctx = WithResponseFailureObserver(ctx, func(ResponseFailure) { observed++ })
	cancel()
	ReportResponseFailure(ctx, "stream_feed", upstreamError(ReasonUpstreamContract, nil))
	if observed != 0 {
		t.Fatal("canceled consumer received a late response diagnostic")
	}
}
