package auth

import (
	"context"
	"strings"
	"sync"
	"time"

	homekv "github.com/router-for-me/CLIProxyAPI/v8/internal/home"
)

type antigravityUseCreditsContextKey struct{}

// WithAntigravityCredits returns a child context that signals the executor to
// inject enabledCreditTypes into the request payload.
func WithAntigravityCredits(ctx context.Context) context.Context {
	return context.WithValue(ctx, antigravityUseCreditsContextKey{}, true)
}

// AntigravityCreditsRequested reports whether the context carries the credits flag.
func AntigravityCreditsRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(antigravityUseCreditsContextKey{}).(bool)
	return v
}

// AntigravityCreditsHintRefreshInterval defines how long a cached AI credits hint
// remains fresh before it should be re-validated.
const AntigravityCreditsHintRefreshInterval = 10 * time.Minute

// AntigravityCreditsHint stores the latest known AI credits state for one auth.
type AntigravityCreditsHint struct {
	Known           bool
	Available       bool
	CreditAmount    float64
	MinCreditAmount float64
	PaidTierID      string
	UpdatedAt       time.Time
}

// IsAntigravityCreditsHintFresh reports whether a known credits hint is still
// within AntigravityCreditsHintRefreshInterval of now.
func IsAntigravityCreditsHintFresh(hint AntigravityCreditsHint, now time.Time) bool {
	return hint.Known && !hint.UpdatedAt.IsZero() && now.Sub(hint.UpdatedAt) < AntigravityCreditsHintRefreshInterval
}

var antigravityCreditsHintByAuth sync.Map

// antigravityCreditsHintMu serializes read-modify-write of the in-memory hint so
// a recovery path cannot erase a hint a refresh just published.
var antigravityCreditsHintMu sync.Mutex

// SetAntigravityCreditsHint updates the latest known AI credits state for an auth.
func SetAntigravityCreditsHint(authID string, hint AntigravityCreditsHint) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	if hint.UpdatedAt.IsZero() {
		hint.UpdatedAt = time.Now()
	}
	if _, homeMode, _ := homekv.CurrentKVClient(); homeMode {
		homekv.KVSetJSONBestEffort(context.Background(), antigravityCreditsHintKey(authID), hint, 30*time.Minute)
		return
	}
	antigravityCreditsHintMu.Lock()
	defer antigravityCreditsHintMu.Unlock()
	antigravityCreditsHintByAuth.Store(authID, hint)
}

// ClearAntigravityCreditsHintIfStale drops a cached unavailable hint only once it
// has left its freshness window. A hint published by a concurrent refresh is left
// in place, so observing recovery cannot erase the state that proves it.
func ClearAntigravityCreditsHintIfStale(authID string, now time.Time) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if _, homeMode, _ := homekv.CurrentKVClient(); homeMode {
		var homeHint AntigravityCreditsHint
		_, found, errGet := homekv.KVGetJSONRequired(context.Background(), antigravityCreditsHintKey(authID), &homeHint)
		if errGet != nil || !found || !homeHint.Known || homeHint.Available || IsAntigravityCreditsHintFresh(homeHint, now) {
			return false
		}
		homekv.KVSetJSONBestEffort(context.Background(), antigravityCreditsHintKey(authID), AntigravityCreditsHint{UpdatedAt: now}, 30*time.Minute)
		return true
	}
	antigravityCreditsHintMu.Lock()
	defer antigravityCreditsHintMu.Unlock()
	value, ok := antigravityCreditsHintByAuth.Load(authID)
	if !ok {
		return false
	}
	hint, ok := value.(AntigravityCreditsHint)
	if !ok || !hint.Known || hint.Available || IsAntigravityCreditsHintFresh(hint, now) {
		return false
	}
	antigravityCreditsHintByAuth.Store(authID, AntigravityCreditsHint{UpdatedAt: now})
	return true
}

// GetAntigravityCreditsHint returns the latest known AI credits state for an auth.
func GetAntigravityCreditsHint(authID string) (AntigravityCreditsHint, bool) {
	hint, ok, err := GetAntigravityCreditsHintRequired(context.Background(), authID)
	if err == nil {
		return hint, ok
	}
	return AntigravityCreditsHint{}, false
}

// GetAntigravityCreditsHintRequired returns the latest known AI credits state for request-time paths.
func GetAntigravityCreditsHintRequired(ctx context.Context, authID string) (AntigravityCreditsHint, bool, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return AntigravityCreditsHint{}, false, nil
	}
	var homeHint AntigravityCreditsHint
	homeMode, found, errGet := homekv.KVGetJSONRequired(ctx, antigravityCreditsHintKey(authID), &homeHint)
	if homeMode {
		return homeHint, found, errGet
	}
	antigravityCreditsHintMu.Lock()
	defer antigravityCreditsHintMu.Unlock()
	value, ok := antigravityCreditsHintByAuth.Load(authID)
	if !ok {
		return AntigravityCreditsHint{}, false, nil
	}
	hint, ok := value.(AntigravityCreditsHint)
	if !ok {
		antigravityCreditsHintByAuth.Delete(authID)
		return AntigravityCreditsHint{}, false, nil
	}
	return hint, true, nil
}

// HasKnownAntigravityCreditsHint reports whether credits state has been discovered for an auth.
func HasKnownAntigravityCreditsHint(authID string) bool {
	hint, ok := GetAntigravityCreditsHint(authID)
	return ok && hint.Known
}

func antigravityCreditsHintKey(authID string) string {
	return "cpa:antigravity:credits-hint:" + strings.TrimSpace(authID)
}

func antigravityCreditsAvailableForModel(auth *Auth, model string) bool {
	if auth == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "antigravity") {
		return false
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(model)), "claude") {
		return false
	}
	hint, ok := GetAntigravityCreditsHint(auth.ID)
	if !ok || !hint.Known {
		return false
	}
	return hint.Available
}
