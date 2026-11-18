package helps

import (
	"context"
	"errors"
	"sync"

	"github.com/gin-gonic/gin"
)

type httpAuxiliaryWorkKey struct{}
type httpAuxiliaryWorkValue struct{ scope *HTTPAuxiliaryWorkScope }

// HTTPAuxiliaryWorkScope gives an opt-in owner cancellation and join semantics
// for independent HTTP maintenance work. Without a scope, lifetimes are unchanged.
type HTTPAuxiliaryWorkScope struct {
	mu        sync.Mutex
	closing   bool
	started   uint64
	completed uint64
	rejected  uint64
	active    map[uint64]context.CancelFunc
	idle      chan struct{}
}

// HTTPAuxiliaryWorkSnapshot contains counters only, never task or credential data.
type HTTPAuxiliaryWorkSnapshot struct {
	Version   int    `json:"version"`
	Started   uint64 `json:"started"`
	Completed uint64 `json:"completed"`
	Rejected  uint64 `json:"rejected"`
	Active    int    `json:"active"`
	Sealed    bool   `json:"sealed"`
}

func NewHTTPAuxiliaryWorkScope() *HTTPAuxiliaryWorkScope {
	idle := make(chan struct{})
	close(idle)
	return &HTTPAuxiliaryWorkScope{active: make(map[uint64]context.CancelFunc), idle: idle}
}

func WithHTTPAuxiliaryWorkScope(ctx context.Context, scope *HTTPAuxiliaryWorkScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpAuxiliaryWorkKey{}, httpAuxiliaryWorkValue{scope})
}

func httpAuxiliaryWorkValueFromContext(ctx context.Context) (httpAuxiliaryWorkValue, bool) {
	if ctx == nil {
		return httpAuxiliaryWorkValue{}, false
	}
	if value, ok := ctx.Value(httpAuxiliaryWorkKey{}).(httpAuxiliaryWorkValue); ok {
		return value, true
	}
	if c, ok := ctx.Value("gin").(*gin.Context); ok && c != nil && c.Request != nil {
		value, ok := c.Request.Context().Value(httpAuxiliaryWorkKey{}).(httpAuxiliaryWorkValue)
		return value, ok
	}
	return httpAuxiliaryWorkValue{}, false
}

// CopyHTTPAuxiliaryWorkScope preserves destination lifetime and explicit disable.
func CopyHTTPAuxiliaryWorkScope(dst, src context.Context) context.Context {
	if dst == nil {
		dst = context.Background()
	}
	if _, ok := httpAuxiliaryWorkValueFromContext(dst); ok {
		return dst
	}
	if value, ok := httpAuxiliaryWorkValueFromContext(src); ok {
		return WithHTTPAuxiliaryWorkScope(dst, value.scope)
	}
	return dst
}

// BeginHTTPAuxiliaryWork registers before launching a goroutine. Release must run
// after its cleanup, not merely when cancellation is requested or transport ends.
func BeginHTTPAuxiliaryWork(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	value, ok := httpAuxiliaryWorkValueFromContext(ctx)
	if !ok || value.scope == nil {
		return ctx, func() {}, nil
	}
	s := value.scope
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		s.rejected++
		return nil, nil, errors.New("auxiliary HTTP work admission closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(s.active) == 0 {
		s.idle = make(chan struct{})
	}
	s.started++
	id := s.started
	work, cancel := context.WithCancel(ctx)
	s.active[id] = cancel
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.active, id)
			s.completed++
			if len(s.active) == 0 {
				close(s.idle)
			}
		})
	}
	return work, release, nil
}

// CloseContext rejects new work, cancels existing work, and awaits release.
// Cancellation of the join leaves active work visible; it cannot certify cleanup.
func (s *HTTPAuxiliaryWorkScope) CloseContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("missing auxiliary HTTP work close context")
	}
	s.mu.Lock()
	s.closing = true
	idle := s.idle
	var cancellations []context.CancelFunc
	for _, cancel := range s.active {
		cancellations = append(cancellations, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancellations {
		cancel()
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *HTTPAuxiliaryWorkScope) Snapshot() HTTPAuxiliaryWorkSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return HTTPAuxiliaryWorkSnapshot{Version: 1, Started: s.started, Completed: s.completed,
		Rejected: s.rejected, Active: len(s.active), Sealed: s.closing && len(s.active) == 0}
}
