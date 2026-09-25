package spool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// Reader delivers records in order and acknowledges what was delivered; a
// segment acknowledged whole is released from the budget.
//
// Next and Ack may be called from different goroutines. The acknowledged
// position is the spool's, not the reader's: two readers on one spool share
// it, and each acknowledges only what it delivered itself.
type Reader struct {
	s  *Spool
	mu sync.Mutex
	// pos is the cursor just past the last record delivered.
	pos Cursor
	// mark is the furthest this reader ever delivered to. It never moves back,
	// so a Rewind cannot widen an acknowledgement, and Ack is bounded by it
	// rather than by a position anything else may have moved.
	mark Cursor
	// file is the segment pos names, open for reading, or nil.
	file    *os.File
	fileSeq uint64
}

// Reader returns a reader positioned at the oldest unacknowledged record. from
// is the zero Cursor or that position under any of its names; anything else is
// ErrCursor, because a reader that started past records another delivered
// could acknowledge them without having exported them.
func (s *Spool) Reader(from Cursor) (*Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, err
	}
	if from != (Cursor{}) {
		at, err := s.resolve(from)
		if err != nil {
			return nil, err
		}
		if at != s.ack {
			return nil, fmt.Errorf("%w: %+v is not the acknowledged position %+v", ErrCursor, from, s.ack)
		}
	}
	return &Reader{s: s, pos: s.ack, mark: s.ack}, nil
}

// Rewind moves the reader back to the acknowledged position, so whatever it
// delivered and nobody acknowledged is delivered again. It leaves the mark of
// what was delivered where it is, except to raise it to the acknowledged
// position, which is never behind it in the log.
func (r *Reader) Rewind() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.s == nil {
		return ErrClosed
	}
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	r.pos = s.ack
	if before(r.mark, s.ack) {
		r.mark = s.ack
	}
	return nil
}

// checkCursor returns c's canonical name, or refuses c unless it lies between
// low and high in log order, both canonical, and names a record boundary, as
// far as one record's checksum can tell.
func (s *Spool) checkCursor(c, low, high Cursor) (Cursor, error) {
	cc, err := s.resolve(c)
	if err != nil {
		return Cursor{}, err
	}
	if before(cc, low) || before(high, cc) {
		return Cursor{}, fmt.Errorf("%w: %+v is outside %+v..%+v", ErrCursor, c, low, high)
	}
	seg := s.find(cc.Segment)
	if seg == nil || cc.Offset == seg.size {
		return cc, nil
	}
	f, err := os.Open(seg.path)
	if err != nil {
		return Cursor{}, fmt.Errorf("spool: %w", err)
	}
	defer f.Close() //nolint:errcheck // read only; nothing to lose on close
	if _, _, err := readRecord(f, cc.Offset, seg.size); err != nil {
		return Cursor{}, fmt.Errorf("%w: %+v is not at a record: %w", ErrCursor, c, err)
	}
	return cc, nil
}

func before(a, b Cursor) bool {
	return a.Segment < b.Segment || (a.Segment == b.Segment && a.Offset < b.Offset)
}

// Next returns the next record and the cursor just past it, or blocks until one
// is appended or ctx is done. A record that does not check inside the
// committed bytes, or whose line does not decode, is ErrCorrupt, and the
// reader stays where it is: the exporter stops on it rather than skipping
// evidence.
func (r *Reader) Next(ctx context.Context) (*controlv1.Event, Cursor, error) {
	if r.s == nil {
		return nil, Cursor{}, ErrClosed
	}
	for {
		// The reader's lock is not held while waiting, so an Ack from another
		// goroutine goes through while Next blocks on the next record.
		event, c, wait, err := r.tryNext()
		if err != nil || event != nil {
			return event, c, err
		}
		select {
		case <-ctx.Done():
			return nil, Cursor{}, ctx.Err()
		case <-wait:
		}
	}
}

// tryNext delivers the record at the reader's position when it is committed,
// or returns the channel that closes when the next commit happens.
func (r *Reader) tryNext() (*controlv1.Event, Cursor, <-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seg, end, wait, err := r.locate()
	if err != nil || seg == nil {
		return nil, Cursor{}, wait, err
	}
	event, c, err := r.deliver(seg, end)
	return event, c, nil, err
}

// locate finds the segment holding the record at r.pos and its committed end,
// moving r.pos past the end of a finished segment. It returns no segment and
// a channel to wait on when the next record is not written yet.
func (r *Reader) locate() (*segment, int64, <-chan struct{}, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return nil, 0, nil, err
	}
	r.pos = s.settle(r.pos)
	if seg := s.find(r.pos.Segment); seg != nil && r.pos.Offset < seg.size {
		return seg, seg.size, nil, nil
	}
	return nil, 0, s.notify, nil
}

func (r *Reader) deliver(seg *segment, end int64) (*controlv1.Event, Cursor, error) {
	if r.file != nil && r.fileSeq != seg.seq {
		_ = r.file.Close()
		r.file = nil
	}
	if r.file == nil {
		f, err := os.Open(seg.path)
		if err != nil {
			return nil, Cursor{}, fmt.Errorf("spool: %w", err)
		}
		r.file, r.fileSeq = f, seg.seq
	}
	line, next, err := readRecord(r.file, r.pos.Offset, end)
	if err != nil {
		return nil, Cursor{}, fmt.Errorf("%w: at %+v: %w", ErrCorrupt, r.pos, err)
	}
	events, err := evidence.DecodeJSONL(bytes.NewReader(line), 1)
	if err != nil {
		return nil, Cursor{}, fmt.Errorf("%w: at %+v: %w", ErrCorrupt, r.pos, err)
	}
	if len(events) != 1 {
		return nil, Cursor{}, fmt.Errorf("%w: at %+v: the line holds no event", ErrCorrupt, r.pos)
	}
	r.pos.Offset = next
	if before(r.mark, r.pos) {
		r.mark = r.pos
	}
	return events[0], r.pos, nil
}

// Ack marks everything before c delivered. c has to be a cursor Next handed
// out by this reader, or a position it stood at: acknowledging past what this
// reader delivered would release evidence nobody exported.
func (r *Reader) Ack(c Cursor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.s == nil {
		return ErrClosed
	}
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	r.pos = s.settle(r.pos)
	r.mark = s.settle(r.mark)
	ack, err := s.checkCursor(c, s.ack, r.mark)
	if err != nil {
		return err
	}
	s.ack = ack
	s.quarantinedSinceAck = 0
	return s.release()
}

// Close releases the file the reader holds. The reader stays valid for Next,
// which reopens what it needs.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
