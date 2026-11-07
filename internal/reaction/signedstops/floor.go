package signedstops

import (
	"context"
	"errors"
	"fmt"

	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// Floor is a plane's stop list floor: the directory its account owns, for
// its tenant and project.
type Floor struct {
	dir, tenant, project string
}

// NewFloor is the floor in dir for tenant and project, or ErrOptions.
func NewFloor(dir, tenant, project string) (Floor, error) {
	if dir == "" || tenant == "" || project == "" {
		return Floor{}, ErrOptions
	}
	return Floor{dir: dir, tenant: tenant, project: project}, nil
}

// Take holds the header of l, a 2.0 list a judge accepted, to the floor and
// raises the floor to it, waiting for the floor's lock until ctx ends. It
// refuses with stoplist.ErrBelowFloor a serial below the floor's, with
// stoplist.ErrForked the floor's serial with another head, with
// stoplist.ErrChain a serial above a floor that holds one when the header
// does not name the floor's head as its previous, and with stoplist.ErrFloor
// a floor it cannot read or raise.
func (f Floor) Take(ctx context.Context, l reaction.List) (policystate.StopsFloor, error) {
	h := l.Header().Signed
	if h.Digest == "" {
		return policystate.StopsFloor{}, fmt.Errorf("%w: the list's header is not signed", stoplist.ErrFloor)
	}
	stored, err := policystate.ReadStops(f.dir, f.tenant, f.project)
	if err != nil {
		return policystate.StopsFloor{}, fmt.Errorf("%w: %w", stoplist.ErrFloor, err)
	}
	if err := Check(stored, h); err != nil {
		return policystate.StopsFloor{}, err
	}
	raised, err := policystate.RaiseStops(ctx, f.dir, f.tenant, f.project, h.Serial, h.Digest)
	if err != nil {
		return policystate.StopsFloor{}, floorRefusal(err)
	}
	return raised, nil
}

// Check holds h to a floor as stored, writing nothing: a lower serial, the
// floor's serial with another head, and a higher serial whose previous head
// is not the floor's when the floor holds one are refused.
func Check(stored policystate.StopsFloor, h reaction.HeaderSigned) error {
	switch {
	case !stored.HasSerial():
		return nil
	case h.Serial < stored.Serial:
		return fmt.Errorf("%w: serial %d, the floor's %d", stoplist.ErrBelowFloor, h.Serial, stored.Serial)
	case h.Serial == stored.Serial && h.Digest != stored.Digest:
		return fmt.Errorf("%w: serial %d", stoplist.ErrForked, h.Serial)
	case h.Serial > stored.Serial && h.Previous != stored.Digest:
		return fmt.Errorf("%w: serial %d over the floor's %d", stoplist.ErrChain, h.Serial, stored.Serial)
	}
	return nil
}

func floorRefusal(err error) error {
	switch {
	case errors.Is(err, policystate.ErrStopsBelowFloor):
		return fmt.Errorf("%w: %w", stoplist.ErrBelowFloor, err)
	case errors.Is(err, policystate.ErrStopsForked):
		return fmt.Errorf("%w: %w", stoplist.ErrForked, err)
	}
	return fmt.Errorf("%w: %w", stoplist.ErrFloor, err)
}
