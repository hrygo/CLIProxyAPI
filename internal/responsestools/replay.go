package responsestools

import "bytes"

// RequiresToolReplay probes the actual preparation path without network or
// tool execution. A private limiter avoids charging a routing probe to shared
// attempt capacity. All probe state is released before returning. A request
// error also requires the managed path so it is rejected before upstream I/O.
// Replaying includes response-only adaptation, not just request-body changes.
func RequiresToolReplay(payload []byte, policy RoutePolicy, limits Limits) bool {
	prepared, err := Prepare(payload, policy, limits, NewLimiter(limits))
	if prepared.Attempt != nil {
		prepared.Attempt.Close()
		return true
	}
	return err != nil || !bytes.Equal(payload, prepared.Body)
}
