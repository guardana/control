package observelog

import (
	"fmt"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"github.com/guardana/control/internal/observe"
)

// index maps each observation id the log holds to its content digest. It is
// rebuilt from the file on every open and never written down, so it cannot
// disagree with the lines it describes.
type index map[string]string

// batch is what one write appends and what it found.
type batch struct {
	lines []byte
	added index
	found Written
}

// fresh encodes the observations neither the index nor an earlier one of obs
// holds, and counts the rest: a duplicate when the content is the same, a
// conflict when it is not. An observation the codec refuses, or one with no
// digest, refuses the whole write.
func (ids index) fresh(obs []*observev1.Observation) (batch, error) {
	b := batch{added: index{}}
	for i, o := range obs {
		line, err := observe.MarshalLine(&observev1.Record{Record: &observev1.Record_Observation{Observation: o}})
		if err != nil {
			return batch{}, fmt.Errorf("%w: observation %d: %w", ErrRecord, i, err)
		}
		sum := observe.ContentDigest(o)
		if sum == "" {
			return batch{}, fmt.Errorf("%w: observation %d has no content digest", ErrRecord, i)
		}
		id := o.GetObservationId()
		held, ok := ids[id]
		if !ok {
			held, ok = b.added[id]
		}
		switch {
		case ok && held == sum:
			b.found.Duplicates++
		case ok:
			b.found.Conflicts++
		default:
			b.added[id] = sum
			b.lines = append(b.lines, line...)
			b.found.Observations++
		}
	}
	return b, nil
}

func (ids index) add(added index) {
	for id, sum := range added {
		ids[id] = sum
	}
}
