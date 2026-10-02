package spool

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// quarantineName is the file that keeps the records an exporter could not
// deliver for good. It is framed as a segment is, and the plane appends to it
// and never deletes or rewrites it.
const quarantineName = "quarantine.log"

// quarantineTailRecords is how many of the last quarantined records the spool
// remembers by event identifier, so a caller can tell a record the quarantine
// already holds from one it does not without reading the file.
const quarantineTailRecords = 256

// quarantine is what the quarantine file holds: the records that check, the
// offset just past the last of them, and the identifiers of its last records.
// info is the file the listing or the create found, nil while there is none,
// and synced whether its directory entry is known to be on disk.
type quarantine struct {
	records int
	size    int64
	tail    []string
	held    map[string]int
	info    fs.FileInfo
	synced  bool
}

// quarantineWhat names the quarantine in a refusal.
const quarantineWhat = "the quarantine"

func (q *quarantine) remember(id string) {
	if q.held == nil {
		q.held = map[string]int{}
	}
	q.tail = append(q.tail, id)
	q.held[id]++
	if len(q.tail) > quarantineTailRecords {
		gone := q.tail[0]
		q.tail = append(q.tail[:0], q.tail[1:]...)
		if q.held[gone]--; q.held[gone] <= 0 {
			delete(q.held, gone)
		}
	}
}

func (q *quarantine) holds(id string) bool { return q.held[id] > 0 }

// loadQuarantine scans the quarantine when there is one and returns the
// file's size. The file is appended to as a segment is, so a record that does
// not check at its end is the torn tail of an append that never returned, and
// the caller cuts it; a record that does not check with one that checks after
// it is corruption of records the spool reported durable.
func (s *Spool) loadQuarantine() (int64, error) {
	known := s.quarantine.info
	if known == nil {
		return 0, nil
	}
	f, info, err := s.openForRead(quarantineName, quarantineWhat, known)
	if err != nil {
		return 0, err
	}
	defer f.Close() //nolint:errcheck // read only; nothing to lose on close
	found, err := scan(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return 0, fmt.Errorf("spool: %w", err)
	}
	if found.good < info.Size() {
		// Every quarantine append is synced, whatever the fsync policy, so a
		// record behind one that does not check is damage.
		if err := s.checkNothingFollows(f, found.good, info.Size()); err != nil {
			return 0, fmt.Errorf("%w: the quarantine: %w", ErrCorrupt, err)
		}
	}
	s.quarantine = quarantine{records: found.records, size: found.good, info: known, synced: s.quarantine.synced}
	if err := s.loadQuarantineTail(f, found); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// loadQuarantineTail reads the identifiers of the last records the quarantine
// holds. It walks the whole file to find where they start, and decodes only
// those, so what it keeps does not grow with the file.
func (s *Spool) loadQuarantineTail(ra io.ReaderAt, found scanned) error {
	offsets := make([]int64, 0, quarantineTailRecords)
	at := int64(0)
	for at < found.good {
		_, next, err := readRecord(ra, at, found.good)
		if err != nil {
			return fmt.Errorf("%w: the quarantine at %d: %w", ErrCorrupt, at, err)
		}
		if len(offsets) == quarantineTailRecords {
			offsets = append(offsets[:0], offsets[1:]...)
		}
		offsets = append(offsets, at)
		at = next
	}
	for _, off := range offsets {
		line, _, err := readRecord(ra, off, found.good)
		if err != nil {
			return fmt.Errorf("%w: the quarantine at %d: %w", ErrCorrupt, off, err)
		}
		events, err := evidence.DecodeJSONL(bytes.NewReader(line), 1)
		if err != nil || len(events) != 1 {
			return fmt.Errorf("%w: the quarantine at %d holds no event: %w", ErrCorrupt, off, err)
		}
		s.quarantine.remember(events[0].GetEventId())
	}
	return nil
}

// cutQuarantineTail truncates the quarantine to the records that check,
// counting what it discards. The file itself stays: the plane never removes
// it.
func (s *Spool) cutQuarantineTail(size int64) error {
	if size == s.quarantine.size {
		return nil
	}
	if err := s.cutBack(quarantineName, quarantineWhat, s.quarantine.info, s.quarantine.size); err != nil {
		return err
	}
	s.truncated += size - s.quarantine.size
	return nil
}

// Quarantine appends to the spool's quarantine the events whose identifiers it
// does not already hold, and returns how many it wrote once they are durable.
// It is how a record a collector refuses for good leaves the export without
// leaving the evidence: a caller acknowledges past the records only after it
// returns.
//
// The bytes count against the budget from then on and nothing releases them,
// so the append is refused with ErrFull unless the records the caller may
// acknowledge next cover it: what the quarantine takes between two
// acknowledgements is at most what the acknowledgement then releases. A record
// the quarantine's tail already holds is not written twice, so a caller
// stopped between quarantining and acknowledging may repeat both.
func (r *Reader) Quarantine(events []*controlv1.Event) (int, error) {
	if r.s == nil {
		return 0, ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return 0, err
	}
	var records bytes.Buffer
	var line bytes.Buffer
	var write []*controlv1.Event
	for i, event := range events {
		if event != nil && (s.quarantine.holds(event.GetEventId()) || named(write, event.GetEventId())) {
			continue
		}
		line.Reset()
		if err := evidence.EncodeJSONL(&line, []*controlv1.Event{event}); err != nil {
			return 0, fmt.Errorf("spool: quarantining event %d: %w", i, err)
		}
		records.Write(frame(line.Bytes()))
		write = append(write, event)
	}
	if len(write) == 0 {
		return 0, nil
	}
	r.mark = s.settle(r.mark)
	covered := s.bytesBetween(s.ack, r.mark)
	if taken := s.quarantinedSinceAck + int64(records.Len()); taken > covered {
		return 0, fmt.Errorf("%w: quarantining %d bytes with %d bytes to release", ErrFull, taken, covered)
	}
	if err := s.appendQuarantine(records.Bytes(), write); err != nil {
		return 0, err
	}
	return len(write), nil
}

// named reports whether events already hold the event identifier id.
func named(events []*controlv1.Event, id string) bool {
	for _, event := range events {
		if event.GetEventId() == id {
			return true
		}
	}
	return false
}

// appendQuarantine writes framed records at the quarantine's end and syncs
// them, and the directory until its entry for the file is known to be on disk. On a failure the file is
// cut back to what it held through the same descriptor, and when that fails
// the spool is broken: a later append could land behind a record nobody can
// vouch for. A close that fails after the sync breaks it too, since the
// records are then on disk and no longer counted.
func (s *Spool) appendQuarantine(records []byte, written []*controlv1.Event) error {
	f, info, err := s.openForAppend(quarantineName, quarantineWhat, s.quarantine.info)
	if err != nil {
		return err
	}
	s.quarantine.info = info
	_, err = f.Write(records)
	if err == nil {
		err = f.Sync()
	}
	if err == nil && !s.quarantine.synced {
		err = s.opts.syncDir(s.root)
		s.quarantine.synced = err == nil
	}
	if err != nil {
		if cut := errors.Join(f.Truncate(s.quarantine.size), f.Close()); cut != nil {
			s.breakWith(fmt.Errorf("spool: quarantine: repair after %w: %w", err, cut))
			return s.broken
		}
		return fmt.Errorf("spool: quarantine: %w", err)
	}
	if err := f.Close(); err != nil {
		s.breakWith(fmt.Errorf("spool: quarantine: closing: %w", err))
		return s.broken
	}
	for _, event := range written {
		s.quarantine.records++
		s.quarantine.remember(event.GetEventId())
	}
	s.quarantine.size += int64(len(records))
	s.quarantinedSinceAck += int64(len(records))
	return nil
}
