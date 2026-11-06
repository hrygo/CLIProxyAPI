package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executionregistry"
)

func homeDrainHoldTestSelection(t *testing.T) (*executionregistry.Registry, *HomeDispatchSelection, context.Context, func()) {
	t.Helper()
	r := executionregistry.New()
	pending, err := r.BeginDispatch()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := r.Install(pending, executionregistry.ScopeSpec{CredentialID: "credential", Model: "model", Accounted: true})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := newHomeDispatchSelection(&Auth{ID: "credential"}, nil, "codex", scope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, release, err := selection.AttemptContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r, selection, ctx, release
}

func TestHomeTerminalAttemptDrainWaitsForRelease(t *testing.T) {
	r, selection, attempt, release := homeDrainHoldTestSelection(t)
	selection.EndWebsocketTurn(attempt, "upstream_error")
	if selection.Active() || attempt.Err() != nil {
		t.Fatalf("selection active=%t attempt error=%v", selection.Active(), attempt.Err())
	}
	if executions := r.FreezeInFlight(time.Now()).Executions; len(executions) != 0 {
		t.Fatalf("released selection still counted: %+v", executions)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- r.Drain(ctx) }()
	select {
	case <-attempt.Done():
	case <-ctx.Done():
		t.Fatal("Drain did not cancel the terminal attempt")
	}
	select {
	case err := <-drained:
		t.Fatalf("Drain returned before attempt cleanup: %v", err)
	default:
	}
	release()
	if err := <-drained; err != nil {
		t.Fatalf("Drain after cleanup: %v", err)
	}
}

func TestHomeTerminalAttemptReleaseRacesWithEndAndDrain(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		r, selection, attempt, release := homeDrainHoldTestSelection(t)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); <-start; selection.EndWebsocketTurn(attempt, "upstream_error") }()
		go func() { defer wg.Done(); <-start; release() }()
		go func() {
			defer wg.Done()
			<-start
			if err := r.Drain(ctx); err != nil {
				t.Errorf("iteration %d Drain: %v", iteration, err)
			}
		}()
		close(start)
		wg.Wait()
		cancel()
		if attempt.Err() == nil {
			t.Fatalf("iteration %d attempt remains active", iteration)
		}
	}
}
