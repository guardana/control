package supervise

import controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

// refSet gathers what one finding rests on without holding all of it: of
// the refs offered it keeps the first MaxFindingRefs of each verdict, all
// that cut can keep of them, and counts the rest, so a rule that compares
// many calls holds a bounded number of refs per finding.
type refSet struct {
	refs    []ref
	kept    [3]int
	leftOut uint64
}

// take reports whether a ref of verdict v is kept, and counts it when it is
// not, so a caller builds only the refs kept.
func (s *refSet) take(v controlv1.FindingVerdict) bool {
	if s.kept[rank(v)] == MaxFindingRefs {
		s.leftOut++
		return false
	}
	s.kept[rank(v)]++
	return true
}

// event offers ev of rq.
func (s *refSet) event(rq *request, ev *controlv1.Event) {
	if s.take(rq.verdict()) {
		s.refs = append(s.refs, rq.ref(ev))
	}
}

// into puts what s gathered into d after d's own refs.
func (s *refSet) into(d *draft) {
	d.refs = append(d.refs, s.refs...)
	d.leftOut += s.leftOut
}
