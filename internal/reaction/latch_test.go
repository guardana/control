package reaction_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// latchList is a header and three stops; the last starts at cut.
func latchList(t *testing.T) (content []byte, cut int) {
	t.Helper()
	b := newList(t, listRoute(t))
	b.stop("fnd-a1", "run-a", clock0, time.Hour)
	b.stop("fnd-b1", "run-b", clock0, time.Hour)
	before := len(b.bytes())
	b.stop("fnd-c1", "run-c", clock0, time.Hour)
	return b.bytes(), before
}

func TestTheAcceptedPrefixOnlyGrows(t *testing.T) {
	r := listRoute(t)
	content, cut := latchList(t)
	first, err := reaction.Judge(r, reaction.Prefix{}, content[:cut], clock0, poll)
	if err != nil {
		t.Fatalf("the first read: %v", err)
	}
	p := first.Prefix()
	if p.Length() != int64(cut) {
		t.Fatalf("prefix %d bytes, want %d", p.Length(), cut)
	}
	grown, err := reaction.Judge(r, p, content, clock0, poll)
	if err != nil || grown.Prefix().Length() != int64(len(content)) || entryRuns(grown.Entries()) != "run-a@2,run-b@3,run-c@4" {
		t.Fatalf("the grown read: %v, %s", err, entryRuns(grown.Entries()))
	}
	same, err := reaction.Judge(r, grown.Prefix(), content, clock0, poll)
	if err != nil || same.Prefix() != grown.Prefix() {
		t.Fatalf("the same read again: %v", err)
	}

	editedLine := bytes.Replace(content, []byte(`"run-a"`), []byte(`"run-x"`), 1)
	otherList := bytes.Replace(content, []byte(`"list-1"`), []byte(`"list-2"`), 1)
	otherRoute := bytes.Replace(content, []byte(`"route_serial":3`), []byte(`"route_serial":4`), 1)
	for _, c := range []judgeCase{
		{"the last line cut off", content[:cut], reaction.ErrListShrunk},
		{"the last line torn", content[:len(content)-1], reaction.ErrListShrunk},
		{"nothing left", nil, reaction.ErrListShrunk},
		{"an earlier line edited", editedLine, reaction.ErrListRewritten},
		{"an earlier line edited and a line added", append(bytes.Clone(editedLine), content[cut:]...), reaction.ErrListRewritten},
		{"a header of another list", otherList, reaction.ErrListHeader},
		{"a header of another route", otherRoute, reaction.ErrListHeader},
	} {
		if bytes.Equal(c.content, content) {
			t.Fatalf("%s: the edit changed nothing", c.name)
		}
		_, err := reaction.Judge(r, grown.Prefix(), c.content, clock0, poll)
		expectOnly(t, c.name, err, c.want, judgeRefusals())
	}
	// From nothing accepted, the edited list is a list like another: only the
	// latch refuses it.
	if _, err := reaction.Judge(r, reaction.Prefix{}, editedLine, clock0, poll); err != nil {
		t.Fatalf("the edited list judged from nothing: %v", err)
	}
}

// TestAFailedReadKeepsThePrefix: a read the judge refuses leaves the prefix
// accepted before, so the same shortened list stays refused on every later
// read, and the whole list is taken again.
func TestAFailedReadKeepsThePrefix(t *testing.T) {
	r := listRoute(t)
	content, cut := latchList(t)
	whole := reaction.Snapshot{}.Next(r, content, clock0, poll)
	if whole.State() != reaction.Stopped {
		t.Fatalf("the whole list: %s %s", whole.State(), whole.Cause())
	}
	short := whole.Next(r, content[:cut], clock0.Add(poll), poll)
	if short.State() != reaction.Unknown || short.Cause() != reaction.CauseShrunk {
		t.Fatalf("the shortened list: %s %s", short.State(), short.Cause())
	}
	if short.Accepted() != whole.Accepted() {
		t.Fatal("a refused read changed the accepted prefix")
	}
	again := short.Next(r, content[:cut], clock0.Add(2*poll), poll)
	if again.State() != reaction.Unknown || again.Cause() != reaction.CauseShrunk {
		t.Fatalf("the shortened list read again: %s %s", again.State(), again.Cause())
	}
	unreadable := again.Unknown("unreadable", "the file went away", clock0.Add(3*poll), poll)
	if unreadable.Accepted() != whole.Accepted() || unreadable.State() != reaction.Unknown {
		t.Fatal("a reader's unknown changed the accepted prefix")
	}
	if back := unreadable.Next(r, content, clock0.Add(4*poll), poll); back.State() != reaction.Stopped {
		t.Fatalf("the whole list read again: %s %s", back.State(), back.Cause())
	}
}

func TestListBoundOfLines(t *testing.T) {
	if reaction.MaxListLines != 20000 {
		t.Fatalf("MaxListLines = %d", reaction.MaxListLines)
	}
	b := newList(t, listRoute(t))
	b.stop("fnd-0", "run-a", clock0, time.Hour)
	for i := 3; i <= 20000; i++ {
		b.covered("fnd-"+strconv.Itoa(i), "run-a", clock0)
	}
	l, err := judge(t, b.bytes())
	if err != nil || l.Usage().Lines != 20000 {
		t.Fatalf("20000 lines: %v", err)
	}
	b.covered("fnd-20001", "run-a", clock0)
	_, err = judge(t, b.bytes())
	expectOnly(t, "20001 lines", err, reaction.ErrListLines, judgeRefusals())
}

// paddedList is a valid list of exactly size bytes: stops padded with white
// space inside their objects.
func paddedList(t *testing.T, size int) []byte {
	t.Helper()
	b := newList(t, listRoute(t))
	remaining := size - len(b.bytes())
	const target = 60000
	lines := (remaining + target - 1) / target
	for i := range lines {
		want := remaining / (lines - i)
		raw, err := stopOf(t, "fnd-"+strconv.Itoa(i), "run-"+strconv.Itoa(i), clock0, time.Hour).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		pad := want - 1 - len(raw)
		b.raw(string(raw[:1]) + strings.Repeat(" ", pad) + string(raw[1:]))
		remaining -= want
	}
	out := b.bytes()
	if len(out) != size {
		t.Fatalf("padded list of %d bytes, want %d", len(out), size)
	}
	return out
}

func TestListBoundOfBytes(t *testing.T) {
	if reaction.MaxListBytes != 4<<20 {
		t.Fatalf("MaxListBytes = %d", reaction.MaxListBytes)
	}
	full := paddedList(t, 4194304)
	l, err := judge(t, full)
	if err != nil || l.Usage().Bytes != 4194304 || !l.Usage().Degraded() {
		t.Fatalf("4 MiB: %v, %+v", err, l.Usage())
	}
	_, err = judge(t, paddedList(t, 4194305))
	expectOnly(t, "4 MiB and a byte", err, reaction.ErrListTooLarge, judgeRefusals())
	_, err = judge(t, append(bytes.Clone(full), '{'))
	expectOnly(t, "4 MiB and a torn byte", err, reaction.ErrListTooLarge, judgeRefusals())
}

func TestUsageIsDegradedPastNineTenths(t *testing.T) {
	for _, tc := range []struct {
		u    reaction.Usage
		want bool
	}{
		{reaction.Usage{Lines: 18000}, false},
		{reaction.Usage{Lines: 18001}, true},
		{reaction.Usage{Bytes: 3774873}, false},
		{reaction.Usage{Bytes: 3774874}, true},
		{reaction.Usage{Bytes: 3774873, Lines: 18000}, false},
	} {
		if got := tc.u.Degraded(); got != tc.want {
			t.Errorf("%+v: Degraded = %v", tc.u, got)
		}
	}
}
