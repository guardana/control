package evidence

import (
	"fmt"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// Position is where a trail stands: the event_id of its last event, which the
// next event links to, and the execution its ACTION_STARTED named, if one has,
// which the event that closes the action stamps.
type Position struct {
	LastEventID string
	ExecutionID string
}

// Position returns where this Builder's trail stands, so a request held for an
// approval, or one whose plane restarts, can be resumed with Resume.
func (b *Builder) Position() Position {
	return Position{LastEventID: b.prev, ExecutionID: b.started}
}

// Resume returns a Builder that continues the trail of ids from at, linking its
// first event to at.LastEventID and closing the action with at.ExecutionID.
// It refuses everything NewBuilder refuses, an empty LastEventID, which
// resumes nothing, and either identifier over contract.MaxStringBytes, which
// ValidateChain refuses on the next event. Only the caller can tell whether at
// is the truth about that trail: a resume from an older position writes a
// fork, and ValidateChain reports it as a link that does not join.
func Resume(
	ids IDs, mode controlv1.EnforcementMode, clock func() time.Time, newID func() string, at Position,
) (*Builder, error) {
	b, err := NewBuilder(ids, mode, clock, newID)
	var problems []string
	if err != nil {
		problems = append(problems, strings.TrimPrefix(err.Error(), ErrInvalidInput.Error()+": "))
	}
	// Held to the bound only: newID and Started are not held to the
	// identifier rule, so a resume must not refuse what the trail holds.
	if at.LastEventID == "" {
		problems = append(problems, "Position.LastEventID is empty")
	}
	for _, field := range [...]struct{ name, value string }{
		{"Position.LastEventID", at.LastEventID},
		{"Position.ExecutionID", at.ExecutionID},
	} {
		if len(field.value) > contract.MaxStringBytes {
			problems = append(problems, fmt.Sprintf("%s is %d bytes, over %d", field.name, len(field.value), contract.MaxStringBytes))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidInput, strings.Join(problems, ", "))
	}
	b.prev, b.started = at.LastEventID, at.ExecutionID
	return b, nil
}
