package common

import "context"

type geminiBufferedUsageKey struct{}

type geminiBufferedUsage struct {
	owner     *any
	snapshots [][]byte
}

// WithGeminiBufferedUsage attaches already-read usage only to the named stream
// translation state, so nested plugin requests cannot inherit its counts.
func WithGeminiBufferedUsage(ctx context.Context, owner *any, snapshots [][]byte) context.Context {
	if ctx == nil || owner == nil || len(snapshots) == 0 {
		return ctx
	}
	return context.WithValue(ctx, geminiBufferedUsageKey{}, geminiBufferedUsage{owner: owner, snapshots: snapshots})
}

// GeminiBufferedUsage returns snapshots only for the stream that owns them.
func GeminiBufferedUsage(ctx context.Context, owner *any) [][]byte {
	if ctx == nil || owner == nil {
		return nil
	}
	value, ok := ctx.Value(geminiBufferedUsageKey{}).(geminiBufferedUsage)
	if !ok || value.owner != owner {
		return nil
	}
	return value.snapshots
}
