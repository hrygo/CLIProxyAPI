package executionregistry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newDrainHoldTestScope(t *testing.T) (*Registry, *Scope) {
	t.Helper()
	r := New()
	pending, err := r.BeginDispatch()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := r.Install(pending, ScopeSpec{CredentialID: "credential", Model: "model", Kind: "websocket", Accounted: true})
	if err != nil {
		t.Fatal(err)
	}
	return r, scope
}

func TestDrainHoldDoesNotAffectReleaseTicketOrInFlightAndDrainWaits(t *testing.T) {
	r, scope := newDrainHoldTestScope(t)
	ack := make(chan struct{})
	var gotGroup ReleaseGroup
	var gotSequence int64
	r.SetReleaseSink(func(group ReleaseGroup, sequence int64) *ReleaseTicket {
		gotGroup, gotSequence = group, sequence
		return NewReleaseTicket(group, sequence, ack)
	})
	canceled := make(chan struct{})
	release, err := scope.TrackDrain(func() { close(canceled) })
	if err != nil {
		t.Fatal(err)
	}
	ticket := scope.EndWithRelease("terminal")
	if gotGroup != (ReleaseGroup{CredentialID: "credential", Model: "model"}) || gotSequence != 1 || ticket == nil || ticket.Group != gotGroup || ticket.Sequence != gotSequence {
		t.Fatalf("release group=%+v sequence=%d ticket=%+v", gotGroup, gotSequence, ticket)
	}
	if got := r.FreezeInFlight(time.Now()).Executions; len(got) != 0 {
		t.Fatalf("delivery hold appeared in Home in-flight snapshot: %+v", got)
	}
	close(ack)
	if err := ticket.Wait(context.Background()); err != nil {
		t.Fatalf("release acknowledgement: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	drained := make(chan error, 1)
	go func() { drained <- r.Drain(ctx) }()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("Drain did not cancel the delivery hold")
	}
	select {
	case err := <-drained:
		t.Fatalf("Drain returned before the hold was released: %v", err)
	default:
	}
	release()
	if err := <-drained; err != nil {
		t.Fatalf("Drain after release: %v", err)
	}
	release()
}

func TestCloseCancelsDrainHoldWithoutWaitingForRelease(t *testing.T) {
	r, scope := newDrainHoldTestScope(t)
	canceled := make(chan struct{})
	release, err := scope.TrackDrain(func() { close(canceled) })
	if err != nil {
		t.Fatal(err)
	}
	scope.End("terminal")
	done := make(chan error, 1)
	go func() { done <- r.Close() }()
	select {
	case <-canceled:
	case <-time.After(8 * time.Second):
		t.Fatal("Close did not cancel the hold")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Close waited for an unconsumed terminal hold")
	}
	release()
}

func TestDrainHoldRacesWithReleaseAndClose(t *testing.T) {
	for i := 0; i < 100; i++ {
		r, scope := newDrainHoldTestScope(t)
		var cancels atomic.Int32
		release, err := scope.TrackDrain(func() { cancels.Add(1) })
		if err != nil {
			t.Fatal(err)
		}
		scope.End("terminal")
		start := make(chan struct{})
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); <-start; release() }()
		go func() {
			defer wg.Done()
			<-start
			if err := r.Drain(ctx); err != nil && !errors.Is(err, ErrRegistryClosed) {
				t.Errorf("iteration %d Drain: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := r.Close(); err != nil {
				t.Errorf("iteration %d Close: %v", i, err)
			}
		}()
		close(start)
		wg.Wait()
		cancel()
		if got := cancels.Load(); got > 1 {
			t.Fatalf("iteration %d cancel called %d times", i, got)
		}
	}
}

func TestDrainHoldRejectsEndedOrDrainingScope(t *testing.T) {
	r, scope := newDrainHoldTestScope(t)
	scope.End("complete")
	if _, err := scope.TrackDrain(func() {}); !errors.Is(err, ErrRegistryNotAccepting) {
		t.Fatalf("TrackDrain on ended scope: %v", err)
	}
	if err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.TrackDrain(func() {}); !errors.Is(err, ErrRegistryNotAccepting) {
		t.Fatalf("TrackDrain after Drain: %v", err)
	}
}

func TestTrackDrainRacesWithScopeEndAndShutdown(t *testing.T) {
	for _, shutdown := range []string{"drain", "close"} {
		t.Run(shutdown, func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				r, scope := newDrainHoldTestScope(t)
				start := make(chan struct{})
				canceled := make(chan struct{})
				var cancelOnce sync.Once
				type result struct {
					release func()
					err     error
				}
				tracked := make(chan result, 1)
				ended := make(chan struct{})
				stopped := make(chan error, 1)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				go func() {
					<-start
					release, err := scope.TrackDrain(func() { cancelOnce.Do(func() { close(canceled) }) })
					tracked <- result{release: release, err: err}
				}()
				go func() { <-start; scope.End("race"); close(ended) }()
				go func() {
					<-start
					if shutdown == "drain" {
						stopped <- r.Drain(ctx)
					} else {
						stopped <- r.Close()
					}
				}()
				close(start)
				got := <-tracked
				if got.err != nil && !errors.Is(got.err, ErrRegistryNotAccepting) {
					t.Fatalf("iteration %d TrackDrain: %v", iteration, got.err)
				}
				select {
				case <-canceled:
				case <-ctx.Done():
					t.Fatalf("iteration %d: registered or rejected hold was not canceled", iteration)
				}
				if got.release != nil {
					got.release()
				}
				if err := <-stopped; err != nil {
					t.Fatalf("iteration %d %s: %v", iteration, shutdown, err)
				}
				<-ended
				cancel()
			}
		})
	}
}
