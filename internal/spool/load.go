package spool

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// load reads the directory in two passes. The first only reads: it scans every
// segment and the quarantine and refuses what is corrupt. The second cuts the
// torn tail of the last segment and of the quarantine, the only changes Open
// makes on disk.
func (s *Spool) load() error {
	if err := s.listSegments(); err != nil {
		return err
	}
	var lastSize int64
	for i, seg := range s.segments {
		size, err := s.scanSegment(seg, i == len(s.segments)-1)
		if err != nil {
			return err
		}
		lastSize = size
	}
	if err := s.checkSequence(); err != nil {
		return err
	}
	quarantined, err := s.loadQuarantine()
	if err != nil {
		return err
	}
	if n := len(s.segments); n > 0 {
		if err := s.cutTail(s.segments[n-1], lastSize); err != nil {
			return err
		}
	}
	if err := s.cutQuarantineTail(quarantined); err != nil {
		return err
	}
	// Nothing on disk is acknowledged: a segment acknowledged whole is deleted.
	s.ack = s.end()
	if len(s.segments) > 0 {
		s.ack = Cursor{Segment: s.segments[0].seq}
	}
	return nil
}

// listSegments names every segment under Dir in log order: ReadDir sorts by
// name and the names are zero-padded. The quarantine and the lock file are
// the spool's too; anything else there is refused.
func (s *Spool) listSegments() error {
	entries, err := os.ReadDir(s.opts.Dir)
	if err != nil {
		return fmt.Errorf("spool: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if (name == quarantineName || (lockFile != "" && name == lockFile)) && entry.Type().IsRegular() {
			continue
		}
		m := segmentName.FindStringSubmatch(name)
		if m == nil || !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %q", ErrForeignFile, name)
		}
		seq, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return fmt.Errorf("%w: %q", ErrForeignFile, name)
		}
		s.segments = append(s.segments, &segment{seq: seq, path: filepath.Join(s.opts.Dir, name)})
	}
	return nil
}

// scanSegment counts seg's records that check and returns the file's size.
// A segment before the last was synced whole before the next one existed, so
// a byte in it that does not check is corruption; one with no record at all
// leaves the next segment's name out of sequence, which checkSequence
// refuses. In the last segment a record that does not check ends the log,
// and under FsyncEveryRecord, where every record before it was forced to
// disk, a record that checks after it is corruption instead.
func (s *Spool) scanSegment(seg *segment, last bool) (int64, error) {
	f, err := os.Open(seg.path)
	if err != nil {
		return 0, fmt.Errorf("spool: %w", err)
	}
	defer f.Close() //nolint:errcheck // read only; nothing to lose on close
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("spool: %w", err)
	}
	found, err := scan(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return 0, fmt.Errorf("spool: %w", err)
	}
	seg.records, seg.size = found.records, found.good
	torn := found.good < info.Size()
	switch {
	case !last && torn:
		return 0, fmt.Errorf("%w: segment %d does not check at byte %d of %d", ErrCorrupt, seg.seq, found.good, info.Size())
	case torn && s.opts.Fsync == FsyncEveryRecord:
		if err := s.checkNothingFollows(f, found.good, info.Size()); err != nil {
			return 0, fmt.Errorf("%w: segment %d: %w", ErrCorrupt, seg.seq, err)
		}
	}
	return info.Size(), nil
}

// checkNothingFollows refuses a tail that holds a record which checks, and one
// that costs more to read than a tail may: under FsyncEveryRecord every record
// the log reported durable was forced to disk before the next one, so bytes
// behind one that does not check are damage, not an interrupted append.
func (s *Spool) checkNothingFollows(ra io.ReaderAt, from, size int64) error {
	after, err := recordAfter(ra, from, size)
	switch {
	case errors.Is(err, errTailCost):
		return err
	case err != nil:
		return fmt.Errorf("reading the tail past %d: %w", from, err)
	case after:
		return fmt.Errorf("a record follows byte %d, which does not check", from)
	}
	return nil
}

// checkSequence refuses a segment numbered 0, one that does not follow the one
// before it, and one whose records would carry the sequence past its range.
func (s *Spool) checkSequence() error {
	s.nextSeq = 1
	for i, seg := range s.segments {
		records := uint64(seg.records) //nolint:gosec // G115: a count of records is never negative
		switch {
		case seg.seq == 0:
			return fmt.Errorf("%w: segment 0 names no record", ErrCorrupt)
		case i > 0 && seg.seq != s.nextSeq:
			return fmt.Errorf("%w: segment %d does not follow segment %d", ErrCorrupt, seg.seq, s.segments[i-1].seq)
		case seg.seq > math.MaxUint64-records:
			return fmt.Errorf("%w: segment %d holds %d records, past the sequence's range", ErrCorrupt, seg.seq, records)
		}
		s.nextSeq = seg.seq + records
	}
	return nil
}

// cutTail truncates the last segment to its records that check, counting what
// it discards, and removes it when none does: the next append would otherwise
// name a new segment after the same sequence number.
func (s *Spool) cutTail(last *segment, size int64) error {
	switch {
	case last.records == 0:
		if err := os.Remove(last.path); err != nil {
			return fmt.Errorf("spool: %w", err)
		}
		s.segments = s.segments[:len(s.segments)-1]
	case last.size < size:
		if err := os.Truncate(last.path, last.size); err != nil {
			return fmt.Errorf("spool: %w", err)
		}
	default:
		return nil
	}
	s.truncated += size - last.size
	return nil
}
