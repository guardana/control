package main

import (
	"testing"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// TestHealthSaysWhereRunsComeFrom: /healthz reads each runs member from its
// own statistic, local runs from the pipeline's count and opened runs from the
// listener's refusals and the pipeline's state failures.
func TestHealthSaysWhereRunsComeFrom(t *testing.T) {
	local := runCounters(false, gateway.Stats{Runs: 3, RunStateFailures: 9}, adaptermcp.Stats{})
	if local["kind"] != "local" || local["kept"] != 3 || len(local) != 2 {
		t.Errorf("local runs: %v, want kind local and kept 3 alone", local)
	}
	adapter := adaptermcp.Stats{
		RunsRefusedAtRequest: adaptermcp.RunRefusals{gateway.RunClosed: 4},
		RunsRefusedAtMessage: adaptermcp.RunRefusals{gateway.RunExpired: 5},
	}
	opened := runCounters(true, gateway.Stats{Runs: 3, RunStateFailures: 2}, adapter)
	request, _ := opened["refused_at_request"].(adaptermcp.RunRefusals)
	message, _ := opened["refused_at_message"].(adaptermcp.RunRefusals)
	if opened["kind"] != "opened" || opened["state_failures"] != uint64(2) ||
		request[gateway.RunClosed] != 4 || message[gateway.RunExpired] != 5 || opened["kept"] != nil {
		t.Errorf("opened runs: %v", opened)
	}
}
