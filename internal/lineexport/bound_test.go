package lineexport

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// countBytes counts the bytes read from a source.
type countBytes struct {
	r io.ReaderAt
	n *int64
}

func (c countBytes) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	*c.n += int64(n)
	return n, err
}

// hugeLine is a line far longer than every buffer an export reads through,
// so reading it whole shows in the bytes read.
var hugeLine = `"n h ` + strings.Repeat("h", 4<<20) + `"`

// readBound is more than the export reads around the byte bound, and far less
// than hugeLine.
const readBound = 256 << 10

// TestTheByteBoundStopsTheReadNotOnlyTheExport: a line that would cross the
// bound is left unread past it, and a first line longer than the bound is
// refused before it is read through, with nothing written. A resumed export's
// first line is its next one, so an export that could only return its own
// cursor again is refused rather than looping its consumer.
func TestTheByteBoundStopsTheReadNotOnlyTheExport(t *testing.T) {
	resumed := []string{`"n a 1"`, hugeLine, `"n b 1"`}
	for name, c := range map[string]struct {
		lines   []string
		after   string
		refused bool
	}{
		"a long line after the first":   {[]string{`"n a 1"`, hugeLine, `"n b 1"`}, "", false},
		"a long first line":             {[]string{hugeLine, `"n b 1"`}, "", true},
		"a long line next after resume": {resumed, cursorAt(resumed, 1), true},
	} {
		var read int64
		s := src(body(c.lines...), false)
		s.R = countBytes{s.R, &read}
		q := query(10)
		q.After = c.after
		q.MaxBytes = 16
		var out bytes.Buffer
		tr, err := Export(testFormat(), s, q, judge, &out)
		switch {
		case c.refused && (!errors.Is(err, errBound) || out.Len() != 0):
			t.Errorf("%s: Export = %v with %d bytes written, want the byte-bound refusal and nothing", name, err, out.Len())
		case !c.refused && (err != nil || tr.ScannedBytes != 8 || tr.EndReached):
			t.Errorf("%s: Export = %+v, %v; want the first line read and the end not reached", name, tr, err)
		}
		if read > readBound {
			t.Errorf("%s: the export read %d bytes under a bound of %d", name, read, q.MaxBytes)
		}
	}
}
