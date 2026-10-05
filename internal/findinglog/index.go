package findinglog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/proto"
)

// key is what the log keys a finding on: a verdict that changes is a new
// record.
type key struct {
	id      string
	verdict controlv1.FindingVerdict
}

func keyOf(f *findingv1alpha1.FindingRecord) key {
	return key{id: f.GetFinding().GetFindingId(), verdict: f.GetFinding().GetVerdict()}
}

// index maps each key the log holds to the digest of its record's content.
// It is rebuilt from the file on every open and never written down, so it
// cannot disagree with the lines it describes.
type index map[key]string

// digest is the SHA-256 of the record's deterministic wire encoding, which
// two equal records share whatever their JSON spelling.
func digest(f *findingv1alpha1.FindingRecord) (string, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(f)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// batch is what one write appends and what it found.
type batch struct {
	lines []byte
	added index
	found Result
}

// fresh encodes every finding, then keeps those whose key neither the index
// nor an earlier one of findings holds, and counts the rest: a duplicate when
// the content is the same, a conflict when it is not. One finding the writer
// refuses refuses them all.
func (ids index) fresh(findings []*findingv1alpha1.FindingRecord) (batch, error) {
	b := batch{added: index{}}
	for i, f := range findings {
		line, err := marshalLine(&findingv1alpha1.Record{Record: &findingv1alpha1.Record_FindingRecord{FindingRecord: f}})
		if err != nil {
			return batch{}, fmt.Errorf("%w: finding %d: %w", ErrRecord, i, err)
		}
		sum, err := digest(f)
		if err != nil {
			return batch{}, fmt.Errorf("%w: finding %d: %w", ErrRecord, i, err)
		}
		k := keyOf(f)
		held, ok := ids[k]
		if !ok {
			held, ok = b.added[k]
		}
		switch {
		case ok && held == sum:
			b.found.Duplicates++
		case ok:
			b.found.Conflicts = append(b.found.Conflicts, k.id)
		default:
			b.added[k] = sum
			b.lines = append(b.lines, line...)
			b.found.Written++
		}
	}
	return b, nil
}

func (ids index) add(added index) {
	for k, sum := range added {
		ids[k] = sum
	}
}
