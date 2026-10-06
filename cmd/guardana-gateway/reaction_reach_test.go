package main

import (
	"testing"

	"github.com/guardana/control/internal/brand"
)

// The stop list's reader, which a plane polls, and its writer, which only
// the operator's commands link (ADR-0046).
var (
	stopReader = []string{brand.ModulePath + "/internal/reaction/stoplist"}
	stopWriter = []string{brand.ModulePath + "/internal/reaction/stopwrite"}
)

// TestThePlaneReachesTheStopReaderAndNotTheWriter: on every release platform
// the plane's binary, its guarded trees and the adapters reach the stop
// list's reader, so the listing examined the code that reads stops, and none
// of them reaches the writer, which could append a stop or a lift.
func TestThePlaneReachesTheStopReaderAndNotTheWriter(t *testing.T) {
	t.Parallel()
	for _, l := range dependencies(t, planePatterns...) {
		for _, tree := range unreached(reached(l.deps, stopReader), stopReader) {
			t.Errorf("the plane does not reach %s on %s, so the listing examined too little", tree, l.platform)
		}
		for _, dep := range reached(l.deps, stopWriter) {
			t.Errorf("%s is reachable from the plane on %s", dep, l.platform)
		}
	}
}

// TestTheStopWriterReachCheckFindsTheWriter is the negative control: the
// listing of the reaction tree holds the writer on every release platform,
// and the same match finds it there.
func TestTheStopWriterReachCheckFindsTheWriter(t *testing.T) {
	t.Parallel()
	for _, l := range dependencies(t, "../../internal/reaction/...") {
		for _, tree := range unreached(reached(l.deps, stopWriter), stopWriter) {
			t.Errorf("the reaction tree holds %s on %s, and the check did not find it", tree, l.platform)
		}
	}
}
