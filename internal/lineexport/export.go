// Package lineexport writes a JSON Lines file that a writer appends to as an
// export: a header, one record per whole line up to the last newline, or to
// the last write the writer committed, and a trailer, resumable after any
// line by a cursor that holds only in the file it was taken from. A format
// names its records, supplies its refusals and judges each line; the
// reading, the bounds, the cursor and the duplicates are this package's.
package lineexport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
)

// Source is the file an export reads.
type Source struct {
	// Name is the file as named, which the header repeats.
	Name string
	R    io.ReaderAt
	// Size is the file's length when it was opened; nothing past it is read.
	Size int64
	// Held asks whether a writer holds the file. It is asked only when bytes
	// follow the end the export reads to; nil answers no.
	Held func() (bool, error)
	// Committed, for a file its writer appends to in writes that each end
	// at a commit, says where the last committed write ends: at a line's
	// end, at or before the last newline. The lines after it belong to a
	// write still open, are never read, and count with the bytes after the
	// last newline as the tail. Nil ends at the last newline.
	Committed func() (int64, error)
	// Identity says what the file's first line, which names it to a cursor,
	// holds; the trailer repeats it. Empty writes no such member.
	Identity string
}

// Query is where an export starts and how much it writes.
type Query struct {
	// After is a cursor an earlier export returned; empty starts at the
	// file's first byte.
	After string
	// Limit bounds the records written, every type counted.
	Limit int
	// MaxBytes bounds the bytes of whole lines read; 0 is no bound.
	MaxBytes int64
	// Echo is the query as the header repeats it; nil is refused, as the
	// header would read null.
	Echo any
}

// Line is one whole line a Judge is asked about.
type Line struct {
	Offset int64
	// Bytes is the line with its newline.
	Bytes []byte
	Sum   Digest
}

// Body is the line as a record carries it: without its newline and one
// carriage return before it, both whitespace and no part of the value.
func (l Line) Body() []byte {
	return bytes.TrimSuffix(l.Bytes[:len(l.Bytes)-1], []byte{'\r'})
}

// Verdict is what a Judge says a line is.
type Verdict struct {
	// Type is one of the format's Lines, or Gap with a Reason.
	Type, Reason string
	// Pass is a line of a Lines type the format's filters pass by: it writes
	// no record and takes none of the limit, and its id still counts. With no
	// Type and no ID it is a line the format writes no record for at all.
	Pass bool
	// ID is the line's id, empty for none. Two lines of one type with one id
	// are the same when their Content is, and a conflict otherwise.
	ID      string
	Content Digest
}

// noRecord is a line passed by that no format type names.
func (v Verdict) noRecord() bool { return v.Pass && v.Type == "" && v.ID == "" }

// Judge says what one line is.
type Judge func(Line) Verdict

// Trailer is what an export's trailer says.
type Trailer struct {
	// NextCursor is where the next export starts, empty when the file has no
	// whole line.
	NextCursor string
	// EndReached is every line to the end the export reads to read, and the
	// bytes after it reported.
	EndReached bool
	// TailBytes is the bytes after that end: after the last newline, or
	// after the last committed write. WriterHeld is whether a writer held the
	// file when it was asked.
	TailBytes  int64
	WriterHeld bool
	// Counts are the records written by type, a key for every type.
	Counts       map[string]int
	ScannedBytes int64
}

// Export writes the whole lines of src after q.After as format f, each line
// the record judge says it is, framed by a header and a trailer, and returns
// what the trailer said. A line longer than f.MaxLineBytes is a gap without
// asking judge; one whose type and id a line this export read carries with
// other content is a gap; one whose type and id a record this export wrote
// carries with the same content is a duplicate. The bytes after the last
// newline, or after src.Committed, are a gap only when Held says no writer
// holds the file.
//
// A refused format, query or cursor writes nothing. An error after the header
// leaves the output without its trailer, which is how a reader knows it was
// cut.
func Export(f Format, src Source, q Query, judge Judge, w io.Writer) (Trailer, error) {
	x, start, end, err := open(f, src, q, judge, w)
	if err != nil {
		return Trailer{}, err
	}
	if err := x.header(src.Name); err != nil {
		return Trailer{}, err
	}
	if err := x.scan(start, end); err != nil {
		return Trailer{}, err
	}
	if err := x.tail(end); err != nil {
		return Trailer{}, err
	}
	if err := x.trailer(); err != nil {
		return Trailer{}, err
	}
	return x.tr, nil
}

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

// seenKey is a line's type and id; each type keeps its own ids.
type seenKey struct{ typ, id string }

// seenAt is the content of the lines this export read with one type and id,
// and where the first of them it wrote is, if it wrote one. One is kept per
// distinct key among the lines scanned: the byte bound limits them when the
// query gives one, and otherwise only the file's length does, since a line
// passed by counts against no record limit.
type seenAt struct {
	offset  int64
	content Digest
	written bool
}

type exporter struct {
	f          Format
	q          Query
	judge      Judge
	in         file
	first      Digest
	identified bool
	identity   string
	out        *bufio.Writer
	seen       map[seenKey]seenAt
	tr         Trailer
}

func (x *exporter) records() int {
	n := 0
	for _, c := range x.tr.Counts {
		n += c
	}
	return n
}

// scan reads the whole lines in [start, end). A line is consumed, and the
// next cursor moves past it, only once its record is written or it is passed
// by; the limit and the byte bound stop before a line, never in it, and a
// line that would cross the bound is read no further than the bound.
func (x *exporter) scan(start, end int64) error {
	lines := bufio.NewReaderSize(io.NewSectionReader(x.in.r, start, end-start), x.f.MaxLineBytes+1)
	for offset := start; offset < end; {
		room := int64(math.MaxInt64)
		if x.q.MaxBytes > 0 {
			room = x.q.MaxBytes - x.tr.ScannedBytes
		}
		line, length, sum, err := x.readLine(lines, room)
		if err != nil {
			return err
		}
		if length > room {
			if x.tr.ScannedBytes == 0 {
				return fmt.Errorf("%w: the line at offset %d is longer than the bound %d", x.f.Refusals.ByteBound, offset, x.q.MaxBytes)
			}
			return nil
		}
		l := Line{Offset: offset, Bytes: line, Sum: sum}
		v, first, err := x.decide(l)
		if err != nil {
			return err
		}
		if !v.Pass && x.records() == x.q.Limit {
			return nil
		}
		next := cursor{First: x.first, Offset: offset + length, Line: sum}.String()
		if err := x.record(v, next, l, first); err != nil {
			return err
		}
		x.tr.ScannedBytes += length
		x.tr.NextCursor = next
		offset += length
	}
	x.tr.EndReached = true
	return nil
}

// decide is the record a line gets and, for a duplicate, the offset of the
// line it repeats. A line nil is one too long to hold. The content is checked
// before Pass is, so a conflict is a gap whatever the filters pass; a line
// whose earlier ones were all passed by is written, not a duplicate.
func (x *exporter) decide(l Line) (Verdict, int64, error) {
	if l.Bytes == nil {
		return Verdict{Type: Gap, Reason: GapTooLong}, 0, nil
	}
	v := x.judge(l)
	switch {
	case v.Type == Gap && v.Reason != "":
		return Verdict{Type: Gap, Reason: v.Reason}, 0, nil
	case v.noRecord():
		return v, 0, nil
	case !x.f.carries(v.Type):
		return Verdict{}, 0, fmt.Errorf("%w: the line at offset %d was judged %q with reason %q", ErrInvalid, l.Offset, v.Type, v.Reason)
	}
	first, seen := x.seen[seenKey{v.Type, v.ID}]
	switch {
	case seen && first.content != v.Content:
		return Verdict{Type: Gap, Reason: x.f.Conflict}, 0, nil
	case v.Pass:
		return v, 0, nil
	case seen && first.written:
		return Verdict{Type: Duplicate, ID: v.ID}, first.offset, nil
	}
	return v, 0, nil
}

// record writes the record decide chose for l, none for a line passed by,
// and keeps the content of a line's id and where it was first written.
func (x *exporter) record(v Verdict, next string, l Line, first int64) error {
	k := seenKey{v.Type, v.ID}
	if at, ok := x.seen[k]; v.ID != "" && x.f.carries(v.Type) && (!ok || !at.written) {
		x.seen[k] = seenAt{offset: l.Offset, content: v.Content, written: !v.Pass}
	}
	switch {
	case v.Pass:
		return nil
	case v.Type == Gap:
		return x.gap(l.Offset, next, v.Reason)
	case v.Type == Duplicate:
		return x.duplicate(l.Offset, v.ID, first)
	}
	return x.line(v.Type, l.Offset, next, l.Body())
}

// tail reports the bytes after the end as a gap, once every whole
// line is read, when no writer holds the file. The gap takes a record, and an
// export with no room left for it has not reached the end.
func (x *exporter) tail(end int64) error {
	switch {
	case !x.tr.EndReached || x.tr.TailBytes == 0 || x.tr.WriterHeld:
		return nil
	case x.records() == x.q.Limit:
		x.tr.EndReached = false
		return nil
	}
	return x.gap(end, "", GapPartialTail)
}

// readLine reads one line and its newline, and its digest. A line longer than
// the format's bound is read through to its newline and returned nil. Once
// more than room bytes are read the line is abandoned, and only its length so
// far, past room, is returned.
func (x *exporter) readLine(r *bufio.Reader, room int64) ([]byte, int64, Digest, error) {
	h := sha256.New()
	var length int64
	for {
		chunk, err := r.ReadSlice('\n')
		h.Write(chunk)
		length += int64(len(chunk))
		switch {
		case err != nil && !errors.Is(err, bufio.ErrBufferFull):
			return nil, 0, Digest{}, fmt.Errorf("%w: no newline where the last one was: %w", x.f.Refusals.ShortRead, err)
		case length > room:
			return nil, length, Digest{}, nil
		case err != nil:
			continue
		}
		var sum Digest
		h.Sum(sum[:0])
		if length > int64(len(chunk)) {
			return nil, length, sum, nil
		}
		return chunk, length, sum, nil
	}
}
