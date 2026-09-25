package spool

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/guardana/control/internal/evidence"
)

// Error is a refusal by the spool, matched with errors.Is. They are constants,
// so no other code in the binary can reassign one and turn a refusal into a
// pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrClosed is an operation on a spool nobody opened, or one already closed.
	ErrClosed Error = "spool: the spool is not open"
	// ErrFull is an append the budget refuses: the bytes on disk, the
	// quarantine's included, plus the reservations leave no room, and nothing
	// can be released, because every segment holds an unacknowledged record and
	// the quarantine is never released at all. It is also an ACTION_STARTED
	// past MaxOpenTrails. A caller blocks the call it was recording.
	ErrFull Error = "spool: the budget or the open trails leave no room"
	// ErrInvalidOptions is an Open whose options cannot make a log.
	ErrInvalidOptions Error = "spool: invalid options"
	// ErrForeignFile is a file under Dir that is neither a segment nor the
	// quarantine. The spool owns its directory and refuses to guess what else
	// is there.
	ErrForeignFile Error = "spool: a file under the directory is not the spool's"
	// ErrCorrupt is a record that does not check where the writer committed it:
	// anywhere in a segment before the last, before a record that checks in the
	// last, or anywhere in the quarantine. It is also a segment that does not
	// follow the one before it. Open refuses it and deletes nothing. A torn
	// tail of the last segment is not this: Open truncates that and counts it.
	ErrCorrupt Error = "spool: the log is corrupt"
	// ErrLocked is an Open of a directory another spool holds.
	ErrLocked Error = "spool: the directory is held by another spool"
	// ErrTrail is an ACTION_STARTED that names no request_id, or one for a
	// trail that already holds a reservation.
	ErrTrail Error = "spool: the record cannot open a trail"
	// ErrCursor is a cursor that names no record boundary the reader may use:
	// before what is acknowledged, past what was delivered, or inside a record.
	ErrCursor Error = "spool: invalid cursor"
)

// FsyncPolicy says when an appended record is forced to disk. The zero value
// is every record, so a policy nobody set is the safe one.
type FsyncPolicy uint8

const (
	// FsyncEveryRecord forces each record to disk before Append returns. Every
	// record the log reported durable is then on disk before the next one, so
	// Open reads a record that checks behind one that does not as damage.
	FsyncEveryRecord FsyncPolicy = iota
	// FsyncInterval forces records to disk on a timer; the records since the
	// last one are what a power loss costs, and the operator chose that. A
	// loss may then keep a later record and not an earlier one, so in the last
	// segment Open ends the log at the first record that does not check and
	// counts the rest as truncated, whatever follows it.
	FsyncInterval
)

// DefaultClosingReserve is the allowance kept per open trail when Options
// names none. A closing record up to this size, header included, is never
// refused on the budget; the bytes of a larger one past it are checked like
// any record's.
const DefaultClosingReserve int64 = 64 << 10

// DefaultMaxOpenTrails is the ceiling on open reservations when Options names
// none.
const DefaultMaxOpenTrails = 1024

// Options configure a spool once, at Open.
type Options struct {
	// Dir holds the segments; it has to exist.
	Dir string
	// MaxBytes bounds the bytes on disk, the quarantine's included, plus the
	// reservations for closing records; it has to be positive. A quarantine
	// append is the one write that can put the bytes past it, by records that
	// already sit in a segment the acknowledgement after it releases.
	MaxBytes int64
	// SegmentBytes is the size at which a segment rolls; it has to be positive
	// and at most MaxBytes. One record larger than it gets a segment of its own.
	SegmentBytes int64
	// Fsync is when a record is forced to disk.
	Fsync FsyncPolicy
	// Interval is the timer of FsyncInterval; it has to be positive under that
	// policy and is refused under the other.
	Interval time.Duration
	// MaxOpenTrails bounds the trails holding a reservation at once, so a plane
	// that never closes a trail cannot lock the budget away without end. Zero
	// means DefaultMaxOpenTrails; it has to be positive or zero.
	MaxOpenTrails int
	// ClosingReserve is the allowance kept per open trail for its closing
	// record, so a closing record no larger than it is never refused on the
	// budget. Zero means DefaultClosingReserve; it has to be at most MaxBytes.
	ClosingReserve int64

	// openFile and syncDir are the seams a test observes the writes and the
	// directory syncs through; nil means the operating system's.
	openFile func(path string) (segmentFile, error)
	syncDir  func(dir string) error
}

// Cursor names a position in the log: a segment by the sequence number of its
// first record, and a byte offset within it.
type Cursor struct {
	Segment uint64
	Offset  int64
}

// Stats is what an operator watches: how much sits on disk unacknowledged,
// how much of the budget is reserved for closing records and for how many
// open trails, what the quarantine holds, what was discarded after a torn
// write, and where the oldest unacknowledged record stands. Bytes counts the
// segments and the quarantine; Unacknowledged counts the segments only.
type Stats struct {
	Segments           int
	Bytes              int64
	Unacknowledged     int64
	Reserved           int64
	OpenTrails         int
	QuarantinedRecords int
	QuarantinedBytes   int64
	Truncated          int64
	Oldest             Cursor
}

// Spool is one segmented log on disk. Open is the only way to make a usable
// one; the zero value refuses every operation.
//
// One mutex orders appenders, readers and the fsync timer. An append holds it
// through its write and, under FsyncEveryRecord, its sync, so readers wait for
// the record to be durable before they can see it.
type Spool struct {
	mu   sync.Mutex
	opts Options
	// open and closed together: neither is the zero value's state.
	open   bool
	closed bool
	// broken latches an I/O failure that left the log in a state a later
	// append could make worse; every operation after it fails with it.
	broken error
	// unlock releases the directory lock Open took.
	unlock func() error

	segments []*segment
	cur      *segment
	nextSeq  uint64
	ack      Cursor

	reserved      map[trail]int64
	reservedTotal int64
	truncated     int64
	quarantine    quarantine
	// quarantinedSinceAck is what the quarantine took since the last
	// acknowledgement, which bounds what a further quarantine may take.
	quarantinedSinceAck int64

	// notify is closed and replaced on every commit, so a waiting reader
	// wakes without polling.
	notify chan struct{}

	stop chan struct{}
	wg   sync.WaitGroup
}

var _ evidence.Sink = (*Spool)(nil)

// Open takes the directory for this process alone and opens or creates the
// log under it, scanning every segment and the quarantine. Only the last
// segment can be torn, since a segment is synced before the next one is
// created: its first record that does not check is where the log ends, and
// what follows is discarded and counted. Anything else that does not check is
// ErrCorrupt, and a refusal changes nothing on disk, because everything is
// read and checked before the torn tails are cut. An I/O failure while they
// are being cut is the one error that answers with the disk already changed.
func Open(opts Options) (*Spool, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	if opts.ClosingReserve == 0 {
		opts.ClosingReserve = DefaultClosingReserve
	}
	if opts.MaxOpenTrails == 0 {
		opts.MaxOpenTrails = DefaultMaxOpenTrails
	}
	if opts.openFile == nil {
		opts.openFile = openOSFile
	}
	if opts.syncDir == nil {
		opts.syncDir = syncDir
	}
	unlock, err := lockDir(opts.Dir)
	if err != nil {
		return nil, err
	}
	s := &Spool{
		opts:     opts,
		open:     true,
		unlock:   unlock,
		reserved: map[trail]int64{},
		notify:   make(chan struct{}),
		stop:     make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, errors.Join(err, unlock())
	}
	if opts.Fsync == FsyncInterval {
		s.wg.Add(1)
		go s.syncOnTimer()
	}
	return s, nil
}

func (o Options) check() error {
	info, err := os.Stat(o.Dir)
	switch {
	case err != nil:
		return fmt.Errorf("%w: dir: %w", ErrInvalidOptions, err)
	case !info.IsDir():
		return fmt.Errorf("%w: %q is not a directory", ErrInvalidOptions, o.Dir)
	}
	if err := o.checkBounds(); err != nil {
		return err
	}
	return o.checkFsync()
}

func (o Options) checkBounds() error {
	switch {
	case o.MaxBytes <= 0:
		return fmt.Errorf("%w: MaxBytes %d is not positive", ErrInvalidOptions, o.MaxBytes)
	case o.SegmentBytes <= 0 || o.SegmentBytes > o.MaxBytes:
		return fmt.Errorf("%w: SegmentBytes %d is not in 1..MaxBytes", ErrInvalidOptions, o.SegmentBytes)
	case o.ClosingReserve < 0 || o.ClosingReserve > o.MaxBytes:
		return fmt.Errorf("%w: ClosingReserve %d is not in 0..MaxBytes", ErrInvalidOptions, o.ClosingReserve)
	case o.MaxOpenTrails < 0:
		return fmt.Errorf("%w: MaxOpenTrails %d is negative", ErrInvalidOptions, o.MaxOpenTrails)
	}
	return nil
}

func (o Options) checkFsync() error {
	switch {
	case o.Fsync == FsyncEveryRecord && o.Interval != 0:
		return fmt.Errorf("%w: Interval is set under FsyncEveryRecord", ErrInvalidOptions)
	case o.Fsync == FsyncInterval && o.Interval <= 0:
		return fmt.Errorf("%w: Interval %v is not positive under FsyncInterval", ErrInvalidOptions, o.Interval)
	case o.Fsync > FsyncInterval:
		return fmt.Errorf("%w: unknown fsync policy %d", ErrInvalidOptions, o.Fsync)
	}
	return nil
}

// breakWith latches cause as the spool's failure and wakes every waiting
// reader, so none sleeps on a log that will take no further record.
func (s *Spool) breakWith(cause error) {
	s.broken = cause
	if !s.closed {
		close(s.notify)
		s.notify = make(chan struct{})
	}
}

func (s *Spool) usable() error {
	switch {
	case !s.open || s.closed:
		return ErrClosed
	case s.broken != nil:
		return s.broken
	}
	return nil
}

// Close releases the log; a later operation answers ErrClosed. Under
// FsyncInterval the records since the last tick are forced to disk first.
func (s *Spool) Close() error {
	s.mu.Lock()
	if !s.open || s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.closed = true
	close(s.notify)
	s.mu.Unlock()

	close(s.stop)
	s.wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.cur != nil {
		err = errors.Join(s.cur.file.Sync(), s.cur.file.Close())
		s.cur = nil
	}
	return errors.Join(err, s.unlock())
}

// Stats reports the log as it stands. A spool nobody opened, or one already
// closed, answers ErrClosed rather than the zero Stats: an empty answer would
// read as a healthy, empty spool in a health check. A spool an I/O failure
// broke answers its numbers and that failure.
func (s *Spool) Stats() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open || s.closed {
		return Stats{}, ErrClosed
	}
	segments := s.segmentBytes()
	st := Stats{
		Segments:           len(s.segments),
		Bytes:              segments + s.quarantine.size,
		Unacknowledged:     segments,
		Reserved:           s.reservedTotal,
		OpenTrails:         len(s.reserved),
		QuarantinedRecords: s.quarantine.records,
		QuarantinedBytes:   s.quarantine.size,
		Truncated:          s.truncated,
		Oldest:             s.ack,
	}
	if seg := s.find(s.ack.Segment); seg != nil {
		st.Unacknowledged -= s.ack.Offset
	}
	return st, s.broken
}

// bytesLocked is what counts against the budget on disk: every segment and
// the quarantine.
func (s *Spool) bytesLocked() int64 {
	return s.segmentBytes() + s.quarantine.size
}

func (s *Spool) segmentBytes() int64 {
	var n int64
	for _, seg := range s.segments {
		n += seg.size
	}
	return n
}

func (s *Spool) syncOnTimer() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.cur != nil && s.broken == nil {
				if err := s.cur.file.Sync(); err != nil {
					// Which of the records since the last tick reached the
					// disk is unknown, and a later append could bury the
					// torn one; refusing everything is the honest answer.
					s.breakWith(fmt.Errorf("spool: sync failed: %w", err))
				}
			}
			s.mu.Unlock()
		}
	}
}
