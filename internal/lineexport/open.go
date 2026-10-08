package lineexport

import (
	"bufio"
	"fmt"
	"io"
)

// open checks the format, the query and the cursor against src and asks about
// a writer, all before a byte is written, and returns where the scan starts
// and ends: after the cursor's line, and at the last newline or the
// committed end.
func open(f Format, src Source, q Query, judge Judge, w io.Writer) (*exporter, int64, int64, error) {
	if err := f.check(); err != nil {
		return nil, 0, 0, err
	}
	if err := checkCall(src, q, judge, w); err != nil {
		return nil, 0, 0, err
	}
	in := file{r: src.R, rf: f.Refusals}
	end, err := in.end(src)
	if err != nil {
		return nil, 0, 0, err
	}
	// With no cursor the first line is the first one scanned, so one longer
	// than the bound is refused before it is read through.
	look := end
	if q.MaxBytes > 0 && q.After == "" {
		look = min(end, q.MaxBytes)
	}
	first, identified, err := in.firstLineDigest(end, look)
	if err != nil {
		return nil, 0, 0, err
	}
	start, err := in.resume(end, first, identified, q.After)
	if err != nil {
		return nil, 0, 0, err
	}
	x := &exporter{f: f, q: q, judge: judge, in: in, first: first, identified: identified, identity: src.Identity,
		out: bufio.NewWriter(w), seen: map[seenKey]seenAt{},
		tr: Trailer{NextCursor: q.After, TailBytes: src.Size - end, Counts: f.zeroCounts()}}
	if x.tr.TailBytes > 0 && src.Held != nil {
		if x.tr.WriterHeld, err = src.Held(); err != nil {
			return nil, 0, 0, fmt.Errorf("asking whether a writer holds the file: %w", err)
		}
	}
	return x, start, end, nil
}

// checkCall refuses a call that leaves out a part an export needs, or a query
// or a size no file can answer.
func checkCall(src Source, q Query, judge Judge, w io.Writer) error {
	if judge == nil || src.R == nil || w == nil || q.Echo == nil || q.Limit < 1 || q.MaxBytes < 0 || src.Size < 0 {
		return fmt.Errorf("%w: limit %d, byte bound %d, size %d, or no judge, reader, writer or echo", ErrInvalid, q.Limit, q.MaxBytes, src.Size)
	}
	return nil
}

// end is where the lines an export reads end: the last newline, or the end
// of the last committed write when src says where that is.
func (f file) end(src Source) (int64, error) {
	end, err := f.lastNewline(src.Size)
	if err != nil || src.Committed == nil {
		return end, err
	}
	return f.committed(end, src.Committed)
}

// committed is the end the committed lines reach, refused unless it ends a
// line at or before end, the last newline.
func (f file) committed(end int64, at func() (int64, error)) (int64, error) {
	c, err := at()
	switch {
	case err != nil:
		return 0, fmt.Errorf("finding the end of the last committed write: %w", err)
	case c < 0 || c > end:
		return 0, fmt.Errorf("%w: a committed end at %d, the last newline ends at %d", ErrInvalid, c, end)
	case c == 0:
		return 0, nil
	}
	var before [1]byte
	if err := f.readFull(before[:], c-1); err != nil {
		return 0, err
	}
	if before[0] != '\n' {
		return 0, fmt.Errorf("%w: a committed end at %d is not the end of a line", ErrInvalid, c)
	}
	return c, nil
}

// resume is the offset an export after the cursor after starts at, 0 with no
// cursor.
func (f file) resume(end int64, first Digest, identified bool, after string) (int64, error) {
	if after == "" {
		return 0, nil
	}
	c, err := parseCursor(after, f.rf)
	if err != nil {
		return 0, err
	}
	if err := f.check(c, end, first, identified); err != nil {
		return 0, err
	}
	return c.Offset, nil
}
