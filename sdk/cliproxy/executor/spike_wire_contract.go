package executor

import "context"

type wireContractContextKey struct{}

// WireContract is the read-only tool contract visible to the outbound guard.
// It carries only tool identifiers, hashes and sizes: never the attempt, original
// client request, credentials, arguments, or tool outputs.
type WireContract struct {
	// SearchBridgeActive marks an attempt that adapted client tool_search,
	// including history-only turns without an active search declaration.
	SearchBridgeActive bool
	// SearchAliases lists bridged search aliases that must survive to the wire.
	SearchAliases []string
	// CustomAliases lists bridged custom aliases that must survive to the wire.
	CustomAliases []string
	// HistoryAliases lists bridged history names that must retain their call
	// identity without requiring a current tool declaration.
	HistoryAliases []string
	// RequiredFunctionNames lists function declarations that must survive all
	// provider translation and post-rule normalization.
	RequiredFunctionNames []string
	// ExactFunctionNames rejects function declarations outside the prepared
	// active set, including deferred tools reintroduced by post rules.
	ExactFunctionNames bool
	// HistoryCalls lists bridged historical calls whose identity and outputs
	// must remain linked in the final outbound payload.
	HistoryCalls []WireToolHistoryReference
	// MultipleCustomOutputs binds the guard to the explicit native replay policy.
	MultipleCustomOutputs bool
	// NativeHistoryEvents protects global order and call-input/output content
	// without retaining raw code, arguments or results in the context.
	NativeHistoryEvents []WireToolHistoryEvent
	// ActiveToolBytes is the normalized declaration size after adaptation.
	ActiveToolBytes int
	// MaxActiveToolBytes is the configured ceiling enforced after all
	// provider translation and post-rule normalization.
	MaxActiveToolBytes int
}

// WireToolHistoryReference is the minimal identity needed to validate one
// bridged historical tool call after provider translation.
type WireToolHistoryReference struct {
	Name           string
	AlternateNames []string
	CallID         string
	// OutputCount is the prepared cardinality; zero retains the legacy count of one.
	OutputCount int
}

// WithWireContract attaches the read-only wire contract for the final send
// guard. A nil contract leaves the context unchanged.
func WithWireContract(ctx context.Context, wire *WireContract) context.Context {
	if ctx == nil || wire == nil {
		return ctx
	}
	return context.WithValue(ctx, wireContractContextKey{}, wire)
}

// WireContractFromContext reads the guard contract back. Nil means the
// attempt is inactive and guards are no-ops.
func WireContractFromContext(ctx context.Context) *WireContract {
	if ctx == nil {
		return nil
	}
	wire, _ := ctx.Value(wireContractContextKey{}).(*WireContract)
	return wire
}
