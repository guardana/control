package supervise

import (
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// place is where an export holds an event: the export's place in the input
// and the event's in the export.
type place struct{ export, index int }

// placesOf is, by event id, where each export holds the event the run read
// under that id, once per export. A copy of other content is no copy of it.
func (e *evaluation) placesOf() map[string][]place {
	if e.places != nil {
		return e.places
	}
	taken := make(map[string]*controlv1.Event, len(e.rd.events))
	for _, ev := range e.rd.events {
		taken[ev.GetEventId()] = ev
	}
	e.places = map[string][]place{}
	for i, x := range e.in.Exports {
		for j, ev := range x.Events {
			id := ev.GetEventId()
			t, ok := taken[id]
			ps := e.places[id]
			if !ok || len(ps) > 0 && ps[len(ps)-1].export == i || t != ev && !proto.Equal(t, ev) {
				continue
			}
			e.places[id] = append(ps, place{export: i, index: j})
		}
	}
	return e.places
}

// after reports whether then came after first, and the verdict that order
// allows. An export that holds both orders them by its sequence, and only
// that confirms; exports that order them both ways leave it untold. When no
// export holds both, their times may come from planes whose clocks differ.
func (e *evaluation) after(first, then *controlv1.Event) (controlv1.FindingVerdict, bool) {
	places := e.placesOf()
	later, earlier := false, false
	for _, p := range places[first.GetEventId()] {
		for _, q := range places[then.GetEventId()] {
			if p.export == q.export {
				later, earlier = later || q.index > p.index, earlier || q.index < p.index
			}
		}
	}
	switch {
	case later && earlier:
		return indeterminate, true
	case later:
		return confirmed, true
	case earlier:
		return indeterminate, false
	}
	return timedAfter(first.GetOccurredAt(), then.GetOccurredAt())
}

// timedAfter places then after first by two clocks that may differ: a later
// time suggests, the same time or none is untold, an earlier time is not
// after.
func timedAfter(first, then *timestamppb.Timestamp) (controlv1.FindingVerdict, bool) {
	switch {
	case !first.IsValid() || !then.IsValid():
		return indeterminate, true
	case then.AsTime().After(first.AsTime()):
		return suspected, true
	case then.AsTime().Equal(first.AsTime()):
		return indeterminate, true
	}
	return indeterminate, false
}
