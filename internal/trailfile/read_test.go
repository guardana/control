package trailfile

import (
	"errors"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

func read(t *testing.T, body string, limit int) Report {
	t.Helper()
	rep, err := Read(strings.NewReader(body), limit)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	return rep
}

type want struct {
	request string
	last    controlv1.EventKind
	verdict Verdict
}

func expect(t *testing.T, rep Report, wants ...want) {
	t.Helper()
	if len(rep.Trails) != len(wants) {
		t.Fatalf("read %d trails, want %d: %+v", len(rep.Trails), len(wants), rep.Trails)
	}
	for i, w := range wants {
		got := rep.Trails[i]
		if got.RequestID != w.request || got.Last != w.last || got.Verdict != w.verdict {
			t.Errorf("trail %d = %s %v %v (%v), want %s %v %v", i, got.RequestID, got.Last, got.Verdict, got.Reason, w.request, w.last, w.verdict)
		}
		if (got.Verdict == Failed || got.Verdict == Indeterminate) != (got.Reason != nil) {
			t.Errorf("trail %d is %v with reason %v", i, got.Verdict, got.Reason)
		}
	}
}

// TestEachTrailIsJudgedOnItsOwn: trails interleaved in the file are grouped
// by request and checked apart, each with the verdict its chain earns.
func TestEachTrailIsJudgedOnItsOwn(t *testing.T) {
	done := chain("p1", "done", kindProposed, kindDecided, kindStarted, kindCompleted)
	blocked := chain("p1", "blocked", kindProposed, kindDecided, kindBlocked)
	held := chain("p1", "held", kindProposed, kindDecided, kindRequested)
	broken := chain("p1", "broken", kindProposed, kindStarted)
	later := chain("p1", "later", kindProposed, kindDecided, controlv1.EventKind(99))
	var body strings.Builder
	for i := range 4 {
		for _, c := range [][]*controlv1.Event{done, blocked, held, broken, later} {
			if i < len(c) {
				body.WriteString(lines(t, c[i]))
			}
		}
	}
	rep := read(t, body.String(), DefaultMaxLines)
	expect(t, rep,
		want{"done", kindCompleted, Passed},
		want{"blocked", kindBlocked, Passed},
		want{"held", kindRequested, StillOpen},
		want{"broken", kindStarted, Failed},
		want{"later", controlv1.EventKind(99), Indeterminate},
	)
	if !errors.Is(rep.Trails[3].Reason, evidence.ErrChainBroken) || !errors.Is(rep.Trails[4].Reason, evidence.ErrChainIndeterminate) {
		t.Errorf("reasons %v, %v", rep.Trails[3].Reason, rep.Trails[4].Reason)
	}
	if rep.Lines != 15 || rep.Duplicates != 0 || rep.Unread != 0 {
		t.Errorf("report %d lines, %d duplicates, %d unread", rep.Lines, rep.Duplicates, rep.Unread)
	}
}

// TestATrailIsReadInTheOrderOfItsLinks: an exporter with more than one
// request in flight can land a later batch first, so a trail is put back in
// the order its links give; links that do not make one chain are left in the
// file's order for the check to refuse.
func TestATrailIsReadInTheOrderOfItsLinks(t *testing.T) {
	c := chain("p1", "r", kindProposed, kindDecided, kindStarted, kindCompleted)
	rep := read(t, lines(t, c[3], c[2], c[0], c[1]), DefaultMaxLines)
	expect(t, rep, want{"r", kindCompleted, Passed})

	gap := read(t, lines(t, c[0], c[1], c[3]), DefaultMaxLines)
	expect(t, gap, want{"r", kindCompleted, Failed})

	fork := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	twin := chain("p1", "r", kindProposed, kindDecided, kindBlocked)[2]
	twin.EventId = "twin"
	forked := read(t, lines(t, fork[0], fork[1], fork[2], twin), DefaultMaxLines)
	expect(t, forked, want{"r", kindBlocked, Failed})

	noHead := chain("p1", "r", kindProposed, kindDecided)
	noHead[0].PrevEventId = "elsewhere"
	expect(t, read(t, lines(t, noHead[1], noHead[0]), DefaultMaxLines), want{"r", kindProposed, Failed})
}

// TestOneRequestIdInTwoProjectsIsTwoTrails: a request id is unique within a
// project, so one id in two projects is two trails, each whole.
func TestOneRequestIdInTwoProjectsIsTwoTrails(t *testing.T) {
	a := chain("pa", "r", kindProposed, kindDecided, kindBlocked)
	b := chain("pb", "r", kindProposed, kindDecided, kindBlocked)
	rep := read(t, lines(t, a[0], b[0], a[1], b[1], a[2], b[2]), DefaultMaxLines)
	expect(t, rep, want{"r", kindBlocked, Passed}, want{"r", kindBlocked, Passed})
	if rep.Trails[0].ProjectID != "pa" || rep.Trails[1].ProjectID != "pb" {
		t.Errorf("projects %q, %q", rep.Trails[0].ProjectID, rep.Trails[1].ProjectID)
	}
}

// TestALineSentTwiceCountsOnce: the file is at least once, so a line that
// repeats one read before is collapsed, and the trail is whole.
func TestALineSentTwiceCountsOnce(t *testing.T) {
	c := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	rep := read(t, lines(t, c[0], c[1], c[1], c[2], c[0]), DefaultMaxLines)
	expect(t, rep, want{"r", kindBlocked, Passed})
	if rep.Lines != 5 || rep.Duplicates != 2 {
		t.Errorf("report %d lines, %d duplicates; want 5, 2", rep.Lines, rep.Duplicates)
	}
}

// TestOneIdWithTwoLinesIsDamage: the same event id carrying other content is
// not a resend, and the whole read is refused rather than one of them kept.
func TestOneIdWithTwoLinesIsDamage(t *testing.T) {
	c := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	other := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	other[1].RunId = "changed"
	_, err := Read(strings.NewReader(lines(t, c[0], c[1], other[1], c[2])), DefaultMaxLines)
	if !errors.Is(err, ErrDamaged) {
		t.Errorf("Read = %v, want ErrDamaged", err)
	}
}

// TestEventsWithNoIdAreNotCollapsed: an event without an id cannot be told
// from another, so nothing collapses it and its chain fails.
func TestEventsWithNoIdAreNotCollapsed(t *testing.T) {
	c := chain("p1", "r", kindProposed, kindDecided)
	c[0].EventId, c[1].EventId, c[1].PrevEventId = "", "", ""
	rep := read(t, lines(t, c[0], c[1], c[1]), DefaultMaxLines)
	expect(t, rep, want{"r", kindDecided, Failed})
	if rep.Duplicates != 0 {
		t.Errorf("%d duplicates collapsed among events with no id", rep.Duplicates)
	}
}

// TestAPartialLastLineIsNotRead: what follows the last newline is a line a
// writer has not finished, and it is counted and not read.
func TestAPartialLastLineIsNotRead(t *testing.T) {
	c := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	whole := lines(t, c...)
	partial := lines(t, event(9))[:20]
	rep := read(t, whole+partial, DefaultMaxLines)
	expect(t, rep, want{"r", kindBlocked, Passed})
	if rep.Unread != int64(len(partial)) || rep.Lines != 3 {
		t.Errorf("report %d lines, %d unread; want 3, %d", rep.Lines, rep.Unread, len(partial))
	}
}

// TestTheBoundsOfARead: a line at evidence.MaxLineBytes is read and one byte
// longer is refused; the limit counts every line, a repeated one included;
// a negative limit is refused, never read as none.
func TestTheBoundsOfARead(t *testing.T) {
	line := strings.TrimSuffix(lines(t, event(1)), "\n")
	at := line[:len(line)-1] + strings.Repeat(" ", evidence.MaxLineBytes-len(line)) + "}\n"
	if rep := read(t, at, 1); rep.Lines != 1 {
		t.Errorf("a line at the bound read as %d lines", rep.Lines)
	}
	over := strings.Replace(at, " }", "  }", 1)
	if _, err := Read(strings.NewReader(over), 1); !errors.Is(err, evidence.ErrLineTooLong) {
		t.Errorf("a line over the bound = %v, want ErrLineTooLong", err)
	}
	three := lines(t, event(1), event(2), event(2))
	if rep := read(t, three, 3); rep.Lines != 3 {
		t.Errorf("three lines under a limit of 3 read as %d", rep.Lines)
	}
	if _, err := Read(strings.NewReader(three), 2); !errors.Is(err, evidence.ErrTooManyEvents) {
		t.Errorf("three lines under a limit of 2 = %v, want ErrTooManyEvents", err)
	}
	if _, err := Read(strings.NewReader(three), -1); !errors.Is(err, ErrLimit) {
		t.Errorf("a negative limit = %v, want ErrLimit", err)
	}
}

func TestALineThatIsNotAnEventIsRefused(t *testing.T) {
	body := lines(t, event(1)) + "{\"nope\":1}\n"
	if _, err := Read(strings.NewReader(body), DefaultMaxLines); !errors.Is(err, evidence.ErrMalformedLine) || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("Read = %v, want ErrMalformedLine at line 2", err)
	}
}

func TestAnEmptyFileHoldsNoTrail(t *testing.T) {
	rep := read(t, "", DefaultMaxLines)
	if len(rep.Trails) != 0 || rep.Lines != 0 {
		t.Errorf("an empty file read as %+v", rep)
	}
}

// FuzzRead: nothing makes the reader panic, and what it reads accounts for
// every line once.
func FuzzRead(f *testing.F) {
	c := chain("p1", "r", kindProposed, kindDecided, kindBlocked)
	var seed strings.Builder
	if err := evidence.EncodeJSONL(&seed, c); err != nil {
		f.Fatal(err)
	}
	f.Add([]byte(seed.String()))
	f.Add([]byte(seed.String() + seed.String()))
	f.Add([]byte(firstLine + firstLine[:10]))
	f.Fuzz(func(t *testing.T, body []byte) {
		rep, err := Read(strings.NewReader(string(body)), 64)
		if err != nil {
			return
		}
		events := 0
		for _, tr := range rep.Trails {
			events += tr.Events
		}
		if events+rep.Duplicates != rep.Lines {
			t.Fatalf("%d events in trails and %d duplicates for %d lines", events, rep.Duplicates, rep.Lines)
		}
	})
}
