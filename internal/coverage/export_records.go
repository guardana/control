package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// gapReasons are the reasons ADR-0035 names for a gap.
var gapReasons = []string{"malformed", "too_long", "unsupported_version", "conflicting_event_id", "carriage_return", "partial_tail"}

func (er *exportReader) readHeader(line []byte) error {
	m, err := object(line, "type", "format", "version", "file", "source", "query")
	if err != nil {
		return err
	}
	if f, err := m.str("format"); err != nil || f != exportFormat {
		return fmt.Errorf("format: want %s", exportFormat)
	}
	if v, err := m.str("version"); err != nil || !versionOfMajor(v, exportMajor) {
		return fmt.Errorf("version: want major %s", exportMajor)
	}
	if _, ok := m["file"]; !ok {
		return errors.New("file: required")
	}
	if er.x.File, err = m.str("file"); err != nil {
		return err
	}
	if err := er.readQuery(m); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	er.header = true
	return nil
}

// readQuery notes a cursor to start after and any filter: either leaves
// lines of the file out of the export.
func (er *exportReader) readQuery(header members) error {
	if _, ok := header["query"]; !ok {
		return errors.New("required")
	}
	q, err := object(header["query"], "after", "limit", "max_bytes", "request", "run", "tenant", "project", "kind")
	if err != nil {
		return err
	}
	if _, err := q.str("after"); err != nil {
		return err
	}
	_, er.after = q["after"]
	for _, filter := range []string{"request", "run", "tenant", "project", "kind"} {
		if _, err := q.list(filter); err != nil {
			return err
		}
		_, present := q[filter]
		er.filtered = er.filtered || present
	}
	return nil
}

func (er *exportReader) readEvent(line []byte) error {
	m, err := object(line, "type", "offset", "cursor", "event")
	if err != nil {
		return err
	}
	raw, ok := m["event"]
	if !ok {
		return errors.New("event: required")
	}
	ev := &controlv1.Event{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, ev); err != nil {
		return errors.New("event: not one Event")
	}
	if !versionOfMajor(ev.GetSchemaVersion(), eventMajor()) {
		return fmt.Errorf("event: schema_version: want major %s", eventMajor())
	}
	er.x.Events++
	if at := ev.GetOccurredAt(); at.IsValid() {
		er.x.widen(at.AsTime())
	}
	if ev.GetKind() == controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED && ev.GetProposed() != nil {
		er.x.proposals = append(er.x.proposals, ev)
	}
	er.counts[recordEvent]++
	return nil
}

func (er *exportReader) readGap(line []byte) error {
	m, err := object(line, "type", "offset", "cursor", "reason")
	if err != nil {
		return err
	}
	if reason, err := m.str("reason"); err != nil || !slices.Contains(gapReasons, reason) {
		return errors.New("reason: not one ADR-0035 names")
	}
	er.counts[recordGap]++
	return nil
}

func (er *exportReader) readDuplicate(line []byte) error {
	if _, err := object(line, "type", "offset", "event_id", "first_offset"); err != nil {
		return err
	}
	er.counts[recordDuplicate]++
	return nil
}

// widen takes t into the export's window of event times.
func (x *Export) widen(t time.Time) {
	if !x.window || t.Before(x.earliest) {
		x.earliest = t
	}
	if !x.window || t.After(x.latest) {
		x.latest = t
	}
	x.window = true
}

// readTrailer takes the trailer's end_reached, and refuses counts that
// disagree with the records read: a record taken out of the export.
func (er *exportReader) readTrailer(line []byte) error {
	m, err := object(line, "type", "next_cursor", "end_reached", "tail_bytes", "writer_held", "counts",
		"scanned_bytes", "dedup_scope")
	if err != nil {
		return err
	}
	switch string(m["end_reached"]) {
	case "true":
		er.endReached = true
	case "false":
	default:
		return errors.New("end_reached: want true or false")
	}
	c, err := object(m["counts"], recordEvent, recordGap, recordDuplicate)
	if err != nil {
		return fmt.Errorf("counts: %w", err)
	}
	for _, name := range []string{recordEvent, recordGap, recordDuplicate} {
		var n int
		if err := json.Unmarshal(c[name], &n); err != nil || n != er.counts[name] {
			return fmt.Errorf("counts: %s does not count the %d records read", name, er.counts[name])
		}
	}
	er.trailer = true
	return nil
}

// versionOfMajor reports whether v is MAJOR.MINOR in decimal with this major.
func versionOfMajor(v, major string) bool {
	maj, minor, ok := strings.Cut(v, ".")
	return ok && maj == major && decimal(maj) && decimal(minor)
}

func decimal(s string) bool {
	_, err := strconv.ParseUint(s, 10, 32)
	return err == nil
}

// eventMajor is the major of the contract package this build was generated
// from, so a build of another major refuses these events.
func eventMajor() string {
	pkg := string((&controlv1.Event{}).ProtoReflect().Descriptor().ParentFile().Package())
	return strings.TrimPrefix(pkg[strings.LastIndex(pkg, ".")+1:], "v")
}
