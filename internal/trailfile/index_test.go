package trailfile

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// changed is event(n) carrying other content under the same event id.
func changed(n int) *controlv1.Event {
	ev := event(n)
	ev.RunId = "other"
	return ev
}

// TestARepeatIsNotAppendedAgain: an event the file holds, sent again, byte
// for byte, is taken and not written a second time, in a later append, in
// the same one, and after the file is opened again.
func TestARepeatIsNotAppendedAgain(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	mustAppend(t, w, event(1))
	mustAppend(t, w, event(1))
	mustAppend(t, w, event(2), event(2))
	want := firstLine + lines(t, event(2))
	if got := contents(t, path); got != want {
		t.Errorf("the file holds %q, want each event once", got)
	}
	_ = w.Close()
	again := openWriter(t, path)
	mustAppend(t, again, event(2), event(1))
	if got := contents(t, path); got != want {
		t.Errorf("after the file was opened again it holds %q, want each event once", got)
	}
}

// TestAnIdWithOtherContentIsRefused: an event id the file holds, or one the
// same append carries twice, arriving with other content refuses the whole
// append for good, and the file keeps what it had.
func TestAnIdWithOtherContentIsRefused(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	mustAppend(t, w, event(1))
	for name, batch := range map[string][]*controlv1.Event{
		"an id the file holds":           {event(2), changed(1)},
		"an id the append carries twice": {event(3), changed(3)},
	} {
		err := w.Append(context.Background(), batch)
		if !errors.Is(err, ErrConflict) || !errors.Is(err, otel.ErrSinkRefused) {
			t.Errorf("%s: Append = %v, want ErrConflict and otel.ErrSinkRefused", name, err)
		}
		if got := contents(t, path); got != firstLine {
			t.Errorf("%s: the refused append left %q", name, got)
		}
	}
	_ = w.Close()
	again := openWriter(t, path)
	if err := again.Append(context.Background(), []*controlv1.Event{changed(1)}); !errors.Is(err, ErrConflict) {
		t.Errorf("after the file was opened again: Append = %v, want ErrConflict", err)
	}
	mustAppend(t, again, event(2))
}

// TestAConflictThroughTheCollectorIsQuarantined drives the plane's real
// exporter against the receiver: the record whose id the file holds with
// other content is answered 400 and quarantined, the others arrive, and the
// file stays one the reader takes.
func TestAConflictThroughTheCollectorIsQuarantined(t *testing.T) {
	path := trailPath(t)
	w := openWriter(t, path)
	mustAppend(t, w, event(1))
	st, url := serveReceiver(t, w)
	sp := spoolHolding(t, []*controlv1.Event{event(2), changed(1), event(3)})
	e, stop := runExporter(t, sp, url)
	within(t, func() bool { return e.Stats().Acknowledged == 3 })
	stop()
	if ex := e.Stats(); ex.Quarantined != 1 || st.count(400) == 0 || st.count(503) != 0 {
		t.Errorf("the exporter reports %+v, the receiver answered 400 %d and 503 %d times", ex, st.count(400), st.count(503))
	}
	if got, want := contents(t, path), firstLine+lines(t, event(2), event(3)); got != want {
		t.Errorf("the file holds %q, want %q", got, want)
	}
	if rep, err := ReadFile(path, DefaultMaxLines); err != nil || len(rep.Trails) != 3 {
		t.Errorf("ReadFile = %+v, %v; want three trails", rep, err)
	}
}

// TestOpenRefusesAFileThatIsNotATrail: a whole line that is not an event, a
// tail no writer leaves, and one event id carrying two lines each refuse the
// open, and the file is left byte for byte as it was.
func TestOpenRefusesAFileThatIsNotATrail(t *testing.T) {
	for name, body := range map[string]string{
		"notes ending in a line cut like a record": "a note\n{not finished",
		"a note without a newline":                 "a note",
		"a trail followed by a note":               firstLine + "a note",
		"a note between two events":                firstLine + "a note\n" + lines(t, event(2)) + "{",
		"one event id carrying two lines":          firstLine + lines(t, changed(1)),
		"a whole line longer than any line":        firstLine + "{" + strings.Repeat("x", evidence.MaxLineBytes) + "\n",
	} {
		path := trailPath(t)
		writeFile(t, path, body)
		if w, err := Open(path); !errors.Is(err, ErrDamaged) || w != nil {
			t.Errorf("%s: Open = %v, %v; want ErrDamaged", name, w, err)
			if w != nil {
				_ = w.Close()
			}
		}
		if got := contents(t, path); got != body {
			t.Errorf("%s: a refused open left %q", name, got)
		}
	}
}

// TestReadFileSaysWhetherAWriterHoldsTheFile: a tail read while a writer
// holds the file is a line being written; the same tail with no writer is
// not.
func TestReadFileSaysWhetherAWriterHoldsTheFile(t *testing.T) {
	path := trailPath(t)
	writeFile(t, path, firstLine+`{"eventId":`)
	rep, err := ReadFile(path, DefaultMaxLines)
	if err != nil || rep.Unread != int64(len(`{"eventId":`)) || rep.Held {
		t.Errorf("with no writer: ReadFile = %+v, %v; want the tail counted and the file not held", rep, err)
	}
	w := openWriter(t, path)
	appendRaw(t, path, `{"eventId":`)
	rep, err = ReadFile(path, DefaultMaxLines)
	if err != nil || rep.Unread != int64(len(`{"eventId":`)) || !rep.Held {
		t.Errorf("under a writer: ReadFile = %+v, %v; want the tail counted and the file held", rep, err)
	}
	_ = w.Close()
	if rep, err = ReadFile(path, DefaultMaxLines); err != nil || rep.Held {
		t.Errorf("once the writer closed: ReadFile = %+v, %v; want the file not held", rep, err)
	}
	if strings.Count(contents(t, path), "\n") != 1 {
		t.Error("reading changed the file")
	}
}

// appendRaw adds text at the end of the file as another process would.
func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // G304: the test's own temp file
	mustDo(t, err)
	_, err = f.WriteString(text)
	mustDo(t, errors.Join(err, f.Close()))
}
