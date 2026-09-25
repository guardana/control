package spool_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/spool"
)

// TestAClosingRecordSpendsItsReservation: the budget holds exactly the
// opening record and a reserve the size of the closing record, so every other
// record is refused while the closing record goes through and spends it.
func TestAClosingRecordSpendsItsReservation(t *testing.T) {
	started := event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1)
	completed := event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-1", 2)
	startedSize, completedSize := recordSize(t, started), recordSize(t, completed)
	budget := startedSize + completedSize
	s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: budget, SegmentBytes: budget, ClosingReserve: completedSize})
	mustAppend(t, s, started)
	if st := stats(t, s); st.Reserved != completedSize || st.Bytes != startedSize || st.OpenTrails != 1 {
		t.Fatalf("Stats after ACTION_STARTED = %+v; want %d reserved for one trail", st, completedSize)
	}
	other := event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-2", 3)
	if err := s.Append(context.Background(), other); !errors.Is(err, spool.ErrFull) {
		t.Errorf("ACTION_STARTED of another trail on the full budget = %v, want ErrFull", err)
	}
	proposed := event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-2", 4)
	if err := s.Append(context.Background(), proposed); !errors.Is(err, spool.ErrFull) {
		t.Errorf("ACTION_PROPOSED on the full budget = %v, want ErrFull", err)
	}
	// A closing record for a trail nobody opened has no reservation to spend.
	stray := event(controlv1.EventKind_EVENT_KIND_ACTION_FAILED, "req-9", 5)
	if err := s.Append(context.Background(), stray); !errors.Is(err, spool.ErrFull) {
		t.Errorf("ACTION_FAILED of a trail with no reservation on the full budget = %v, want ErrFull", err)
	}
	mustAppend(t, s, completed)
	st := stats(t, s)
	if st.Reserved != 0 || st.OpenTrails != 0 || st.Bytes != budget {
		t.Errorf("Stats after the closing record = %+v; want nothing reserved and %d bytes", st, budget)
	}
	// The trail is closed: its next closing record is an ordinary one.
	if err := s.Append(context.Background(), completed); !errors.Is(err, spool.ErrFull) {
		t.Errorf("a second ACTION_COMPLETED for the closed trail = %v, want ErrFull", err)
	}
}

// TestTheBytesPastAReservationAreBudgeted: a reserve one byte short of the
// closing record covers all but that byte, which the budget checks like any.
func TestTheBytesPastAReservationAreBudgeted(t *testing.T) {
	started := event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1)
	completed := event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-1", 2)
	startedSize, completedSize := recordSize(t, started), recordSize(t, completed)
	reserve := completedSize - 1
	for _, tc := range []struct {
		name     string
		maxBytes int64
		want     error
	}{
		{"no room for the byte past the reserve", startedSize + reserve, spool.ErrFull},
		{"room for exactly that byte", startedSize + reserve + 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: tc.maxBytes, SegmentBytes: tc.maxBytes, ClosingReserve: reserve})
			mustAppend(t, s, started)
			err := s.Append(context.Background(), completed)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ACTION_COMPLETED = %v, want %v", err, tc.want)
			}
			st := stats(t, s)
			switch {
			case tc.want != nil && (st.Reserved != reserve || st.Bytes != startedSize):
				t.Errorf("a refused closing record changed the spool: %+v", st)
			case tc.want == nil && (st.Reserved != 0 || st.Bytes != tc.maxBytes):
				t.Errorf("Stats after the closing record = %+v; want the budget exactly full", st)
			}
		})
	}
}

// TestTheReservationIsPartOfTheBudget: an opening record is refused when it
// fits and its reservation does not, and taken when both fit exactly.
func TestTheReservationIsPartOfTheBudget(t *testing.T) {
	started := event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1)
	startedSize := recordSize(t, started)
	const reserve = 1000
	tight := startedSize + reserve - 1
	s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: tight, SegmentBytes: tight, ClosingReserve: reserve})
	if err := s.Append(context.Background(), started); !errors.Is(err, spool.ErrFull) {
		t.Errorf("ACTION_STARTED with room for the record and all but one byte of its reserve = %v, want ErrFull", err)
	}
	exact := startedSize + reserve
	s = open(t, spool.Options{Dir: t.TempDir(), MaxBytes: exact, SegmentBytes: exact, ClosingReserve: reserve})
	mustAppend(t, s, started)
}

// TestATrailIsOpenedOnce: a second ACTION_STARTED for a trail holding a
// reservation, and one naming no trail, are refused whatever the budget.
func TestATrailIsOpenedOnce(t *testing.T) {
	s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: 1 << 20, SegmentBytes: 1 << 16, ClosingReserve: 1000})
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1))
	if err := s.Append(context.Background(), event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 2)); !errors.Is(err, spool.ErrTrail) {
		t.Errorf("a second ACTION_STARTED for an open trail = %v, want ErrTrail", err)
	}
	if err := s.Append(context.Background(), event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "", 3)); !errors.Is(err, spool.ErrTrail) {
		t.Errorf("ACTION_STARTED with no request_id = %v, want ErrTrail", err)
	}
	if st := stats(t, s); st.Reserved != 1000 || st.OpenTrails != 1 {
		t.Errorf("refused openings changed the reservations: %+v", st)
	}
	// Once closed, the trail may be opened again.
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-1", 4))
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 5))
}

// TestBothClosingKindsSpendTheReservation: ACTION_FAILED closes a trail as
// ACTION_COMPLETED does, and the reservation is per trail, not per record.
func TestBothClosingKindsSpendTheReservation(t *testing.T) {
	s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: 1 << 20, SegmentBytes: 1 << 16, ClosingReserve: 1000})
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1))
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-2", 2))
	if st := stats(t, s); st.Reserved != 2000 || st.OpenTrails != 2 {
		t.Fatalf("Stats for two open trails = %+v, want 2000 reserved", st)
	}
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_FAILED, "req-1", 4))
	if st := stats(t, s); st.Reserved != 1000 || st.OpenTrails != 1 {
		t.Errorf("Stats after ACTION_FAILED closed one trail = %+v, want 1000 reserved", st)
	}
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-2", 5))
	if st := stats(t, s); st.Reserved != 0 || st.OpenTrails != 0 {
		t.Errorf("Stats after both trails closed = %+v, want nothing reserved", st)
	}
}

// TestReservationsDoNotSurviveReopen: reservations live with the process that
// holds the calls they cover, so an open trail from before a restart holds
// nothing after it.
func TestReservationsDoNotSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	opts := spool.Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: 1 << 16, ClosingReserve: 1000}
	s := open(t, opts)
	mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-1", 1))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, opts)
	if st := stats(t, s); st.Reserved != 0 || st.OpenTrails != 0 {
		t.Errorf("Stats after reopen = %+v, want nothing reserved", st)
	}
}

// TestAClosingRecordSpendsOnlyItsOwnTrailsReservation: the reservation is
// keyed by the request and the execution the ACTION_STARTED named, so a
// closing record of another call, or of another attempt at this one, is an
// ordinary budgeted append and leaves the allowance where it is.
func TestAClosingRecordSpendsOnlyItsOwnTrailsReservation(t *testing.T) {
	started := execution(event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-a", 1), "exec-1")
	closing := execution(event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-a", 2), "exec-1")
	startedSize, closingSize := recordSize(t, started), recordSize(t, closing)
	budget := startedSize + closingSize
	for name, stray := range map[string]*controlv1.Event{
		"another execution of the same request": execution(event(controlv1.EventKind_EVENT_KIND_ACTION_FAILED, "req-a", 3), "exec-2"),
		"no execution at all":                   event(controlv1.EventKind_EVENT_KIND_ACTION_FAILED, "req-a", 4),
	} {
		t.Run(name, func(t *testing.T) {
			s := open(t, spool.Options{Dir: t.TempDir(), MaxBytes: budget, SegmentBytes: budget, ClosingReserve: closingSize})
			mustAppend(t, s, started)
			if err := s.Append(context.Background(), stray); !errors.Is(err, spool.ErrFull) {
				t.Fatalf("a closing record naming %s = %v, want ErrFull: it spends no reservation", name, err)
			}
			if st := stats(t, s); st.Reserved != closingSize || st.OpenTrails != 1 {
				t.Fatalf("Stats = %+v, want the reservation untouched", st)
			}
			mustAppend(t, s, closing)
			if st := stats(t, s); st.Reserved != 0 || st.Bytes != budget {
				t.Errorf("Stats after the trail's own closing record = %+v", st)
			}
		})
	}
}

// TestMaxOpenTrailsBoundsWhatTheReservationsLockAway: a plane that never
// closes a trail cannot lock the budget away without end.
func TestMaxOpenTrailsBoundsWhatTheReservationsLockAway(t *testing.T) {
	started := func(n int) *controlv1.Event {
		return execution(event(controlv1.EventKind_EVENT_KIND_ACTION_STARTED, "req-"+strconv.Itoa(n), n), "exec-"+strconv.Itoa(n))
	}
	size := recordSize(t, started(1))
	const trails = 3
	reserve := 4 * size
	s := open(t, spool.Options{
		Dir: t.TempDir(), MaxBytes: 400 * size, SegmentBytes: 8 * size,
		ClosingReserve: reserve, MaxOpenTrails: trails,
	})
	for i := 1; i <= trails; i++ {
		mustAppend(t, s, started(i))
	}
	if err := s.Append(context.Background(), started(trails+1)); !errors.Is(err, spool.ErrFull) {
		t.Fatalf("ACTION_STARTED past MaxOpenTrails = %v, want ErrFull", err)
	}
	st := stats(t, s)
	if st.OpenTrails != trails || st.Reserved != int64(trails)*reserve {
		t.Fatalf("Stats = %+v, want %d trails holding %d bytes", st, trails, int64(trails)*reserve)
	}
	// Closing one makes room for one more, and no more.
	mustAppend(t, s, execution(event(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, "req-1", 99), "exec-1"))
	mustAppend(t, s, started(trails+1))
	if err := s.Append(context.Background(), started(trails+2)); !errors.Is(err, spool.ErrFull) {
		t.Errorf("ACTION_STARTED past the ceiling again = %v, want ErrFull", err)
	}
}

// execution returns ev with the execution identifier set.
func execution(ev *controlv1.Event, id string) *controlv1.Event {
	ev.ExecutionId = id
	return ev
}
