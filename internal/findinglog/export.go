package findinglog

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/lineexport"
)

const (
	// ExportFormat names the format ExportFile writes.
	ExportFormat = brand.OTelNamespace + ".findings-export"
	// ExportVersion is the format's version, unstable as the records are.
	ExportVersion = "0.1"

	// DefaultExportLimit is the records an export writes when not told.
	DefaultExportLimit = 1000
	// MaxExportLimit is the most records one export writes.
	MaxExportLimit = 100_000
)

// Record types, gap reasons and identities, as the export spells them.
const (
	typeFinding     = "finding_record"
	typeReport      = "supervise_report"
	gapMalformed    = "malformed"
	gapConflict     = "conflicting_finding"
	identityLogID   = "log_id"
	identityContent = "content"
	identityNone    = "none"
)

// The export's refusals. Each is returned before a byte is written.
const (
	// ErrExportQuery is a limit or a byte bound out of range.
	ErrExportQuery Error = "findinglog: the export query is out of range"
	// ErrCursorMalformed is a cursor not spelled as an export spells one.
	ErrCursorMalformed Error = "findinglog: not a cursor an export writes"
	// ErrCursorOtherLog is a cursor of another log: another first line, so
	// for a log with a header another log id.
	ErrCursorOtherLog Error = "findinglog: the cursor is of another log"
	// ErrCursorPastEnd is a cursor past the end of the last committed write.
	ErrCursorPastEnd Error = "findinglog: the cursor is past the last committed write"
	// ErrCursorOffLine is a cursor whose offset does not follow a newline.
	ErrCursorOffLine Error = "findinglog: the cursor is not at the end of a line"
	// ErrCursorChanged is a cursor whose line is not the line ending there.
	ErrCursorChanged Error = "findinglog: the cursor's line is not the line ending there"
	// ErrExportShortRead is a log that no longer holds what it held when the
	// export opened it.
	ErrExportShortRead Error = "findinglog: the log no longer holds what it held when the export opened it"
	// ErrByteBound is a next line longer than the byte bound.
	ErrByteBound Error = "findinglog: the next line is longer than the byte bound"
)

// ExportQuery is where an export starts and how much it writes.
type ExportQuery struct {
	// After is a cursor an earlier export of this log returned; empty starts
	// at the log's first line.
	After string
	// Limit bounds the records written, every type counted.
	Limit int
	// MaxBytes bounds the bytes of whole lines the export reads; 0 is no
	// bound. The judgement of the log before it reads every line.
	MaxBytes int64
}

// queryRecord is the query as the export's header repeats it.
type queryRecord struct {
	After    string `json:"after,omitempty"`
	Limit    int    `json:"limit"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// ExportFile writes the findings log at path as the findings export, JSON
// Lines framed by a header and a trailer, and returns what the trailer said.
//
// The file is opened read only and judged as ReadFile judges it, under no
// lock, and a log ReadFile refuses is refused. The export reads to the end of
// the last write a report committed: the lines after it are a write still
// open, never a record, and with the bytes after the last newline they are
// the trailer's tail, a gap only when no writer holds the log. Each finding
// and each report is a record carrying its line as the log holds it; the
// log's header is none. A cursor names the log by its first line, which in a
// log with a header holds its log id, and the trailer's identity says which:
// "log_id", "content" for a log with no header, or "none" before a line is
// committed.
//
// A refused query, log or cursor writes nothing. An error after the header
// leaves the output without its trailer.
func ExportFile(path string, q ExportQuery, w io.Writer) (lineexport.Trailer, error) {
	switch {
	case q.Limit < 1 || q.Limit > MaxExportLimit:
		return lineexport.Trailer{}, fmt.Errorf("%w: limit %d, want 1 to %d", ErrExportQuery, q.Limit, MaxExportLimit)
	case q.MaxBytes < 0:
		return lineexport.Trailer{}, fmt.Errorf("%w: byte bound %d", ErrExportQuery, q.MaxBytes)
	}
	f, size, err := openToRead(path)
	if err != nil {
		return lineexport.Trailer{}, err
	}
	defer func() { _ = f.Close() }()
	committed, identity, err := judgeCommitted(f, size)
	if err != nil {
		return lineexport.Trailer{}, err
	}
	src := lineexport.Source{Name: path, R: f, Size: size, Identity: identity,
		Committed: func() (int64, error) { return committed, nil },
		Held:      func() (bool, error) { return heldByWriter(f) }}
	return lineexport.Export(exportFormat(), src,
		lineexport.Query{After: q.After, Limit: q.Limit, MaxBytes: q.MaxBytes, Echo: queryRecord(q)}, judgeLine, w)
}

func exportFormat() lineexport.Format {
	return lineexport.Format{Name: ExportFormat, Version: ExportVersion, Lines: []string{typeFinding, typeReport},
		IDMember: "finding_key", Conflict: gapConflict, MaxLineBytes: MaxLineBytes, Refusals: lineexport.Refusals{
			CursorMalformed: ErrCursorMalformed, CursorOtherFile: ErrCursorOtherLog, CursorPastEnd: ErrCursorPastEnd,
			CursorOffLine: ErrCursorOffLine, CursorChanged: ErrCursorChanged, ShortRead: ErrExportShortRead, ByteBound: ErrByteBound,
		}}
}

// openToRead opens the log file at path read only, as ReadFile does, and
// returns it with its length when opened.
func openToRead(path string) (*os.File, int64, error) {
	if !files.PermissionBits {
		return nil, 0, ErrNoPermissionBits
	}
	named, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, 0, err
	case !named.Mode().IsRegular():
		return nil, 0, fmt.Errorf("%w: %s", ErrNotRegular, strconv.Quote(path))
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0) //nolint:gosec // G304: the path is the operator's to name, and the descriptor is judged before a byte is read
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err == nil {
		err = judgeFile(info)
	}
	if err == nil && info.Size() > MaxLogBytes {
		err = fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, info.Size(), MaxLogBytes)
	}
	if err != nil {
		return nil, 0, errors.Join(err, f.Close())
	}
	return f, info.Size(), nil
}

// judgeCommitted judges the first size bytes of r as Open does and returns
// where the last committed write ends, and what the first committed line
// says the log's identity rests on.
func judgeCommitted(r io.ReaderAt, size int64) (int64, string, error) {
	end, err := lastLineEnd(r, size)
	if err != nil {
		return 0, "", err
	}
	_, committed, err := scanLog(io.NewSectionReader(r, 0, end), nil)
	if err != nil || committed == 0 {
		return committed, identityNone, err
	}
	first, err := bufio.NewReaderSize(io.NewSectionReader(r, 0, committed), MaxLineBytes+1).ReadSlice('\n')
	if err != nil {
		return 0, "", fmt.Errorf("%w: %w", ErrExportShortRead, err)
	}
	rec, err := unmarshalLine(first)
	switch {
	case err != nil:
		return 0, "", fmt.Errorf("%w: line 1 no longer reads as it did", ErrExportShortRead)
	case rec.GetLogHeader() != nil:
		return committed, identityLogID, nil
	}
	return committed, identityContent, nil
}

// judgeLine says what record one committed line is. The log was judged
// whole before the export, so a gap here is a line changed since.
func judgeLine(l lineexport.Line) lineexport.Verdict {
	r, err := unmarshalLine(l.Bytes)
	switch {
	case err != nil:
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	case r.GetLogHeader() != nil && l.Offset == 0:
		return lineexport.Verdict{Pass: true}
	case r.GetLogHeader() != nil:
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	case r.GetSuperviseReport() != nil:
		return lineexport.Verdict{Type: typeReport}
	}
	f := r.GetFindingRecord()
	sum, err := digest(f)
	var content lineexport.Digest
	if err == nil {
		_, err = hex.Decode(content[:], []byte(sum))
	}
	if err != nil {
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	}
	k := keyOf(f)
	return lineexport.Verdict{Type: typeFinding, ID: k.id + ":" + k.verdict.String(), Content: content}
}
