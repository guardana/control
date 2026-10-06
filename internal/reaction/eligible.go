package reaction

import (
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
)

// FindingFacts is what the emitter copies out of a finding record.
type FindingFacts struct {
	Source   controlv1.FindingSource
	Verdict  controlv1.FindingVerdict
	TenantID string
	RunID    string
}

// RunFacts is what the emitter copies out of the run record the runs
// directory holds under the finding's run id. Each field is one a zero value
// fails: a run not found is the zero RunFacts.
type RunFacts struct {
	RunID    string
	TenantID string
	// Root is the run whose state the run shares; a run opened with no
	// parent is its own root.
	Root string
	// Open is true while the record holds no close.
	Open      bool
	ExpiresAt time.Time
}

// Eligible returns nil when a finding may become a stop of its run at now: the
// finding is DETERMINISTIC and CONFIRMED, and its run is an opened run of its
// tenant that is open, no child, and not expired at now. Any other value of
// either enum, the zero and a number this build does not know among them, is
// refused, and so is a time that is not usable. A finding's escalation is
// not read: the route decides.
func Eligible(f FindingFacts, r RunFacts, now time.Time) error {
	switch {
	case f.Source != controlv1.FindingSource_FINDING_SOURCE_DETERMINISTIC:
		return ErrFindingSource
	case f.Verdict != controlv1.FindingVerdict_FINDING_VERDICT_CONFIRMED:
		return ErrFindingVerdict
	case f.RunID == "" || r.RunID != f.RunID:
		return ErrRunUnknown
	case f.TenantID == "" || r.TenantID != f.TenantID:
		return ErrRunTenant
	case !r.Open:
		return ErrRunClosed
	case r.Root != r.RunID:
		return ErrRunChild
	case !policy.UsableTime(now):
		return ErrClock
	case !now.Before(r.ExpiresAt):
		return ErrRunExpired
	}
	return nil
}
