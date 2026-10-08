package supervise

import (
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/proto"
)

func link(id, prev string) *controlv1.Event {
	return &controlv1.Event{EventId: id, PrevEventId: prev}
}

func ids(events []*controlv1.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.GetEventId()+"<"+ev.GetPrevEventId())
	}
	return out
}

// TestLinkedOrdersOneChainAndRefusesEveryOtherShape: events of one chain in
// any order come back in the order of their links; any other shape comes
// back as read and refused, the walk taking no event twice.
func TestLinkedOrdersOneChainAndRefusesEveryOtherShape(t *testing.T) {
	e1, e2, e3, e4 := link("e1", ""), link("e2", "e1"), link("e3", "e2"), link("e4", "e3")
	for name, c := range map[string]struct {
		in   []*controlv1.Event
		want []string
		ok   bool
	}{
		"in order":                   {[]*controlv1.Event{e1, e2, e3, e4}, []string{"e1<", "e2<e1", "e3<e2", "e4<e3"}, true},
		"the last batch first":       {[]*controlv1.Event{e4, e1, e2, e3}, []string{"e1<", "e2<e1", "e3<e2", "e4<e3"}, true},
		"reversed":                   {[]*controlv1.Event{e4, e3, e2, e1}, []string{"e1<", "e2<e1", "e3<e2", "e4<e3"}, true},
		"two heads":                  {[]*controlv1.Event{e2, e1, link("e9", ""), e3}, []string{"e2<e1", "e1<", "e9<", "e3<e2"}, false},
		"no head":                    {[]*controlv1.Event{e3, e2}, []string{"e3<e2", "e2<e1"}, false},
		"a fork":                     {[]*controlv1.Event{e1, e2, e3, link("e9", "e2")}, []string{"e1<", "e2<e1", "e3<e2", "e9<e2"}, false},
		"a gap":                      {[]*controlv1.Event{e4, e1, e3}, []string{"e4<e3", "e1<", "e3<e2"}, false},
		"an exact repeat":            {[]*controlv1.Event{e1, e2, proto.Clone(e2).(*controlv1.Event), e3}, []string{"e1<", "e2<e1", "e2<e1", "e3<e2"}, false},
		"an id twice off the chain":  {[]*controlv1.Event{e1, e2, link("e9", "e7"), link("e9", "e8")}, []string{"e1<", "e2<e1", "e9<e7", "e9<e8"}, false},
		"a cycle off the chain":      {[]*controlv1.Event{e1, e2, link("e8", "e9"), link("e9", "e8")}, []string{"e1<", "e2<e1", "e8<e9", "e9<e8"}, false},
		"a cycle reached from head":  {[]*controlv1.Event{e1, e2, link("e1", "e2")}, []string{"e1<", "e2<e1", "e1<e2"}, false},
		"a link to itself from head": {[]*controlv1.Event{link("e1", "e1"), e1}, []string{"e1<e1", "e1<"}, false},
		"nothing":                    {nil, []string{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := linked(c.in)
			if ok != c.ok || !slices.Equal(ids(got), c.want) {
				t.Fatalf("linked gave %q, %v; want %q, %v", ids(got), ok, c.want, c.ok)
			}
		})
	}
}
