package trailfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// ErrCursorMalformed is a cursor not spelled v1:<first>:<offset>:<line>,
	// each digest 64 lowercase hex digits and the offset a decimal above zero.
	ErrCursorMalformed Error = "trailfile: the cursor is not v1:<sha256>:<offset>:<sha256>"
	// ErrCursorOtherFile is a cursor whose first line is not this file's.
	ErrCursorOtherFile Error = "trailfile: the cursor is from another file"
	// ErrCursorPastEnd is a cursor past the file's last newline.
	ErrCursorPastEnd Error = "trailfile: the cursor is past the file's last newline"
	// ErrCursorOffLine is a cursor whose offset does not follow a newline.
	ErrCursorOffLine Error = "trailfile: the cursor does not follow a newline"
	// ErrCursorChanged is a cursor whose line is not the line ending there.
	ErrCursorChanged Error = "trailfile: the line before the cursor is not the one the cursor names"
)

// chunkBytes is how much a search for a newline reads at a time.
const chunkBytes = 64 << 10

// cursor names the end of one line of one file: the digest of the file's
// first line, the offset right after the line's newline and the digest of the
// line, its newline included.
type cursor struct {
	first  digest
	offset int64
	line   digest
}

func (c cursor) String() string {
	return "v1:" + hex.EncodeToString(c.first[:]) + ":" + strconv.FormatInt(c.offset, 10) + ":" + hex.EncodeToString(c.line[:])
}

func parseCursor(s string) (cursor, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != "v1" {
		return cursor{}, ErrCursorMalformed
	}
	var c cursor
	var ok bool
	if c.first, ok = parseSum(parts[1]); !ok {
		return cursor{}, ErrCursorMalformed
	}
	if c.line, ok = parseSum(parts[3]); !ok {
		return cursor{}, ErrCursorMalformed
	}
	// One spelling per offset: no sign, no leading zero, no zero, since a
	// line ends at least one byte into the file.
	digits := parts[2]
	if digits == "" || digits[0] < '1' || digits[0] > '9' {
		return cursor{}, ErrCursorMalformed
	}
	offset, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return cursor{}, ErrCursorMalformed
	}
	c.offset = offset
	return c, nil
}

// parseSum reads 64 lowercase hex digits; hex.DecodeString alone would also
// take capitals, and one digest would have two spellings.
func parseSum(s string) (digest, bool) {
	var out digest
	if len(s) != 2*sha256.Size || strings.ContainsFunc(s, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) {
		return out, false
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, false
	}
	return out, true
}

// check refuses a cursor that does not name the end of a line this file
// holds before end, its last newline. first is the file's first line's
// digest, absent when the file has no whole line.
func (c cursor) check(r io.ReaderAt, end int64, first digest, hasFirst bool) error {
	switch {
	case !hasFirst || c.first != first:
		return ErrCursorOtherFile
	case c.offset > end:
		return fmt.Errorf("%w: offset %d, last newline ends at %d", ErrCursorPastEnd, c.offset, end)
	}
	var before [1]byte
	if err := readFull(r, before[:], c.offset-1); err != nil {
		return err
	}
	if before[0] != '\n' {
		return fmt.Errorf("%w: offset %d", ErrCursorOffLine, c.offset)
	}
	start, err := lastNewline(r, c.offset-1)
	if err != nil {
		return err
	}
	got, err := sumRange(r, start, c.offset)
	if err != nil {
		return err
	}
	if got != c.line {
		return fmt.Errorf("%w: offset %d", ErrCursorChanged, c.offset)
	}
	return nil
}

// lastNewline is the offset right after the last newline in r's first limit
// bytes, 0 when there is none. It reads backwards a chunk at a time, so what
// it costs is the distance to that newline.
func lastNewline(r io.ReaderAt, limit int64) (int64, error) {
	buf := make([]byte, min(limit, chunkBytes))
	for at := limit; at > 0; {
		n := min(at, int64(len(buf)))
		chunk := buf[:n]
		if err := readFull(r, chunk, at-n); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			return at - n + int64(i) + 1, nil
		}
		at -= n
	}
	return 0, nil
}

// firstLineDigest is the digest of the file's first line, its newline included,
// when a newline ends one before end.
func firstLineDigest(r io.ReaderAt, end int64) (digest, bool, error) {
	if end == 0 {
		return digest{}, false, nil
	}
	buf := make([]byte, min(end, chunkBytes))
	for at := int64(0); at < end; {
		chunk := buf[:min(end-at, int64(len(buf)))]
		if err := readFull(r, chunk, at); err != nil {
			return digest{}, false, err
		}
		if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
			s, err := sumRange(r, 0, at+int64(i)+1)
			return s, err == nil, err
		}
		at += int64(len(chunk))
	}
	return digest{}, false, fmt.Errorf("%w: no newline before offset %d", ErrShortRead, end)
}

func sumRange(r io.ReaderAt, from, to int64) (digest, error) {
	h := sha256.New()
	if n, err := io.Copy(h, io.NewSectionReader(r, from, to-from)); err != nil || n != to-from {
		return digest{}, shortRead(n, to-from, from, err)
	}
	var out digest
	h.Sum(out[:0])
	return out, nil
}

// readFull reads len(p) bytes at off. A short read is the file cut under the
// export, never the end of what it holds.
func readFull(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	return shortRead(int64(n), int64(len(p)), off, err)
}

func shortRead(n, want, off int64, err error) error {
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("%w: read %d of %d bytes at offset %d: %w", ErrShortRead, n, want, off, err)
}
