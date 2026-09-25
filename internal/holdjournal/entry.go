package holdjournal

import (
	"strconv"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/internal/evidence"
)

// SchemaVersion is the entry shape this build writes. An entry whose major
// differs is refused: a reader that skipped what it could not name would speak
// for a hold it did not understand.
const SchemaVersion = "1.0"

// State is where an entry stands. Its zero value is no state this build knows,
// which is read the same way as an interrupted close: the plane cannot say, so
// it says nothing and writes nothing.
type State uint8

const (
	// StateUnknown is an entry in no state this build knows.
	StateUnknown State = iota
	// StateHeld is the one state that proves something: the trail stands at
	// its request for approval and can still be closed.
	StateHeld
	// StateResuming is an entry whose plane was about to write the approval's
	// answer on the trail.
	StateResuming
	// StateClosing is an entry whose plane was about to close the trail.
	StateClosing
)

// String names the state as an entry spells it.
func (s State) String() string {
	switch s {
	case StateUnknown:
		return "unknown"
	case StateHeld:
		return "held"
	case StateResuming:
		return "resuming"
	case StateClosing:
		return "closing"
	}
	return "state(" + strconv.Itoa(int(s)) + ")"
}

// Entry is what a plane records about a request it holds for an approval, so
// that a hold it loses to a restart can still be closed on its own trail.
//
// It carries no envelope and no decision: a lost hold is closed, never
// resumed, and storing what a retry would be compared against would widen what
// the plane keeps on disk for behaviour nobody asked for.
type Entry struct {
	// SchemaVersion is the version of this shape; the journal refuses a major
	// it does not know rather than reading past it.
	SchemaVersion string
	// State is where the entry stands.
	State State
	// IDs name the held trail.
	IDs evidence.IDs
	// LastEventID is where that trail stands: its request for approval.
	LastEventID string
	// Binding is what the approval was held under, and all the plane needs to
	// ask a store about it.
	Binding approval.Binding
	// Approval is what the plane minted and the agent was told to retry with.
	Approval *controlv1.Approval
	// Expires is the expiry the plane minted, which an answer may not exceed.
	Expires time.Time
}

// Listing is one pass over a journal: the entries that read as held, and what
// the pass could not say about the rest.
type Listing struct {
	// Held is the entries in StateHeld, at most the bound the pass was given.
	Held []Entry
	// Interrupted counts entries in any other state: a close a plane did not
	// finish, whose trail nobody can speak for.
	Interrupted int
	// Unreadable counts entries the journal could not decode. They are
	// unmeasured, never skipped, and a pass that met one is not complete.
	Unreadable int
	// Complete is false when a bound stopped the pass before the journal was
	// read out, or an entry could not be decoded: either way the pass has not
	// seen every hold, and may not report itself done.
	Complete bool
}
