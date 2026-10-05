package supervise

import (
	"cmp"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// instance is one call of a step: a plane request. An observation no plane
// call joins is the runtime's claim alone and never an instance. at is when
// it was proposed, and its seq orders it after another of the same time;
// endAt is when its terminal event happened.
type instance struct {
	step            int
	at, endAt       time.Time
	timed, endTimed bool
	seq             int
	failed          bool
	start, end      ref
	request         string
}

func (a *instance) before(b *instance) bool {
	return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.seq, b.seq)) < 0
}

// evaluation is one run's inputs as the rules read them.
type evaluation struct {
	in        Input
	p         *Procedure
	ix        index
	rd        *read
	reqs      []*request
	byRequest map[string]*request
	// unjoined are the run's tool observations that no plane call joins, and
	// joinDoubt those of them a join that ran out of steps may have missed.
	unjoined  []*observev1.Observation
	joinDoubt map[string]bool
	inst      []*instance
	ofStep    map[int][]*instance
	// reported are the unjoined observations of each step, by id.
	reported map[int][]string
	// stepIndex maps a step's id to its place in the procedure.
	stepIndex map[string]int
	// first and last are the run's earliest and latest events with a time.
	first, last *controlv1.Event
	silent      map[string]bool
	anySilent   bool
	neverHeard  []string
	lapsed      []string
	orderFired  map[int]bool
}

func newEvaluation(in Input, rd *read) *evaluation {
	e := &evaluation{in: in, p: in.Procedure, ix: indexOf(in.Procedure), rd: rd,
		byRequest: map[string]*request{}, ofStep: map[int][]*instance{}, stepIndex: map[string]int{},
		joinDoubt: map[string]bool{}, reported: map[int][]string{}}
	for i, s := range e.p.steps {
		e.stepIndex[s.ID] = i
	}
	e.reqs = requestsOf(rd.events, rd.doubtful)
	for _, rq := range e.reqs {
		e.byRequest[rq.id] = rq
	}
	e.spanEvents()
	e.hearSources()
	j := newJoiner(e.ix, e.reqs, rd.spans)
	for _, o := range rd.obs {
		joined, doubt := j.joined(o, rd.obsSource[o.GetObservationId()])
		if !joined {
			e.unjoined = append(e.unjoined, o)
			e.joinDoubt[o.GetObservationId()] = doubt
		}
	}
	e.instances()
	return e
}

// spanEvents finds the run's earliest and latest events with a time, the
// first read winning a tie.
func (e *evaluation) spanEvents() {
	for _, ev := range e.rd.events {
		if !ev.GetOccurredAt().IsValid() {
			continue
		}
		t := ev.GetOccurredAt().AsTime()
		if e.first == nil || t.Before(e.first.GetOccurredAt().AsTime()) {
			e.first = ev
		}
		if e.last == nil || t.After(e.last.GetOccurredAt().AsTime()) {
			e.last = ev
		}
	}
}

// hearSources marks a source silent when it was never heard, or when its
// heartbeat ran out before the run's last plane event: what it did not report
// by then may simply not have arrived. A source named and not read reported
// nothing that was read.
func (e *evaluation) hearSources() {
	e.silent = map[string]bool{}
	e.anySilent = len(e.in.SourcesNotRead) > 0
	for _, s := range e.in.Sources {
		silent := !s.Heard
		if e.last != nil && !silent {
			deadline := s.LastHeard.Add(time.Duration(s.HeartbeatSeconds) * time.Second)
			silent = deadline.Before(e.last.GetOccurredAt().AsTime())
		}
		switch {
		case !s.Heard:
			e.neverHeard = append(e.neverHeard, s.SourceID)
		case silent:
			e.lapsed = append(e.lapsed, s.SourceID)
		}
		e.silent[s.SourceID] = silent
		e.anySilent = e.anySilent || silent
	}
}

func (e *evaluation) instances() {
	for _, rq := range e.reqs {
		tool, ok := rq.tool()
		en, known := e.ix.byTool[tool]
		if !ok || !known || en.step == noStep {
			continue
		}
		at := rq.proposal.GetOccurredAt()
		in := &instance{step: en.step, at: at.AsTime(), timed: at.IsValid(), seq: rq.seq, failed: rq.failed(),
			start: rq.ref(rq.proposal), request: rq.id}
		if rq.terminal != nil {
			end := rq.terminal.GetOccurredAt()
			in.end, in.endAt, in.endTimed = rq.ref(rq.terminal), end.AsTime(), end.IsValid()
		}
		e.add(in)
	}
	for _, o := range e.unjoined {
		if en, known := e.ix.byName[o.GetSubject().GetName()]; known && en.step != noStep {
			e.reported[en.step] = append(e.reported[en.step], o.GetObservationId())
		}
	}
}

func (e *evaluation) add(in *instance) {
	e.inst = append(e.inst, in)
	e.ofStep[in.step] = append(e.ofStep[in.step], in)
}

// firstOf is a step's first instance and whether its time is known: its
// earliest timed instance when the instance read first has a time, or else
// that first read, which may have come before any timed one. The step must
// have an instance.
func (e *evaluation) firstOf(step int) (*instance, bool) {
	if read := e.ofStep[step][0]; !read.timed {
		return read, false
	}
	return e.firstTimed(step), true
}

// firstTimed is a step's earliest instance with a time, or nil.
func (e *evaluation) firstTimed(step int) *instance {
	var first *instance
	for _, in := range e.ofStep[step] {
		if in.timed && (first == nil || in.before(first)) {
			first = in
		}
	}
	return first
}

// obsRef cites an observation: it suggests at most, and nothing when its id
// was read with two contents, its source is silent, or the join may have
// missed it.
func (e *evaluation) obsRef(o *observev1.Observation) ref {
	id := o.GetObservationId()
	src := e.rd.obsSource[id]
	v := suspected
	if e.rd.obsDoubt[id] || e.joinDoubt[id] || e.silent[src.SourceID] {
		v = indeterminate
	}
	return ref{v: v, r: &findingv1alpha1.Reference{Ref: &findingv1alpha1.Reference_Observation{
		Observation: &findingv1alpha1.ObservationRef{SourceId: src.SourceID, ObservationId: id},
	}}}
}

func (e *evaluation) apply(rule string) []draft {
	switch rule {
	case RuleRepeatedDenial:
		return e.repeatedDenial()
	case RuleStepOutsideProcedure:
		return e.outsideProcedure()
	case RuleDeadlineExceeded:
		return e.deadline()
	case RuleRequiredStepSkipped:
		return e.skipped()
	case RuleStepOutOfOrder:
		var out []draft
		out, e.orderFired = e.outOfOrder()
		return out
	case RuleContinuedAfterFailure:
		return e.continued(e.orderFired)
	}
	return nil
}
