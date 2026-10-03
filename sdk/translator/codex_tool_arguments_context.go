package translator

import "context"

type codexToolArgumentNormalizationKey struct{}

// WithCodexToolArgumentNormalization selects the Codex integer-argument
// compatibility policy for responses translated with ctx. HTTP handlers select
// it from the inbound client identity; direct SDK callers can explicitly opt in.
// Without this policy, Registry leaves numeric representations unchanged.
func WithCodexToolArgumentNormalization(ctx context.Context, enabled bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, codexToolArgumentNormalizationKey{}, enabled)
}

func codexToolArgumentNormalizationEnabled(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(codexToolArgumentNormalizationKey{}).(bool)
	return enabled
}
