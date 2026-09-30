package trailfile

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/evidence"
)

const (
	// ExportFormat names the format Export writes.
	ExportFormat = brand.OTelNamespace + ".evidence-export"
	// ExportVersion is the format's version, read by the MAJOR.MINOR rules
	// of the wire contracts.
	ExportVersion = "1.0"

	// DefaultExportLimit is the records an export writes when not told.
	DefaultExportLimit = 1000
	// MaxExportLimit is the most records one export writes.
	MaxExportLimit = 100_000
)

const (
	// ErrQuery is a query the export refuses: a limit outside 1 to
	// MaxExportLimit, a negative byte bound, an empty filter value or a kind
	// the contract does not name.
	ErrQuery Error = "trailfile: the export's query is refused"
	// ErrByteBound is a next line longer than the byte bound, which no export
	// under that bound can pass.
	ErrByteBound Error = "trailfile: the next line is longer than the byte bound"
	// ErrShortRead is a file that no longer holds what it held when the
	// export opened it.
	ErrShortRead Error = "trailfile: the file no longer holds what it held when the export opened it"
)

// Query is what an export reads and how much of it.
type Query struct {
	// After is a cursor an earlier export returned; the export starts after
	// the line it names. Empty starts at the file's first byte.
	After string
	// Limit bounds the records written, every type counted.
	Limit int
	// MaxBytes bounds the bytes of whole lines read; 0 is no bound.
	MaxBytes int64
	// An event is written when it matches one value of every filter given.
	Requests, Runs, Tenants, Projects, Kinds []string
}

// Source is the file an export reads.
type Source struct {
	// Name is the file as named, which the header repeats.
	Name string
	R    io.ReaderAt
	// Size is the file's length when it was opened; nothing past it is read.
	Size int64
	// Held asks whether a writer holds the file. It is asked only when bytes
	// follow the last newline; nil answers no.
	Held func() (bool, error)
}

// Trailer is what an export's trailer says.
type Trailer struct {
	// NextCursor is where the next export starts, empty when the file has no
	// whole line.
	NextCursor string
	// EndReached is every line to the last newline read, and the bytes after
	// it reported.
	EndReached bool
	// TailBytes is the bytes after the last newline, and WriterHeld whether a
	// writer held the file when it was asked.
	TailBytes  int64
	WriterHeld bool
	// The records written by type, and the bytes of the whole lines read.
	Events, Gaps, Duplicates int
	ScannedBytes             int64
}

// Gap reasons, as the gap record spells them.
const (
	gapMalformed   = "malformed"
	gapTooLong     = "too_long"
	gapVersion     = "unsupported_version"
	gapConflict    = "conflicting_event_id"
	gapPartialTail = "partial_tail"
	gapCR          = "carriage_return"
)

// Export writes the whole lines of src after q.After as the evidence export,
// JSON Lines framed by a header and a trailer, and returns what the trailer
// said. docs/contracts.md, "The evidence export", is the format.
//
// Each line is one record: an event, carrying the line's bytes as they stand
// but for its line ending; a gap, for a line that is not one event of a major
// this build reads, holds a carriage return before its line ending, or
// carries an event id a line this export read carries with other content; or
// a duplicate, for a line repeating one this export wrote. Duplicates and
// conflicts are found among the lines this export reads only, whatever the
// filters. The bytes after the last newline are a gap only when Held says no
// writer holds the file.
//
// A refused query or cursor writes nothing. An error after the header leaves
// the output without its trailer, which is how a reader knows it was cut.
func Export(src Source, q Query, w io.Writer) (Trailer, error) {
	x, start, end, err := newExporter(src, q, w)
	if err != nil {
		return Trailer{}, err
	}
	if err := x.header(src.Name); err != nil {
		return Trailer{}, err
	}
	if err := x.scan(src.R, start, end); err != nil {
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

// newExporter checks the query and the cursor against src and asks about a
// writer, all before a byte is written, and returns where the scan starts
// and ends: after the cursor's line, and at the last newline.
func newExporter(src Source, q Query, w io.Writer) (*exporter, int64, int64, error) {
	f, err := q.filter()
	if err != nil {
		return nil, 0, 0, err
	}
	if src.Size < 0 {
		return nil, 0, 0, fmt.Errorf("%w: size %d", ErrLimit, src.Size)
	}
	end, err := lastNewline(src.R, src.Size)
	if err != nil {
		return nil, 0, 0, err
	}
	first, identified, err := firstLineDigest(src.R, end)
	if err != nil {
		return nil, 0, 0, err
	}
	start, err := resume(src.R, end, first, identified, q.After)
	if err != nil {
		return nil, 0, 0, err
	}
	x := &exporter{q: q, f: f, first: first, identified: identified, out: bufio.NewWriter(w), seen: map[string]seenAt{}}
	x.tr.NextCursor, x.tr.TailBytes = q.After, src.Size-end
	if x.tr.TailBytes > 0 && src.Held != nil {
		if x.tr.WriterHeld, err = src.Held(); err != nil {
			return nil, 0, 0, fmt.Errorf("asking whether a writer holds the file: %w", err)
		}
	}
	return x, start, end, nil
}

// resume is the offset an export after the cursor after starts at, 0 with no
// cursor.
func resume(r io.ReaderAt, end int64, first digest, identified bool, after string) (int64, error) {
	if after == "" {
		return 0, nil
	}
	c, err := parseCursor(after)
	if err != nil {
		return 0, err
	}
	if err := c.check(r, end, first, identified); err != nil {
		return 0, err
	}
	return c.offset, nil
}

// ExportFile exports the file at path as Export does, reading the length it
// has when opened and holding no lock, so it reads a file a collector is
// writing. Anything that is not a regular file is refused, without waiting on
// a named pipe.
func ExportFile(path string, q Query, w io.Writer) (Trailer, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|readFlags, 0) //nolint:gosec // G304: the path is the operator's to name, and the descriptor is judged before a byte is read
	if err != nil {
		return Trailer{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	switch {
	case err != nil:
		return Trailer{}, err
	case !info.Mode().IsRegular():
		return Trailer{}, fmt.Errorf("%w: %s", ErrNotRegular, strconv.Quote(path))
	}
	return Export(Source{Name: path, R: f, Size: info.Size(), Held: func() (bool, error) { return heldByWriter(f) }}, q, w)
}

// seenAt is where an event id this export read was first seen, written or
// passed by, and the digest of that line. One is kept per distinct id among
// the lines scanned, so the scan's bounds bound them.
type seenAt struct {
	offset int64
	line   digest
}

type exporter struct {
	q          Query
	f          filter
	first      digest
	identified bool
	out        *bufio.Writer
	seen       map[string]seenAt
	tr         Trailer
}

func (x *exporter) records() int { return x.tr.Events + x.tr.Gaps + x.tr.Duplicates }

// scan reads the whole lines in [start, end). A line is consumed, and the
// next cursor moves past it, only once its record is written or the filters
// passed it by; the limit and the byte bound stop before a line, never in it.
func (x *exporter) scan(r io.ReaderAt, start, end int64) error {
	lines := bufio.NewReaderSize(io.NewSectionReader(r, start, end-start), evidence.MaxLineBytes+1)
	for offset := start; offset < end; {
		line, length, sum, err := readLine(lines)
		if err != nil {
			return err
		}
		if x.q.MaxBytes > 0 && x.tr.ScannedBytes+length > x.q.MaxBytes {
			if x.tr.ScannedBytes == 0 {
				return fmt.Errorf("%w: the line at offset %d is %d bytes, the bound %d", ErrByteBound, offset, length, x.q.MaxBytes)
			}
			return nil
		}
		next := cursor{first: x.first, offset: offset + length, line: sum}.String()
		kind, reason, id := x.judge(line, sum)
		if kind != "" && x.records() == x.q.Limit {
			return nil
		}
		if err := x.record(kind, reason, id, offset, next, line, sum); err != nil {
			return err
		}
		x.tr.ScannedBytes += length
		x.tr.NextCursor = next
		offset += length
	}
	x.tr.EndReached = true
	return nil
}

// record writes the record judge chose for the line at offset, none for a
// line the filters passed by.
func (x *exporter) record(kind, reason, id string, offset int64, next string, line []byte, sum digest) error {
	if _, ok := x.seen[id]; !ok && id != "" && (kind == "event" || kind == "") {
		x.seen[id] = seenAt{offset: offset, line: sum}
	}
	switch kind {
	case "event":
		return x.event(offset, next, eventBytes(line))
	case "gap":
		return x.gap(offset, next, reason)
	case "duplicate":
		return x.duplicate(offset, id)
	}
	return nil
}

// tail reports the bytes after the last newline as a gap, once every whole
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
	return x.gap(end, "", gapPartialTail)
}

// judge says what record a line is: an event, a gap and why, a duplicate of
// an event id, or none when the filters pass the event by. A line nil is one
// too long to hold. An event id is checked against the lines read before the
// filters, so a conflict is a gap whatever they pass.
func (x *exporter) judge(line []byte, sum digest) (kind, reason, id string) {
	if line == nil {
		return "gap", gapTooLong, ""
	}
	events, err := evidence.DecodeJSONL(bytes.NewReader(line), 1)
	if err != nil {
		return "gap", gapMalformed, ""
	}
	ev := events[0]
	if evidence.CheckEventVersion(ev) != nil {
		return "gap", gapVersion, ""
	}
	if bytes.IndexByte(eventBytes(line), '\r') >= 0 {
		return "gap", gapCR, ""
	}
	id = ev.GetEventId()
	if first, ok := x.seen[id]; ok && first.line != sum {
		return "gap", gapConflict, ""
	}
	// A line repeating one exactly matches the filters as that one did, so a
	// duplicate is written only of an event this export wrote.
	if !x.f.match(ev) {
		return "", "", id
	}
	if _, ok := x.seen[id]; ok {
		return "duplicate", "", id
	}
	return "event", "", id
}

// eventBytes is a line as an event record carries it: without its newline
// and one carriage return before it, both whitespace and no part of the value.
func eventBytes(line []byte) []byte {
	return bytes.TrimSuffix(line[:len(line)-1], []byte{'\r'})
}

// readLine reads one line and its newline, and its digest. A line longer than
// the codec's bound is read through to its newline and returned nil.
func readLine(r *bufio.Reader) ([]byte, int64, digest, error) {
	h := sha256.New()
	var length int64
	for {
		chunk, err := r.ReadSlice('\n')
		h.Write(chunk)
		length += int64(len(chunk))
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err != nil:
			return nil, 0, digest{}, fmt.Errorf("%w: no newline where the last one was: %w", ErrShortRead, err)
		}
		var sum digest
		h.Sum(sum[:0])
		if length > int64(len(chunk)) {
			return nil, length, sum, nil
		}
		return chunk, length, sum, nil
	}
}
