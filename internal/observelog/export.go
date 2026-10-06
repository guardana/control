package observelog

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/lineexport"
	"github.com/guardana/control/internal/observe"
)

const (
	// ExportFormat names the format Export writes.
	ExportFormat = brand.OTelNamespace + ".observation-export"
	// ExportVersion is the format's version, read by the MAJOR.MINOR rules
	// of the wire contracts.
	ExportVersion = "0.1"

	// DefaultExportLimit is the records an export writes when not told.
	DefaultExportLimit = 1000
	// MaxExportLimit is the most records one export writes.
	MaxExportLimit = 100_000
)

// Record types and gap reasons, as the export spells them.
const (
	typeObservation  = "observation"
	typeImportReport = "import_report"
	gapMalformed     = "malformed"
	gapVersion       = "unsupported_version"
	gapConflict      = "conflicting_observation_id"
	gapCR            = "carriage_return"
)

// Query is where an export starts and how much it writes.
type Query struct {
	// After is a cursor an earlier export returned; the export starts after
	// the line it names. Empty starts at the file's first byte.
	After string
	// Limit bounds the records written, every type counted.
	Limit int
	// MaxBytes bounds the bytes of whole lines read; 0 is no bound.
	MaxBytes int64
}

// queryRecord is the query as the export's header repeats it.
type queryRecord struct {
	After    string `json:"after,omitempty"`
	Limit    int    `json:"limit"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
}

// Export writes the whole lines of src after q.After as the observation
// export, JSON Lines framed by a header and a trailer, and returns what the
// trailer said.
//
// Each line is one record: an observation or an import report, carrying the
// line's bytes as they stand but for its newline; a gap, for a line that is
// not one record of a version this build reads in the form the codec writes
// it, holds a carriage return anywhere, the one before its newline included,
// or carries an observation id a line this export read carries with other
// content; or a duplicate, for a line repeating a record this export wrote.
// An observation repeats another when its id and content digest do, an
// import report only when every byte does. The bytes after the last newline
// are a gap only when src.Held says no writer holds the file.
//
// A refused query or cursor writes nothing. An error after the header leaves
// the output without its trailer, which is how a reader knows it was cut.
func Export(src lineexport.Source, q Query, w io.Writer) (lineexport.Trailer, error) {
	switch {
	case q.Limit < 1 || q.Limit > MaxExportLimit:
		return lineexport.Trailer{}, fmt.Errorf("%w: limit %d, want 1 to %d", ErrQuery, q.Limit, MaxExportLimit)
	case q.MaxBytes < 0:
		return lineexport.Trailer{}, fmt.Errorf("%w: byte bound %d", ErrQuery, q.MaxBytes)
	}
	return lineexport.Export(exportFormat(), src,
		lineexport.Query{After: q.After, Limit: q.Limit, MaxBytes: q.MaxBytes,
			Echo: queryRecord(q)}, judge, w)
}

// exportFormat is the observation export as the line export writes it. A
// duplicate's id member is "id", since it names an observation id or an
// import report's line digest.
func exportFormat() lineexport.Format {
	return lineexport.Format{Name: ExportFormat, Version: ExportVersion, Lines: []string{typeObservation, typeImportReport},
		IDMember: "id", Conflict: gapConflict, MaxLineBytes: observe.MaxLineBytes, Refusals: lineexport.Refusals{
			CursorMalformed: ErrCursorMalformed, CursorOtherFile: ErrCursorOtherFile, CursorPastEnd: ErrCursorPastEnd,
			CursorOffLine: ErrCursorOffLine, CursorChanged: ErrCursorChanged, ShortRead: ErrShortRead, ByteBound: ErrByteBound,
		}}
}

// ExportFile exports the file at path as Export does, reading the length it
// has when opened and holding no lock, so it reads a log a writer holds. The
// file must pass the judgement ReadFile makes, a link at path refused as
// ErrNotRegular, without waiting on a named pipe.
func ExportFile(path string, q Query, w io.Writer) (lineexport.Trailer, error) {
	f, info, err := openJudged(path)
	if err != nil {
		return lineexport.Trailer{}, err
	}
	defer func() { _ = f.Close() }()
	return Export(lineexport.Source{Name: path, R: f, Size: info.Size(), Held: func() (bool, error) { return heldByWriter(f) }}, q, w)
}

// judge says what record a line is. An observation's id is its observation
// id and its content the digest observe.ContentDigest defines, so a line read
// again at a later receive time is a duplicate; an import report's id and
// content are its line's digest.
func judge(l lineexport.Line) lineexport.Verdict {
	r, err := observe.UnmarshalLine(l.Bytes)
	switch {
	case err != nil && (errors.Is(err, observe.ErrVersion) || unreadVersion(l.Body())):
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapVersion}
	case err != nil:
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	case bytes.IndexByte(l.Bytes, '\r') >= 0:
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapCR}
	case observe.CheckLineForm(l.Bytes) != nil:
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	}
	o := r.GetObservation()
	if o == nil {
		return lineexport.Verdict{Type: typeImportReport, ID: hex.EncodeToString(l.Sum[:]), Content: l.Sum}
	}
	var content lineexport.Digest
	if n, err := hex.Decode(content[:], []byte(observe.ContentDigest(o))); err != nil || n != len(content) {
		return lineexport.Verdict{Type: lineexport.Gap, Reason: gapMalformed}
	}
	return lineexport.Verdict{Type: typeObservation, ID: o.GetObservationId(), Content: content}
}

// unreadVersion reports a line the strict codec refused that names, as its
// one record's schema_version, a version outside the table. A later minor may
// add a member this build cannot name, and its line is then a version this
// build does not read rather than a malformed one.
func unreadVersion(body []byte) bool {
	var outer map[string]map[string]json.RawMessage
	if json.Unmarshal(body, &outer) != nil || len(outer) != 1 {
		return false
	}
	for kind, inner := range outer {
		if kind != "observation" && kind != "importReport" && kind != "import_report" {
			return false
		}
		for _, name := range []string{"schemaVersion", "schema_version"} {
			var v string
			if raw, ok := inner[name]; ok && json.Unmarshal(raw, &v) == nil {
				return observe.CheckVersion(v) != nil
			}
		}
	}
	return false
}
