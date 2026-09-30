package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// maxRecordBytes bounds one record: the exporter's longest event line and
// the few hundred bytes that frame it, with room to spare.
const maxRecordBytes = 4 << 20

// exportMajor is the major of the export format this reader reads.
const exportMajor = 1

// contractPackage is the wire contract's Protobuf package, whose last
// segment is its major, v1.
var contractPackage = string((&controlv1.Event{}).ProtoReflect().Descriptor().ParentFile().Package())

// exportFormat is the format this reader reads: the contract names it in the
// namespace of the wire contract's package, without its major segment, and
// TestDemoExportFormat holds it to what the exporter wrote.
var exportFormat = contractPackage[:strings.LastIndex(contractPackage, ".")] + ".evidence-export"

// eventMajor is the event schema major the generated package was built from.
var eventMajor = strings.TrimPrefix(contractPackage[strings.LastIndex(contractPackage, ".")+1:], "v")

// export is what a read of an export found: its events in the file's order,
// what it could not use, and the trailer when one was read and accepted.
type export struct {
	events   []*event
	byKey    map[eventKey]*event
	atOffset map[int64]*event
	trailer  *trailerState

	gaps, duplicates, conflicting, refused int
	problems                               []string

	// The records read by type, which the trailer's counts have to match.
	seenEvents, seenGaps, seenDuplicates int
	line                                 int
	lastOffset                           int64
	ended                                bool
}

type event struct {
	ev          *controlv1.Event
	raw         []byte
	conflicting bool
}

// eventKey is how a consumer tells events apart across a tenant's projects.
type eventKey struct{ tenant, project, id string }

type trailerState struct {
	endReached bool
	tailBytes  int64
}

// readExport reads an export to its end. The error is a refusal of the whole
// export: no header, another format, or a major this reader does not read.
// Everything wrong past the header is counted and said, and the read goes on.
func readExport(r io.Reader) (*export, error) {
	br := bufio.NewReader(r)
	first, err := nextLine(br)
	switch {
	case errors.Is(err, io.EOF):
		return nil, errors.New("no header: the input is empty")
	case err != nil:
		return nil, fmt.Errorf("the header could not be read: %w", err)
	}
	if err := checkHeader(first); err != nil {
		return nil, err
	}
	x := &export{byKey: map[eventKey]*event{}, atOffset: map[int64]*event{}, line: 1, lastOffset: -1}
	x.readRecords(br)
	return x, nil
}

// readRecords reads the records after the header. Input that cannot be read
// is refused where it stops, and without a trailer read the export is cut.
func (x *export) readRecords(br *bufio.Reader) {
	for {
		l, err := nextLine(br)
		if errors.Is(err, io.EOF) {
			return
		}
		x.line++
		if err != nil {
			x.refuse("the input could not be read: " + err.Error())
			return
		}
		x.record(l)
	}
}

type rawLine struct {
	b                     []byte
	tooLong, unterminated bool
}

// nextLine returns the next record without its newline, or io.EOF when no
// byte is left. A record longer than maxRecordBytes is read through and its
// bytes dropped.
func nextLine(r *bufio.Reader) (rawLine, error) {
	var l rawLine
	n := 0
	for {
		chunk, err := r.ReadSlice('\n')
		n += len(chunk)
		if n <= maxRecordBytes+1 {
			l.b = append(l.b, chunk...)
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && n == 0:
			return l, io.EOF
		case errors.Is(err, io.EOF):
			l.unterminated = true
		case err != nil:
			return l, err
		}
		l.b = bytes.TrimSuffix(l.b, []byte("\n"))
		l.tooLong = n-len("\n") > maxRecordBytes
		return l, nil
	}
}

func (x *export) refuse(why string) {
	x.refused++
	x.problems = append(x.problems, fmt.Sprintf("line %d: %s", x.line, why))
}

func (x *export) record(l rawLine) {
	switch {
	case x.ended:
		x.refuse("a record after the trailer")
		return
	case l.tooLong:
		x.refuse(fmt.Sprintf("a record longer than %d bytes", maxRecordBytes))
		return
	case l.unterminated:
		x.refuse("a record the input ends inside, before its newline: the export was cut")
		return
	}
	ms, err := object(l.b)
	if err != nil {
		x.refuse("a record that is not one well-formed JSON object: " + err.Error())
		return
	}
	typ, err := recordType(ms)
	if err != nil {
		x.refuse(err.Error())
		return
	}
	if !x.held(typ) {
		return
	}
	if err := only(ms, recordMembers[typ]); err != nil {
		x.refuse(fmt.Sprintf("a %s record the contract does not give: %s", typ, err))
		return
	}
	switch typ {
	case "event":
		x.event(l.b)
	case "gap":
		x.gap(l.b)
	case "duplicate":
		x.duplicate(l.b)
	default:
		x.readTrailer(l.b, ms)
	}
}

// held counts a record by the type it names, which the trailer's counts have
// to match, and refuses a type no record after the header has.
func (x *export) held(typ string) bool {
	switch typ {
	case "event":
		x.seenEvents++
	case "gap":
		x.seenGaps++
	case "duplicate":
		x.seenDuplicates++
	case "trailer":
		x.ended = true
	case "header":
		x.refuse("a second header")
		return false
	default:
		x.refuse(fmt.Sprintf("record type %q, which this reader does not know", typ))
		return false
	}
	return true
}

// recordType is the type a record names in its member spelled "type".
func recordType(ms []member) (string, error) {
	raw, ok := valueOf(ms, "type")
	var typ string
	if ok && json.Unmarshal(raw, &typ) == nil {
		return typ, nil
	}
	for _, m := range ms {
		if strings.EqualFold(m.name, "type") && m.name != "type" {
			return "", fmt.Errorf("a record whose member %q differs from %q only in case", m.name, "type")
		}
	}
	return "", errors.New("a record that is not a JSON object naming its type")
}

// inOrder holds the records to the file's order, in which every offset is
// past the one before.
func (x *export) inOrder(offset int64) bool {
	if offset <= x.lastOffset {
		x.refuse(fmt.Sprintf("offset %d does not follow offset %d: the records are not in the file's order", offset, x.lastOffset))
		return false
	}
	x.lastOffset = offset
	return true
}

func (x *export) event(b []byte) {
	var r struct {
		Offset *int64          `json:"offset"`
		Cursor *string         `json:"cursor"`
		Event  json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		x.refuse("an event record that does not decode: " + err.Error())
		return
	}
	if r.Offset == nil || r.Cursor == nil || *r.Cursor == "" || len(r.Event) == 0 || string(r.Event) == "null" {
		x.refuse("an event record without its offset, cursor or event")
		return
	}
	if !x.inOrder(*r.Offset) {
		return
	}
	ev := &controlv1.Event{}
	if err := protojson.Unmarshal(r.Event, ev); err != nil {
		x.refuse("an event the contract does not read: " + err.Error())
		return
	}
	if major, ok := majorOf(ev.GetSchemaVersion()); !ok || strconv.FormatUint(major, 10) != eventMajor {
		x.refuse(fmt.Sprintf("event %s has schema_version %q, and this reader reads major %s",
			safe(ev.GetEventId()), ev.GetSchemaVersion(), eventMajor))
		return
	}
	x.add(*r.Offset, r.Event, ev)
}

// add keeps an event once. The same line twice is a duplicate; one id with
// two lines is a conflict, and every request holding either is unknown.
func (x *export) add(offset int64, raw []byte, ev *controlv1.Event) {
	e := &event{ev: ev, raw: raw}
	k := eventKey{ev.GetTenantId(), ev.GetProjectId(), ev.GetEventId()}
	if first, seen := x.byKey[k]; seen && k.id != "" {
		if bytes.Equal(first.raw, raw) {
			x.duplicates++
			return
		}
		x.conflicting++
		first.conflicting, e.conflicting = true, true
		x.problems = append(x.problems, fmt.Sprintf("line %d: event %s is held twice with different content", x.line, safe(k.id)))
	} else {
		x.byKey[k] = e
	}
	x.atOffset[offset] = e
	x.events = append(x.events, e)
}

func (x *export) gap(b []byte) {
	var r struct {
		Offset *int64  `json:"offset"`
		Reason *string `json:"reason"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Offset == nil || r.Reason == nil {
		x.refuse("a gap record without its offset or reason")
		return
	}
	if !x.inOrder(*r.Offset) {
		return
	}
	x.gaps++
	x.problems = append(x.problems, fmt.Sprintf("line %d: a gap at offset %d: %s", x.line, *r.Offset, safe(*r.Reason)))
}

// duplicate accepts a duplicate only of an event this export wrote at the
// offset it names, so a duplicate record cannot stand in for a missing event.
func (x *export) duplicate(b []byte) {
	var r struct {
		Offset      *int64  `json:"offset"`
		EventID     *string `json:"event_id"`
		FirstOffset *int64  `json:"first_offset"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Offset == nil || r.EventID == nil || r.FirstOffset == nil {
		x.refuse("a duplicate record without its offset, event_id or first_offset")
		return
	}
	if !x.inOrder(*r.Offset) {
		return
	}
	if first, ok := x.atOffset[*r.FirstOffset]; !ok || first.ev.GetEventId() != *r.EventID {
		x.refuse(fmt.Sprintf("a duplicate of event %s at offset %d, where this export wrote no such event",
			safe(*r.EventID), *r.FirstOffset))
		return
	}
	x.duplicates++
}
