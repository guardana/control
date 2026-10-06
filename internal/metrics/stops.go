package metrics

import (
	"slices"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// stopRows are the stop list reader's (ADR-0046).
var stopRows = []Metric{
	count("stops_polls_total", "Stops.Polls",
		"Reads of the stop list, the first included.",
		func(r Reading) uint64 { return r.Stops.Polls }),
	labelled(Counter, "stops_poll_failures_total", "cause", "Stops.Failed",
		"Reads of the stop list whose state was unknown, by cause; each blocked every call until a read was whole again; "+
			"a cause the reader does not declare is counted under "+Other+".",
		func(r Reading) ([]sample, error) { return stopCauseSamples(r.Stops.Failed) }),
	level("stops_active_entries", "Stops.Active",
		"Stops active at the clock of the last read of the stop list.",
		func(r Reading) int64 { return int64(r.Stops.Active) }),
}

func stopCauseSamples(failed map[reaction.Cause]uint64) ([]sample, error) {
	return closedSamples(failed, func(c reaction.Cause) bool { return slices.Contains(stoplist.Causes(), c) })
}
