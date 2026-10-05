package supervise

import (
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/observe"
)

// RecordSchemaVersion is the version of the finding records and reports
// Evaluate writes.
const RecordSchemaVersion = "0.1"

// Run is the opened run under supervision, as its record says.
type Run struct {
	// ID is "run-" and 32 lowercase hex digits.
	ID     string
	Tenant string
	Closed bool
}

// Export is one evidence export's events. Whole is true when it holds every
// line of its trail; NotWhole says why it does not.
type Export struct {
	Events   []*controlv1.Event
	Whole    bool
	NotWhole string
}

// Source is one observation source with the observations of its log.
// LastHeard is the newest event time its import reports name, each taken no
// later than its report's receive time, and Heard is false when none names
// one.
type Source struct {
	SourceID         string
	HeartbeatSeconds uint32
	LastHeard        time.Time
	Heard            bool
	Observations     []*observev1.Observation
}

// Input is everything one supervision reads. SourcesNotRead names, as the
// caller named them, the sources it was given whose descriptor it did not
// find: each is in doubt as a silent source is.
type Input struct {
	Procedure      *Procedure
	Run            Run
	Exports        []Export
	Sources        []Source
	SourcesNotRead []string
}

// StepMatch names one step's instances, the plane requests, and the
// observations of it no plane call joins: those are reported by the runtime
// only, and no rule counts them as the step taken.
type StepMatch struct {
	StepID         string
	RequestIDs     []string
	ObservationIDs []string
}

// Result is one supervision's findings and its report. Report.Read's
// EventsTaken is 0 when no plane event of the run was read, and then no rule
// was checked: that is never a pass.
type Result struct {
	Findings []*findingv1alpha1.FindingRecord
	Report   *findingv1alpha1.SuperviseReport
	Steps    []StepMatch
	// SourcesNotRead is Input's: the named sources whose observations were
	// not read, so every finding that rests on what they did not report is
	// indeterminate.
	SourcesNotRead []string
	// PlaneBlocks counts the run's blocks that are no denial: the plane's
	// own, and the kernel's on a verdict other than DENY, by the first reason
	// code of the decision each carries.
	PlaneBlocks map[string]uint64
}

// Evaluate checks one run against its procedure. It refuses a run id that is
// not an opened run's with ErrRunID, and a procedure ReadProcedure did not
// make, a run with no tenant, a source given twice, or a run whose events
// name two projects with ErrInput.
func Evaluate(in Input) (*Result, error) {
	if err := checkInput(in); err != nil {
		return nil, err
	}
	rd := newRead()
	if err := rd.takeEvents(in.Run, in.Exports); err != nil {
		return nil, err
	}
	rd.takeObservations(in.Run, in.Sources)
	e := newEvaluation(in, rd)
	states := e.states()
	var drafts []draft
	for _, id := range ruleIDs {
		if states[id].state == findingv1alpha1.RuleState_RULE_STATE_CHECKED {
			drafts = append(drafts, e.apply(id)...)
		}
	}
	return e.result(drafts, states), nil
}

func checkInput(in Input) error {
	if in.Procedure == nil || in.Procedure.digest == "" {
		return ErrInput.with("no procedure ReadProcedure accepted")
	}
	if !observe.ValidRunID(in.Run.ID) {
		return ErrRunID
	}
	if in.Run.Tenant == "" {
		return ErrInput.with("the run names no tenant")
	}
	seen := map[string]bool{}
	for _, s := range in.Sources {
		if seen[s.SourceID] {
			return ErrInput.with("a source is given twice")
		}
		seen[s.SourceID] = true
	}
	return nil
}
