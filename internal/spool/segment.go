package spool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// segmentFile is what the spool writes through: an appending file that can be
// forced to disk. The operating system's file is the one outside tests.
type segmentFile interface {
	io.Writer
	Sync() error
	Close() error
}

// A segment is named by the sequence number of its first record, zero-padded
// so the directory lists it in log order.
var segmentName = regexp.MustCompile(`^([0-9]{20})\.seg$`)

func segmentPath(dir string, seq uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d.seg", seq))
}

// segment is one file of the log. size is the committed length: bytes before
// it are whole records readers may deliver, bytes at or past it are not part
// of the log.
type segment struct {
	seq     uint64
	path    string
	size    int64
	records int
	// file is non-nil while the segment is open for append.
	file segmentFile
}

// openOSFile opens path for append, creating it. The mode keeps the evidence
// to the process's own user.
func openOSFile(path string) (segmentFile, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // G304: the path is the spool's own, built from a sequence number
}

// write puts one framed record at the end of the current segment, rolling to
// a new one when it would not fit, and forces it to disk under
// FsyncEveryRecord. The record is committed only when every step succeeded;
// on a failure the segment is cut back to its committed length so the next
// append never lands behind a torn one.
func (s *Spool) write(record []byte) error {
	size := int64(len(record))
	if err := s.ensureSegment(size); err != nil {
		return err
	}
	if _, err := s.cur.file.Write(record); err != nil {
		return s.repair(fmt.Errorf("spool: write: %w", err))
	}
	if s.opts.Fsync == FsyncEveryRecord {
		if err := s.cur.file.Sync(); err != nil {
			return s.repair(fmt.Errorf("spool: sync: %w", err))
		}
	}
	s.cur.size += size
	s.cur.records++
	s.nextSeq++
	return nil
}

// ensureSegment leaves s.cur open with room for size bytes: the current one,
// the last one on disk reopened after Open or a repair, or a new one.
func (s *Spool) ensureSegment(size int64) error {
	if s.cur != nil {
		if s.cur.size+size <= s.opts.SegmentBytes {
			return nil
		}
		if err := s.retire(); err != nil {
			return err
		}
	}
	if n := len(s.segments); n > 0 && s.segments[n-1].size+size <= s.opts.SegmentBytes {
		last := s.segments[n-1]
		f, err := s.opts.openFile(last.path)
		if err != nil {
			return fmt.Errorf("spool: %w", err)
		}
		last.file, s.cur = f, last
		return nil
	}
	seg := &segment{seq: s.nextSeq, path: segmentPath(s.opts.Dir, s.nextSeq)}
	if _, err := os.Stat(seg.path); err == nil {
		return fmt.Errorf("%w: segment %d already exists", ErrCorrupt, seg.seq)
	}
	f, err := s.opts.openFile(seg.path)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	if err := s.opts.syncDir(s.opts.Dir); err != nil {
		return errors.Join(fmt.Errorf("spool: %w", err), f.Close(), os.Remove(seg.path))
	}
	seg.file = f
	s.segments = append(s.segments, seg)
	s.cur = seg
	return nil
}

// retire syncs and closes the current segment for append; it stays on disk
// for readers. The sync is what lets Open treat a tear in any segment but the
// last as corruption.
func (s *Spool) retire() error {
	err := errors.Join(s.cur.file.Sync(), s.cur.file.Close())
	s.cur = nil
	if err != nil {
		s.breakWith(fmt.Errorf("spool: closing a segment: %w", err))
		return s.broken
	}
	return nil
}

// repair follows a failed write or sync: the segment is closed and cut back
// to its committed length. When that cut fails, the log has a tail nobody can
// vouch for and the spool refuses everything from then on.
func (s *Spool) repair(cause error) error {
	seg := s.cur
	s.cur = nil
	err := errors.Join(seg.file.Close(), os.Truncate(seg.path, seg.size))
	if err == nil && seg.size == 0 {
		err = os.Remove(seg.path)
		s.segments = s.segments[:len(s.segments)-1]
	}
	if err != nil {
		s.breakWith(fmt.Errorf("spool: repair after %w: %w", cause, err))
		return s.broken
	}
	return cause
}

// release deletes every segment before the acknowledged cursor, and the one
// it names when it is acknowledged whole. The current segment counts: a spool
// idle at a full budget could otherwise never take another record.
func (s *Spool) release() error {
	keep := s.segments[:0]
	for _, seg := range s.segments {
		whole := seg.seq < s.ack.Segment || (seg.seq == s.ack.Segment && s.ack.Offset >= seg.size)
		if !whole {
			keep = append(keep, seg)
			continue
		}
		if seg == s.cur {
			if err := s.retire(); err != nil {
				return err
			}
		}
		if err := os.Remove(seg.path); err != nil {
			s.breakWith(fmt.Errorf("spool: releasing a segment: %w", err))
			return s.broken
		}
	}
	for i := len(keep); i < len(s.segments); i++ {
		s.segments[i] = nil
	}
	s.segments = keep
	s.ack = s.settle(s.ack)
	return nil
}

// bytesBetween is the bytes of the log from one canonical cursor to another,
// which for the acknowledged position and a reader's mark is what the next
// acknowledgement releases.
func (s *Spool) bytesBetween(from, to Cursor) int64 {
	var n int64
	for _, seg := range s.segments {
		if seg.seq < from.Segment || seg.seq > to.Segment {
			continue
		}
		low, high := int64(0), seg.size
		if seg.seq == from.Segment {
			low = from.Offset
		}
		if seg.seq == to.Segment {
			high = to.Offset
		}
		if high > low {
			n += high - low
		}
	}
	return n
}

func (s *Spool) find(seq uint64) *segment {
	for _, seg := range s.segments {
		if seg.seq == seq {
			return seg
		}
	}
	return nil
}

func (s *Spool) firstAfter(seq uint64) *segment {
	for _, seg := range s.segments {
		if seg.seq > seq {
			return seg
		}
	}
	return nil
}

// settle maps a position that is trusted to be a record boundary to its one
// canonical name: the end of a released or finished segment is the start of
// the one after it, and the end of the log is the end of the last segment on
// disk, or the first record's position when there is none.
func (s *Spool) settle(c Cursor) Cursor {
	for {
		seg := s.find(c.Segment)
		if seg != nil && (c.Offset < seg.size || seg == s.cur) {
			return c
		}
		next := s.firstAfter(c.Segment)
		switch {
		case next != nil:
			c = Cursor{Segment: next.seq}
		case seg != nil:
			return c
		default:
			return s.end()
		}
	}
}

// end is the position just past the last committed record.
func (s *Spool) end() Cursor {
	if n := len(s.segments); n > 0 {
		last := s.segments[n-1]
		return Cursor{Segment: last.seq, Offset: last.size}
	}
	return Cursor{Segment: s.nextSeq}
}

// resolve is settle for a cursor a caller handed in: one naming no segment on
// disk is refused, unless it is the first record's position of an empty log.
func (s *Spool) resolve(c Cursor) (Cursor, error) {
	seg := s.find(c.Segment)
	switch {
	case seg == nil && len(s.segments) == 0 && c == s.end():
		return c, nil
	case seg == nil || c.Offset < 0 || c.Offset > seg.size:
		return Cursor{}, fmt.Errorf("%w: %+v names no position in the log", ErrCursor, c)
	}
	return s.settle(c), nil
}

// syncDir forces a directory entry to disk, so a segment created before a
// power loss is found after it.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the operator's spool directory, checked at Open
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
