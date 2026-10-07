package findinglog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
)

// lineFault is a line the reader refuses: over MaxLineBytes, or judged and
// found damaged. Its cause can quote the line, so it is worded without it,
// naming at most the field a fieldError names.
type lineFault struct {
	number int
	offset int64
	over   bool
	field  fieldError
}

func (f *lineFault) Error() string {
	switch {
	case f.over:
		return fmt.Sprintf("%s: line %d, at byte %d, is over %d bytes", ErrDamaged, f.number, f.offset, MaxLineBytes)
	case f.field != "":
		return fmt.Sprintf("%s: line %d, at byte %d: %s", ErrDamaged, f.number, f.offset, f.field)
	}
	return fmt.Sprintf("%s: line %d, at byte %d", ErrDamaged, f.number, f.offset)
}

func (f *lineFault) Unwrap() error { return ErrDamaged }

// scanLog reads r as whole log lines and returns the index of the findings
// every report closes, with the length up to the end of the last report, or
// of the log's header when no report follows it. The findings after it are a
// write a crash cut before its report, so they are read and judged but not
// indexed. A line that is not one record in the writer's form, a carriage
// return, a key carried twice, a report whose findings written is not the
// number of findings of its write and a header after the first line are
// ErrDamaged: the writer leaves none of them. When keep is not nil, it is
// handed the header, then each write's records once the write's report
// closes it.
func scanLog(r io.Reader, keep func([]*findingv1alpha1.Record)) (index, int64, error) {
	reader := bufio.NewReaderSize(r, MaxLineBytes+1)
	ids, pending := index{}, index{}
	var write []*findingv1alpha1.Record
	var offset, committed int64
	for number := 1; ; number++ {
		line, err := reader.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			return nil, 0, &lineFault{number: number, offset: offset, over: true}
		case errors.Is(err, io.EOF) && len(line) == 0:
			return ids, committed, nil
		case err != nil:
			return nil, 0, fmt.Errorf("reading line %d: %w", number, err)
		}
		record, err := ids.read(pending, line, number == 1)
		if err != nil {
			fault := &lineFault{number: number, offset: offset}
			errors.As(err, &fault.field)
			return nil, 0, fault
		}
		offset += int64(len(line))
		if keep != nil {
			write = append(write, record)
		}
		if record.GetSuperviseReport() != nil || record.GetLogHeader() != nil {
			ids.add(pending)
			clear(pending)
			committed = offset
			if keep != nil {
				keep(write)
				write = nil
			}
		}
	}
}

// read judges one whole line and adds a finding to pending. A report must
// count the findings of its write, pending, as the writer sets it, and only
// the first line may be the header.
func (ids index) read(pending index, line []byte, first bool) (*findingv1alpha1.Record, error) {
	r, err := unmarshalLine(line)
	if err != nil {
		return nil, err
	}
	if r.GetLogHeader() != nil && !first {
		return nil, fieldError("log_header: not the first line")
	}
	if rep := r.GetSuperviseReport(); rep != nil && rep.GetFindingsWritten() != uint64(len(pending)) {
		return nil, errors.New("a report that miscounts its write")
	}
	f := r.GetFindingRecord()
	if f == nil {
		return r, nil
	}
	k := keyOf(f)
	_, held := ids[k]
	_, waiting := pending[k]
	if held || waiting {
		return nil, errors.New("a key an earlier line carries")
	}
	sum, err := digest(f)
	if err != nil {
		return nil, err
	}
	pending[k] = sum
	return r, nil
}

// lastLineEnd returns the length of the file up to and including its last
// newline. Only the last MaxLineBytes+1 bytes are read: a writer leaves at
// most MaxLineBytes after the last newline, so a newline that is not among
// them is ErrDamaged. So is a tail that cannot be the start of a line the
// writer writes, which is no write a crash cut.
func lastLineEnd(f io.ReaderAt, size int64) (int64, error) {
	if size == 0 {
		return 0, nil
	}
	window := min(size, int64(MaxLineBytes)+1)
	tail := make([]byte, window)
	if _, err := f.ReadAt(tail, size-window); err != nil {
		return 0, err
	}
	start := bytes.LastIndexByte(tail, '\n') + 1
	end := size - window + int64(start)
	switch {
	case start == 0 && size > MaxLineBytes:
		return 0, fmt.Errorf("%w: more than %d bytes follow the last newline", ErrDamaged, MaxLineBytes)
	case start < len(tail) && !torn(tail[start:], end == 0):
		return 0, fmt.Errorf("%w: the bytes after the last newline are not the start of a log line", ErrDamaged)
	}
	return end, nil
}

// opening is a way a line the writer writes can begin: an object whose one
// member is a record's kind, in its JSON name and with no space.
type opening struct {
	prefix []byte
	header bool
}

var openings = func() []opening {
	fields := (&findingv1alpha1.Record{}).ProtoReflect().Descriptor().Fields()
	header := (&findingv1alpha1.Record{}).ProtoReflect().Descriptor().Fields().ByName("log_header")
	out := make([]opening, 0, fields.Len())
	for i := range fields.Len() {
		fd := fields.Get(i)
		out = append(out, opening{prefix: []byte(`{"` + fd.JSONName() + `":`), header: fd == header})
	}
	return out
}()

// torn reports whether tail can be what a crash left of a line the writer
// wrote; first says whether it starts the file, the one place the writer
// writes a header. A whole JSON value can, only when it reads as one record
// in the writer's form: the write was cut between the object and its
// newline. Anything shorter can only when it agrees with one opening as far
// as either goes and is a proper prefix of one JSON value.
func torn(tail []byte, first bool) bool {
	if json.Valid(tail) {
		r, err := unmarshalLine(tail)
		return err == nil && (first || r.GetLogHeader() == nil)
	}
	for _, o := range openings {
		n := min(len(tail), len(o.prefix))
		if bytes.Equal(tail[:n], o.prefix[:n]) && (first || !o.header) {
			return prefixOfOneValue(tail)
		}
	}
	return false
}

// prefixOfOneValue reports whether b is a proper prefix of one JSON value: a
// walk of its tokens meets no syntax error, and ends only because the bytes
// ran out inside that value. A value that closes with bytes after it is not.
func prefixOfOneValue(b []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	depth := 0
	for {
		tok, err := dec.Token()
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF):
			return true
		case errors.Is(err, io.EOF):
			return depth > 0
		case err != nil:
			return false
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return false
		}
	}
}
