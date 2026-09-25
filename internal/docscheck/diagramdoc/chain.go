package diagramdoc

import (
	"fmt"
	"strings"

	"github.com/guardana/control/internal/evidence"
)

// stateIDs names each state of the chain, as the validator names it, by a
// diagram identifier. A state the validator names otherwise is refused, so a
// renamed state cannot be drawn under an old name.
var stateIDs = map[string]string{
	"the start of a trail":               "[*]",
	"ACTION_PROPOSED":                    "proposed",
	"POLICY_DECIDED":                     "decided",
	"APPROVAL_REQUESTED":                 "requested",
	"a decided approval":                 "approved",
	"an approval window nobody answered": "expired",
	"ACTION_STARTED":                     "started",
	"a closed action":                    "closed",
}

// chainDiagram renders the chain's state machine as a state diagram from
// the listing: one transition per allowed edge that moves the trail, and a
// note for the kinds that leave it where it is.
func chainDiagram() ([]byte, error) {
	steps := evidence.ChainSteps()
	if len(steps) == 0 {
		return nil, fmt.Errorf("%w: the evidence package listed no chain step", ErrBlock)
	}
	var b strings.Builder
	b.WriteString("```mermaid\nstateDiagram-v2\n")
	stays := map[string][]string{}
	var stayOrder []string
	for _, s := range steps {
		from, ok := stateIDs[s.From]
		if !ok {
			return nil, fmt.Errorf("%w: no identifier for the state %q", ErrBlock, s.From)
		}
		switch {
		case s.Unplaceable || !s.Allowed:
			continue
		case s.To == s.From:
			kind := kindName(s.Kind)
			if _, seen := stays[kind]; !seen {
				stayOrder = append(stayOrder, kind)
			}
			stays[kind] = append(stays[kind], from)
		default:
			to, ok := stateIDs[s.To]
			if !ok {
				return nil, fmt.Errorf("%w: no identifier for the state %q", ErrBlock, s.To)
			}
			fmt.Fprintf(&b, "    %s --> %s: %s\n", from, to, kindName(s.Kind))
		}
	}
	b.WriteString("```\n\nSources: `internal/evidence/chain.go`, `internal/evidence/chainsteps.go`.\n\n")
	for _, kind := range stayOrder {
		fmt.Fprintf(&b, "`%s` leaves the trail where it is, from %s.\n", kind, fromList(stays[kind]))
	}
	b.WriteString("A kind this version does not know is placed nowhere and read as indeterminate, never refused.\n")
	return []byte(b.String()), nil
}

func fromList(states []string) string {
	if len(states) == len(stateIDs) {
		return "every state"
	}
	return "every state but " + missing(states)
}

func missing(states []string) string {
	var out []string
	for _, id := range []string{"[*]", "proposed", "decided", "requested", "approved", "expired", "started", "closed"} {
		found := false
		for _, s := range states {
			if s == id {
				found = true
			}
		}
		if !found {
			if id == "[*]" {
				id = "the start"
			}
			out = append(out, id)
		}
	}
	return strings.Join(out, ", ")
}

func kindName(k interface{ String() string }) string {
	return strings.TrimPrefix(k.String(), "EVENT_KIND_")
}
