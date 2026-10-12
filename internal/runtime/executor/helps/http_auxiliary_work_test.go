package helps

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHTTPAuxiliaryWorkScopeCancelsAndJoinsBeforeSeal(t *testing.T) {
	scope := NewHTTPAuxiliaryWorkScope()
	ctx := WithHTTPAuxiliaryWorkScope(context.Background(), scope)
	work, release, err := BeginHTTPAuxiliaryWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closeResult := make(chan error, 1)
	go func() { closeResult <- scope.CloseContext(context.Background()) }()
	<-work.Done()
	select {
	case <-closeResult:
		t.Fatal("scope sealed before work released")
	default:
	}
	if _, _, err := BeginHTTPAuxiliaryWork(ctx); err == nil {
		t.Fatal("closing scope admitted new work")
	}
	release()
	release()
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
	state := scope.Snapshot()
	if !state.Sealed || state.Active != 0 || state.Started != 1 || state.Completed != 1 || state.Rejected != 1 {
		t.Fatalf("unexpected settlement: %+v", state)
	}
}

func TestHTTPAuxiliaryWorkScopeCanceledJoinRetainsActiveWork(t *testing.T) {
	scope := NewHTTPAuxiliaryWorkScope()
	ctx := WithHTTPAuxiliaryWorkScope(context.Background(), scope)
	_, release, err := BeginHTTPAuxiliaryWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scope.CloseContext(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("unsettled close = %v", err)
	}
	if state := scope.Snapshot(); state.Sealed || state.Active != 1 || state.Completed != 0 {
		t.Fatal("cancellation fabricated settlement")
	}
	release()
	if err := scope.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAuxiliaryWorkScopeCopyPreservesIndependentLifetimeAndExplicitDisable(t *testing.T) {
	scope := NewHTTPAuxiliaryWorkScope()
	source, cancel := context.WithCancel(WithHTTPAuxiliaryWorkScope(context.Background(), scope))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(source)
	copy := CopyHTTPAuxiliaryWorkScope(context.Background(), context.WithValue(context.Background(), "gin", c))
	work, release, err := BeginHTTPAuxiliaryWork(copy)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if work.Err() != nil {
		t.Fatal("copy inherited source cancellation")
	}
	disabled := CopyHTTPAuxiliaryWorkScope(WithHTTPAuxiliaryWorkScope(context.Background(), nil), source)
	_, disabledRelease, err := BeginHTTPAuxiliaryWork(disabled)
	if err != nil || scope.Snapshot().Started != 1 {
		t.Fatal("explicitly disabled scope was overwritten")
	}
	disabledRelease()
	release()
	if err := scope.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAuxiliaryWorkScopeConcurrentReleaseAndClose(t *testing.T) {
	scope := NewHTTPAuxiliaryWorkScope()
	ctx := WithHTTPAuxiliaryWorkScope(context.Background(), scope)
	var workers sync.WaitGroup
	for range 32 {
		work, release, err := BeginHTTPAuxiliaryWork(ctx)
		if err != nil {
			t.Fatal(err)
		}
		workers.Add(1)
		go func() { defer workers.Done(); <-work.Done(); release() }()
	}
	if err := scope.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if state := scope.Snapshot(); state.Active != 0 || state.Completed != 32 || !state.Sealed {
		t.Fatalf("concurrent work leaked: %+v", state)
	}
}
