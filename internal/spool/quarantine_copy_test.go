package spool_test

import (
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/spool"
)

// TestTheQuarantineHoldsOneCopyOfEveryRecordAcrossARestart: a run stopped
// after quarantining a batch and before acknowledging it repeats both, and
// none of the batch is written twice however many records it held.
func TestTheQuarantineHoldsOneCopyOfEveryRecordAcrossARestart(t *testing.T) {
	const batch = 300
	events := make([]*controlv1.Event, batch)
	for i := range events {
		events[i] = event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-1", i)
	}
	opts := spool.Options{Dir: t.TempDir(), MaxBytes: 1 << 22, SegmentBytes: 1 << 20}
	s := open(t, opts)
	for _, ev := range events {
		mustAppend(t, s, ev)
	}
	quarantineAll(t, s, events, batch)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, opts)
	quarantineAll(t, s, events, 0)
	if st := stats(t, s); st.QuarantinedRecords != batch {
		t.Errorf("the quarantine holds %d records after the run repeated, want %d", st.QuarantinedRecords, batch)
	}
}

// quarantineAll delivers events from a new reader of s and quarantines them,
// expecting want of them written.
func quarantineAll(t *testing.T, s *spool.Spool, events []*controlv1.Event, want int) {
	t.Helper()
	r := reader(t, s)
	deliveredCursors(t, r, len(events))
	if written, err := r.Quarantine(events); err != nil || written != want {
		t.Fatalf("Quarantine of %d records = %d, %v; want %d written", len(events), written, err, want)
	}
}
