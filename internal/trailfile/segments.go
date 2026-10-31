package trailfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/lineexport"
	"github.com/guardana/control/internal/segmentlog"
)

// ErrCursorExpired is a cursor naming a segment that was pruned.
const ErrCursorExpired Error = "trailfile: cursor expired: the segment it names was pruned"

// moveRetries bounds how often a read of a log starts again when a seal or a
// prune moved the segment it was opening.
const moveRetries = 3

// IsLog reports whether path names a directory, which only a log of trail
// segments is to a reader of trails.
func IsLog(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// ExportPath exports a trail file as ExportFile does, or one segment of a log
// as ExportLog does when path is a directory.
func ExportPath(path string, q Query, w io.Writer) (Trailer, error) {
	dir, err := IsLog(path)
	switch {
	case err != nil:
		return Trailer{}, err
	case dir:
		return ExportLog(path, q, w)
	}
	return ExportFile(path, q, w)
}

// ExportLog writes the evidence export of the log in dir, one segment at a
// time. With no cursor it is the first segment not pruned. A segment is named
// to a cursor, and in the header's source, by segmentlog.SegmentKey of its
// number and its first line, so no two segments of the log share a cursor.
// The trailer says the end was reached only at the end of the last segment
// that held a line when the export began; at the end of an earlier one its
// next_cursor leads into the next. A cursor of a pruned segment is
// ErrCursorExpired, and one no segment the log names has the key of is
// ErrCursorOtherFile. The header's file is the segment's. A segment opened is
// read to the end from its descriptor, so a seal or a prune while it is read
// changes nothing read.
func ExportLog(dir string, q Query, w io.Writer) (Trailer, error) {
	if _, err := q.filter(); err != nil {
		return Trailer{}, err
	}
	for range moveRetries {
		src, after, done, err := openForExport(dir, q.After)
		if errors.Is(err, segmentlog.ErrMoved) {
			continue
		}
		if err != nil {
			return Trailer{}, err
		}
		tr, err := export(src, q, after, w)
		return tr, errors.Join(err, done())
	}
	return Trailer{}, fmt.Errorf("%w: %d times running", segmentlog.ErrMoved, moveRetries)
}

// openForExport finds the segment an export after the cursor reads, and opens
// it. It returns the source, the cursor to start after within it, and what
// closes it.
func openForExport(dir, cursor string) (Source, string, func() error, error) {
	v, err := segmentlog.Inspect(dir, evidence.MaxLineBytes)
	if err != nil {
		return Source{}, "", nil, err
	}
	ans := v.First()
	after := cursor
	if cursor != "" {
		if ans, after, err = resume(v, cursor); err != nil {
			return Source{}, "", nil, err
		}
	}
	return openSource(dir, v, ans.Segment(), after)
}

// resume answers for the segment a cursor names, moving past the end of a
// sealed one to the next once that holds a line.
func resume(v *segmentlog.View, cursor string) (segmentlog.SegmentAnswer, string, error) {
	key, offset, err := lineexport.CursorAt(cursor, ErrCursorMalformed)
	if err != nil {
		return segmentlog.SegmentAnswer{}, "", err
	}
	ans := v.Locate(key)
	switch ans.State() {
	case segmentlog.StatePruned:
		s := ans.Segment()
		return ans, "", fmt.Errorf("%w: segment %d, pruned at %s", ErrCursorExpired, s.Seq, s.PrunedAt.Format(time.RFC3339))
	case segmentlog.StateSealed, segmentlog.StateLive:
	default:
		return ans, "", fmt.Errorf("%w: %s", ErrCursorOtherFile, ans.Cause())
	}
	s := ans.Segment()
	if s.State != segmentlog.StateSealed || offset != s.Bytes {
		return ans, cursor, nil
	}
	if holdsLater(v, s) {
		return v.After(s.Seq), "", nil
	}
	return ans, cursor, nil
}

// openSource opens the segment s of the log in dir as an export's source. A
// live segment that holds no line and is not there is an empty source.
func openSource(dir string, v *segmentlog.View, s segmentlog.Segment, after string) (Source, string, func() error, error) {
	name := filepath.Join(dir, s.File)
	f, size, err := v.Open(s)
	if s.State == segmentlog.StateLive && s.Lines == 0 && errors.Is(err, fs.ErrNotExist) {
		return Source{Name: name, R: bytes.NewReader(nil)}, after, func() error { return nil }, nil
	}
	if err != nil {
		return Source{}, "", nil, err
	}
	seq := s.Seq
	src := Source{Name: name, R: f, Size: size, More: holdsLater(v, s),
		Key: func(first lineexport.Digest) lineexport.Digest { return segmentlog.SegmentKey(seq, first) }}
	if s.State == segmentlog.StateLive {
		src.Held = func() (bool, error) { return heldByWriter(f) }
	}
	return src, after, f.Close, nil
}

// holdsLater reports whether a segment after s held a line when the view was
// read, so an export of s has not reached the log's end at s's.
func holdsLater(v *segmentlog.View, s segmentlog.Segment) bool {
	if s.State != segmentlog.StateSealed {
		return false
	}
	next := v.After(s.Seq)
	return next.State() == segmentlog.StateSealed || next.State() == segmentlog.StateLive && next.Segment().Lines > 0
}

// ReadPath reads a trail file as ReadFile does, or every segment of a log not
// pruned, in order, when path is a directory.
func ReadPath(path string, maxLines int) (Report, error) {
	dir, err := IsLog(path)
	switch {
	case err != nil:
		return Report{}, err
	case dir:
		return ReadLog(path, maxLines)
	}
	return ReadFile(path, maxLines)
}

// ReadLog reads every segment of the log in dir that is not pruned, sealed
// ones first and in order, as one file Read reads. A trail whose first events
// were in a pruned segment fails the chain check, which sees only its rest.
func ReadLog(dir string, maxLines int) (Report, error) {
	for range moveRetries {
		rep, err := readLog(dir, maxLines)
		if errors.Is(err, segmentlog.ErrMoved) {
			continue
		}
		return rep, err
	}
	return Report{}, fmt.Errorf("%w: %d times running", segmentlog.ErrMoved, moveRetries)
}

func readLog(dir string, maxLines int) (Report, error) {
	r, live, done, err := openLog(dir)
	if err != nil {
		return Report{}, err
	}
	defer done()
	rep, err := Read(r, maxLines)
	if err != nil || rep.Unread == 0 || live == nil {
		return rep, err
	}
	if rep.Held, err = heldByWriter(live); err != nil {
		return Report{}, err
	}
	return rep, nil
}

// openLog opens every segment of the log in dir that is not pruned and
// returns them as one reader, in order, each to the length it had when
// opened, with the live segment's file when there is one and what closes
// them all.
func openLog(dir string) (io.Reader, *os.File, func(), error) {
	v, err := segmentlog.Inspect(dir, evidence.MaxLineBytes)
	if err != nil {
		return nil, nil, nil, err
	}
	m := v.Manifest()
	var readers []io.Reader
	var live *os.File
	var opened []*os.File
	done := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	for _, s := range append(m.Segments, m.Live) {
		if s.State == segmentlog.StatePruned {
			continue
		}
		f, size, err := v.Open(s)
		if s.State == segmentlog.StateLive && s.Lines == 0 && errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			done()
			return nil, nil, nil, err
		}
		opened = append(opened, f)
		readers = append(readers, io.NewSectionReader(f, 0, size))
		if s.State == segmentlog.StateLive {
			live = f
		}
	}
	return io.MultiReader(readers...), live, done, nil
}

// ErrTooLarge is a log whose segments not pruned hold more than a read's
// bound.
const ErrTooLarge Error = "trailfile: the log's segments hold more than the bound"

// LogBytes is every whole line of the segments of the log in dir that are
// not pruned, in order, at most limit bytes of them: a log holding more is
// ErrTooLarge.
func LogBytes(dir string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("%w: %d", ErrLimit, limit)
	}
	for range moveRetries {
		r, _, done, err := openLog(dir)
		if errors.Is(err, segmentlog.ErrMoved) {
			continue
		}
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(r, limit+1))
		done()
		switch {
		case err != nil:
			return nil, err
		case int64(len(raw)) > limit:
			return nil, fmt.Errorf("%w: %d", ErrTooLarge, limit)
		}
		return raw[:bytes.LastIndexByte(raw, '\n')+1], nil
	}
	return nil, fmt.Errorf("%w: %d times running", segmentlog.ErrMoved, moveRetries)
}
