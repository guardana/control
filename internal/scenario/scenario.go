package scenario

import (
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/pause"
)

// Kind is the one kind of scenario this build reads. A change to the format
// is a new kind.
const Kind = "agent-scenario/v1alpha1"

// The bounds of a document.
const (
	MaxBytes      = 1 << 20
	MaxDepth      = 64
	MaxSteps      = 256
	MaxAboutBytes = 200
	MaxListItems  = 64
)

// Scenario is one read document. ID is its file name.
type Scenario struct {
	ID    string
	About string
	Plane Plane
	Run   Run
	Steps []Step
}

// Plane is what the plane under test must be before the first step.
// BundleDigest is empty when the scenario does not name one.
type Plane struct {
	Mode         string
	BundleID     string
	BundleDigest string
}

// Run says whether the scenario starts on a run nothing has touched.
type Run string

// The two runs.
const (
	RunFresh     Run = "fresh"
	RunContinues Run = "continues"
)

// Step is one step: exactly one of its members is set.
type Step struct {
	Call    *Call
	Approve *Approval
	Reject  *Approval
	Pause   *Pause
	Unpause *Unpause
}

// Call is one tool call and the three things the plane must show for it.
// Args is the arguments object as the document spells it.
type Call struct {
	Tool    string
	Args    []byte
	Answer  Answer
	Decided Decided
	Trail   Trail

	refs []refString
}

// AnswerKind is what kind of answer the agent must get.
type AnswerKind string

// The four kinds of answer.
const (
	AnswerResult  AnswerKind = "result"
	AnswerError   AnswerKind = "error"
	AnswerBlocked AnswerKind = "blocked"
	AnswerPending AnswerKind = "pending"
)

// Answer is the answer a call must get and the reason codes it carries.
type Answer struct {
	Kind  AnswerKind
	Codes []string
}

// Decided is the decision the call's trail must hold, or, when None is set,
// that the trail holds no decision.
type Decided struct {
	None        bool
	Verdict     controlv1.Verdict
	Codes       []string
	Obligations []Obligation
}

// Obligation is one obligation the decision must carry. Params is the
// parameters object as the document spells it.
type Obligation struct {
	Type     string
	Params   []byte
	Advisory bool
}

// NewRequest is Trail.Request for a call that starts a request of its own.
const NewRequest = -1

// Trail names the request a call's trail belongs to, as NewRequest or the
// index of the pending call step whose held request the call resumes, and
// the kinds of every event that trail must then hold, in order.
type Trail struct {
	Request int
	Kinds   []controlv1.EventKind
}

// Approval answers the pending approval of the call step at Step.
type Approval struct {
	Step     int
	Approver string
	Reason   string
}

// PauseMarkerBytes is the room a pause step's reason leaves under the pause
// file's bound for the marker a runner ends it with: " [scenario ", the 26
// characters of crypto/rand's Text, and "]".
const PauseMarkerBytes = len(" [scenario ]") + 26

// Pause adds one entry to the plane's pause file.
type Pause struct {
	Scope  pause.Scope
	Reason string
}

// Unpause removes the entry the pause step at Step added.
type Unpause struct {
	Step int
}
