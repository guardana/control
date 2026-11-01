package segmentlog

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// stats is what a scan found in a segment: its whole lines, their bytes, the
// digests of the first and the last, and the bytes after the last newline.
type stats struct {
	lines, bytes int64
	first, last  Digest
	tail         int64
}

// matches reports whether the scan found what the manifest names s with.
func (st stats) matches(s Segment) bool {
	return st.tail == 0 && st.lines == s.Lines && st.bytes == s.Bytes && st.first == s.First && st.last == s.Last
}

// scan reads r's lines to its last newline and hands each, its newline
// included, to visit when it is set. A line longer than maxLine bytes before
// its newline is refused, so a scan holds one bounded line at a time.
func scan(r io.Reader, maxLine int, visit func([]byte) error) (stats, error) {
	reader := bufio.NewReaderSize(r, maxLine+1)
	var st stats
	for {
		line, err := reader.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			return stats{}, fmt.Errorf("%w: line %d is over %d bytes", ErrManifest, st.lines+1, maxLine)
		case errors.Is(err, io.EOF):
			st.tail = int64(len(line))
			return st, nil
		case err != nil:
			return stats{}, err
		}
		if visit != nil {
			if err := visit(line); err != nil {
				return stats{}, fmt.Errorf("line %d: %w", st.lines+1, err)
			}
		}
		st.add(line)
	}
}

// add counts one whole line.
func (st *stats) add(line []byte) {
	sum := sha256.Sum256(line)
	if st.lines == 0 {
		st.first = sum
	}
	st.last = sum
	st.lines++
	st.bytes += int64(len(line))
}

// segment is the scan as a segment of the state given.
func (st stats) segment(state State) Segment {
	return Segment{State: state, Lines: st.lines, Bytes: st.bytes, First: st.first, Last: st.last}
}
