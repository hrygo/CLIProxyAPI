//go:build race

package executor

// Wall-clock assertions in this package measure single-pass traversal cost. The
// race detector changes absolute timings, so those bounds only run without it.
const raceDetectorEnabled = true
