package spool

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/guardana/control/internal/evidence"
)

// A record on disk is a header and one JSONL line, the line exactly as
// internal/evidence writes it, newline included:
//
//	length   uint32, big-endian, the byte count of the line
//	checksum uint32, big-endian, CRC32C (Castagnoli) over the line
//	line     length bytes
//
// The header carries no checksum of its own: a length that is zero, past the
// longest line the codec writes, or past the end of the file fails the length
// check, and a length that lies within bounds points at bytes whose checksum
// does not match. Either way the record does not check and the log ends there.
const headerBytes = 8

// maxLineBytes bounds the length field: the longest line the codec writes
// plus its newline. A larger length is not a record, whatever follows it.
const maxLineBytes = evidence.MaxLineBytes + 1

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// frame returns line framed as one record.
func frame(line []byte) []byte {
	out := make([]byte, headerBytes+len(line))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(line))) //nolint:gosec // G115: the codec bounds a line at maxLineBytes, far below the field
	binary.BigEndian.PutUint32(out[4:8], crc32.Checksum(line, castagnoli))
	copy(out[headerBytes:], line)
	return out
}

// scanned is what a scan found: the records that check, in order, and the
// offset just past the last of them. Everything from good onward is not part
// of the log.
type scanned struct {
	records int
	good    int64
}

// scan reads records from r until one does not check or the input ends. A
// record that does not check is not an error: it is where the log ends, and
// the caller truncates there. An error is a read failure other than the end
// of the input.
//
// It reads through a bounded buffer sized to the longest record, so the memory
// it uses does not depend on what the file claims a record's length is.
func scan(r io.Reader) (scanned, error) {
	var s scanned
	header := make([]byte, headerBytes)
	line := make([]byte, 0, 4096)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return s, endOfLog(err)
		}
		length := binary.BigEndian.Uint32(header[0:4])
		if length == 0 || length > maxLineBytes {
			return s, nil
		}
		line = grow(line, int(length))
		if _, err := io.ReadFull(r, line); err != nil {
			return s, endOfLog(err)
		}
		if crc32.Checksum(line, castagnoli) != binary.BigEndian.Uint32(header[4:8]) {
			return s, nil
		}
		s.records++
		s.good += headerBytes + int64(length)
	}
}

// endOfLog reads a short read as the log's end and anything else as a failure.
// io.ReadFull reports the input ending inside a record as ErrUnexpectedEOF and
// before one as EOF; both are a torn tail, which is not an error.
func endOfLog(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

// readRecord reads the record at off from ra and returns its line and the
// offset just past it, where end is the offset up to which the bytes are known
// to be complete. A record that does not check inside that range is corrupt,
// not torn, because the writer committed it.
func readRecord(ra io.ReaderAt, off, end int64) ([]byte, int64, error) {
	if off+headerBytes > end {
		return nil, off, ErrCorrupt
	}
	var header [headerBytes]byte
	if _, err := ra.ReadAt(header[:], off); err != nil {
		return nil, off, err
	}
	length := binary.BigEndian.Uint32(header[0:4])
	if length == 0 || length > maxLineBytes || off+headerBytes+int64(length) > end {
		return nil, off, ErrCorrupt
	}
	line := make([]byte, length)
	if _, err := ra.ReadAt(line, off+headerBytes); err != nil {
		return nil, off, err
	}
	if crc32.Checksum(line, castagnoli) != binary.BigEndian.Uint32(header[4:8]) {
		return nil, off, ErrCorrupt
	}
	return line, off + headerBytes + int64(length), nil
}

// errTailCost is a tail whose search for a committed record would cost more
// than maxTailWork. What such a tail holds is unknown, and an unknown tail is
// not a tear: the caller answers ErrCorrupt.
const errTailCost Error = "spool: the tail costs more to read than a tail may"

// maxTailWork bounds the bytes recordAfter checksums. A tail that needs more
// than this is crafted, not written by an interrupted append.
const maxTailWork = 8 << 20

// recordAfter reports whether a record checks at any offset past from. It is
// how Open tells a torn tail, where nothing follows the record that does not
// check, from corruption with committed records behind it.
//
// The search is complete and bounded: the writer appends one record at a time,
// so the bytes an interrupted append left past from are one record's at most,
// and a committed record behind them begins at from+headerBytes+maxLineBytes
// or nearer. It reads through a window twice the longest record, so one fill
// covers every offset it visits and every record that can begin at one, and it
// answers errTailCost rather than checksum more than maxTailWork bytes.
func recordAfter(ra io.ReaderAt, from, size int64) (bool, error) {
	// The furthest a committed record can begin is one record past from, and
	// the loop visits that offset, so the bound carries a second header.
	last := min(size, from+2*headerBytes+maxLineBytes)
	// The window holds two records, so one fill covers every offset the search
	// visits and every record that can start at one.
	window := make([]byte, min(2*(headerBytes+maxLineBytes), size-from))
	base, filled := int64(0), 0
	fill := func(at int64) error {
		n := min(int64(len(window)), size-at)
		read, err := ra.ReadAt(window[:n], at)
		if int64(read) < n {
			return fmt.Errorf("reading at %d: %w", at, errors.Join(err, io.ErrUnexpectedEOF))
		}
		base, filled = at, read
		return nil
	}
	var work int64
	for at := from + 1; at+headerBytes <= last; at++ {
		if at+headerBytes > base+int64(filled) {
			if err := fill(at); err != nil {
				return false, err
			}
		}
		length := int64(binary.BigEndian.Uint32(window[at-base:]))
		end := at + headerBytes + length
		if length == 0 || length > maxLineBytes || end > size {
			continue
		}
		if work += length; work > maxTailWork {
			return false, fmt.Errorf("%w: %d bytes past %d", errTailCost, work, from)
		}
		if end > base+int64(filled) {
			if err := fill(at); err != nil {
				return false, err
			}
		}
		line := window[at-base+headerBytes : end-base]
		if crc32.Checksum(line, castagnoli) == binary.BigEndian.Uint32(window[at-base+4:]) {
			return true, nil
		}
	}
	return false, nil
}

func grow(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}
